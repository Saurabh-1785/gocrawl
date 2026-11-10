package extractor

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Saurabh-1785/gocrawl/urlutil"
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

// Extract parses raw HTML and returns a Document with clean content.
func Extract(pageURL string, body string, statusCode int) Document {
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		root = nil // can't parse: return what we know (URL, domain, ...)
	}
	return ExtractNode(pageURL, root, statusCode)
}

// ExtractNode builds a Document from an already-parsed page.
// The crawler uses this so each page is parsed only once.
func ExtractNode(pageURL string, root *html.Node, statusCode int) Document {
	// Normalize first, so "/" and "/index.html" give the same ID.
	normalizedURL := normalizeForDedup(pageURL)

	doc := Document{
		ID:         generateID(normalizedURL),
		URL:        normalizedURL,
		Domain:     extractDomain(normalizedURL),
		StatusCode: statusCode,
		Timestamp:  time.Now().Unix(),
	}
	if root == nil {
		return doc
	}

	doc.Title = extractTitle(root)
	doc.Content = extractContent(root)

	// ContentLength = length of the clean text, not raw HTML.
	// It tells a search engine how much searchable text there really is.
	doc.ContentLength = len(doc.Content)
	return doc
}

// normalizeForDedup uses the same URL rules as the crawler (urlutil), so a
// page's document ID always matches the key the crawler deduped it with.
func normalizeForDedup(rawURL string) string {
	normalized, err := urlutil.NormalizeURL(rawURL)
	if err != nil {
		return rawURL
	}
	return normalized
}

// generateID creates a deterministic ID from the URL using SHA-256.
// The same URL always produces the same ID, on any machine, in any run.
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

// extractTitle finds the page title: <title> first, then the first <h1>.
func extractTitle(root *html.Node) string {
	if title := strings.TrimSpace(findFirstTag(root, "title")); title != "" {
		return collapseWhitespace(title)
	}
	if h1 := strings.TrimSpace(findFirstTag(root, "h1")); h1 != "" {
		return collapseWhitespace(h1)
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

// extractContent extracts clean text from the page.
func extractContent(root *html.Node) string {
	var parts []string
	collectContent(root, &parts)
	return collapseWhitespace(strings.Join(parts, " "))
}

// contentTags are elements that usually hold readable text.
// All text inside them is taken.
var contentTags = map[string]bool{
	"p": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true,
	"li": true, "td": true, "th": true,
	"blockquote": true, "pre": true,
	"figcaption": true, "caption": true,
	"dt": true, "dd": true,
}

// skipTags are elements with no useful text. Their whole subtree is skipped,
// even when nested inside a content tag (Wikipedia puts <style> inside <p>).
//
// <form> is NOT skipped: some sites (e.g. ASP.NET) wrap the whole page in one.
var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"nav": true, "header": true, "footer": true,
	"aside": true, "iframe": true,
	"svg": true, "math": true,
	"select": true, "option": true, "button": true,
}

// looseTextParents are containers whose DIRECT text is kept even though they
// are not content tags, e.g. <div>Some text</div>. Inline tags like <a> are
// not listed, so loose menu links don't leak in.
var looseTextParents = map[string]bool{
	"div": true, "section": true, "article": true, "main": true,
	"body": true, "span": true, "font": true, "center": true,
}

// blockTags break words apart: "<td>foo<br>bar</td>" must give "foo bar",
// not "foobar". Inline tags (<a>, <b>, <span>) don't add a space, the same
// way a browser renders them.
var blockTags = map[string]bool{
	"br": true, "p": true, "div": true, "li": true, "ul": true, "ol": true,
	"td": true, "th": true, "tr": true, "table": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"section": true, "article": true, "blockquote": true, "pre": true,
	"dt": true, "dd": true, "dl": true, "hr": true, "figcaption": true,
}

// collectContent walks the DOM tree depth-first, collecting text.
func collectContent(node *html.Node, parts *[]string) {
	switch {
	case node.Type == html.ElementNode && skipTags[node.Data]:
		return

	case node.Type == html.ElementNode && contentTags[node.Data]:
		if text := strings.TrimSpace(getTextContent(node)); text != "" {
			*parts = append(*parts, text)
		}
		return // already took all text inside

	case node.Type == html.TextNode:
		if node.Parent != nil && node.Parent.Type == html.ElementNode && looseTextParents[node.Parent.Data] {
			if text := strings.TrimSpace(node.Data); text != "" {
				*parts = append(*parts, text)
			}
		}
		return
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		collectContent(child, parts)
	}
}

// getTextContent returns all text inside a node, without tags.
func getTextContent(node *html.Node) string {
	var sb strings.Builder
	writeText(node, &sb)
	return sb.String()
}

func writeText(node *html.Node, sb *strings.Builder) {
	if node.Type == html.TextNode {
		sb.WriteString(node.Data)
		return
	}
	if node.Type == html.ElementNode && skipTags[node.Data] {
		return
	}
	block := node.Type == html.ElementNode && blockTags[node.Data]
	if block {
		sb.WriteByte(' ')
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		writeText(child, sb)
	}
	if block {
		sb.WriteByte(' ')
	}
}

// collapseWhitespace replaces every run of whitespace with one space and trims.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
