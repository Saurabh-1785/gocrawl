package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Saurabh-1785/gocrawl/crawl"
)

func main() {
	seedURL := flag.String("url", "", "Seed URL to start crawling (required)")
	maxDepth := flag.Int("depth", crawl.DefaultMaxDepth, "Maximum crawl depth (0 = seed page only)")
	maxPages := flag.Int("pages", crawl.DefaultMaxPages, "Maximum number of pages to crawl")
	workers := flag.Int("workers", crawl.DefaultWorkers, "Number of concurrent workers")
	timeout := flag.Duration("timeout", crawl.DefaultTimeout, "HTTP request timeout (e.g., 10s, 30s)")
	crawlDelay := flag.Duration("delay", crawl.DefaultCrawlDelay, "Delay between requests to the same domain (0 = none)")
	retries := flag.Int("retries", crawl.DefaultMaxRetries, "Retries for timeouts, 429 and 5xx (0 = none)")
	outputFile := flag.String("output", "output.jsonl", "Output JSONL file (appends; documents already in it are skipped)")
	checkpoint := flag.String("checkpoint", "", "File to save crawl progress to, e.g. crawl.state")
	resume := flag.Bool("resume", false, "Continue from the -checkpoint file instead of starting over")
	ignoreRobots := flag.Bool("ignore-robots", false, "Do not obey robots.txt (only for sites you own)")
	userAgent := flag.String("user-agent", "", "User-Agent header (default \"GoCrawl/1.0\")")

	flag.Parse()

	if *seedURL == "" {
		fmt.Fprintf(os.Stderr, "Error: seed URL is required (use -url flag)\n\n")
		fmt.Println("Usage:")
		flag.PrintDefaults()
		os.Exit(1)
	}
	if *resume && *checkpoint == "" {
		fmt.Fprintln(os.Stderr, "Error: -resume needs -checkpoint")
		os.Exit(1)
	}

	printBanner()

	cfg := crawl.CrawlConfig{
		SeedURL:        *seedURL,
		MaxDepth:       *maxDepth,
		MaxPages:       *maxPages,
		Workers:        *workers,
		Timeout:        *timeout,
		CrawlDelay:     *crawlDelay,
		MaxRetries:     *retries,
		UserAgent:      *userAgent,
		IgnoreRobots:   *ignoreRobots,
		OutputPath:     *outputFile,
		CheckpointPath: *checkpoint,
		Resume:         *resume,
	}
	// On the command line, 0 really means 0.
	if *crawlDelay == 0 {
		cfg.CrawlDelay = crawl.NoDelay
	}
	if *retries == 0 {
		cfg.MaxRetries = crawl.NoRetries
	}

	// Ctrl-C (or SIGTERM) stops the crawl cleanly: workers finish or put back
	// their page, the output file is flushed, and the checkpoint is saved.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result, err := crawl.RunContext(ctx, cfg)
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "[ERROR] Crawl failed: %v\n", err)
		if result == (crawl.CrawlResult{}) {
			os.Exit(1)
		}
	}

	title := "CRAWL COMPLETE"
	if result.Interrupted {
		title = "CRAWL INTERRUPTED"
	}
	fmt.Println()
	fmt.Println("===========================================")
	fmt.Printf("          %s\n", title)
	fmt.Println("===========================================")
	fmt.Printf("  Pages Visited:      %d\n", result.PagesVisited)
	fmt.Printf("  Pages Failed:       %d\n", result.PagesFailed)
	fmt.Printf("  Pages Skipped:      %d\n", result.PagesSkipped)
	fmt.Printf("  Robots Blocked:     %d\n", result.RobotsBlocked)
	fmt.Printf("  Retries:            %d\n", result.Retries)
	fmt.Printf("  Unique URLs:        %d\n", result.UniqueURLs)
	fmt.Printf("  Documents Written:  %d\n", result.DocumentsWritten)
	fmt.Printf("  Already in File:    %d\n", result.DocumentsSkipped)
	fmt.Printf("  Duration:           %v\n", result.Duration.Round(time.Millisecond))
	fmt.Printf("  Output:             %s\n", result.OutputPath)
	fmt.Println("===========================================")
	if result.Interrupted && *checkpoint != "" {
		fmt.Printf("\nResume with: -checkpoint %s -resume\n", *checkpoint)
	}

	if err != nil {
		os.Exit(1)
	}
}

// printBanner displays a startup message
func printBanner() {
	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════╗")
	fmt.Println("  ║           GoCrawl v1.0            ║")
	fmt.Println("  ║     Crawl · Extract · Store       ║")
	fmt.Println("  ╚═══════════════════════════════════╝")
	fmt.Println()
}
