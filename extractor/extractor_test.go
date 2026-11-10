package extractor

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestExtract_BasicHTML(t *testing.T) {
	body := `<!DOCTYPE html>
<html>
<head><title>Test Page Title</title></head>
<body>
<h1>Main Heading</h1>
<p>This is the first paragraph of content.</p>
<p>This is the second paragraph.</p>
</body>
</html>`

	doc := Extract("https://example.com/page", body, 200)

	if doc.Title != "Test Page Title" {
		t.Errorf("expected title 'Test Page Title', got '%s'", doc.Title)
	}

	if !strings.Contains(doc.Content, "first paragraph") {
		t.Errorf("expected content to contain 'first paragraph', got '%s'", doc.Content)
	}

	if !strings.Contains(doc.Content, "second paragraph") {
		t.Errorf("expected content to contain 'second paragraph', got '%s'", doc.Content)
	}

	if doc.Domain != "example.com" {
		t.Errorf("expected domain 'example.com', got '%s'", doc.Domain)
	}

	if doc.StatusCode != 200 {
		t.Errorf("expected status code 200, got %d", doc.StatusCode)
	}

	// ContentLength should be length of clean text, not raw HTML
	if doc.ContentLength != len(doc.Content) {
		t.Errorf("expected content length %d (len of clean text), got %d", len(doc.Content), doc.ContentLength)
	}

	if doc.ContentLength >= len(body) {
		t.Errorf("clean text length (%d) should be less than raw HTML length (%d)", doc.ContentLength, len(body))
	}

	if doc.URL != "https://example.com/page" {
		t.Errorf("expected URL 'https://example.com/page', got '%s'", doc.URL)
	}

	if doc.ID == "" {
		t.Error("expected non-empty ID")
	}

	if doc.Timestamp == 0 {
		t.Error("expected non-zero timestamp")
	}
}

func TestExtract_TitleFallbackToH1(t *testing.T) {
	body := `<html><body><h1>Heading As Title</h1><p>Some content.</p></body></html>`

	doc := Extract("https://example.com/no-title", body, 200)

	if doc.Title != "Heading As Title" {
		t.Errorf("expected title 'Heading As Title', got '%s'", doc.Title)
	}
}

func TestExtract_NoTitle(t *testing.T) {
	body := `<html><body><p>Just a paragraph, no title or heading.</p></body></html>`

	doc := Extract("https://example.com/no-title", body, 200)

	if doc.Title != "" {
		t.Errorf("expected empty title, got '%s'", doc.Title)
	}
}

func TestExtract_SkipsScriptAndStyle(t *testing.T) {
	body := `<html>
<head><title>Page</title><style>body { color: red; }</style></head>
<body>
<script>var x = "should not appear";</script>
<p>Visible content here.</p>
<style>.hidden { display: none; }</style>
</body>
</html>`

	doc := Extract("https://example.com/scripts", body, 200)

	if strings.Contains(doc.Content, "should not appear") {
		t.Error("content should not include script text")
	}

	if strings.Contains(doc.Content, "color: red") {
		t.Error("content should not include style text")
	}

	if !strings.Contains(doc.Content, "Visible content") {
		t.Errorf("expected visible content, got '%s'", doc.Content)
	}
}

func TestExtract_SkipsNestedStyleInContent(t *testing.T) {
	// Wikipedia puts <style> tags inside content elements (e.g., inside <p> for citations)
	body := `<html><body>
<p>Real article content here.
<style>.mw-parser-output cite.citation{font-style:inherit;word-wrap:break-word}</style>
More real content after style.
</p>
</body></html>`

	doc := Extract("https://en.wikipedia.org/wiki/Test", body, 200)

	if strings.Contains(doc.Content, "mw-parser-output") {
		t.Errorf("content should not include nested CSS, got '%s'", doc.Content)
	}

	if strings.Contains(doc.Content, "font-style") {
		t.Errorf("content should not include CSS properties, got '%s'", doc.Content)
	}

	if !strings.Contains(doc.Content, "Real article content") {
		t.Errorf("expected real content, got '%s'", doc.Content)
	}

	if !strings.Contains(doc.Content, "More real content") {
		t.Errorf("expected content after style tag, got '%s'", doc.Content)
	}
}

