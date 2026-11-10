package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Config holds all the settings the crawler engine needs.
// Every value is literal: 0 means 0. Defaults are applied by the crawl package.
type Config struct {
	SeedURL       string        // Starting URL to begin crawling
	MaxDepth      int           // How many links deep to follow (0 = seed page only)
	MaxPages      int           // Maximum number of pages to crawl successfully
	Workers       int           // Number of concurrent worker goroutines
	Timeout       time.Duration // HTTP request timeout per page
	CrawlDelay    time.Duration // Minimum gap between requests to the same domain (0 = none)
	MaxRetries    int           // Extra attempts for timeouts, 429 and 5xx (0 = none)
	UserAgent     string        // Sent with every request; "" = fetcher.DefaultUserAgent
	RespectRobots bool          // Obey robots.txt
}

// ValidateConfig checks if the given config has valid values.
// Returns an error describing what's wrong, or nil if everything is fine.
func ValidateConfig(cfg Config) error {
	if cfg.SeedURL == "" {
		return fmt.Errorf("seed URL is required (use -url flag)")
	}

	u, err := url.Parse(cfg.SeedURL)
	if err != nil {
		return fmt.Errorf("invalid seed URL: %v", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("seed URL must start with http:// or https://")
	}
	if u.Host == "" {
		return fmt.Errorf("seed URL has no host: %q", cfg.SeedURL)
	}

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
	if cfg.CrawlDelay < 0 {
		return fmt.Errorf("crawl delay must be 0 or greater")
	}
	if cfg.MaxRetries < 0 {
		return fmt.Errorf("max retries must be 0 or greater (got %d)", cfg.MaxRetries)
	}
	return nil
}
