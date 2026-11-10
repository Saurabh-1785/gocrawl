package parser

import (
	"net/url"
	"strings"

	"github.com/Saurabh-1785/gocrawl/urlutil"
	"golang.org/x/net/html"
)

// Links returns every crawlable link on an already-parsed page.
//
// Each link is an absolute http(s) URL with the fragment removed, but
// otherwise exactly as the page author wrote it (for example "/blog/" keeps
// its trailing slash). That matters: the crawler fetches this URL, and the
// page it gets back resolves its own relative links against it.
//
// Duplicates are removed using the normalized form, so "/about" and
// "/about#team" count once. pageURL should be the final URL of the page
// (after redirects). A <base href> tag on the page overrides it.
//
// The tree is walked depth-first (recursion), visiting every node once.
func Links(root *html.Node, pageURL string) []string {
	if root == nil {
		return nil
	}
	base := baseURL(root, pageURL)

	seen := make(map[string]bool)
	var links []string

	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			if href, ok := attr(n, "href"); ok {
				if link, key, ok := clean(base, href); ok && !seen[key] {
					seen[key] = true
					links = append(links, link)
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return links
}

// ExtractLinks parses an HTML page and returns its links in normalized form.
// It is the simple, one-call version of Links, kept for callers that only
// need dedupe keys.
func ExtractLinks(body string, baseURL string) []string {
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}
	raw := Links(root, baseURL)
	links := make([]string, 0, len(raw))
	for _, link := range raw {
		if key, err := urlutil.NormalizeURL(link); err == nil {
			links = append(links, key)
		}
	}
	return links
}

// clean turns one href into (absolute link, dedupe key).
// It rejects empty links, "#", javascript:, mailto:, tel:, and non-http(s).
func clean(base, href string) (link, key string, ok bool) {
	href = strings.TrimSpace(href)
	lower := strings.ToLower(href)
	if href == "" || strings.HasPrefix(href, "#") ||
		strings.HasPrefix(lower, "javascript:") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "tel:") {
		return "", "", false
	}

	abs, err := urlutil.ResolveURL(base, href)
	if err != nil || !urlutil.IsValidURL(abs) {
		return "", "", false
	}

	u, err := url.Parse(abs)
	if err != nil {
		return "", "", false
	}
	u.Fragment = ""
	u.RawFragment = ""
	link = u.String()

	key, err = urlutil.NormalizeURL(link)
	if err != nil {
		return "", "", false
	}
	return link, key, true
}

// baseURL returns the URL that relative links resolve against:
// the first <base href>, if present, otherwise the page URL.
func baseURL(root *html.Node, pageURL string) string {
	var found string
	var walk func(n *html.Node) bool
	walk = func(n *html.Node) bool {
		if n.Type == html.ElementNode && n.Data == "base" {
			if href, ok := attr(n, "href"); ok && strings.TrimSpace(href) != "" {
				found = strings.TrimSpace(href)
				return true
			}
		}
		if n.Type == html.ElementNode && n.Data == "body" {
			return false // <base> only belongs in <head>
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if walk(c) {
				return true
			}
		}
		return false
	}
	if !walk(root) {
		return pageURL
	}
	if abs, err := urlutil.ResolveURL(pageURL, found); err == nil {
		return abs
	}
	return pageURL
}

// attr returns the value of an HTML attribute.
func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}
