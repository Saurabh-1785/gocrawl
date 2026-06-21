package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Saurabh-1785/gocrawl/config"
	"github.com/Saurabh-1785/gocrawl/crawler"
	"github.com/Saurabh-1785/gocrawl/docstore"
	"github.com/Saurabh-1785/gocrawl/extractor"
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
	outputFile := flag.String("output", "output.jsonl", "Output JSONL file path for extracted documents (appends to existing file)")

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

	// Set up the document store (JSONL output)
	store, err := docstore.NewDocStore(*outputFile)
	if err != nil {
		log.Fatalf("[ERROR] Failed to create document store: %v", err)
	}
	defer store.Close()

	log.Printf("[INFO]  Output file: %s (append mode)", *outputFile)

	// Create the crawler and wire up the extraction pipeline
	c := crawler.NewCrawler(cfg)

	// Wire: Crawler → Extractor → Document Store
	c.OnPageFetched = func(pageURL string, body string, statusCode int) {
		doc := extractor.Extract(pageURL, body, statusCode)
		if err := store.Save(doc); err != nil {
			log.Printf("[ERROR] Failed to save document for %s: %v", pageURL, err)
		}
	}

	c.Run()

	log.Printf("[INFO]  Crawl completed! %d documents saved to %s", store.Count(), *outputFile)
}

// printBanner displays a startup message
func printBanner() {
	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════╗")
	fmt.Println("  ║           GoCrawl v2.0            ║")
	fmt.Println("  ║     Crawl · Extract · Store       ║")
	fmt.Println("  ╚═══════════════════════════════════╝")
	fmt.Println()
}
