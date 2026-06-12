package crawler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Saurabh-1785/gocrawl/config"
)

// ===================================================================
// Visited Store Tests
// ===================================================================

// TestMarkVisited tests the basic add-and-check behavior.
func TestMarkVisited(t *testing.T) {
	store := NewVisitedStore()

	// First time marking a URL should return true (it's new)
	if !store.MarkVisited("http://example.com") {
		t.Error("First MarkVisited should return true (new URL)")
	}

	// Second time should return false (already visited)
	if store.MarkVisited("http://example.com") {
		t.Error("Second MarkVisited should return false (duplicate)")
	}

	// A different URL should return true
	if !store.MarkVisited("http://example.com/page2") {
		t.Error("Different URL should return true (new URL)")
	}
}

// TestIsVisited tests the read-only check.
func TestIsVisited(t *testing.T) {
	store := NewVisitedStore()

	if store.IsVisited("http://example.com") {
		t.Error("URL should not be visited yet")
	}

	store.MarkVisited("http://example.com")

	if !store.IsVisited("http://example.com") {
		t.Error("URL should be visited after marking")
	}
}

// TestVisitedCount tests the URL counter.
func TestVisitedCount(t *testing.T) {
	store := NewVisitedStore()

	if store.Count() != 0 {
		t.Errorf("Expected count 0, got %d", store.Count())
	}

	store.MarkVisited("http://example.com/page1")
	store.MarkVisited("http://example.com/page2")
	store.MarkVisited("http://example.com/page3")

	if store.Count() != 3 {
		t.Errorf("Expected count 3, got %d", store.Count())
	}

	// Marking a duplicate shouldn't change the count
	store.MarkVisited("http://example.com/page1")

	if store.Count() != 3 {
		t.Errorf("Expected count 3 after duplicate, got %d", store.Count())
	}
}

// TestConcurrentVisitedAccess tests that multiple goroutines can safely
// use the VisitedStore at the same time without data races.
func TestConcurrentVisitedAccess(t *testing.T) {
	store := NewVisitedStore()
	var wg sync.WaitGroup

	// Launch 100 goroutines, each trying to mark the same 10 URLs
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				url := "http://example.com/page" + string(rune('A'+j))
				store.MarkVisited(url)
				store.IsVisited(url)
				store.Count()
			}
		}()
	}

	wg.Wait()

	if store.Count() != 10 {
		t.Errorf("Expected 10 unique URLs after concurrent access, got %d", store.Count())
	}
}

// TestGetAll tests retrieving all visited URLs.
func TestGetAll(t *testing.T) {
	store := NewVisitedStore()

	store.MarkVisited("http://example.com/a")
	store.MarkVisited("http://example.com/b")
	store.MarkVisited("http://example.com/c")

	all := store.GetAll()
	if len(all) != 3 {
		t.Errorf("Expected 3 URLs in GetAll, got %d", len(all))
	}
}

// ===================================================================
// Rate Limiter Tests
// ===================================================================

// TestRateLimiterDelay tests that consecutive requests to the same domain are delayed.
func TestRateLimiterDelay(t *testing.T) {
	delay := 100 * time.Millisecond
	rl := NewRateLimiter(delay)

	url := "http://example.com/page1"

	// First request should be instant
	start := time.Now()
	rl.Wait(url)
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Errorf("First request should be instant, took %v", elapsed)
	}

	// Second request to the SAME domain should be delayed
	start = time.Now()
	rl.Wait("http://example.com/page2") // same domain, different page
	elapsed = time.Since(start)

	if elapsed < 50*time.Millisecond {
		t.Errorf("Second request should be delayed, only took %v", elapsed)
	}
}

// TestRateLimiterDifferentDomains tests that different domains aren't affected by each other.
func TestRateLimiterDifferentDomains(t *testing.T) {
	delay := 200 * time.Millisecond
	rl := NewRateLimiter(delay)

	rl.Wait("http://example.com/page")

	start := time.Now()
	rl.Wait("http://other.com/page")
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Errorf("Different domain should not be delayed, took %v", elapsed)
	}
}

// TestRateLimiterZeroDelay tests that zero delay means no waiting.
func TestRateLimiterZeroDelay(t *testing.T) {
	rl := NewRateLimiter(0)

	start := time.Now()
	rl.Wait("http://example.com/page1")
	rl.Wait("http://example.com/page2")
	rl.Wait("http://example.com/page3")
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Errorf("Zero delay should not cause any waiting, took %v", elapsed)
	}
}

// ===================================================================
// Crawler Integration Tests
// ===================================================================

