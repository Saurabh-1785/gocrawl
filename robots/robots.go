// Package robots reads robots.txt files and answers "may I crawl this URL?".
//
// It follows RFC 9309, the robots.txt standard:
//   - Rules come in groups. A group starts with one or more "User-agent:" lines.
//   - We use the groups that name our product token (for example "GoCrawl"),
//     or the "*" group if none does.
//   - The longest matching rule wins. If an Allow and a Disallow rule are the
//     same length, Allow wins.
//   - "*" matches any characters and "$" anchors the end of the path.
//   - /robots.txt itself is always allowed.
//
// It also reads the non-standard but common "Crawl-delay:" line.
package robots

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxSize is how much of a robots.txt file we read (RFC 9309 asks for at least 500 KiB).
const maxSize = 500 * 1024

// Rules are the robots.txt rules that apply to one crawler.
type Rules struct {
	rules      []rule
	CrawlDelay time.Duration // 0 if the file sets none
}

type rule struct {
	pattern string
	allow   bool
}

// AllowAll is used when a site has no robots.txt.
var AllowAll = &Rules{}

// DisallowAll is used when robots.txt can't be read because the server
// failed: the standard says to assume everything is off limits.
var DisallowAll = &Rules{rules: []rule{{pattern: "/", allow: false}}}

// Parse reads a robots.txt body and returns the rules for userAgent.
// userAgent is the product token, e.g. "GoCrawl" (matched case-insensitively).
func Parse(body string, userAgent string) *Rules {
	agent := strings.ToLower(productToken(userAgent))

	type group struct {
		agents []string
		rules  []rule
		delay  time.Duration
	}
	var groups []*group
	var current *group
	inRules := false // true once the current group has seen a rule line

	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 64*1024), maxSize)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		switch key {
		case "user-agent":
			// A user-agent line after rules starts a new group.
			if current == nil || inRules {
				current = &group{}
				groups = append(groups, current)
				inRules = false
			}
			current.agents = append(current.agents, strings.ToLower(value))
		case "allow", "disallow":
			if current == nil {
				continue // rule before any user-agent line: ignored
			}
			inRules = true
			if value == "" {
				continue // "Disallow:" with no path means allow everything
			}
			current.rules = append(current.rules, rule{pattern: value, allow: key == "allow"})
		case "crawl-delay":
			if current == nil {
				continue
			}
			inRules = true
			if secs, err := strconv.ParseFloat(value, 64); err == nil && secs > 0 {
				current.delay = time.Duration(secs * float64(time.Second))
			}
		}
	}

	// Combine every group that names us. If none does, use the "*" groups.
	pick := func(match func(string) bool) *Rules {
		var out *Rules
		for _, g := range groups {
			for _, a := range g.agents {
				if match(a) {
					if out == nil {
						out = &Rules{}
					}
					out.rules = append(out.rules, g.rules...)
					if g.delay > out.CrawlDelay {
						out.CrawlDelay = g.delay
					}
					break
				}
			}
		}
		return out
	}
	if r := pick(func(a string) bool { return a == agent }); r != nil {
		return r
	}
	if r := pick(func(a string) bool { return a == "*" }); r != nil {
		return r
	}
	return AllowAll
}

// Allowed reports whether the crawler may fetch u.
func (r *Rules) Allowed(u *url.URL) bool {
	return r.AllowedPath(u.RequestURI())
}

// AllowedPath reports whether the crawler may fetch path (path plus query).
func (r *Rules) AllowedPath(path string) bool {
	if path == "/robots.txt" {
		return true
	}
	bestLen := -1
	allowed := true
	for _, rl := range r.rules {
		if !match(rl.pattern, path) {
			continue
		}
		n := len(rl.pattern)
		if n > bestLen || (n == bestLen && rl.allow) {
			bestLen = n
			allowed = rl.allow
		}
	}
	return allowed
}

// match reports whether a robots.txt pattern matches the start of path.
// "*" matches any run of characters; a final "$" means "end of path".
func match(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	if anchored {
		pattern = strings.TrimSuffix(pattern, "$")
	}
	parts := strings.Split(pattern, "*")

	if !strings.HasPrefix(path, parts[0]) {
		return false
	}
	pos := len(parts[0])
	if len(parts) == 1 {
		return !anchored || pos == len(path)
	}
	for i := 1; i < len(parts); i++ {
		p := parts[i]
		if i == len(parts)-1 && anchored {
			return len(path)-len(p) >= pos && strings.HasSuffix(path, p)
		}
		idx := strings.Index(path[pos:], p)
		if idx < 0 {
			return false
		}
		pos += idx + len(p)
	}
	return true
}

// productToken turns "GoCrawl/1.0 (+info)" into "GoCrawl".
func productToken(userAgent string) string {
	token := strings.TrimSpace(userAgent)
	if i := strings.IndexAny(token, "/ ("); i >= 0 {
		token = token[:i]
	}
	if token == "" {
		return "*"
	}
	return token
}

// Cache fetches robots.txt once per site and remembers the answer.
// It is safe for many goroutines. If several ask about a new site at the
// same moment, only one request is made and the rest wait for it.
type Cache struct {
	userAgent string
	client    *http.Client

	mu    sync.Mutex
	sites map[string]*entry // "scheme://host" → rules
}

type entry struct {
	ready chan struct{} // closed when rules is set
	rules *Rules
}

// NewCache returns a Cache. transport may be nil (http.DefaultTransport).
func NewCache(userAgent string, timeout time.Duration, transport http.RoundTripper) *Cache {
	return &Cache{
		userAgent: userAgent,
		client:    &http.Client{Timeout: timeout, Transport: transport},
		sites:     make(map[string]*entry),
	}
}

// Get returns the rules for the site of u, fetching robots.txt if needed.
func (c *Cache) Get(ctx context.Context, u *url.URL) *Rules {
	key := strings.ToLower(u.Scheme + "://" + u.Host)

	c.mu.Lock()
	e, ok := c.sites[key]
	if !ok {
		e = &entry{ready: make(chan struct{})}
		c.sites[key] = e
	}
	c.mu.Unlock()

	if !ok {
		e.rules = c.fetch(ctx, key)
		close(e.ready)
	}

	select {
	case <-e.ready:
		return e.rules
	case <-ctx.Done():
		return DisallowAll
	}
}

// fetch downloads and parses one robots.txt, applying the RFC 9309 rules:
//   - 2xx: parse the file
//   - 4xx (e.g. 404 Not Found): no rules, everything is allowed
//   - 5xx, 429 or a network error: assume everything is disallowed
func (c *Cache) fetch(ctx context.Context, site string) *Rules {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, site+"/robots.txt", nil)
	if err != nil {
		return DisallowAll
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return DisallowAll
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
		if err != nil {
			return DisallowAll
		}
		return Parse(string(body), c.userAgent)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return DisallowAll
	case resp.StatusCode >= 400:
		return AllowAll
	default:
		return DisallowAll // e.g. 3xx left after too many redirects
	}
}

// String is handy for logs and tests.
func (r *Rules) String() string {
	return fmt.Sprintf("robots.Rules{%d rules, crawl-delay %v}", len(r.rules), r.CrawlDelay)
}
