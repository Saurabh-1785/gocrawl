package crawler

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/Saurabh-1785/gocrawl/fetcher"
	"github.com/Saurabh-1785/gocrawl/parser"
	"github.com/Saurabh-1785/gocrawl/urlutil"
	"golang.org/x/net/html"
)

// maxBackoff caps the wait between retries.
const maxBackoff = 30 * time.Second

// retryBaseDelay is the wait before the first retry. A variable so tests can shrink it.
var retryBaseDelay = 500 * time.Millisecond

// worker takes tasks from the frontier until it says the crawl is over.
func (c *Crawler) worker(ctx context.Context, id int) {
	for {
		task, ok := c.frontier.Next()
		if !ok {
			return
		}
		c.frontier.Done(task, c.processTask(ctx, id, task))
	}
}

// processTask handles one task and says how it went.
//
//  1. Fetch the page (rate limited, with retries)
//  2. Skip it if a redirect landed on a page we already have
//  3. Parse the HTML once
//  4. Extract links, call OnPage
//  5. Queue new links: same site, not seen, within depth, allowed by robots.txt
func (c *Crawler) processTask(ctx context.Context, id int, task CrawlTask) (out outcome) {
	// One bad page (or a bug in the OnPage callback) must not kill the crawl.
	defer func() {
		if r := recover(); r != nil {
			c.log.Printf("[ERROR] [Worker %d] panic while processing %s: %v\n%s", id, task.URL, r, debug.Stack())
			c.stats.IncrementErrors()
			out = failed
		}
	}()

	c.log.Printf("[INFO]  [Worker %d] Crawling: %s (depth: %d)", id, task.URL, task.Depth)

	res, err := c.fetchWithRetry(ctx, id, task)
	if err != nil {
		if ctx.Err() != nil {
			return requeue // stopped, not failed: a resume will try again
		}
		if errors.Is(err, fetcher.ErrNotHTML) || errors.Is(err, fetcher.ErrRedirectBlocked) {
			c.log.Printf("[INFO]  [Worker %d] Skipped %s: %v", id, task.URL, err)
			c.stats.IncrementSkipped()
			return failed
		}
		c.log.Printf("[ERROR] [Worker %d] Error fetching %s: %v", id, task.URL, err)
		c.stats.IncrementErrors()
		return failed
	}

	// A redirect may land on a page that another task already has
	// (e.g. "/blog" → "/blog/"). Only one of them should be processed.
	if finalKey, err := urlutil.NormalizeURL(res.FinalURL); err == nil && finalKey != task.Key {
		if !c.frontier.MarkSeen(finalKey) {
			c.log.Printf("[INFO]  [Worker %d] Skipped %s: redirects to %s, already seen", id, task.URL, res.FinalURL)
			c.stats.IncrementSkipped()
			return failed
		}
	}
	// If the seed redirects to another host (golang.org → go.dev),
	// that host becomes part of the site we crawl.
	if task.Depth == 0 {
		c.addSite(res.FinalURL)
	}

	root, err := html.Parse(strings.NewReader(res.Body))
	if err != nil {
		c.log.Printf("[ERROR] [Worker %d] Error parsing %s: %v", id, task.URL, err)
		c.stats.IncrementErrors()
		return failed
	}

	c.stats.IncrementCrawled()
	c.log.Printf("[INFO]  [Worker %d] Fetched %s (status: %d, size: %d bytes)", id, res.FinalURL, res.StatusCode, len(res.Body))

	// Resolve links against the FINAL URL as served. A page at "/blog/"
	// with a link "post-1" means "/blog/post-1", not "/post-1".
	links := parser.Links(root, res.FinalURL)
	c.log.Printf("[DEBUG] [Worker %d] Found %d links on %s", id, len(links), res.FinalURL)

	if c.OnPage != nil {
		c.OnPage(Page{
			URL:        task.URL,
			FinalURL:   res.FinalURL,
			StatusCode: res.StatusCode,
			Body:       res.Body,
			Root:       root,
			Depth:      task.Depth,
		})
	}

	if task.Depth < c.config.MaxDepth { // else its links would be too deep
		for _, link := range links {
			c.consider(ctx, link, task.Depth+1)
		}
	}
	if ctx.Err() != nil {
		// Stopped during this page (Ctrl-C, or Stop because a document
		// couldn't be saved): some links or the document may be missing.
		// Put the page back so a resume does it again. OnPage may then see
		// it twice; the DocStore dedupes by ID.
		return requeue
	}
	return success
}

// consider queues one discovered link if we should crawl it.
func (c *Crawler) consider(ctx context.Context, link string, depth int) {
	key, err := urlutil.NormalizeURL(link)
	if err != nil || !c.inSite(link) || c.frontier.Seen(key) {
		return
	}
	u, err := url.Parse(link)
	if err != nil {
		return
	}
	if !c.robotsAllow(ctx, u) {
		if ctx.Err() != nil {
			return // not a robots answer, just shutting down
		}
		if c.frontier.MarkSeen(key) { // count each blocked URL once
			c.stats.IncrementRobotsBlocked()
			c.log.Printf("[DEBUG] Blocked by robots.txt: %s", link)
		}
		return
	}
	c.frontier.Push(CrawlTask{URL: link, Key: key, Depth: depth})
}

// fetchWithRetry fetches a task's URL. Network errors, timeouts, 429 and
// 5xx are retried up to MaxRetries times with exponential backoff. Every
// attempt waits for the rate limiter, so retries stay polite too.
func (c *Crawler) fetchWithRetry(ctx context.Context, id int, task CrawlTask) (fetcher.Result, error) {
	opts := fetcher.Options{
		Timeout:   c.config.Timeout,
		UserAgent: c.userAgent(),
		Transport: c.Transport,
		AllowRedirect: func(to *url.URL) bool {
			// The seed may redirect anywhere (http → https, golang.org →
			// go.dev). Other pages must stay on our site.
			if task.Depth > 0 && !c.inSite(to.String()) {
				return false
			}
			return c.robotsAllow(ctx, to)
		},
	}

	for attempt := 0; ; attempt++ {
		if err := c.rateLimiter.WaitContext(ctx, task.URL); err != nil {
			return fetcher.Result{}, err
		}
		res, err := fetcher.Fetch(ctx, task.URL, opts)
		if err == nil {
			return res, nil
		}
		if attempt >= c.config.MaxRetries || ctx.Err() != nil || !fetcher.IsRetryable(err) {
			return res, err
		}

		wait := backoff(attempt, err)
		c.stats.IncrementRetries()
		c.log.Printf("[INFO]  [Worker %d] Retry %d/%d for %s in %v: %v",
			id, attempt+1, c.config.MaxRetries, task.URL, wait.Round(time.Millisecond), err)

		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return res, ctx.Err()
		}
	}
}

// backoff returns how long to wait before retry number attempt+1:
// the server's Retry-After if it sent one, else 500ms, 1s, 2s, ...
// plus up to 50% random jitter (so workers don't all retry at once),
// capped at maxBackoff.
func backoff(attempt int, err error) time.Duration {
	var se *fetcher.StatusError
	if errors.As(err, &se) && se.RetryAfter > 0 {
		return min(se.RetryAfter, maxBackoff)
	}
	d := retryBaseDelay << attempt
	d += time.Duration(rand.Int64N(int64(d)/2 + 1))
	return min(d, maxBackoff)
}
