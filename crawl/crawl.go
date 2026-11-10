// Package crawl provides the public API for gocrawl.
// It is designed to be imported by other Go projects (like go-search)
// to run web crawls and receive extracted documents.
//
// Basic usage:
//
//	cfg := crawl.DefaultConfig()
//	cfg.SeedURL = "https://example.com"
//	cfg.MaxPages = 100
//	cfg.OnDocument = func(doc extractor.Document) {
//	    // index the document, save to DB, etc.
//	}
//	result, err := crawl.Run(cfg)
package crawl

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Saurabh-1785/gocrawl/config"
	"github.com/Saurabh-1785/gocrawl/crawler"
	"github.com/Saurabh-1785/gocrawl/docstore"
	"github.com/Saurabh-1785/gocrawl/extractor"
	"github.com/Saurabh-1785/gocrawl/fetcher"
)

// NoDelay turns off the per-domain delay. CrawlDelay 0 means "use the
// polite default of 1s", because forgetting to set it should never make
// the crawler hammer a site.
const NoDelay time.Duration = -1

// NoRetries turns off retries. MaxRetries 0 means "use the default of 2".
const NoRetries = -1

// Defaults used when a field is left at its zero value.
const (
	DefaultMaxDepth   = 2
	DefaultMaxPages   = 50
	DefaultWorkers    = 3
	DefaultTimeout    = 10 * time.Second
	DefaultCrawlDelay = 1 * time.Second
	DefaultMaxRetries = 2
)

// CrawlConfig holds all parameters for a crawl job.
type CrawlConfig struct {
	// SeedURL is the starting URL to crawl (required).
	SeedURL string

	// MaxDepth is how many links deep to follow. 0 means the seed page only:
	// zero is a real value here. DefaultConfig sets it to 2.
	MaxDepth int

	// MaxPages is the maximum number of HTML pages to fetch (0 = 50).
	MaxPages int

	// Workers is the number of concurrent worker goroutines (0 = 3).
	Workers int

	// Timeout is the HTTP request timeout per page (0 = 10s).
	Timeout time.Duration

	// CrawlDelay is the minimum gap between requests to the same domain
	// (0 = 1s, NoDelay = none). A larger robots.txt Crawl-delay wins.
	CrawlDelay time.Duration

	// MaxRetries is how many extra attempts a page gets after a timeout,
	// network error, 429 or 5xx (0 = 2, NoRetries = none).
	MaxRetries int

	// UserAgent is sent with every request ("" = "GoCrawl/1.0").
	UserAgent string

	// IgnoreRobots skips robots.txt. By default robots.txt is obeyed.
	IgnoreRobots bool

	// OutputPath is an optional JSONL file to append documents to.
	// Documents already in the file (same ID) are not written again.
	OutputPath string

	// CheckpointPath, if set, saves crawl progress there while crawling.
	// With Resume, a later run continues from it instead of starting over.
	CheckpointPath string
	Resume         bool

	// Transport sends the HTTP requests (nil = http.DefaultTransport).
	Transport http.RoundTripper

	// Logger receives progress messages (nil = log.Default()).
	// Use log.New(io.Discard, "", 0) to silence the crawler.
	Logger *log.Logger

	// OnDocument is called for every successfully extracted document.
	// It is called from worker goroutines, so it must be thread-safe.
	// When resuming after an interruption, a document may be delivered twice.
	OnDocument func(doc extractor.Document)
}

// CrawlResult contains metrics from a crawl.
type CrawlResult struct {
	DocumentsWritten int           // Documents saved to OutputPath in this run
	DocumentsSkipped int           // Documents not saved because their ID was already in the file
	PagesVisited     int           // HTML pages fetched successfully
	PagesFailed      int           // Pages that failed after all retries
	PagesSkipped     int           // Non-HTML pages, refused or duplicate redirects
	RobotsBlocked    int           // URLs robots.txt told us not to fetch
	Retries          int           // Extra fetch attempts made
	UniqueURLs       int           // Different URLs seen
	Duration         time.Duration // Wall-clock time for the crawl
	OutputPath       string        // Echoed back for convenience
	Interrupted      bool          // true if the crawl was cancelled before finishing
}

