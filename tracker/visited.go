package tracker

import (
	"sync"
)

// VisitedStore keeps track of which URLs we've already visited.
// It's safe to use from multiple goroutines at the same time.
//
// We use sync.RWMutex which allows:
//   - Multiple goroutines to READ at the same time (RLock)
//   - Only ONE goroutine to WRITE at a time (Lock)
//
// This is efficient because we check "is visited?" much more often than we add new URLs.
type VisitedStore struct {
	mu      sync.RWMutex
	visited map[string]bool
}

// NewVisitedStore creates a new empty VisitedStore.
func NewVisitedStore() *VisitedStore {
	return &VisitedStore{
		visited: make(map[string]bool),
	}
}

// MarkVisited tries to add a URL to the visited set.
// Returns true if the URL was NEW (not seen before).
// Returns false if the URL was ALREADY visited.
//
// This is an atomic "check and set" operation — it locks, checks, adds, and unlocks.
// This prevents two workers from both thinking the same URL is new.
func (v *VisitedStore) MarkVisited(url string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()

	// If we've already visited this URL, return false
	if v.visited[url] {
		return false
	}

	// Mark it as visited and return true (it's new!)
	v.visited[url] = true
	return true
}

// IsVisited checks if we've already visited a URL.
// Uses a read lock, so multiple goroutines can check at the same time.
func (v *VisitedStore) IsVisited(url string) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.visited[url]
}

// Count returns the total number of unique URLs we've visited.
func (v *VisitedStore) Count() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.visited)
}

// GetAll returns a copy of all visited URLs as a slice.
// Useful for printing the final report.
func (v *VisitedStore) GetAll() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()

	urls := make([]string, 0, len(v.visited))
	for u := range v.visited {
		urls = append(urls, u)
	}
	return urls
}
