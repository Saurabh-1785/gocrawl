package tracker

import (
	"sync"
	"testing"
)

// TestMarkVisited tests the basic add-and-check behavior.
func TestMarkVisited(t *testing.T) {
	store := NewVisitedStore()

	// First time marking a URL should return true (it's new)
	if !store.MarkVisited("http://example.com") {
		t.Error("First MarkVisited should return true (new URL)")
	}

	// Second time should return false (already visited)
	if store.MarkVisited("http://example.com") {
		t.Error("Second MarkVisited should return false (duplicate)")
	}

	// A different URL should return true
	if !store.MarkVisited("http://example.com/page2") {
		t.Error("Different URL should return true (new URL)")
	}
}

// TestIsVisited tests the read-only check.
func TestIsVisited(t *testing.T) {
	store := NewVisitedStore()

	// Not visited yet
	if store.IsVisited("http://example.com") {
		t.Error("URL should not be visited yet")
	}

	// Mark it
	store.MarkVisited("http://example.com")

	// Now it should be visited
	if !store.IsVisited("http://example.com") {
		t.Error("URL should be visited after marking")
	}
}

// TestCount tests the URL counter.
func TestCount(t *testing.T) {
	store := NewVisitedStore()

	if store.Count() != 0 {
		t.Errorf("Expected count 0, got %d", store.Count())
	}

	store.MarkVisited("http://example.com/page1")
	store.MarkVisited("http://example.com/page2")
	store.MarkVisited("http://example.com/page3")

	if store.Count() != 3 {
		t.Errorf("Expected count 3, got %d", store.Count())
	}

	// Marking a duplicate shouldn't change the count
	store.MarkVisited("http://example.com/page1")

	if store.Count() != 3 {
		t.Errorf("Expected count 3 after duplicate, got %d", store.Count())
	}
}

// TestConcurrentAccess tests that multiple goroutines can safely
// use the VisitedStore at the same time without data races.
func TestConcurrentAccess(t *testing.T) {
	store := NewVisitedStore()
	var wg sync.WaitGroup

	// Launch 100 goroutines, each trying to mark the same 10 URLs
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				url := "http://example.com/page" + string(rune('A'+j))
				store.MarkVisited(url)
				store.IsVisited(url)
				store.Count()
			}
		}()
	}

	wg.Wait()

	// We should have exactly 10 unique URLs
	if store.Count() != 10 {
		t.Errorf("Expected 10 unique URLs after concurrent access, got %d", store.Count())
	}
}

// TestGetAll tests retrieving all visited URLs.
func TestGetAll(t *testing.T) {
	store := NewVisitedStore()

	store.MarkVisited("http://example.com/a")
	store.MarkVisited("http://example.com/b")
	store.MarkVisited("http://example.com/c")

	all := store.GetAll()

	if len(all) != 3 {
		t.Errorf("Expected 3 URLs in GetAll, got %d", len(all))
	}
}
