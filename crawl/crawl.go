// Package crawl provides the public API for gocrawl.
// It is designed to be imported by other Go projects (like go-search)
// to trigger web crawls and receive extracted documents.
//
// Basic usage:
//
//	result, err := crawl.Run(crawl.CrawlConfig{
//	    SeedURL:  "https://example.com",
//	    MaxPages: 100,
//	    OnDocument: func(doc extractor.Document) {
//	        // index the document, save to DB, etc.
//	    },
//	})
package crawl

import (
	"fmt"
	"log"
	"time"

	"github.com/Saurabh-1785/gocrawl/config"
	"github.com/Saurabh-1785/gocrawl/crawler"
	"github.com/Saurabh-1785/gocrawl/docstore"
	"github.com/Saurabh-1785/gocrawl/extractor"
)

// CrawlConfig holds all parameters for a crawl job.
type CrawlConfig struct {
	// SeedURL is the starting URL to crawl (required).
	SeedURL string

	// MaxDepth is how many links deep to follow (default: 2, 0 = seed page only).
	MaxDepth int

	// MaxPages is the maximum number of pages to crawl (default: 50).
	MaxPages int

	// Workers is the number of concurrent worker goroutines (default: 3).
	Workers int

	// Timeout is the HTTP request timeout per page (default: 10s).
	Timeout time.Duration

	// CrawlDelay is the minimum delay between requests to the same domain (default: 1s).
	CrawlDelay time.Duration

	// OutputPath is an optional file path for JSONL output (append mode).
	// If empty, no file is written — use OnDocument for streaming.
	OutputPath string

	// OnDocument is called for every successfully extracted document.
	// Use this to stream documents directly to an indexer without intermediate files.
	// Called from worker goroutines — must be thread-safe.
	// If nil, documents are only written to OutputPath (if set).
	OnDocument func(doc extractor.Document)
}

// CrawlResult contains metrics from a completed crawl.
type CrawlResult struct {
	DocumentsWritten int           // Documents saved to JSONL (0 if no OutputPath)
	DocumentsSkipped int           // Duplicate documents skipped by docstore
	PagesVisited     int           // Total pages successfully fetched
	PagesFailed      int           // Pages that returned errors
	Duration         time.Duration // Wall-clock time for the entire crawl
	OutputPath       string        // Echoed back for convenience
}

// Run executes a crawl with the given config and blocks until complete.
// It returns a CrawlResult with metrics about the crawl.
//
// Thread-safe: multiple crawls can run concurrently with different configs.
// Each call creates its own crawler instance, visited set, and rate limiter.
//
// At least one of OutputPath or OnDocument should be set, otherwise
// documents are extracted but go nowhere.
func Run(cfg CrawlConfig) (CrawlResult, error) {
	// Apply defaults
	cfg = applyDefaults(cfg)

	// Validate
	if cfg.SeedURL == "" {
		return CrawlResult{}, fmt.Errorf("SeedURL is required")
	}

	// Build the internal config
	internalCfg := config.Config{
		SeedURL:    cfg.SeedURL,
		MaxDepth:   cfg.MaxDepth,
		MaxPages:   cfg.MaxPages,
		Workers:    cfg.Workers,
		Timeout:    cfg.Timeout,
		CrawlDelay: cfg.CrawlDelay,
	}

	if err := config.ValidateConfig(internalCfg); err != nil {
		return CrawlResult{}, fmt.Errorf("invalid config: %w", err)
	}

	// Set up optional docstore
	var store *docstore.DocStore
	if cfg.OutputPath != "" {
		var err error
		store, err = docstore.NewDocStore(cfg.OutputPath)
		if err != nil {
			return CrawlResult{}, fmt.Errorf("failed to create docstore: %w", err)
		}
		defer store.Close()
	}

	// Create the crawler
	c := crawler.NewCrawler(internalCfg)

	// Wire the pipeline: Crawler → Extractor → OnDocument + DocStore
	c.OnPageFetched = func(pageURL string, body string, statusCode int) {
		doc := extractor.Extract(pageURL, body, statusCode)

		// Stream to callback (e.g., go-search indexer)
		if cfg.OnDocument != nil {
			cfg.OnDocument(doc)
		}

		// Optionally save to JSONL file
		if store != nil {
			if err := store.Save(doc); err != nil {
				log.Printf("[ERROR] Failed to save document for %s: %v", pageURL, err)
			}
		}
	}

	// Run the crawl (blocks until complete)
	c.Run()

	// Build result from crawler stats
	stats := c.Stats()
	duration := time.Since(stats.GetStartTime())

	result := CrawlResult{
		PagesVisited: stats.GetCrawledCount(),
		PagesFailed:  stats.GetErrorCount(),
		Duration:     duration,
		OutputPath:   cfg.OutputPath,
	}

	if store != nil {
		result.DocumentsWritten = store.Count()
	}

	return result, nil
}

// applyDefaults fills in zero values with sensible defaults.
func applyDefaults(cfg CrawlConfig) CrawlConfig {
	if cfg.MaxDepth == 0 && cfg.SeedURL != "" {
		cfg.MaxDepth = 2
	}
	if cfg.MaxPages == 0 {
		cfg.MaxPages = 50
	}
	if cfg.Workers == 0 {
		cfg.Workers = 3
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.CrawlDelay == 0 {
		cfg.CrawlDelay = 1 * time.Second
	}
	return cfg
}