func TestExtract_EmptyHTML(t *testing.T) {
	doc := Extract("https://example.com/empty", "", 200)

	if doc.Title != "" {
		t.Errorf("expected empty title for empty HTML, got '%s'", doc.Title)
	}

	if doc.Domain != "example.com" {
		t.Errorf("expected domain 'example.com', got '%s'", doc.Domain)
	}
}

func TestExtract_DeterministicID(t *testing.T) {
	doc1 := Extract("https://example.com/page", "<p>content</p>", 200)
	doc2 := Extract("https://example.com/page", "<p>different content</p>", 200)

	if doc1.ID != doc2.ID {
		t.Error("same URL should produce same ID regardless of content")
	}

	doc3 := Extract("https://example.com/other", "<p>content</p>", 200)
	if doc1.ID == doc3.ID {
		t.Error("different URLs should produce different IDs")
	}
}

func TestExtract_DeduplicatesIndexHTML(t *testing.T) {
	body := `<html><body><p>Hello</p></body></html>`

	doc1 := Extract("https://example.com/", body, 200)
	doc2 := Extract("https://example.com/index.html", body, 200)

	if doc1.ID != doc2.ID {
		t.Errorf("/ and /index.html should produce the same ID, got '%s' and '%s'", doc1.ID, doc2.ID)
	}

	if doc1.URL != doc2.URL {
		t.Errorf("URLs should be normalized the same, got '%s' and '%s'", doc1.URL, doc2.URL)
	}
}

func TestExtract_DeduplicatesSubdirIndex(t *testing.T) {
	body := `<html><body><p>Hello</p></body></html>`

	doc1 := Extract("https://example.com/catalogue/", body, 200)
	doc2 := Extract("https://example.com/catalogue/index.html", body, 200)

	if doc1.ID != doc2.ID {
		t.Errorf("/catalogue/ and /catalogue/index.html should produce the same ID")
	}
}

func TestExtract_DeduplicatesIndexPhp(t *testing.T) {
	body := `<html><body><p>Hello</p></body></html>`

	doc1 := Extract("https://example.com/", body, 200)
	doc2 := Extract("https://example.com/index.php", body, 200)

	if doc1.ID != doc2.ID {
		t.Errorf("/ and /index.php should produce the same ID")
	}
}

func TestExtract_ContentWithLists(t *testing.T) {
	body := `<html><body>
<ul>
<li>Item one</li>
<li>Item two</li>
</ul>
<table><tr><td>Cell data</td></tr></table>
</body></html>`

	doc := Extract("https://example.com/list", body, 200)

	if !strings.Contains(doc.Content, "Item one") {
		t.Errorf("expected list item content, got '%s'", doc.Content)
	}

	if !strings.Contains(doc.Content, "Cell data") {
		t.Errorf("expected table cell content, got '%s'", doc.Content)
	}
}

func TestExtract_CollapsesWhitespace(t *testing.T) {
	body := `<html><body>
<p>  Lots   of    spaces   and
	tabs   here  </p>
</body></html>`

	doc := Extract("https://example.com/ws", body, 200)

	if strings.Contains(doc.Content, "  ") {
		t.Errorf("content should not have double spaces, got '%s'", doc.Content)
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		url    string
		domain string
	}{
		{"https://en.wikipedia.org/wiki/Go", "en.wikipedia.org"},
		{"http://localhost:8080/page", "localhost"},
		{"https://example.com", "example.com"},
	}

	for _, tt := range tests {
		got := extractDomain(tt.url)
		if got != tt.domain {
			t.Errorf("extractDomain(%q) = %q, want %q", tt.url, got, tt.domain)
		}
	}
}

func TestExtract_SkipsNavAndFooter(t *testing.T) {
	body := `<html><body>
<nav><a href="/">Home</a><a href="/about">About</a></nav>
<p>Main article content here.</p>
<footer>Copyright 2024</footer>
</body></html>`

	doc := Extract("https://example.com/nav", body, 200)

	if strings.Contains(doc.Content, "Home") {
		t.Error("content should not include nav text")
	}

	if strings.Contains(doc.Content, "Copyright") {
		t.Error("content should not include footer text")
	}

	if !strings.Contains(doc.Content, "Main article content") {
		t.Errorf("expected main content, got '%s'", doc.Content)
	}
}

