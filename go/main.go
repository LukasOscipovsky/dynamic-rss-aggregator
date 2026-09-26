package main

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

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

// fetchRSS fetches and parses RSS from any URL
func fetchRSS(ctx context.Context, url string) ([]Article, error) {
	feed, err := fetchFeed(ctx, url)
	if err != nil {
		return nil, err
	}

	var articles []Article
	for _, item := range feed.Items {
		published := time.Now()
		if item.PublishedParsed != nil {
			published = *item.PublishedParsed
		} else if item.UpdatedParsed != nil {
			published = *item.UpdatedParsed
		}

		articles = append(articles, Article{
			Title:       item.Title,
			Link:        item.Link,
			Description: item.Description,
			Published:   published,
			Source:      feed.Title,
			Image:       itemImage(item),
		})
	}

	return articles, nil
}

func main() {
	startScheduler(context.Background())

	r := gin.Default()

	// Enable CORS for frontend
	r.Use(cors.Default())

	r.GET("/articles", func(c *gin.Context) {
		feedURL := c.Query("source") // get URL from query parameter
		if feedURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing 'source' query parameter"})
			return
		}

		articles, err := fetchRSS(c.Request.Context(), feedURL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, articles)
	})

	r.Run(":8080")
}
