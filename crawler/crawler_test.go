package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Saurabh-1785/gocrawl/config"
)

// ===================================================================
// Helpers
// ===================================================================

var quietLog = log.New(io.Discard, "", 0)

// newTestCrawler builds a crawler with test-friendly defaults: no delay,
// short timeout, robots.txt respected, no log output.
func newTestCrawler(seed string, change func(*config.Config)) *Crawler {
	cfg := config.Config{
		SeedURL:       seed,
		MaxDepth:      5,
		MaxPages:      100,
		Workers:       2,
		Timeout:       5 * time.Second,
		RespectRobots: true,
	}
	if change != nil {
		change(&cfg)
	}
	c := NewCrawler(cfg)
	c.Logger = quietLog
	return c
}

// hitLog records every request a test server receives.
type hitLog struct {
	mu    sync.Mutex
	paths []string
	times []time.Time
}

func (h *hitLog) record(r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paths = append(h.paths, r.URL.Path)
	h.times = append(h.times, time.Now())
}

// pages returns hits excluding /robots.txt.
func (h *hitLog) pages() ([]string, []time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var paths []string
	var times []time.Time
	for i, p := range h.paths {
		if p != "/robots.txt" {
			paths = append(paths, p)
			times = append(times, h.times[i])
		}
	}
	return paths, times
}

func (h *hitLog) count(path string) int {
	paths, _ := h.pages()
	n := 0
	for _, p := range paths {
		if p == path {
			n++
		}
	}
	return n
}

func writeHTML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, "<html><body>"+body+"</body></html>")
}

// wideSite serves "/" with links to /p0 … /p{n-1}. The first `dead` pages
// return 404. Every page takes `latency` to answer.
func wideSite(n, dead int, latency time.Duration) (*httptest.Server, *hitLog) {
	hits := &hitLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.record(r)
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		if latency > 0 {
			time.Sleep(latency)
		}
		if r.URL.Path == "/" {
			var b strings.Builder
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, `<a href="/p%d">page %d</a>`, i, i)
			}
			writeHTML(w, b.String())
			return
		}
		var i int
		if _, err := fmt.Sscanf(r.URL.Path, "/p%d", &i); err != nil || i >= n || i < dead {
			http.NotFound(w, r)
			return
		}
		writeHTML(w, "<p>hello</p>")
	}))
	return srv, hits
}

// createTestSite builds a small link graph:
//
//	/ (home) → /about, /blog
//	/about   → /contact
//	/blog    → /blog/post1
//	/blog/post1 → /
//	/contact → no links
func createTestSite() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		writeHTML(w, `<h1>Home</h1><a href="/about">About</a><a href="/blog">Blog</a>`)
	})
	mux.HandleFunc("/about", func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, `<h1>About</h1><a href="/contact">Contact</a>`)
	})
	mux.HandleFunc("/blog", func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, `<h1>Blog</h1><a href="/blog/post1">Post 1</a>`)
	})
	mux.HandleFunc("/blog/post1", func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, `<h1>Post 1</h1><a href="/">Home</a>`)
	})
	mux.HandleFunc("/contact", func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, `<h1>Contact</h1><p>No links here</p>`)
	})
	return httptest.NewServer(mux)
}

// ===================================================================
// Frontier
// ===================================================================

func task(key string) CrawlTask { return CrawlTask{URL: key, Key: key} }

func TestFrontierPushDedupes(t *testing.T) {
	f := newFrontier(10)
	if !f.Push(task("a")) {
		t.Fatal("first push should succeed")
	}
	if f.Push(task("a")) {
		t.Error("second push of the same key should be rejected")
	}
	if !f.MarkSeen("b") || f.MarkSeen("b") {
		t.Error("MarkSeen should be true once, then false")
	}
	if f.Push(task("b")) {
		t.Error("a key marked seen must not be queued")
	}
}