func TestExtract_ContentLengthIsCleanTextLength(t *testing.T) {
	body := `<html>
<head><title>Big Page</title></head>
<body>
<nav>lots of navigation stuff here that is very long</nav>
<script>var x = "lots of javascript code";</script>
<style>.big { font-size: 100px; color: red; background: blue; }</style>
<p>Short actual content.</p>
</body>
</html>`

	doc := Extract("https://example.com/big", body, 200)

	// ContentLength should equal len(Content), not len(body)
	if doc.ContentLength != len(doc.Content) {
		t.Errorf("ContentLength (%d) should equal len(Content) (%d)", doc.ContentLength, len(doc.Content))
	}

	// Clean text should be much shorter than raw HTML
	if doc.ContentLength >= len(body) {
		t.Errorf("clean text (%d bytes) should be shorter than raw HTML (%d bytes)", doc.ContentLength, len(body))
	}
}

func TestNormalizeForDedup(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// index.html → /
		{"https://example.com/index.html", "https://example.com/"},
		{"https://example.com/index.htm", "https://example.com/"},
		{"https://example.com/index.php", "https://example.com/"},
		// Root stays root
		{"https://example.com/", "https://example.com/"},
		{"https://example.com", "https://example.com/"},
		// Subdirectory index
		{"https://example.com/blog/index.html", "https://example.com/blog"},
		// Non-index pages stay unchanged
		{"https://example.com/about", "https://example.com/about"},
		// Fragments removed
		{"https://example.com/page#section", "https://example.com/page"},
		// Case normalization
		{"HTTPS://Example.COM/Page", "https://example.com/Page"},
	}

	for _, tt := range tests {
		got := normalizeForDedup(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeForDedup(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// Block elements and <br> separate words; they used to be glued together.
func TestExtract_BlockBoundariesAddSpaces(t *testing.T) {
	doc := Extract("https://example.com/", `<table><tr><td>foo<br>bar</td></tr></table><ul><li><p>one</p><p>two</p></li></ul>`, 200)
	for _, want := range []string{"foo bar", "one two"} {
		if !strings.Contains(doc.Content, want) {
			t.Errorf("content %q should contain %q", doc.Content, want)
		}
	}
	// Inline tags don't add spaces, the same as a browser.
	doc = Extract("https://example.com/", `<p>un<b>believ</b>able</p>`, 200)
	if doc.Content != "unbelievable" {
		t.Errorf("content = %q, want %q", doc.Content, "unbelievable")
	}
}

// Some sites wrap the whole page in a <form>; its text must not be lost.
func TestExtract_KeepsTextInsideForm(t *testing.T) {
	doc := Extract("https://example.com/", `<form><p>Whole page lives here</p><select><option>Choice</option></select></form>`, 200)
	if !strings.Contains(doc.Content, "Whole page lives here") {
		t.Errorf("content = %q, want the form's text", doc.Content)
	}
	if strings.Contains(doc.Content, "Choice") {
		t.Errorf("dropdown options should be skipped, got %q", doc.Content)
	}
}

// Text placed straight inside a <div> is kept; loose link text is not.
func TestExtract_LooseDivText(t *testing.T) {
	doc := Extract("https://example.com/", `<div>Plain text in a div<a href="/x">menu link</a></div>`, 200)
	if !strings.Contains(doc.Content, "Plain text in a div") {
		t.Errorf("content = %q, want the div's text", doc.Content)
	}
	if strings.Contains(doc.Content, "menu link") {
		t.Errorf("loose link text should not be content, got %q", doc.Content)
	}
}

// ExtractNode on a parsed tree gives the same result as Extract on raw HTML.
func TestExtractNodeMatchesExtract(t *testing.T) {
	body := `<html><head><title>T</title></head><body><p>Hello</p></body></html>`
	root, _ := html.Parse(strings.NewReader(body))
	a := Extract("https://example.com/x", body, 200)
	b := ExtractNode("https://example.com/x", root, 200)
	if a.ID != b.ID || a.Title != b.Title || a.Content != b.Content {
		t.Errorf("Extract = %+v, ExtractNode = %+v", a, b)
	}
}
