package robots

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestParseAndAllowed(t *testing.T) {
	body := `
# comment
User-agent: OtherBot
Disallow: /

User-agent: *
Disallow: /private
Allow: /private/public
Disallow: /*.pdf$
Disallow: /search?
Crawl-delay: 2
`
	r := Parse(body, "GoCrawl/1.0")

	tests := []struct {
		path string
		want bool
	}{
		{"/", true},
		{"/about", true},
		{"/private", false},
		{"/private/x", false},
		{"/private/public", true}, // longer Allow beats shorter Disallow
		{"/private/public/a", true},
		{"/files/a.pdf", false},    // "*" wildcard and "$" anchor
		{"/files/a.pdf?x=1", true}, // "$" means the path must END there
		{"/search?q=go", false},
		{"/searchresults", true},
		{"/robots.txt", true}, // always allowed
	}
	for _, tt := range tests {
		if got := r.AllowedPath(tt.path); got != tt.want {
			t.Errorf("AllowedPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
	if r.CrawlDelay != 2*time.Second {
		t.Errorf("CrawlDelay = %v, want 2s", r.CrawlDelay)
	}
}

func TestParseSpecificAgentWins(t *testing.T) {
	body := "User-agent: *\nDisallow: /\n\nUser-agent: gocrawl\nDisallow: /admin\n"
	r := Parse(body, "GoCrawl")
	if !r.AllowedPath("/page") || r.AllowedPath("/admin") {
		t.Error("the group naming GoCrawl (any case) should replace the * group")
	}
}

func TestParseGroupWithSeveralAgents(t *testing.T) {
	body := "User-agent: a\nUser-agent: GoCrawl\nDisallow: /x\n"
	if Parse(body, "GoCrawl").AllowedPath("/x") {
		t.Error("a group listing several agents should apply to each")
	}
}

func TestParseTieGoesToAllow(t *testing.T) {
	r := Parse("User-agent: *\nDisallow: /page\nAllow: /page\n", "GoCrawl")
	if !r.AllowedPath("/page") {
		t.Error("equal-length Allow and Disallow: Allow should win")
	}
}

func TestParseEmptyDisallowAllowsAll(t *testing.T) {
	r := Parse("User-agent: *\nDisallow:\n", "GoCrawl")
	if !r.AllowedPath("/anything") {
		t.Error(`"Disallow:" with no path should allow everything`)
	}
}

func TestParseNoMatchingGroup(t *testing.T) {
	r := Parse("User-agent: OtherBot\nDisallow: /\n", "GoCrawl")
	if !r.AllowedPath("/x") {
		t.Error("no group for us and no * group: everything is allowed")
	}
}

// RFC 9309: 4xx → allow all; 5xx → disallow all.
func TestCacheStatusHandling(t *testing.T) {
	for _, tt := range []struct {
		status int
		want   bool
	}{
		{http.StatusNotFound, true},
		{http.StatusForbidden, true},
		{http.StatusInternalServerError, false},
		{http.StatusServiceUnavailable, false},
		{http.StatusTooManyRequests, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tt.status)
		}))
		u, _ := url.Parse(srv.URL + "/page")
		got := NewCache("GoCrawl", time.Second, nil).Get(context.Background(), u).Allowed(u)
		if got != tt.want {
			t.Errorf("robots.txt status %d: allowed=%v, want %v", tt.status, got, tt.want)
		}
		srv.Close()
	}
}

// Many goroutines asking about a new site cause ONE robots.txt request.
func TestCacheFetchesOncePerSite(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		fmt.Fprint(w, "User-agent: *\nDisallow: /no\n")
	}))
	defer srv.Close()

	cache := NewCache("GoCrawl", time.Second, nil)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, _ := url.Parse(srv.URL + "/no")
			if cache.Get(context.Background(), u).Allowed(u) {
				t.Error("/no should be disallowed")
			}
		}()
	}
	wg.Wait()
	if requests != 1 {
		t.Errorf("robots.txt fetched %d times, want 1", requests)
	}
}

func TestCacheUnreachableSite(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:1/page") // nothing listens on port 1
	if NewCache("GoCrawl", time.Second, nil).Get(context.Background(), u).Allowed(u) {
		t.Error("unreachable robots.txt should mean disallow all")
	}
}