// 100 goroutines push the same 10 keys: each key is queued exactly once.
func TestFrontierConcurrentPush(t *testing.T) {
	f := newFrontier(1000)
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if f.Push(task(fmt.Sprint(i))) {
					mu.Lock()
					accepted++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if accepted != 10 || f.SeenCount() != 10 {
		t.Errorf("accepted=%d seen=%d, want 10 and 10", accepted, f.SeenCount())
	}
}

// Next never hands out more tasks than the page budget allows.
func TestFrontierBudget(t *testing.T) {
	f := newFrontier(2)
	for i := 0; i < 5; i++ {
		f.Push(task(fmt.Sprint(i)))
	}
	a, _ := f.Next()
	b, _ := f.Next()

	got := make(chan CrawlTask, 1)
	go func() {
		c, ok := f.Next()
		if ok {
			got <- c
		}
		close(got)
	}()

	select {
	case <-got:
		t.Fatal("third task handed out while budget is full")
	case <-time.After(50 * time.Millisecond):
	}

	f.Done(a, failed) // a failure gives its slot back
	if _, ok := <-got; !ok {
		t.Fatal("expected a task after a slot was freed")
	}
	f.Done(b, success)
}

func TestFrontierEndsWhenIdle(t *testing.T) {
	f := newFrontier(10)
	f.Push(task("a"))
	a, _ := f.Next()
	f.Push(task("b")) // found while "a" is in flight
	f.Done(a, success)
	b, ok := f.Next()
	if !ok || b.Key != "b" {
		t.Fatal("expected task b")
	}
	f.Done(b, success)
	if _, ok := f.Next(); ok {
		t.Error("Next should report the crawl is over")
	}
	if !f.Complete() {
		t.Error("Complete should be true")
	}
}

func TestFrontierSnapshotRestore(t *testing.T) {
	f := newFrontier(10)
	f.Push(task("a"))
	f.Push(task("b"))
	f.MarkSeen("blocked")
	inFlight, _ := f.Next() // "a" is in flight when we snapshot

	s := f.Snapshot()
	if len(s.Queue) != 2 || s.Queue[0].Key != inFlight.Key {
		t.Fatalf("queue = %+v, want in-flight a first, then b", s.Queue)
	}

	g := newFrontier(10)
	g.Restore(s)
	for _, k := range []string{"a", "b", "blocked"} {
		if !g.Seen(k) {
			t.Errorf("%q should be seen after restore", k)
		}
	}
	x, _ := g.Next()
	y, _ := g.Next()
	if x.Key != "a" || y.Key != "b" {
		t.Errorf("restored order = %s, %s; want a, b", x.Key, y.Key)
	}
}

// ===================================================================
// Rate limiter
// ===================================================================

func TestRateLimiterDelay(t *testing.T) {
	rl := NewRateLimiter(100 * time.Millisecond)

	start := time.Now()
	rl.Wait("http://example.com/page1")
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("First request should be instant, took %v", elapsed)
	}

	start = time.Now()
	rl.Wait("http://example.com/page2")
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Errorf("Second request should be delayed, only took %v", elapsed)
	}
}

func TestRateLimiterDifferentDomains(t *testing.T) {
	rl := NewRateLimiter(200 * time.Millisecond)
	start := time.Now()
	rl.Wait("http://a.com/")
	rl.Wait("http://b.com/")
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("Different domains should not wait for each other, took %v", elapsed)
	}
}

func TestRateLimiterZeroDelay(t *testing.T) {
	rl := NewRateLimiter(0)
	start := time.Now()
	for i := 0; i < 3; i++ {
		rl.Wait("http://example.com/page")
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("Zero delay should not cause any waiting, took %v", elapsed)
	}
}

// The old limiter let concurrent callers through in a burst. Now 8
// goroutines at once must be released one delay apart.
func TestRateLimiterNoBurst(t *testing.T) {
	const delay = 50 * time.Millisecond
	rl := NewRateLimiter(delay)
	var mu sync.Mutex
	var times []time.Time
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rl.Wait("http://a.com/x")
			mu.Lock()
			times = append(times, time.Now())
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < delay-10*time.Millisecond {
			t.Errorf("release %d came %v after the previous one, want ≥ ~%v", i, gap, delay)
		}
	}
}

func TestRateLimiterHostDelayAndCancel(t *testing.T) {
	rl := NewRateLimiter(0)
	rl.SetHostDelay("http://slow.com/", time.Hour)
	rl.Wait("http://slow.com/a") // first is free

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := rl.WaitContext(ctx, "http://slow.com/b"); err == nil {
		t.Fatal("expected a context error")
	}
	if time.Since(start) > time.Second {
		t.Error("WaitContext should return when the context ends")
	}
}

// ===================================================================
// Crawler integration
// ===================================================================

