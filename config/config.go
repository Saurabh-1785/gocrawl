package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Config holds all the settings for the crawler.
// These values come from command-line flags in main.go.
type Config struct {
	SeedURL    string        // Starting URL to begin crawling
	MaxDepth   int           // How many links deep to follow (0 = seed only)
	MaxPages   int           // Maximum number of pages to crawl
	Workers    int           // Number of concurrent worker goroutines
	Timeout    time.Duration // HTTP request timeout per page
	CrawlDelay time.Duration // Minimum delay between requests to the same domain
}

// ValidateConfig checks if the given config has valid values.
// Returns an error describing what's wrong, or nil if everything is fine.
func ValidateConfig(cfg Config) error {
	// Check that a seed URL was provided
	if cfg.SeedURL == "" {
		return fmt.Errorf("seed URL is required (use -url flag)")
	}

	// Check that the URL starts with http:// or https://
	if !strings.HasPrefix(cfg.SeedURL, "http://") && !strings.HasPrefix(cfg.SeedURL, "https://") {
		return fmt.Errorf("seed URL must start with http:// or https://")
	}

	// Try to parse the URL to make sure it's valid
	_, err := url.Parse(cfg.SeedURL)
	if err != nil {
		return fmt.Errorf("invalid seed URL: %v", err)
	}

	// Check numeric limits
	if cfg.MaxDepth < 0 {
		return fmt.Errorf("max depth must be 0 or greater (got %d)", cfg.MaxDepth)
	}

	if cfg.MaxPages < 1 {
		return fmt.Errorf("max pages must be at least 1 (got %d)", cfg.MaxPages)
	}

	if cfg.Workers < 1 {
		return fmt.Errorf("worker count must be at least 1 (got %d)", cfg.Workers)
	}

	if cfg.Timeout <= 0 {
		return fmt.Errorf("timeout must be greater than 0")
	}

	// CrawlDelay can be 0 (no delay), so we only check for negative
	if cfg.CrawlDelay < 0 {
		return fmt.Errorf("crawl delay must be 0 or greater")
	}

	return nil // All checks passed!
}
