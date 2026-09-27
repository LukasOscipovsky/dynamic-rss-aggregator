package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v9"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/mmcdole/gofeed"
)

type Article struct {
	Title       string    `json:"title"`
	Link        string    `json:"link"`
	Description string    `json:"description"`
	Published   time.Time `json:"published"`
	Source      string    `json:"source,omitempty"`
	Category    string    `json:"category,omitempty"`
	Country     string    `json:"country,omitempty"`
	Image       string    `json:"image,omitempty"`
}

var imgSrc = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)

// itemImage finds an image URL in the item's image, media tags or enclosures
func itemImage(item *gofeed.Item) string {
	if item.Image != nil && item.Image.URL != "" {
		return item.Image.URL
	}
	if media, ok := item.Extensions["media"]; ok {
		for _, tag := range []string{"content", "thumbnail"} {
			for _, ext := range media[tag] {
				if url := ext.Attrs["url"]; url != "" && ext.Attrs["medium"] != "video" {
					return url
				}
			}
		}
		// Some feeds nest thumbnails inside media:group
		for _, group := range media["group"] {
			for _, tag := range []string{"content", "thumbnail"} {
				for _, ext := range group.Children[tag] {
					if url := ext.Attrs["url"]; url != "" {
						return url
					}
				}
			}
		}
	}
	for _, enc := range item.Enclosures {
		if strings.HasPrefix(enc.Type, "image/") {
			return enc.URL
		}
	}
	// Fall back to the first <img> embedded in the article HTML
	for _, html := range []string{item.Content, item.Description} {
		if m := imgSrc.FindStringSubmatch(html); m != nil {
			return m[1]
		}
	}
	return ""
}

const fetchTimeout = 10 * time.Second

// fetchFeed downloads and parses a feed from any URL
func fetchFeed(ctx context.Context, url string) (*gofeed.Feed, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	fp := gofeed.NewParser()
	fp.UserAgent = "Mozilla/5.0 (compatible; dynamic-rss-aggregator/1.0)"
	return fp.ParseURLWithContext(url, ctx)
}

const (
	defaultArticlesLimit = 100
	maxArticlesLimit     = 500
)

// searchArticles returns the newest scraped articles for the given feed URLs from Elasticsearch
func searchArticles(ctx context.Context, es *elasticsearch.Client, sourceURLs []string, limit int) ([]Article, error) {
	query := map[string]any{
		"size": limit,
		"sort": []any{map[string]any{"published_at": "desc"}},
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{map[string]any{"terms": map[string]any{"source.url": sourceURLs}}},
			},
		},
		"_source": []string{"title", "link", "description", "published_at", "source.name", "source.category", "source.country", "image"},
	}
	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	res, err := es.Search(
		es.Search.WithContext(ctx),
		es.Search.WithIndex(articlesAlias),
		es.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("search articles: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, fmt.Errorf("search articles: %s", res.String())
	}

	var result struct {
		Hits struct {
			Hits []struct {
				Source ArticleDoc `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}

	articles := make([]Article, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		doc := hit.Source
		articles = append(articles, Article{
			Title:       doc.Title,
			Link:        doc.Link,
			Description: doc.Description,
			Published:   doc.PublishedAt,
			Source:      doc.Source.Name,
			Category:    doc.Source.Category,
			Country:     doc.Source.Country,
			Image:       doc.Image,
		})
	}
	return articles, nil
}

// newElasticsearchClient connects to ELASTICSEARCH_URL (default localhost)
func newElasticsearchClient() (*elasticsearch.Client, error) {
	esURL := os.Getenv("ELASTICSEARCH_URL")
	if esURL == "" {
		esURL = "http://localhost:9200"
	}
	log.Printf("elasticsearch: %s", esURL)
	return elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{esURL}})
}

func main() {
	es, err := newElasticsearchClient()
	if err != nil {
		log.Fatalf("create elasticsearch client: %v", err)
	}
	startScheduler(context.Background(), es)

	r := gin.Default()

	// Enable CORS for frontend
	r.Use(cors.Default())

	// GET /articles?source=<feed url>[&source=<feed url>...][&limit=100]
	r.GET("/articles", func(c *gin.Context) {
		sources := c.QueryArray("source") // feed URLs from query parameters
		if len(sources) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing 'source' query parameter"})
			return
		}

		limit := defaultArticlesLimit
		if v := c.Query("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > maxArticlesLimit {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("'limit' must be between 1 and %d", maxArticlesLimit)})
				return
			}
			limit = n
		}

		articles, err := searchArticles(c.Request.Context(), es, sources, limit)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, articles)
	})

	r.Run(":8080")
}