func TestCrawlerFindsAllPages(t *testing.T) {
	server := createTestSite()
	defer server.Close()

	c := newTestCrawler(server.URL, nil)
	c.Run()

	// 5 pages: /, /about, /blog, /blog/post1, /contact
	if got := c.Stats().GetCrawledCount(); got != 5 {
		t.Errorf("crawled %d pages, want 5: %v", got, c.frontier.SeenKeys())
	}
}

func TestCrawlerDepthLimit(t *testing.T) {
	server := createTestSite()
	defer server.Close()

	c := newTestCrawler(server.URL, func(cfg *config.Config) { cfg.MaxDepth = 1 })
	c.Run()

	// Depth 0: /   Depth 1: /about, /blog   (/contact, /blog/post1 are depth 2)
	keys := c.frontier.SeenKeys()
	if len(keys) != 3 {
		t.Errorf("want 3 URLs with depth limit 1, got %v", keys)
	}
	for _, u := range keys {
		if strings.HasSuffix(u, "/contact") || strings.HasSuffix(u, "/blog/post1") {
			t.Errorf("should not have reached %s with depth limit 1", u)
		}
	}
}

func TestCrawlerDepthZero(t *testing.T) {
	server := createTestSite()
	defer server.Close()

	c := newTestCrawler(server.URL, func(cfg *config.Config) { cfg.MaxDepth = 0 })
	c.Run()
	if got := c.Stats().GetCrawledCount(); got != 1 {
		t.Errorf("depth 0 crawled %d pages, want 1 (seed only)", got)
	}
}

// The page limit must hold with many workers, not only with one.
func TestCrawlerPageLimitManyWorkers(t *testing.T) {
	for _, workers := range []int{1, 5, 20} {
		srv, hits := wideSite(200, 0, 10*time.Millisecond)
		c := newTestCrawler(srv.URL, func(cfg *config.Config) {
			cfg.MaxPages = 10
			cfg.Workers = workers
		})
		c.Run()
		paths, _ := hits.pages()
		if got := c.Stats().GetCrawledCount(); got != 10 || len(paths) != 10 {
			t.Errorf("workers=%d: crawled %d, server saw %d requests; want exactly 10 and 10",
				workers, got, len(paths))
		}
		srv.Close()
	}
}

// With 10 workers the server must still see one request per delay.
func TestCrawlerRateLimitManyWorkers(t *testing.T) {
	const delay = 40 * time.Millisecond
	srv, hits := wideSite(50, 0, 0)
	defer srv.Close()

	c := newTestCrawler(srv.URL, func(cfg *config.Config) {
		cfg.MaxPages = 8
		cfg.Workers = 10
		cfg.CrawlDelay = delay
	})
	c.Run()

	_, times := hits.pages()
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < delay-10*time.Millisecond {
			t.Errorf("requests %d and %d were %v apart, want ≥ ~%v", i-1, i, gap, delay)
		}
	}
}

// Dead links used to fill the fixed-size queue so good links were dropped.
func TestCrawlerDeadLinksDontStarveCrawl(t *testing.T) {
	srv, _ := wideSite(100, 10, 0) // links /p0../p99, first 10 are 404
	defer srv.Close()

	c := newTestCrawler(srv.URL, func(cfg *config.Config) {
		cfg.MaxPages = 10
		cfg.Workers = 1
	})
	c.Run()
	if got := c.Stats().GetCrawledCount(); got != 10 {
		t.Errorf("crawled %d pages, want 10 (dead links must not use up the queue)", got)
	}
	if got := c.Stats().GetErrorCount(); got != 10 {
		t.Errorf("errors = %d, want 10", got)
	}
}

func TestCrawlerSameDomainOnly(t *testing.T) {
	external := &hitLog{}
	externalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		external.record(r)
		writeHTML(w, `<h1>External Site</h1>`)
	}))
	defer externalServer.Close()

	mainServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, fmt.Sprintf(`<a href="%s">External</a><a href="/internal">Internal</a>`, externalServer.URL))
	}))
	defer mainServer.Close()

	c := newTestCrawler(mainServer.URL, nil)
	c.Run()
	if paths, _ := external.pages(); len(paths) != 0 {
		t.Errorf("external server was requested: %v", paths)
	}
}

