package extractor

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Document represents a single crawled page, ready for indexing by a search engine.
type Document struct {
	ID            string `json:"id"`             // SHA-256 hash of the normalized URL (deterministic, unique)
	URL           string `json:"url"`            // The page URL (normalized)
	Domain        string `json:"domain"`         // e.g., "en.wikipedia.org"
	Title         string `json:"title"`          // Page title from <title> or first <h1>
	Content       string `json:"content"`        // Clean text extracted from the page
	StatusCode    int    `json:"status_code"`    // HTTP status code (e.g., 200)
	ContentLength int    `json:"content_length"` // Length of extracted clean text (not raw HTML)
	Timestamp     int64  `json:"timestamp"`      // Unix timestamp when the page was crawled
}

// Extract takes a page URL, its raw HTML body, and HTTP metadata,
// and returns a Document with clean extracted content.
func Extract(pageURL string, body string, statusCode int) Document {
	// Normalize the URL first — this deduplicates /index.html vs /
	normalizedURL := normalizeForDedup(pageURL)

	doc := Document{
		ID:         generateID(normalizedURL),
		URL:        normalizedURL,
		Domain:     extractDomain(normalizedURL),
		StatusCode: statusCode,
		Timestamp:  time.Now().Unix(),
	}

	// Parse the HTML
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		// If we can't parse, return what we have (URL, domain, etc.)
		return doc
	}

	doc.Title = extractTitle(root)
	doc.Content = extractContent(root)

	// ContentLength = length of the clean extracted text, not raw HTML.
	// This tells the search engine how much searchable text there actually is.
	doc.ContentLength = len(doc.Content)

	return doc
}

// normalizeForDedup normalizes a URL for deduplication purposes.
// It strips index files (index.html, index.htm, index.php, default.html)
// so that "/" and "/index.html" produce the same ID.
func normalizeForDedup(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	// Lowercase scheme and host
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)

	// Remove fragment
	parsed.Fragment = ""

	// Strip common index files — these all represent the same page as "/"
	indexFiles := []string{
		"/index.html", "/index.htm", "/index.php",
		"/default.html", "/default.htm",
	}
	pathLower := strings.ToLower(parsed.Path)
	for _, idx := range indexFiles {
		if pathLower == idx {
			parsed.Path = "/"
			break
		}
		// Also handle /subdir/index.html → /subdir/
		if strings.HasSuffix(pathLower, idx) {
			parsed.Path = parsed.Path[:len(parsed.Path)-len(idx)+1]
			break
		}
	}

	// Normalize empty path to "/"
	if parsed.Path == "" {
		parsed.Path = "/"
	}

	// Remove trailing slash for non-root paths
	result := parsed.String()
	if strings.HasSuffix(result, "/") && parsed.Path != "/" {
		result = strings.TrimSuffix(result, "/")
	}

	return result
}

// generateID creates a deterministic ID from the URL using SHA-256.
// The same URL always produces the same ID.
func generateID(pageURL string) string {
	hash := sha256.Sum256([]byte(pageURL))
	return fmt.Sprintf("%x", hash)
}

// extractDomain pulls the hostname from a URL.
// e.g., "https://en.wikipedia.org/wiki/Go" → "en.wikipedia.org"
func extractDomain(pageURL string) string {
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// extractTitle finds the page title.
// Priority: <title> tag first, then falls back to the first <h1>.
func extractTitle(root *html.Node) string {
	// Try <title> first
	if title := findFirstTag(root, "title"); title != "" {
		return strings.TrimSpace(title)
	}

	// Fallback to first <h1>
	if h1 := findFirstTag(root, "h1"); h1 != "" {
		return strings.TrimSpace(h1)
	}

	return ""
}

// findFirstTag finds the first occurrence of a tag and returns its text content.
func findFirstTag(node *html.Node, tag string) string {
	if node.Type == html.ElementNode && node.Data == tag {
		return getTextContent(node)
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if result := findFirstTag(child, tag); result != "" {
			return result
		}
	}

	return ""
}

// extractContent extracts clean text from content-bearing HTML elements.
// It collects text from <p>, <h1>-<h6>, <li>, and <td> tags,
// skipping <script>, <style>, <nav>, <header>, <footer> elements.
func extractContent(root *html.Node) string {
	var parts []string
	collectContent(root, &parts)

	// Join all text parts with spaces, collapse whitespace
	raw := strings.Join(parts, " ")
	return collapseWhitespace(raw)
}

// contentTags are HTML elements that typically contain readable page content.
var contentTags = map[string]bool{
	"p": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true,
	"li": true, "td": true, "th": true,
	"blockquote": true, "pre": true,
	"figcaption": true, "caption": true,
	"dt": true, "dd": true,
}

// skipTags are HTML elements we should skip entirely (no useful content).
var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true,
	"nav": true, "header": true, "footer": true,
	"aside": true, "form": true, "iframe": true,
	"svg": true, "math": true,
}

// collectContent recursively walks the DOM tree, collecting text from content tags.
func collectContent(node *html.Node, parts *[]string) {
	// Skip non-content elements entirely
	if node.Type == html.ElementNode && skipTags[node.Data] {
		return
	}

	// If this is a content-bearing element, grab its text
	if node.Type == html.ElementNode && contentTags[node.Data] {
		text := strings.TrimSpace(getTextContent(node))
		if text != "" {
			*parts = append(*parts, text)
		}
		return // Don't recurse into children (we already got the text)
	}

	// Otherwise, recurse into children
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		collectContent(child, parts)
	}
}

// getTextContent returns all text content within an HTML node, recursively.
// It strips all HTML tags and returns just the text.
// It also skips <script>, <style>, and other non-content elements that may be
// nested inside content tags (e.g., Wikipedia puts <style> inside <p> for citations).
func getTextContent(node *html.Node) string {
	if node.Type == html.TextNode {
		return node.Data
	}

	// Skip script/style/etc. even when nested inside content elements.
	// This prevents CSS rules and JS from leaking into extracted text.
	if node.Type == html.ElementNode && skipTags[node.Data] {
		return ""
	}

	var sb strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		sb.WriteString(getTextContent(child))
	}
	return sb.String()
}

// collapseWhitespace replaces all runs of whitespace (spaces, tabs, newlines)
// with a single space, and trims leading/trailing whitespace.
func collapseWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}
