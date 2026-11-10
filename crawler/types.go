package crawler

import (
	"sync"
	"time"

	"golang.org/x/net/html"
)

// CrawlTask is one URL waiting to be crawled.
type CrawlTask struct {
	URL   string `json:"url"`   // URL to fetch: absolute, as the page author wrote it
	Key   string `json:"key"`   // normalized URL, used for dedupe
	Depth int    `json:"depth"` // how many links away from the seed
}

// Page is what the crawler hands to OnPage after a successful fetch.
type Page struct {
	URL        string     // URL that was requested
	FinalURL   string     // URL after redirects
	StatusCode int        // always 200 today
	Body       string     // raw HTML
	Root       *html.Node // the parsed page; parsed once, shared with link extraction
	Depth      int
}

// CrawlStats tracks numbers for the whole crawl.
// A mutex protects it because every worker updates it.
type CrawlStats struct {
	mu            sync.Mutex
	PagesCrawled  int // HTML pages fetched successfully
	PagesSkipped  int // not HTML, duplicate after redirect, redirect refused
	Errors        int // fetch failed after all retries, or processing panicked
	RobotsBlocked int // URLs robots.txt told us not to fetch
	Retries       int // extra attempts made
	StartTime     time.Time
}

// NewCrawlStats creates a new CrawlStats with the start time set to now.
func NewCrawlStats() *CrawlStats {
	return &CrawlStats{StartTime: time.Now()}
}

func (s *CrawlStats) add(field *int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	*field++
}

func (s *CrawlStats) get(field *int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *field
}

func (s *CrawlStats) IncrementCrawled()       { s.add(&s.PagesCrawled) }
func (s *CrawlStats) IncrementSkipped()       { s.add(&s.PagesSkipped) }
func (s *CrawlStats) IncrementErrors()        { s.add(&s.Errors) }
func (s *CrawlStats) IncrementRobotsBlocked() { s.add(&s.RobotsBlocked) }
func (s *CrawlStats) IncrementRetries()       { s.add(&s.Retries) }

func (s *CrawlStats) GetCrawledCount() int       { return s.get(&s.PagesCrawled) }
func (s *CrawlStats) GetSkippedCount() int       { return s.get(&s.PagesSkipped) }
func (s *CrawlStats) GetErrorCount() int         { return s.get(&s.Errors) }
func (s *CrawlStats) GetRobotsBlockedCount() int { return s.get(&s.RobotsBlocked) }
func (s *CrawlStats) GetRetryCount() int         { return s.get(&s.Retries) }

// GetStartTime safely reads the start time.
func (s *CrawlStats) GetStartTime() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.StartTime
}