// A normal page must not redirect us onto another site.
func TestCrawlerRefusesOffSiteRedirect(t *testing.T) {
	other := &hitLog{}
	otherServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		other.record(r)
		writeHTML(w, "<p>other site</p>")
	}))
	defer otherServer.Close()

	mainServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			writeHTML(w, `<a href="/go">go</a>`)
		case "/go":
			http.Redirect(w, r, otherServer.URL+"/landing", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mainServer.Close()

	c := newTestCrawler(mainServer.URL, nil)
	c.Run()
	if paths, _ := other.pages(); len(paths) != 0 {
		t.Errorf("followed a redirect to another site: %v", paths)
	}
	if got := c.Stats().GetSkippedCount(); got != 1 {
		t.Errorf("skipped = %d, want 1 (the refused redirect)", got)
	}
}

// The seed may redirect to another host; the crawl then continues there,
// resolving relative links against the FINAL URL.
func TestCrawlerSeedRedirect(t *testing.T) {
	newHits := &hitLog{}
	newSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newHits.record(r)
		switch r.URL.Path {
		case "/start":
			writeHTML(w, `<a href="next">relative link</a>`)
		case "/next":
			writeHTML(w, "<p>next</p>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer newSite.Close()

	oldHits := &hitLog{}
	oldSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oldHits.record(r)
		if r.URL.Path == "/" {
			http.Redirect(w, r, newSite.URL+"/start", http.StatusMovedPermanently)
			return
		}
		http.NotFound(w, r)
	}))
	defer oldSite.Close()

	c := newTestCrawler(oldSite.URL, nil)
	c.Run()
	if newHits.count("/next") != 1 {
		t.Error("relative link on the redirected seed should resolve against the new site")
	}
	if oldHits.count("/next") != 0 {
		t.Error("relative link was resolved against the old site")
	}
}

// "/blog/" with a link "post-1" means "/blog/post-1". The crawler used to
// strip the slash and request "/post-1".
func TestCrawlerTrailingSlashRelativeLinks(t *testing.T) {
	hits := &hitLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.record(r)
		switch r.URL.Path {
		case "/":
			writeHTML(w, `<a href="/blog/">Blog</a>`)
		case "/blog/":
			writeHTML(w, `<a href="post-1">Post</a>`)
		case "/blog/post-1":
			writeHTML(w, "<p>post</p>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestCrawler(srv.URL, nil)
	c.Run()
	if hits.count("/blog/post-1") != 1 || hits.count("/post-1") != 0 {
		t.Errorf("hits = %v; want /blog/post-1 once and no /post-1", hits.paths)
	}
	if got := c.Stats().GetErrorCount(); got != 0 {
		t.Errorf("errors = %d, want 0", got)
	}
}

// Two URLs that redirect to the same page are processed once.
func TestCrawlerRedirectToSeenPage(t *testing.T) {
	var mu sync.Mutex
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			writeHTML(w, `<a href="/old">old</a><a href="/new">new</a>`)
		case "/old":
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
		case "/new":
			writeHTML(w, "<p>new</p>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestCrawler(srv.URL, func(cfg *config.Config) { cfg.Workers = 1 })
	c.OnPage = func(p Page) {
		if strings.HasSuffix(p.FinalURL, "/new") {
			mu.Lock()
			pages++
			mu.Unlock()
		}
	}
	c.Run()
	if pages != 1 {
		t.Errorf("/new was processed %d times, want 1", pages)
	}
}

func TestCrawlerNonHTMLIsSkippedNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/logo.png" {
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("png"))
			return
		}
		writeHTML(w, `<a href="/logo.png">logo</a>`)
	}))
	defer srv.Close()

	c := newTestCrawler(srv.URL, nil)
	c.Run()
	if c.Stats().GetSkippedCount() != 1 || c.Stats().GetErrorCount() != 0 {
		t.Errorf("skipped=%d errors=%d, want 1 and 0", c.Stats().GetSkippedCount(), c.Stats().GetErrorCount())
	}
}