// createTestSite builds a local test server with a small link graph.
// This lets us test the crawler without hitting real websites.
//
// The test site has this structure:
//
//	/ (home) → links to /about, /blog
//	/about   → links to /contact
//	/blog    → links to /blog/post1
//	/blog/post1 → links to /
//	/contact → no links
func createTestSite() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
			<h1>Home</h1>
			<a href="/about">About</a>
			<a href="/blog">Blog</a>
		</body></html>`)
	})

	mux.HandleFunc("/about", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
			<h1>About</h1>
			<a href="/contact">Contact</a>
		</body></html>`)
	})

	mux.HandleFunc("/blog", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
			<h1>Blog</h1>
			<a href="/blog/post1">Post 1</a>
		</body></html>`)
	})

	mux.HandleFunc("/blog/post1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
			<h1>Post 1</h1>
			<a href="/">Home</a>
		</body></html>`)
	})

	mux.HandleFunc("/contact", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
			<h1>Contact</h1>
			<p>No links here</p>
		</body></html>`)
	})

	return httptest.NewServer(mux)
}

// TestCrawlerFindsAllPages tests that the crawler discovers all reachable pages.
func TestCrawlerFindsAllPages(t *testing.T) {
	server := createTestSite()
	defer server.Close()

	cfg := config.Config{
		SeedURL:    server.URL,
		MaxDepth:   5,
		MaxPages:   100,
		Workers:    2,
		Timeout:    5 * time.Second,
		CrawlDelay: 0,
	}

	c := NewCrawler(cfg)
	c.Run()

	// The test site has 5 pages: /, /about, /blog, /blog/post1, /contact
	visitedURLs := c.visited.GetAll()
	expectedPages := 5

	if len(visitedURLs) != expectedPages {
		t.Errorf("Expected %d pages visited, got %d", expectedPages, len(visitedURLs))
		t.Logf("Visited URLs: %v", visitedURLs)
	}
}

// TestCrawlerDepthLimit tests that the crawler stops at the configured depth.
func TestCrawlerDepthLimit(t *testing.T) {
	server := createTestSite()
	defer server.Close()

	cfg := config.Config{
		SeedURL:    server.URL,
		MaxDepth:   1, // Only go 1 level deep from seed
		MaxPages:   100,
		Workers:    2,
		Timeout:    5 * time.Second,
		CrawlDelay: 0,
	}

	c := NewCrawler(cfg)
	c.Run()

	// Depth 0: / (home)
	// Depth 1: /about, /blog
	// /contact and /blog/post1 are depth 2, should NOT be visited
	visitedURLs := c.visited.GetAll()

	if len(visitedURLs) != 3 {
		t.Errorf("Expected 3 pages with depth limit 1, got %d", len(visitedURLs))
		t.Logf("Visited URLs: %v", visitedURLs)
	}

	for _, u := range visitedURLs {
		if strings.HasSuffix(u, "/contact") || strings.HasSuffix(u, "/blog/post1") {
			t.Errorf("Should not have visited %s with depth limit 1", u)
		}
	}
}

// TestCrawlerPageLimit tests that the crawler stops after the configured number of pages.
func TestCrawlerPageLimit(t *testing.T) {
	server := createTestSite()
	defer server.Close()

	cfg := config.Config{
		SeedURL:    server.URL,
		MaxDepth:   10,
		MaxPages:   2,
		Workers:    1, // Use 1 worker for deterministic behavior
		Timeout:    5 * time.Second,
		CrawlDelay: 0,
	}

	c := NewCrawler(cfg)
	c.Run()

	crawledCount := c.stats.GetCrawledCount()
	if crawledCount > 2 {
		t.Errorf("Expected at most 2 pages crawled, got %d", crawledCount)
	}
}

// TestCrawlerSameDomainOnly tests that external links are not followed.
func TestCrawlerSameDomainOnly(t *testing.T) {
	externalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><h1>External Site</h1></body></html>`)
	}))
	defer externalServer.Close()

	mainServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body>
			<a href="%s">External Link</a>
			<a href="/internal">Internal Link</a>
		</body></html>`, externalServer.URL)
	}))
	defer mainServer.Close()

	cfg := config.Config{
		SeedURL:    mainServer.URL,
		MaxDepth:   5,
		MaxPages:   100,
		Workers:    2,
		Timeout:    5 * time.Second,
		CrawlDelay: 0,
	}

	c := NewCrawler(cfg)
	c.Run()

	for _, u := range c.visited.GetAll() {
		if strings.Contains(u, externalServer.URL) {
			t.Errorf("Should not have visited external URL: %s", u)
		}
	}
}