// DefaultConfig returns a CrawlConfig with every default filled in.
// Set SeedURL (and anything else) on it, then call Run.
func DefaultConfig() CrawlConfig {
	return CrawlConfig{
		MaxDepth:   DefaultMaxDepth,
		MaxPages:   DefaultMaxPages,
		Workers:    DefaultWorkers,
		Timeout:    DefaultTimeout,
		CrawlDelay: DefaultCrawlDelay,
		MaxRetries: DefaultMaxRetries,
	}
}

// Run executes a crawl and blocks until it is complete.
func Run(cfg CrawlConfig) (CrawlResult, error) {
	return RunContext(context.Background(), cfg)
}

// RunContext executes a crawl and blocks until it is complete or ctx is
// cancelled. On cancellation it returns the partial result, with
// Interrupted set, and ctx.Err().
//
// Thread-safe: each call builds its own crawler, frontier and rate limiter,
// so several crawls can run at the same time.
func RunContext(ctx context.Context, cfg CrawlConfig) (CrawlResult, error) {
	internalCfg := toInternal(cfg)
	if err := config.ValidateConfig(internalCfg); err != nil {
		return CrawlResult{}, fmt.Errorf("invalid config: %w", err)
	}

	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}

	var store *docstore.DocStore
	if cfg.OutputPath != "" {
		var err error
		store, err = docstore.NewDocStore(cfg.OutputPath)
		if err != nil {
			return CrawlResult{}, fmt.Errorf("failed to create docstore: %w", err)
		}
	}

	c := crawler.NewCrawler(internalCfg)
	c.Logger = logger
	c.Transport = cfg.Transport
	c.CheckpointPath = cfg.CheckpointPath
	c.Resume = cfg.Resume

	// Wire the pipeline: Crawler → Extractor → OnDocument + DocStore.
	c.OnPage = func(page crawler.Page) {
		doc := extractor.ExtractNode(page.FinalURL, page.Root, page.StatusCode)

		if cfg.OnDocument != nil {
			cfg.OnDocument(doc)
		}
		if store != nil {
			if err := store.Save(doc); err != nil {
				// A write error (disk full, file gone) will hit every page
				// after this one too, so stop instead of losing them all.
				c.Stop(fmt.Errorf("saving document for %s: %w", page.FinalURL, err))
			}
		}
	}

	runErr := c.RunContext(ctx)

	stats := c.Stats()
	result := CrawlResult{
		PagesVisited:  stats.GetCrawledCount(),
		PagesFailed:   stats.GetErrorCount(),
		PagesSkipped:  stats.GetSkippedCount(),
		RobotsBlocked: stats.GetRobotsBlockedCount(),
		Retries:       stats.GetRetryCount(),
		UniqueURLs:    c.UniqueURLs(),
		Duration:      time.Since(stats.GetStartTime()),
		OutputPath:    cfg.OutputPath,
		Interrupted:   ctx.Err() != nil,
	}

	if store != nil {
		result.DocumentsWritten = store.Count()
		result.DocumentsSkipped = store.Skipped()
		if err := store.Close(); err != nil && runErr == nil {
			runErr = fmt.Errorf("closing docstore: %w", err)
		}
		logger.Printf("[INFO]  DocStore: wrote %d documents, skipped %d duplicates",
			result.DocumentsWritten, result.DocumentsSkipped)
	}
	return result, runErr
}

// toInternal applies defaults and converts to the engine's config,
// where every value is literal.
func toInternal(cfg CrawlConfig) config.Config {
	if cfg.MaxPages == 0 {
		cfg.MaxPages = DefaultMaxPages
	}
	if cfg.Workers == 0 {
		cfg.Workers = DefaultWorkers
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	switch {
	case cfg.CrawlDelay == 0:
		cfg.CrawlDelay = DefaultCrawlDelay
	case cfg.CrawlDelay == NoDelay:
		cfg.CrawlDelay = 0
	}
	switch {
	case cfg.MaxRetries == 0:
		cfg.MaxRetries = DefaultMaxRetries
	case cfg.MaxRetries == NoRetries:
		cfg.MaxRetries = 0
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = fetcher.DefaultUserAgent
	}
	return config.Config{
		SeedURL:       cfg.SeedURL,
		MaxDepth:      cfg.MaxDepth, // literal: 0 = seed only
		MaxPages:      cfg.MaxPages,
		Workers:       cfg.Workers,
		Timeout:       cfg.Timeout,
		CrawlDelay:    cfg.CrawlDelay,
		MaxRetries:    cfg.MaxRetries,
		UserAgent:     cfg.UserAgent,
		RespectRobots: !cfg.IgnoreRobots,
	}
}