func TestCrawlerRespectsRobots(t *testing.T) {
	hits := &hitLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.record(r)
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nDisallow: /private\n")
		case "/":
			writeHTML(w, `<a href="/private/secret">secret</a><a href="/public">public</a>`)
		default:
			writeHTML(w, "<p>page</p>")
		}
	}))
	defer srv.Close()

	c := newTestCrawler(srv.URL, nil)
	c.Run()
	if hits.count("/private/secret") != 0 {
		t.Error("fetched a page robots.txt disallows")
	}
	if hits.count("/public") != 1 {
		t.Error("allowed page was not fetched")
	}
	if got := c.Stats().GetRobotsBlockedCount(); got != 1 {
		t.Errorf("robots blocked = %d, want 1", got)
	}

	// With robots.txt ignored, the private page is fetched.
	hits2 := &hitLog{}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits2.record(r)
		srv.Config.Handler.ServeHTTP(w, r)
	}))
	defer srv2.Close()
	c2 := newTestCrawler(srv2.URL, func(cfg *config.Config) { cfg.RespectRobots = false })
	c2.Run()
	if hits2.count("/private/secret") != 1 {
		t.Error("with robots ignored, the private page should be fetched")
	}
}

func TestCrawlerRobotsCrawlDelay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nCrawl-delay: 0.1\n")
		case "/":
			writeHTML(w, `<a href="/a">a</a><a href="/b">b</a><a href="/c">c</a>`)
		default:
			writeHTML(w, "<p>page</p>")
		}
	}))
	defer srv.Close()

	c := newTestCrawler(srv.URL, func(cfg *config.Config) { cfg.Workers = 4 })
	start := time.Now()
	c.Run()
	// 4 pages, 100ms apart → at least 300ms, even with no CrawlDelay set.
	if elapsed := time.Since(start); elapsed < 280*time.Millisecond {
		t.Errorf("crawl took %v; robots.txt Crawl-delay was not applied", elapsed)
	}
}

func TestCrawlerSeedBlockedByRobots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
			return
		}
		writeHTML(w, "<p>home</p>")
	}))
	defer srv.Close()

	c := newTestCrawler(srv.URL, nil)
	if err := c.RunContext(context.Background()); err == nil {
		t.Error("expected an error when robots.txt blocks the seed")
	}
}

func TestCrawlerRetries(t *testing.T) {
	retryBaseDelay = 5 * time.Millisecond
	defer func() { retryBaseDelay = 500 * time.Millisecond }()

	var mu sync.Mutex
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			writeHTML(w, `<a href="/flaky">flaky</a><a href="/missing">missing</a>`)
		case "/flaky":
			mu.Lock()
			attempts++
			n := attempts
			mu.Unlock()
			if n <= 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			writeHTML(w, "<p>finally</p>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestCrawler(srv.URL, func(cfg *config.Config) { cfg.MaxRetries = 2 })
	c.Run()
	if attempts != 3 {
		t.Errorf("/flaky was tried %d times, want 3 (1 + 2 retries)", attempts)
	}
	st := c.Stats()
	if st.GetCrawledCount() != 2 || st.GetRetryCount() != 2 || st.GetErrorCount() != 1 {
		t.Errorf("crawled=%d retries=%d errors=%d; want 2, 2, 1 (404 is not retried)",
			st.GetCrawledCount(), st.GetRetryCount(), st.GetErrorCount())
	}
}

// A panic in OnPage is recovered: the crawl carries on.
func TestCrawlerRecoversFromPanic(t *testing.T) {
	server := createTestSite()
	defer server.Close()

	c := newTestCrawler(server.URL, nil)
	c.OnPage = func(p Page) {
		if strings.HasSuffix(p.FinalURL, "/about") {
			panic("boom")
		}
	}
	c.Run()
	if c.Stats().GetErrorCount() != 1 {
		t.Errorf("errors = %d, want 1", c.Stats().GetErrorCount())
	}
	if c.Stats().GetCrawledCount() < 3 {
		t.Errorf("crawl stopped after the panic: crawled %d", c.Stats().GetCrawledCount())
	}
}

func TestCrawlerCancel(t *testing.T) {
	srv, _ := wideSite(100, 0, 100*time.Millisecond)
	defer srv.Close()

	c := newTestCrawler(srv.URL, func(cfg *config.Config) { cfg.Workers = 2 })
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := c.RunContext(ctx)
	if err == nil {
		t.Fatal("expected a context error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("cancel took %v to stop the crawl", elapsed)
	}
	if c.Stats().GetCrawledCount() >= 100 {
		t.Error("crawl was not stopped")
	}
}

// Stop the crawl at the page limit, then resume with a bigger limit:
// no page is fetched twice and the crawl picks up where it stopped.
func TestCrawlerCheckpointResume(t *testing.T) {
	srv, hits := wideSite(20, 0, 0)
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "crawl.state")

	c1 := newTestCrawler(srv.URL, func(cfg *config.Config) { cfg.MaxPages = 5 })
	c1.CheckpointPath = path
	c1.Run()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("checkpoint not written: %v", err)
	}

	var cp checkpointFile
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &cp); err != nil || cp.Done != 5 {
		t.Fatalf("checkpoint done=%d err=%v, want 5", cp.Done, err)
	}

	c2 := newTestCrawler(srv.URL, func(cfg *config.Config) { cfg.MaxPages = 100 })
	c2.CheckpointPath = path
	c2.Resume = true
	c2.Run()

	paths, _ := hits.pages()
	seen := map[string]int{}
	for _, p := range paths {
		seen[p]++
		if seen[p] > 1 {
			t.Errorf("%s fetched twice", p)
		}
	}
	if len(seen) != 21 { // "/" + 20 pages
		t.Errorf("fetched %d different pages over both runs, want 21", len(seen))
	}
	if c2.Stats().GetCrawledCount() != 16 {
		t.Errorf("resumed run crawled %d, want 16", c2.Stats().GetCrawledCount())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("checkpoint should be removed once the crawl is complete")
	}
}

