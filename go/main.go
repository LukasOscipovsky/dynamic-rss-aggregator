package main

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
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
}

type Source struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Category string `json:"category"`
}

// defaultSources is a curated list of free, publicly available RSS feeds
var defaultSources = []Source{
	// World news
	{Name: "BBC World", URL: "https://feeds.bbci.co.uk/news/world/rss.xml", Category: "world"},
	{Name: "The Guardian World", URL: "https://www.theguardian.com/world/rss", Category: "world"},
	{Name: "NYT World", URL: "https://rss.nytimes.com/services/xml/rss/nyt/World.xml", Category: "world"},
	{Name: "Al Jazeera", URL: "https://www.aljazeera.com/xml/rss/all.xml", Category: "world"},
	{Name: "Deutsche Welle", URL: "https://rss.dw.com/rdf/rss-en-all", Category: "world"},
	{Name: "France 24", URL: "https://www.france24.com/en/rss", Category: "world"},
	{Name: "Euronews", URL: "https://www.euronews.com/rss", Category: "world"},
	{Name: "Sky News World", URL: "https://feeds.skynews.com/feeds/rss/world.xml", Category: "world"},
	{Name: "Washington Post World", URL: "https://feeds.washingtonpost.com/rss/world", Category: "world"},

	// Top stories
	{Name: "NYT Home", URL: "https://rss.nytimes.com/services/xml/rss/nyt/HomePage.xml", Category: "top"},
	{Name: "CNN", URL: "http://rss.cnn.com/rss/edition.rss", Category: "top"},
	{Name: "NPR News", URL: "https://feeds.npr.org/1001/rss.xml", Category: "top"},
	{Name: "CBS News", URL: "https://www.cbsnews.com/latest/rss/main", Category: "top"},
	{Name: "ABC News", URL: "https://abcnews.go.com/abcnews/topstories", Category: "top"},

	// Business
	{Name: "CNBC", URL: "https://www.cnbc.com/id/100003114/device/rss/rss.html", Category: "business"},

	// Technology
	{Name: "BBC Technology", URL: "https://feeds.bbci.co.uk/news/technology/rss.xml", Category: "tech"},
	{Name: "The Guardian Technology", URL: "https://www.theguardian.com/technology/rss", Category: "tech"},
	{Name: "TechCrunch", URL: "https://techcrunch.com/feed/", Category: "tech"},
	{Name: "The Verge", URL: "https://www.theverge.com/rss/index.xml", Category: "tech"},
	{Name: "Ars Technica", URL: "https://feeds.arstechnica.com/arstechnica/index", Category: "tech"},
	{Name: "Wired", URL: "https://www.wired.com/feed/rss", Category: "tech"},
	{Name: "Hacker News", URL: "https://news.ycombinator.com/rss", Category: "tech"},

	// Science
	{Name: "ScienceDaily", URL: "https://www.sciencedaily.com/rss/all.xml", Category: "science"},
	{Name: "NASA", URL: "https://www.nasa.gov/news-release/feed/", Category: "science"},

	// Sports
	{Name: "ESPN", URL: "https://www.espn.com/espn/rss/news", Category: "sports"},
}

const fetchTimeout = 10 * time.Second

// fetchRSS fetches and parses RSS from any URL
func fetchRSS(ctx context.Context, url string) ([]Article, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	fp := gofeed.NewParser()
	fp.UserAgent = "Mozilla/5.0 (compatible; dynamic-rss-aggregator/1.0)"
	feed, err := fp.ParseURLWithContext(url, ctx)
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
		})
	}

	return articles, nil
}

// fetchAll fetches the given sources concurrently, skipping any that fail,
// and returns the articles sorted newest first
func fetchAll(ctx context.Context, sources []Source) []Article {
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		articles []Article
	)

	for _, src := range sources {
		wg.Add(1)
		go func(src Source) {
			defer wg.Done()
			items, err := fetchRSS(ctx, src.URL)
			if err != nil {
				return
			}
			for i := range items {
				items[i].Source = src.Name
			}
			mu.Lock()
			articles = append(articles, items...)
			mu.Unlock()
		}(src)
	}
	wg.Wait()

	sort.Slice(articles, func(i, j int) bool {
		return articles[i].Published.After(articles[j].Published)
	})
	return articles
}

func filterSources(category string) []Source {
	if category == "" {
		return defaultSources
	}
	var filtered []Source
	for _, src := range defaultSources {
		if strings.EqualFold(src.Category, category) {
			filtered = append(filtered, src)
		}
	}
	return filtered
}

func main() {
	r := gin.Default()

	// Enable CORS for frontend
	r.Use(cors.Default())

	r.GET("/sources", func(c *gin.Context) {
		c.JSON(http.StatusOK, filterSources(c.Query("category")))
	})

	r.GET("/articles", func(c *gin.Context) {
		feedURL := c.Query("source") // get URL from query parameter
		if feedURL == "" {
			// No single source given: aggregate all default sources (optionally by category)
			sources := filterSources(c.Query("category"))
			if len(sources) == 0 {
				c.JSON(http.StatusNotFound, gin.H{"error": "No sources for given category"})
				return
			}
			c.JSON(http.StatusOK, fetchAll(c.Request.Context(), sources))
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
