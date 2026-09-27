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

// Settings and mappings for the rss_sources index
//
//go:embed rss-sources-index.json
var sourcesIndexSettings []byte

const (
	// Daily indices are named articles_v1_2026_09_26
	articlesIndexPrefix = "articles_v1_"
	// Every daily index joins this alias so all days can be searched at once
	articlesAlias = "articles"
	// One document per feed with its details and last scrape result
	sourcesIndex = "rss_sources"

	defaultScrapeInterval = 15 * time.Minute
	scrapeRunTimeout      = 5 * time.Minute
	// Older items (some feeds keep stories for years) are skipped so they
	// don't create lots of tiny daily indices
	maxArticleAge = 7 * 24 * time.Hour
	// Feeds are fetched all at once, but Elasticsearch only gets this many
	// concurrent writes: a burst of one bulk per feed can exceed its memory limit
	maxConcurrentWrites = 4
)

type FeedSource struct {
	Name     string
	URL      string
	Category string
	// Country is the publisher's ISO 3166-1 alpha-2 code
	Country string
}

// feedSources are the feeds scraped into Elasticsearch
// (keep in sync with SOURCES in frontend/think-stream/src/App.tsx)
var feedSources = []FeedSource{
	{"DennikN", "https://dennikn.sk/feed", "Slovakia", "SK"},
	{"Sme", "https://www.sme.sk/rss", "Slovakia", "SK"},

	{"BBC World", "https://feeds.bbci.co.uk/news/world/rss.xml", "World", "GB"},
	{"The Guardian World", "https://www.theguardian.com/world/rss", "World", "GB"},
	{"NYT World", "https://rss.nytimes.com/services/xml/rss/nyt/World.xml", "World", "US"},
	{"Al Jazeera", "https://www.aljazeera.com/xml/rss/all.xml", "World", "QA"},
	{"Deutsche Welle", "https://rss.dw.com/rdf/rss-en-all", "World", "DE"},
	{"France 24", "https://www.france24.com/en/rss", "World", "FR"},
	{"Euronews", "https://www.euronews.com/rss", "World", "FR"},
	{"Sky News World", "https://feeds.skynews.com/feeds/rss/world.xml", "World", "GB"},
	{"Washington Post World", "https://feeds.washingtonpost.com/rss/world", "World", "US"},

	{"NYT Home", "https://rss.nytimes.com/services/xml/rss/nyt/HomePage.xml", "Top stories", "US"},
	{"CNN", "http://rss.cnn.com/rss/edition.rss", "Top stories", "US"},
	{"NPR News", "https://feeds.npr.org/1001/rss.xml", "Top stories", "US"},
	{"CBS News", "https://www.cbsnews.com/latest/rss/main", "Top stories", "US"},
	{"ABC News", "https://abcnews.go.com/abcnews/topstories", "Top stories", "US"},

	{"CNBC", "https://www.cnbc.com/id/100003114/device/rss/rss.html", "Business", "US"},

	{"BBC Technology", "https://feeds.bbci.co.uk/news/technology/rss.xml", "Tech", "GB"},
	{"The Guardian Technology", "https://www.theguardian.com/technology/rss", "Tech", "GB"},
	{"TechCrunch", "https://techcrunch.com/feed/", "Tech", "US"},
	{"The Verge", "https://www.theverge.com/rss/index.xml", "Tech", "US"},
	{"Ars Technica", "https://feeds.arstechnica.com/arstechnica/index", "Tech", "US"},
	{"Wired", "https://www.wired.com/feed/rss", "Tech", "US"},
	{"Hacker News", "https://news.ycombinator.com/rss", "Tech", "US"},

	{"ScienceDaily", "https://www.sciencedaily.com/rss/all.xml", "Science", "US"},
	{"NASA", "https://www.nasa.gov/news-release/feed/", "Science", "US"},

	{"ESPN", "https://www.espn.com/espn/rss/news", "Sports", "US"},
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
	Country  string `json:"country"`
}

// SourceDoc is the document stored in the rss_sources index (see rss-sources-index.json)
type SourceDoc struct {
	Name          string    `json:"name"`
	URL           string    `json:"url"`
	Category      string    `json:"category"`
	Country       string    `json:"country"`
	LastScrapedAt time.Time `json:"last_scraped_at"`
	LastStatus    string    `json:"last_status"`
	// null clears the previous error after a successful scrape
	LastError *string `json:"last_error"`
	// Only sent on success, so a failed scrape keeps the previous values
	LastSuccessAt    *time.Time `json:"last_success_at,omitempty"`
	LastArticleCount *int       `json:"last_article_count,omitempty"`
}

// scrapeResult is the outcome of scraping one feed
type scrapeResult struct {
	source    FeedSource
	scrapedAt time.Time
	articles  int
	err       error
}

type Scheduler struct {
	es       *elasticsearch.Client
	interval time.Duration

	// createIndexBody is the index settings plus the alias, built once
	createIndexBody []byte
	// putMappingBody adds new fields to indices created with an older mapping
	putMappingBody []byte
	// Same for the rss_sources index
	sourcesCreateBody  []byte
	sourcesMappingBody []byte

	// writeSlots limits concurrent requests to Elasticsearch
	writeSlots chan struct{}

	mu           sync.Mutex
	knownIndices map[string]bool
}

