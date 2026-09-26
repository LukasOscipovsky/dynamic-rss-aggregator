package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elastic/go-elasticsearch/v9"
	"github.com/mmcdole/gofeed"
)

// Settings and mappings for the daily article indices
//
//go:embed articles-index.json
var articlesIndexSettings []byte

const (
	// Daily indices are named articles_v1_2026_09_26
	articlesIndexPrefix = "articles_v1_"
	// Every daily index joins this alias so all days can be searched at once
	articlesAlias = "articles"

	defaultScrapeInterval = 15 * time.Minute
	scrapeRunTimeout      = 5 * time.Minute
	// Older items (some feeds keep stories for years) are skipped so they
	// don't create lots of tiny daily indices
	maxArticleAge = 7 * 24 * time.Hour
)

type FeedSource struct {
	Name     string
	URL      string
	Category string
}

// feedSources are the feeds scraped into Elasticsearch
// (keep in sync with SOURCES in frontend/think-stream/src/App.tsx)
var feedSources = []FeedSource{
	{"DennikN", "https://dennikn.sk/feed", "Slovakia"},
	{"Sme", "https://www.sme.sk/rss", "Slovakia"},

	{"BBC World", "https://feeds.bbci.co.uk/news/world/rss.xml", "World"},
	{"The Guardian World", "https://www.theguardian.com/world/rss", "World"},
	{"NYT World", "https://rss.nytimes.com/services/xml/rss/nyt/World.xml", "World"},
	{"Al Jazeera", "https://www.aljazeera.com/xml/rss/all.xml", "World"},
	{"Deutsche Welle", "https://rss.dw.com/rdf/rss-en-all", "World"},
	{"France 24", "https://www.france24.com/en/rss", "World"},
	{"Euronews", "https://www.euronews.com/rss", "World"},
	{"Sky News World", "https://feeds.skynews.com/feeds/rss/world.xml", "World"},
	{"Washington Post World", "https://feeds.washingtonpost.com/rss/world", "World"},

	{"NYT Home", "https://rss.nytimes.com/services/xml/rss/nyt/HomePage.xml", "Top stories"},
	{"CNN", "http://rss.cnn.com/rss/edition.rss", "Top stories"},
	{"NPR News", "https://feeds.npr.org/1001/rss.xml", "Top stories"},
	{"CBS News", "https://www.cbsnews.com/latest/rss/main", "Top stories"},
	{"ABC News", "https://abcnews.go.com/abcnews/topstories", "Top stories"},

	{"CNBC", "https://www.cnbc.com/id/100003114/device/rss/rss.html", "Business"},

	{"BBC Technology", "https://feeds.bbci.co.uk/news/technology/rss.xml", "Tech"},
	{"The Guardian Technology", "https://www.theguardian.com/technology/rss", "Tech"},
	{"TechCrunch", "https://techcrunch.com/feed/", "Tech"},
	{"The Verge", "https://www.theverge.com/rss/index.xml", "Tech"},
	{"Ars Technica", "https://feeds.arstechnica.com/arstechnica/index", "Tech"},
	{"Wired", "https://www.wired.com/feed/rss", "Tech"},
	{"Hacker News", "https://news.ycombinator.com/rss", "Tech"},

	{"ScienceDaily", "https://www.sciencedaily.com/rss/all.xml", "Science"},
	{"NASA", "https://www.nasa.gov/news-release/feed/", "Science"},

	{"ESPN", "https://www.espn.com/espn/rss/news", "Sports"},
}

// ArticleDoc is the document stored in Elasticsearch (see articles-index.json)
type ArticleDoc struct {
	ID          string         `json:"id"`
	GUID        string         `json:"guid,omitempty"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Content     string         `json:"content,omitempty"`
	Link        string         `json:"link"`
	Image       string         `json:"image,omitempty"`
	Author      string         `json:"author,omitempty"`
	Language    string         `json:"language,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Source      ArticleDocFeed `json:"source"`
	PublishedAt time.Time      `json:"published_at"`
	FetchedAt   time.Time      `json:"fetched_at"`
}

type ArticleDocFeed struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Category string `json:"category"`
}

type Scheduler struct {
	es       *elasticsearch.Client
	interval time.Duration

	// createIndexBody is the index settings plus the alias, built once
	createIndexBody []byte

	mu           sync.Mutex
	knownIndices map[string]bool
}

// startScheduler scrapes all feeds into Elasticsearch now and then every
// SCRAPE_INTERVAL (default 15m). ELASTICSEARCH_URL defaults to localhost.
func startScheduler(ctx context.Context) {
	esURL := os.Getenv("ELASTICSEARCH_URL")
	if esURL == "" {
		esURL = "http://localhost:9200"
	}
	interval := defaultScrapeInterval
	if v := os.Getenv("SCRAPE_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Printf("scheduler: invalid SCRAPE_INTERVAL %q, using %s", v, interval)
		} else {
			interval = d
		}
	}

	s, err := NewScheduler(esURL, interval)
	if err != nil {
		log.Printf("scheduler: disabled: %v", err)
		return
	}
	log.Printf("scheduler: scraping %d feeds into %s every %s", len(feedSources), esURL, interval)
	go s.Run(ctx)
}

func NewScheduler(esURL string, interval time.Duration) (*Scheduler, error) {
	es, err := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{esURL}})
	if err != nil {
		return nil, fmt.Errorf("create elasticsearch client: %w", err)
	}

	var body map[string]any
	if err := json.Unmarshal(articlesIndexSettings, &body); err != nil {
		return nil, fmt.Errorf("parse articles-index.json: %w", err)
	}
	body["aliases"] = map[string]any{articlesAlias: map[string]any{}}
	createIndexBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	return &Scheduler{
		es:              es,
		interval:        interval,
		createIndexBody: createIndexBody,
		knownIndices:    map[string]bool{},
	}, nil
}

