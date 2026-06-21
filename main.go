package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Saurabh-1785/gocrawl/crawl"
)

func main() {
	// Define command-line flags
	seedURL := flag.String("url", "", "Seed URL to start crawling (required)")
	maxDepth := flag.Int("depth", 2, "Maximum crawl depth (0 = seed page only)")
	maxPages := flag.Int("pages", 50, "Maximum number of pages to crawl")
	workers := flag.Int("workers", 3, "Number of concurrent workers")
	timeout := flag.Duration("timeout", 10*time.Second, "HTTP request timeout (e.g., 10s, 30s)")
	crawlDelay := flag.Duration("delay", 1*time.Second, "Delay between requests to the same domain (e.g., 1s, 500ms)")
	outputFile := flag.String("output", "output.jsonl", "Output JSONL file path for extracted documents (appends to existing file)")

	flag.Parse()

	if *seedURL == "" {
		fmt.Fprintf(os.Stderr, "Error: seed URL is required (use -url flag)\n\n")
		fmt.Println("Usage:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Print a nice startup banner
	printBanner()

	// Build config and run using the public API
	cfg := crawl.CrawlConfig{
		SeedURL:    *seedURL,
		MaxDepth:   *maxDepth,
		MaxPages:   *maxPages,
		Workers:    *workers,
		Timeout:    *timeout,
		CrawlDelay: *crawlDelay,
		OutputPath: *outputFile,
	}

	result, err := crawl.Run(cfg)
	if err != nil {
		log.Fatalf("[ERROR] Crawl failed: %v", err)
	}

	// Print summary
	fmt.Println()
	fmt.Println("===========================================")
	fmt.Println("          CRAWL COMPLETE")
	fmt.Println("===========================================")
	fmt.Printf("  Documents Written:  %d\n", result.DocumentsWritten)
	fmt.Printf("  Pages Visited:      %d\n", result.PagesVisited)
	fmt.Printf("  Pages Failed:       %d\n", result.PagesFailed)
	fmt.Printf("  Duration:           %v\n", result.Duration.Round(time.Millisecond))
	fmt.Printf("  Output:             %s\n", result.OutputPath)
	fmt.Println("===========================================")
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