// Interrupt a crawl, then resume it: every page ends up crawled.
func TestCrawlerInterruptAndResume(t *testing.T) {
	srv, _ := wideSite(30, 0, 30*time.Millisecond)
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "crawl.state")

	var mu sync.Mutex
	got := map[string]bool{}
	onPage := func(p Page) {
		mu.Lock()
		got[p.FinalURL] = true
		mu.Unlock()
	}

	c1 := newTestCrawler(srv.URL, func(cfg *config.Config) { cfg.Workers = 3 })
	c1.CheckpointPath = path
	c1.OnPage = onPage
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := c1.RunContext(ctx); err == nil {
		t.Fatal("expected the first run to be interrupted")
	}
	mu.Lock()
	first := len(got)
	mu.Unlock()
	if first == 0 || first == 31 {
		t.Fatalf("first run crawled %d pages; test needs a partial crawl", first)
	}

	c2 := newTestCrawler(srv.URL, func(cfg *config.Config) { cfg.Workers = 3 })
	c2.CheckpointPath = path
	c2.Resume = true
	c2.OnPage = onPage
	if err := c2.RunContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 31 {
		t.Errorf("after resume %d of 31 pages crawled", len(got))
	}
}

func TestCrawlerResumeRejectsOtherSeed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crawl.state")
	data, _ := json.Marshal(checkpointFile{Version: checkpointVersion, Seed: "http://other.example/"})
	os.WriteFile(path, data, 0644)

	c := newTestCrawler("http://example.com/", nil)
	c.CheckpointPath = path
	c.Resume = true
	if err := c.RunContext(context.Background()); err == nil {
		t.Error("expected an error for a checkpoint from another seed")
	}
}

// If the seed redirected to another host, a resumed crawl must still
// follow links on that host.
func TestCrawlerResumeKeepsRedirectedSite(t *testing.T) {
	newSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			writeHTML(w, `<a href="/a">a</a><a href="/b">b</a>`)
		case "/a":
			writeHTML(w, `<a href="/a2">a2</a>`)
		case "/b", "/a2":
			writeHTML(w, "<p>leaf</p>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer newSite.Close()
	oldSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, newSite.URL+"/start", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer oldSite.Close()
	path := filepath.Join(t.TempDir(), "crawl.state")

	c1 := newTestCrawler(oldSite.URL, func(cfg *config.Config) { cfg.MaxPages = 1; cfg.Workers = 1 })
	c1.CheckpointPath = path
	c1.Run()

	c2 := newTestCrawler(oldSite.URL, func(cfg *config.Config) { cfg.Workers = 1 })
	c2.CheckpointPath = path
	c2.Resume = true
	c2.Run()
	// /start (run 1), then /a, /b and /a2, which is only linked from /a.
	if got := c2.Stats().GetCrawledCount(); got != 3 {
		t.Errorf("resumed run crawled %d pages, want 3", got)
	}
}
