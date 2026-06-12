package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Saurabh-1785/gocrawl/config"
	"github.com/Saurabh-1785/gocrawl/crawler"
)

func main() {
	// Define command-line flags
	// These let the user control how the crawler behaves
	seedURL := flag.String("url", "", "Seed URL to start crawling (required)")
	maxDepth := flag.Int("depth", 2, "Maximum crawl depth (0 = seed page only)")
	maxPages := flag.Int("pages", 50, "Maximum number of pages to crawl")
	workers := flag.Int("workers", 3, "Number of concurrent workers")
	timeout := flag.Duration("timeout", 10*time.Second, "HTTP request timeout (e.g., 10s, 30s)")
	crawlDelay := flag.Duration("delay", 1*time.Second, "Delay between requests to the same domain (e.g., 1s, 500ms)")

	// Parse the flags from the command line
	flag.Parse()

	// Build the configuration struct
	cfg := config.Config{
		SeedURL:    *seedURL,
		MaxDepth:   *maxDepth,
		MaxPages:   *maxPages,
		Workers:    *workers,
		Timeout:    *timeout,
		CrawlDelay: *crawlDelay,
	}

	// Validate the configuration
	if err := config.ValidateConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		fmt.Println("Usage:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Print a nice startup banner
	printBanner()

	// Create the crawler and start crawling!
	c := crawler.NewCrawler(cfg)
	c.Run()

	log.Println("[INFO]  Crawl completed successfully!")
}

// printBanner displays a startup message
func printBanner() {
	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════╗")
	fmt.Println("  ║             GoCrawl v1.0          ║")
	fmt.Println("  ║        Concurrent Web Crawler     ║")
	fmt.Println("  ╚═══════════════════════════════════╝")
	fmt.Println()
}