// startScheduler scrapes all feeds into Elasticsearch now and then every
// SCRAPE_INTERVAL (default 15m)
func startScheduler(ctx context.Context, es *elasticsearch.Client) {
	interval := defaultScrapeInterval
	if v := os.Getenv("SCRAPE_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Printf("scheduler: invalid SCRAPE_INTERVAL %q, using %s", v, interval)
		} else {
			interval = d
		}
	}

	s, err := NewScheduler(es, interval)
	if err != nil {
		log.Printf("scheduler: disabled: %v", err)
		return
	}
	log.Printf("scheduler: scraping %d feeds every %s", len(feedSources), interval)
	go s.Run(ctx)
}

func NewScheduler(es *elasticsearch.Client, interval time.Duration) (*Scheduler, error) {
	var body map[string]any
	if err := json.Unmarshal(articlesIndexSettings, &body); err != nil {
		return nil, fmt.Errorf("parse articles-index.json: %w", err)
	}
	body["aliases"] = map[string]any{articlesAlias: map[string]any{}}
	createIndexBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	putMappingBody, err := json.Marshal(body["mappings"])
	if err != nil {
		return nil, err
	}

	var sourcesBody map[string]json.RawMessage
	if err := json.Unmarshal(sourcesIndexSettings, &sourcesBody); err != nil {
		return nil, fmt.Errorf("parse rss-sources-index.json: %w", err)
	}

	return &Scheduler{
		es:                 es,
		interval:           interval,
		createIndexBody:    createIndexBody,
		putMappingBody:     putMappingBody,
		sourcesCreateBody:  sourcesIndexSettings,
		sourcesMappingBody: sourcesBody["mappings"],
		writeSlots:         make(chan struct{}, maxConcurrentWrites),
		knownIndices:       map[string]bool{},
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
		// Each goroutine writes only its own slot
		results = make([]scrapeResult, len(feedSources))
	)
	for i, src := range feedSources {
		wg.Go(func() {
			scrapedAt := time.Now().UTC()
			n, err := s.scrapeFeed(ctx, src)
			results[i] = scrapeResult{source: src, scrapedAt: scrapedAt, articles: n, err: err}
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

	if err := s.updateSources(ctx, results); err != nil {
		log.Printf("scheduler: update %s: %v", sourcesIndex, err)
	}
}

// updateSources upserts every feed into rss_sources with its last scrape result
func (s *Scheduler) updateSources(ctx context.Context, results []scrapeResult) error {
	if err := s.ensureIndex(ctx, sourcesIndex, s.sourcesCreateBody, s.sourcesMappingBody); err != nil {
		return err
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range results {
		doc := SourceDoc{
			Name:          r.source.Name,
			URL:           r.source.URL,
			Category:      r.source.Category,
			Country:       r.source.Country,
			LastScrapedAt: r.scrapedAt,
			LastStatus:    "ok",
		}
		if r.err != nil {
			msg := r.err.Error()
			doc.LastStatus, doc.LastError = "error", &msg
		} else {
			doc.LastSuccessAt, doc.LastArticleCount = &r.scrapedAt, &r.articles
		}

		sum := sha256.Sum256([]byte(r.source.URL))
		meta := map[string]any{"update": map[string]string{"_index": sourcesIndex, "_id": hex.EncodeToString(sum[:])}}
		if err := enc.Encode(meta); err != nil {
			return err
		}
		// Partial update, so fields left out (e.g. last_success_at on failure) keep their value
		if err := enc.Encode(map[string]any{"doc": doc, "doc_as_upsert": true}); err != nil {
			return err
		}
	}
	return s.bulk(ctx, sourcesIndex, &buf)
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

	select {
	case s.writeSlots <- struct{}{}:
		defer func() { <-s.writeSlots }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}

	total := 0
	for index, docs := range byIndex {
		if err := s.ensureIndex(ctx, index, s.createIndexBody, s.putMappingBody); err != nil {
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

// ensureIndex creates the index from createBody if it doesn't exist yet,
// or adds any new fields from mappingBody to an existing index
func (s *Scheduler) ensureIndex(ctx context.Context, name string, createBody, mappingBody []byte) error {
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
		// Existing index: add any fields introduced since it was created
		res, err := s.es.Indices.PutMapping([]string{name}, bytes.NewReader(mappingBody),
			s.es.Indices.PutMapping.WithContext(ctx),
		)
		if err != nil {
			return fmt.Errorf("update mapping %s: %w", name, err)
		}
		defer res.Body.Close()
		if res.IsError() {
			return fmt.Errorf("update mapping %s: %s", name, res.String())
		}
	case http.StatusNotFound:
		res, err := s.es.Indices.Create(name,
			s.es.Indices.Create.WithBody(bytes.NewReader(createBody)),
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

	return s.bulk(ctx, index, &buf)
}

// bulk sends an NDJSON bulk request and fails if any item failed
func (s *Scheduler) bulk(ctx context.Context, index string, body *bytes.Buffer) error {
	res, err := s.es.Bulk(body, s.es.Bulk.WithContext(ctx))
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
		Source:      ArticleDocFeed{Name: src.Name, URL: src.URL, Category: src.Category, Country: src.Country},
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
