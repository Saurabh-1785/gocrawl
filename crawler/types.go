package crawler

import (
	"sync"
	"time"
)

// CrawlTask represents a single URL that needs to be crawled.
// Each task knows its URL and how deep it is from the seed URL.
type CrawlTask struct {
	URL   string // The URL to crawl
	Depth int    // How many links deep from the seed URL
}

// CrawlResult holds the outcome of crawling one page.
// It tells us what happened when we tried to fetch and parse a URL.
type CrawlResult struct {
	URL        string   // The URL we crawled
	StatusCode int      // HTTP status code (200, 404, etc.)
	Links      []string // New URLs we discovered on this page
	Error      error    // Any error that occurred (nil if successful)
	Depth      int      // Depth of this page from the seed
}

// CrawlStats tracks overall statistics for the entire crawl.
// It uses a mutex because multiple workers update it at the same time.
type CrawlStats struct {
	mu           sync.Mutex // Protects all fields below
	PagesCrawled int        // Successfully crawled pages
	PagesSkipped int        // Pages we skipped (already visited, wrong domain, etc.)
	Errors       int        // Pages that had errors
	StartTime    time.Time  // When the crawl started
}

// IncrementCrawled safely adds 1 to the crawled page count.
func (s *CrawlStats) IncrementCrawled() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PagesCrawled++
}

// IncrementSkipped safely adds 1 to the skipped page count.
func (s *CrawlStats) IncrementSkipped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PagesSkipped++
}

// IncrementErrors safely adds 1 to the error count.
func (s *CrawlStats) IncrementErrors() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Errors++
}

// GetCrawledCount safely reads the crawled page count.
func (s *CrawlStats) GetCrawledCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.PagesCrawled
}

// GetErrorCount safely reads the error count.
func (s *CrawlStats) GetErrorCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Errors
}

// GetSkippedCount safely reads the skipped page count.
func (s *CrawlStats) GetSkippedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.PagesSkipped
}

// NewCrawlStats creates a new CrawlStats with the start time set to now.
func NewCrawlStats() *CrawlStats {
	return &CrawlStats{
		StartTime: time.Now(),
	}
}
