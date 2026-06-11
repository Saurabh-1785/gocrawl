package parser

import (
	"testing"
)

// TestExtractLinks tests that we correctly find links in HTML.
func TestExtractLinks(t *testing.T) {
	baseURL := "http://example.com"

	html := `
	<html>
	<body>
		<a href="/about">About</a>
		<a href="/contact">Contact</a>
		<a href="http://example.com/blog">Blog</a>
		<a href="http://other.com/page">External</a>
		<a href="/about">Duplicate About</a>
	</body>
	</html>`

	links := ExtractLinks(html, baseURL)

	// We expect 4 unique links (the duplicate /about should be removed)
	expectedCount := 4
	if len(links) != expectedCount {
		t.Errorf("Expected %d links, got %d: %v", expectedCount, len(links), links)
	}

	// Check that specific links are present
	expectedLinks := map[string]bool{
		"http://example.com/about":   false,
		"http://example.com/contact": false,
		"http://example.com/blog":    false,
		"http://other.com/page":      false,
	}

	for _, link := range links {
		if _, ok := expectedLinks[link]; ok {
			expectedLinks[link] = true
		}
	}

	for link, found := range expectedLinks {
		if !found {
			t.Errorf("Expected link %q was not found in results", link)
		}
	}
}

// TestExtractLinksFiltersJavascript tests that javascript: links are filtered out.
func TestExtractLinksFiltersJavascript(t *testing.T) {
	baseURL := "http://example.com"

	html := `
	<html>
	<body>
		<a href="javascript:alert('hi')">Click me</a>
		<a href="mailto:user@example.com">Email</a>
		<a href="tel:+1234567890">Call</a>
		<a href="/real-page">Real Page</a>
	</body>
	</html>`

	links := ExtractLinks(html, baseURL)

	// Only the real page should survive filtering
	if len(links) != 1 {
		t.Errorf("Expected 1 link after filtering, got %d: %v", len(links), links)
	}

	if len(links) > 0 && links[0] != "http://example.com/real-page" {
		t.Errorf("Expected 'http://example.com/real-page', got %q", links[0])
	}
}

// TestExtractLinksEmptyHTML tests with empty or minimal HTML.
func TestExtractLinksEmptyHTML(t *testing.T) {
	links := ExtractLinks("", "http://example.com")
	if len(links) != 0 {
		t.Errorf("Expected 0 links from empty HTML, got %d", len(links))
	}

	links = ExtractLinks("<html><body>No links here</body></html>", "http://example.com")
	if len(links) != 0 {
		t.Errorf("Expected 0 links from HTML without anchors, got %d", len(links))
	}
}

// TestExtractLinksRelativeURLs tests that relative URLs are resolved correctly.
func TestExtractLinksRelativeURLs(t *testing.T) {
	baseURL := "http://example.com/blog/post1"

	html := `
	<html>
	<body>
		<a href="../about">About (relative)</a>
		<a href="post2">Post 2 (relative)</a>
		<a href="/contact">Contact (absolute path)</a>
	</body>
	</html>`

	links := ExtractLinks(html, baseURL)

	expectedLinks := map[string]bool{
		"http://example.com/about":      false,
		"http://example.com/blog/post2": false,
		"http://example.com/contact":    false,
	}

	for _, link := range links {
		if _, ok := expectedLinks[link]; ok {
			expectedLinks[link] = true
		}
	}

	for link, found := range expectedLinks {
		if !found {
			t.Errorf("Expected link %q was not found in results: %v", link, links)
		}
	}
}

// TestExtractLinksWithFragments tests that fragments are stripped from links.
func TestExtractLinksWithFragments(t *testing.T) {
	baseURL := "http://example.com"

	html := `
	<html>
	<body>
		<a href="/page#section1">Section 1</a>
		<a href="/page#section2">Section 2</a>
	</body>
	</html>`

	links := ExtractLinks(html, baseURL)

	// Both links point to /page (fragments removed), so we should get just 1
	if len(links) != 1 {
		t.Errorf("Expected 1 unique link (fragments stripped), got %d: %v", len(links), links)
	}
}