// Run scrapes immediately and then on every tick until ctx is cancelled
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		s.scrapeAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// scrapeAll scrapes every feed in its own goroutine
func (s *Scheduler) scrapeAll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, scrapeRunTimeout)
	defer cancel()

	start := time.Now()
	var (
		wg      sync.WaitGroup
		indexed atomic.Int64
		failed  atomic.Int64
	)
	for _, src := range feedSources {
		wg.Go(func() {
			n, err := s.scrapeFeed(ctx, src)
			if err != nil {
				failed.Add(1)
				log.Printf("scheduler: %s: %v", src.Name, err)
				return
			}
			indexed.Add(int64(n))
		})
	}
	wg.Wait()

	log.Printf("scheduler: indexed %d articles from %d/%d feeds in %s",
		indexed.Load(), len(feedSources)-int(failed.Load()), len(feedSources),
		time.Since(start).Round(time.Millisecond))
}

// scrapeFeed fetches one feed and bulk-indexes its articles into their daily indices
func (s *Scheduler) scrapeFeed(ctx context.Context, src FeedSource) (int, error) {
	feed, err := fetchFeed(ctx, src.URL)
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}

	fetchedAt := time.Now().UTC()
	oldest := fetchedAt.Add(-maxArticleAge)
	byIndex := map[string][]ArticleDoc{}
	for _, item := range feed.Items {
		published := item.PublishedParsed
		if published == nil {
			published = item.UpdatedParsed
		}
		// Undated items are usually stale, and would move to a new daily index on every run
		if item.Link == "" || published == nil || published.Before(oldest) {
			continue
		}
		doc := newArticleDoc(item, feed, src, published.UTC(), fetchedAt)
		index := dailyIndexName(doc.PublishedAt)
		byIndex[index] = append(byIndex[index], doc)
	}

	total := 0
	for index, docs := range byIndex {
		if err := s.ensureIndex(ctx, index); err != nil {
			return total, err
		}
		if err := s.bulkIndex(ctx, index, docs); err != nil {
			return total, err
		}
		total += len(docs)
	}
	return total, nil
}

func dailyIndexName(t time.Time) string {
	return articlesIndexPrefix + t.UTC().Format("2006_01_02")
}

// ensureIndex creates the daily index (with mappings and alias) if it doesn't exist yet
func (s *Scheduler) ensureIndex(ctx context.Context, name string) error {
	// Held for the whole check so concurrent feeds don't race to create the same index
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.knownIndices[name] {
		return nil
	}

	res, err := s.es.Indices.Exists([]string{name}, s.es.Indices.Exists.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("check index %s: %w", name, err)
	}
	res.Body.Close()

	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		res, err := s.es.Indices.Create(name,
			s.es.Indices.Create.WithBody(bytes.NewReader(s.createIndexBody)),
			s.es.Indices.Create.WithContext(ctx),
		)
		if err != nil {
			return fmt.Errorf("create index %s: %w", name, err)
		}
		defer res.Body.Close()
		// Another instance may have created it in the meantime
		if res.IsError() && !strings.Contains(res.String(), "resource_already_exists_exception") {
			return fmt.Errorf("create index %s: %s", name, res.String())
		}
		log.Printf("scheduler: created index %s", name)
	default:
		return fmt.Errorf("check index %s: unexpected status %d", name, res.StatusCode)
	}

	s.knownIndices[name] = true
	return nil
}

// bulkIndex upserts the docs by ID, so re-scraping the same article overwrites it
func (s *Scheduler) bulkIndex(ctx context.Context, index string, docs []ArticleDoc) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, doc := range docs {
		meta := map[string]any{"index": map[string]string{"_index": index, "_id": doc.ID}}
		if err := enc.Encode(meta); err != nil {
			return err
		}
		if err := enc.Encode(doc); err != nil {
			return err
		}
	}

	res, err := s.es.Bulk(&buf, s.es.Bulk.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("bulk %s: %w", index, err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("bulk %s: %s", index, res.String())
	}

	var result struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int             `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return fmt.Errorf("bulk %s: decode response: %w", index, err)
	}
	if result.Errors {
		for _, item := range result.Items {
			for _, op := range item {
				if op.Error != nil {
					return fmt.Errorf("bulk %s: item failed (%d): %s", index, op.Status, op.Error)
				}
			}
		}
	}
	return nil
}

func newArticleDoc(item *gofeed.Item, feed *gofeed.Feed, src FeedSource, published, fetchedAt time.Time) ArticleDoc {
	var author string
	if len(item.Authors) > 0 && item.Authors[0] != nil {
		author = item.Authors[0].Name
	}

	sum := sha256.Sum256([]byte(item.Link))
	return ArticleDoc{
		ID:          hex.EncodeToString(sum[:]),
		GUID:        item.GUID,
		Title:       plainText(item.Title),
		Description: plainText(item.Description),
		Content:     item.Content,
		Link:        item.Link,
		Image:       itemImage(item),
		Author:      author,
		Language:    strings.ToLower(feed.Language),
		Tags:        item.Categories,
		Source:      ArticleDocFeed{Name: src.Name, URL: src.URL, Category: src.Category},
		PublishedAt: published,
		FetchedAt:   fetchedAt,
	}
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

// plainText strips HTML tags and entities and collapses whitespace
func plainText(s string) string {
	s = html.UnescapeString(htmlTag.ReplaceAllString(s, " "))
	return strings.Join(strings.Fields(s), " ")
}
