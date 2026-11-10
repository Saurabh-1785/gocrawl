package crawler

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"
)

// RateLimiter enforces a minimum gap between requests to the same domain.
//
// It works by handing out time slots. Under the lock, each caller takes the
// next free slot for its domain and moves the slot forward by one delay.
// Then it sleeps until its slot, WITHOUT the lock.
//
// Taking the slot before sleeping is the important part. If callers only
// looked at "last request time" and updated it after sleeping, several
// workers would see the same old time, sleep the same amount, and all fire
// together in a burst.
type RateLimiter struct {
	mu      sync.Mutex
	next    map[string]time.Time     // domain → earliest start of its next request
	delay   time.Duration            // default gap
	perHost map[string]time.Duration // larger gaps asked for by robots.txt Crawl-delay
}

// NewRateLimiter creates a rate limiter. A delay of 0 means no waiting.
func NewRateLimiter(delay time.Duration) *RateLimiter {
	return &RateLimiter{
		next:    make(map[string]time.Time),
		delay:   delay,
		perHost: make(map[string]time.Duration),
	}
}

// SetHostDelay raises the gap for the domain of rawURL (for example, to a
// robots.txt Crawl-delay). It never lowers it below the default.
func (r *RateLimiter) SetHostDelay(rawURL string, d time.Duration) {
	domain := extractDomain(rawURL)
	if domain == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if d > r.perHost[domain] {
		r.perHost[domain] = d
	}
}

// Wait blocks until the domain of rawURL may be requested again.
func (r *RateLimiter) Wait(rawURL string) {
	_ = r.WaitContext(context.Background(), rawURL)
}

// WaitContext is Wait, but returns early with ctx.Err() if ctx is cancelled.
func (r *RateLimiter) WaitContext(ctx context.Context, rawURL string) error {
	domain := extractDomain(rawURL)
	if domain == "" {
		return nil
	}

	r.mu.Lock()
	delay := r.delay
	if d := r.perHost[domain]; d > delay {
		delay = d
	}
	if delay <= 0 {
		r.mu.Unlock()
		return nil
	}
	now := time.Now()
	slot := r.next[domain]
	if slot.Before(now) {
		slot = now
	}
	r.next[domain] = slot.Add(delay) // reserve: the next caller queues behind us
	r.mu.Unlock()

	wait := slot.Sub(now)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// extractDomain pulls the host from a URL: "https://www.example.com/page" → "www.example.com".
func extractDomain(rawURL string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsedURL.Host)
}
