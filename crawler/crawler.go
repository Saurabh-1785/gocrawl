package crawler

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Saurabh-1785/gocrawl/config"
	"github.com/Saurabh-1785/gocrawl/fetcher"
	"github.com/Saurabh-1785/gocrawl/robots"
	"github.com/Saurabh-1785/gocrawl/urlutil"
)

// maxCrawlDelay caps a robots.txt Crawl-delay, so one site asking for
// "Crawl-delay: 3600" can't stall the crawl for hours per page.
const maxCrawlDelay = 30 * time.Second

// Crawler is the engine that coordinates everything.
type Crawler struct {
	config      config.Config
	frontier    *frontier
	rateLimiter *RateLimiter
	stats       *CrawlStats
	robots      *robots.Cache // nil when robots.txt is ignored
	seedKey     string

	// OnPage, if set, is called for every HTML page fetched successfully.
	// It runs inside worker goroutines, so it must be thread-safe.
	// A panic inside it is recovered and counted as an error.
	OnPage func(Page)

	// Logger receives progress messages. nil means log.Default().
	Logger *log.Logger

	// Transport sends the HTTP requests. nil means http.DefaultTransport,
	// which lets a program install its own (e.g. one that blocks private IPs).
	Transport http.RoundTripper

	// CheckpointPath, if set, is where crawl progress is saved: every
	// CheckpointEvery (default 10s) and when the crawl stops. With Resume
	// set, a later run continues from that file instead of the seed.
	CheckpointPath  string
	CheckpointEvery time.Duration
	Resume          bool

	siteMu sync.RWMutex
	sites  map[string]bool // urlutil.SiteHost values we are allowed to crawl

	mu       sync.Mutex
	cancel   context.CancelFunc
	fatalErr error
	log      *log.Logger
}

// NewCrawler creates a new Crawler with the given configuration.
func NewCrawler(cfg config.Config) *Crawler {
	c := &Crawler{
		config:      cfg,
		frontier:    newFrontier(cfg.MaxPages),
		rateLimiter: NewRateLimiter(cfg.CrawlDelay),
		stats:       NewCrawlStats(),
		sites:       make(map[string]bool),
	}
	c.addSite(cfg.SeedURL)
	return c
}

// Run crawls until done. It is RunContext without cancellation.
func (c *Crawler) Run() {
	_ = c.RunContext(context.Background())
}

// RunContext crawls until there is nothing left to do, the page limit is
// reached, ctx is cancelled, or Stop is called. It returns ctx.Err() if
// cancelled, the error given to Stop, or nil.
//
// The flow:
//  1. Queue the seed URL (or load a checkpoint when resuming)
//  2. Start N worker goroutines; each loops: take task → process → report done
//  3. Wait for all workers to exit (the frontier tells them when to stop)
//  4. Save or remove the checkpoint
func (c *Crawler) RunContext(ctx context.Context) error {
	c.log = c.Logger
	if c.log == nil {
		c.log = log.Default()
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()

	if c.config.RespectRobots {
		c.robots = robots.NewCache(c.userAgent(), c.config.Timeout, c.Transport)
	}

	seed, seedKey, err := cleanSeed(c.config.SeedURL)
	if err != nil {
		return err
	}
	c.seedKey = seedKey

	c.log.Printf("[INFO]  Starting crawl from: %s", seed)
	c.log.Printf("[INFO]  Config: depth=%d, pages=%d, workers=%d, delay=%v, retries=%d, robots=%v",
		c.config.MaxDepth, c.config.MaxPages, c.config.Workers, c.config.CrawlDelay,
		c.config.MaxRetries, c.config.RespectRobots)

	resumed, err := c.restoreCheckpoint()
	if err != nil {
		return err
	}
	if !resumed {
		u, _ := url.Parse(seed)
		if !c.robotsAllow(ctx, u) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("robots.txt does not allow crawling %s", seed)
		}
		c.frontier.Push(CrawlTask{URL: seed, Key: seedKey, Depth: 0})
	}

	// When ctx is cancelled, stop handing out tasks. Workers in the middle
	// of a task see ctx.Done() in their fetch or wait and put it back.
	stopClose := context.AfterFunc(ctx, c.frontier.Close)
	defer stopClose()

	var wg sync.WaitGroup // counts WORKERS here; the frontier counts tasks
	for i := 1; i <= c.config.Workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			c.worker(ctx, id)
		}(i)
	}

	stopCheckpoints := c.startCheckpoints()
	wg.Wait()
	stopCheckpoints()
	c.finishCheckpoint()

	c.mu.Lock()
	fatal := c.fatalErr
	c.mu.Unlock()
	if fatal != nil {
		return fatal
	}
	return ctx.Err()
}

// Stop ends the crawl early because of err (for example, the disk is full).
// RunContext then returns err. Only the first error is kept.
func (c *Crawler) Stop(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fatalErr == nil {
		c.fatalErr = err
	}
	if c.cancel != nil {
		c.cancel()
	}
}

// Stats returns the crawler's statistics.
func (c *Crawler) Stats() *CrawlStats {
	return c.stats
}

// UniqueURLs returns how many different URLs were seen (queued, crawled,
// failed or blocked).
func (c *Crawler) UniqueURLs() int {
	return c.frontier.SeenCount()
}

// cleanSeed removes the fragment and returns (URL to fetch, dedupe key).
func cleanSeed(raw string) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", fmt.Errorf("invalid seed URL: %w", err)
	}
	u.Fragment = ""
	u.RawFragment = ""
	seed := u.String()
	key, err := urlutil.NormalizeURL(seed)
	if err != nil {
		return "", "", fmt.Errorf("invalid seed URL: %w", err)
	}
	return seed, key, nil
}

func (c *Crawler) userAgent() string {
	if c.config.UserAgent != "" {
		return c.config.UserAgent
	}
	return fetcher.DefaultUserAgent
}

// addSite allows crawling the website of rawURL.
func (c *Crawler) addSite(rawURL string) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	c.siteMu.Lock()
	defer c.siteMu.Unlock()
	c.sites[urlutil.SiteHost(u.Scheme, u.Host)] = true
}

// siteList returns the allowed sites, sorted (saved in checkpoints).
func (c *Crawler) siteList() []string {
	c.siteMu.RLock()
	defer c.siteMu.RUnlock()
	list := make([]string, 0, len(c.sites))
	for s := range c.sites {
		list = append(list, s)
	}
	sort.Strings(list)
	return list
}

// inSite reports whether rawURL is on a website we may crawl.
// "www." and the plain host count as the same site.
func (c *Crawler) inSite(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	c.siteMu.RLock()
	defer c.siteMu.RUnlock()
	return c.sites[urlutil.SiteHost(u.Scheme, u.Host)]
}

// robotsAllow reports whether robots.txt lets us fetch u.
// It also applies the site's Crawl-delay to the rate limiter.
func (c *Crawler) robotsAllow(ctx context.Context, u *url.URL) bool {
	if c.robots == nil {
		return true
	}
	rules := c.robots.Get(ctx, u)
	if rules.CrawlDelay > 0 {
		delay := rules.CrawlDelay
		if delay > maxCrawlDelay {
			delay = maxCrawlDelay
		}
		c.rateLimiter.SetHostDelay(u.String(), delay)
	}
	return rules.Allowed(u)
}
