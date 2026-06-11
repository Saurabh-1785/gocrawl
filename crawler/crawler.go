package crawler

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Saurabh/gocrawl/config"
	"github.com/Saurabh/gocrawl/urlutil"
)

// Crawler is the main engine that coordinates everything.
// It holds the configuration, visited URLs, rate limiter, and stats.
type Crawler struct {
	config      config.Config
	visited     *VisitedStore
	rateLimiter *RateLimiter
	stats       *CrawlStats
}

// NewCrawler creates a new Crawler with the given configuration.
func NewCrawler(cfg config.Config) *Crawler {
	return &Crawler{
		config:      cfg,
		visited:     NewVisitedStore(),
		rateLimiter: NewRateLimiter(cfg.CrawlDelay),
		stats:       NewCrawlStats(),
	}
}

// Run starts the crawling process. This is the main entry point.
//
// Here's the high-level flow:
//  1. Create a buffered channel as the task queue
//  2. Start N worker goroutines
//  3. Send the seed URL as the first task
//  4. Wait for all tasks to complete
//  5. Close the channel (workers exit)
//  6. Print the final report
func (c *Crawler) Run() {
	log.Printf("[INFO]  Starting crawl from: %s", c.config.SeedURL)
	log.Printf("[INFO]  Config: depth=%d, pages=%d, workers=%d, delay=%v",
		c.config.MaxDepth, c.config.MaxPages, c.config.Workers, c.config.CrawlDelay)

	// Create the task channel.
	// We make it buffered (size = MaxPages) so workers can send new tasks
	// without blocking, which prevents deadlocks.
	tasks := make(chan CrawlTask, c.config.MaxPages)

	// WaitGroup tracks how many tasks are "in flight" (queued or being processed).
	// We Add(1) before sending a task, and Done() after processing it.
	// When the counter hits 0, all work is done.
	var wg sync.WaitGroup

	// done channel signals when all crawling is complete
	done := make(chan bool)

	// Start the worker goroutines
	for i := 1; i <= c.config.Workers; i++ {
		go c.worker(i, tasks, &wg)
	}

	// Normalize the seed URL before sending it
	seedNormalized, err := urlutil.NormalizeURL(c.config.SeedURL)
	if err != nil {
		log.Printf("[ERROR] Failed to normalize seed URL: %v", err)
		close(tasks)
		return
	}

	// Send the seed URL as the first task
	wg.Add(1)
	tasks <- CrawlTask{
		URL:   seedNormalized,
		Depth: 0,
	}

	// Wait for all tasks to complete in a separate goroutine,
	// then close the channel so workers exit their "for range" loop.
	go func() {
		wg.Wait()
		close(tasks)
		done <- true
	}()

	// Block here until everything is done
	<-done

	// Print the final crawl report
	c.printReport()
}

// printReport displays the final crawl summary.
func (c *Crawler) printReport() {
	elapsed := time.Since(c.stats.StartTime)

	fmt.Println()
	fmt.Println("===========================================")
	fmt.Println("          CRAWL REPORT")
	fmt.Println("===========================================")
	fmt.Printf("  Seed URL:       %s\n", c.config.SeedURL)
	fmt.Printf("  Pages Crawled:  %d\n", c.stats.PagesCrawled)
	fmt.Printf("  Pages Skipped:  %d\n", c.stats.PagesSkipped)
	fmt.Printf("  Errors:         %d\n", c.stats.Errors)
	fmt.Printf("  Unique URLs:    %d\n", c.visited.Count())
	fmt.Printf("  Time Elapsed:   %v\n", elapsed.Round(time.Millisecond))
	fmt.Println("===========================================")

	// Print all visited URLs
	fmt.Println()
	fmt.Println("Visited URLs:")
	for i, u := range c.visited.GetAll() {
		fmt.Printf("  %d. %s\n", i+1, u)
	}
	fmt.Println()
}
