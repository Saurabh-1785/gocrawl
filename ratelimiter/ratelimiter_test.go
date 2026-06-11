package ratelimiter

import (
	"testing"
	"time"
)

// TestRateLimiterDelay tests that consecutive requests to the same domain are delayed.
func TestRateLimiterDelay(t *testing.T) {
	delay := 100 * time.Millisecond
	rl := NewRateLimiter(delay)

	url := "http://example.com/page1"

	// First request should be instant
	start := time.Now()
	rl.Wait(url)
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Errorf("First request should be instant, took %v", elapsed)
	}

	// Second request to the SAME domain should be delayed
	start = time.Now()
	rl.Wait("http://example.com/page2") // same domain, different page
	elapsed = time.Since(start)

	// Should have waited approximately 'delay' duration
	if elapsed < 50*time.Millisecond {
		t.Errorf("Second request should be delayed, only took %v", elapsed)
	}
}

// TestRateLimiterDifferentDomains tests that different domains aren't affected by each other.
func TestRateLimiterDifferentDomains(t *testing.T) {
	delay := 200 * time.Millisecond
	rl := NewRateLimiter(delay)

	// First request to domain A
	rl.Wait("http://example.com/page")

	// Request to domain B should NOT be delayed
	start := time.Now()
	rl.Wait("http://other.com/page")
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Errorf("Different domain should not be delayed, took %v", elapsed)
	}
}

// TestRateLimiterZeroDelay tests that zero delay means no waiting.
func TestRateLimiterZeroDelay(t *testing.T) {
	rl := NewRateLimiter(0)

	start := time.Now()
	rl.Wait("http://example.com/page1")
	rl.Wait("http://example.com/page2")
	rl.Wait("http://example.com/page3")
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Errorf("Zero delay should not cause any waiting, took %v", elapsed)
	}
}
