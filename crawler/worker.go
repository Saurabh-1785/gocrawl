package crawler

import (
	"log"
	"sync"

	"github.com/Saurabh/gocrawl/fetcher"
	"github.com/Saurabh/gocrawl/parser"
	"github.com/Saurabh/gocrawl/urlutil"
)

// worker is a goroutine that processes crawl tasks from the channel.
// Each worker runs in its own goroutine, pulling tasks and crawling pages.
//
// The worker loop:
//  1. Pull a task from the channel
//  2. Check limits (pages, depth, visited)
//  3. Fetch the page
//  4. Extract links
//  5. Send new tasks for discovered links
//  6. Mark task as done
func (c *Crawler) worker(id int, tasks chan CrawlTask, wg *sync.WaitGroup) {
	for task := range tasks {
		c.processTask(id, task, tasks, wg)
	}
}

// processTask handles a single crawl task.
// This is separated from the worker loop for clarity.
func (c *Crawler) processTask(workerID int, task CrawlTask, tasks chan CrawlTask, wg *sync.WaitGroup) {
	// Always mark this task as done when we finish, no matter what happens
	defer wg.Done()

	// Check 1: Have we already crawled enough pages?
	if c.stats.GetCrawledCount() >= c.config.MaxPages {
		c.stats.IncrementSkipped()
		return
	}

	// Check 2: Is this URL deeper than our maximum depth?
	if task.Depth > c.config.MaxDepth {
		c.stats.IncrementSkipped()
		return
	}

	// Check 3: Have we already visited this URL?
	// MarkVisited returns true if the URL is NEW (not visited before).
	// This is atomic (check + mark in one step) to prevent race conditions.
	if !c.visited.MarkVisited(task.URL) {
		c.stats.IncrementSkipped()
		return
	}

	// Double-check page limit after marking as visited
	if c.stats.GetCrawledCount() >= c.config.MaxPages {
		c.stats.IncrementSkipped()
		return
	}

	log.Printf("[INFO]  [Worker %d] Crawling: %s (depth: %d)", workerID, task.URL, task.Depth)

	// Wait for rate limiter (may sleep if we recently hit this domain)
	c.rateLimiter.Wait(task.URL)

	// Fetch the page
	body, statusCode, err := fetcher.FetchPage(task.URL, c.config.Timeout)
	if err != nil {
		log.Printf("[ERROR] [Worker %d] Error fetching %s: %v", workerID, task.URL, err)
		c.stats.IncrementErrors()
		return
	}

	// Count this as a successfully crawled page
	c.stats.IncrementCrawled()

	log.Printf("[INFO]  [Worker %d] Fetched %s (status: %d, size: %d bytes)",
		workerID, task.URL, statusCode, len(body))

	// Extract links from the page
	links := parser.ExtractLinks(body, task.URL)
	log.Printf("[DEBUG] [Worker %d] Found %d links on %s", workerID, len(links), task.URL)

	// Process each discovered link
	for _, link := range links {
		// Only follow links on the same domain
		if !urlutil.IsSameDomain(c.config.SeedURL, link) {
			continue
		}

		// Don't bother if we've already visited it
		if c.visited.IsVisited(link) {
			continue
		}

		// Don't schedule if we've hit the page limit
		if c.stats.GetCrawledCount() >= c.config.MaxPages {
			break
		}

		// Don't schedule if the next depth would exceed the limit
		if task.Depth+1 > c.config.MaxDepth {
			continue
		}

		// Schedule this link as a new task
		wg.Add(1)
		// Use a select to avoid blocking if the channel is full
		select {
		case tasks <- CrawlTask{URL: link, Depth: task.Depth + 1}:
			// Task sent successfully
		default:
			// Channel is full, skip this link
			wg.Done()
			c.stats.IncrementSkipped()
		}
	}
}
