package crawler

import (
	"net/url"
	"strings"
	"sync"
	"time"
)

// RateLimiter enforces a minimum delay between requests to the same domain.
// This prevents us from overwhelming a website with too many requests too fast.
//
// How it works:
//   - We keep a map of domain -> last request time
//   - Before making a request, we check when we last visited that domain
//   - If not enough time has passed, we sleep until the delay has elapsed
type RateLimiter struct {
	mu        sync.Mutex
	lastVisit map[string]time.Time // domain name -> when we last made a request
	delay     time.Duration        // minimum time between requests to the same domain
}

// NewRateLimiter creates a new rate limiter with the given delay.
// If delay is 0, requests won't be delayed at all.
func NewRateLimiter(delay time.Duration) *RateLimiter {
	return &RateLimiter{
		lastVisit: make(map[string]time.Time),
		delay:     delay,
	}
}

// Wait blocks until it's safe to make a request to the domain of the given URL.
// If we recently made a request to this domain, we sleep for the remaining time.
func (r *RateLimiter) Wait(rawURL string) {
	// If no delay is configured, return immediately
	if r.delay == 0 {
		return
	}

	// Extract the domain from the URL
	domain := extractDomain(rawURL)
	if domain == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Check when we last visited this domain
	lastTime, exists := r.lastVisit[domain]

	if exists {
		// Calculate how long we need to wait
		elapsed := time.Since(lastTime)
		if elapsed < r.delay {
			// We need to wait a bit longer
			waitTime := r.delay - elapsed
			// Unlock while sleeping so other domains aren't blocked
			r.mu.Unlock()
			time.Sleep(waitTime)
			r.mu.Lock()
		}
	}

	// Update the last visit time for this domain
	r.lastVisit[domain] = time.Now()
}

// extractDomain pulls the domain name from a URL string.
// For example: "https://www.example.com/page" -> "www.example.com"
func extractDomain(rawURL string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsedURL.Host)
}
