package parser

import (
	"strings"

	"github.com/Saurabh-1785/gocrawl/urlutil"
	"golang.org/x/net/html"
)

// ExtractLinks parses an HTML page and returns all the URLs it links to.
//
// How it works:
//  1. Parse the HTML into a tree of nodes
//  2. Walk through every node in the tree
//  3. When we find an <a> tag, grab its "href" attribute
//  4. Convert relative URLs to absolute (e.g., "/about" -> "http://example.com/about")
//  5. Normalize each URL for consistent comparison
//  6. Filter out non-http/https links (javascript:, mailto:, etc.)
//  7. Remove duplicates within this page
//  8. Return the clean list
func ExtractLinks(body string, baseURL string) []string {
	// Parse the HTML string into a node tree
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil // If we can't parse the HTML, return no links
	}

	// seen keeps track of URLs we've already found on THIS page
	// so we don't return duplicates
	seen := make(map[string]bool)
	var links []string

	// walkNode recursively visits every node in the HTML tree.
	// We define it as a variable so it can call itself (recursion).
	var walkNode func(node *html.Node)
	walkNode = func(node *html.Node) {
		// Check if this node is an <a> (anchor) element
		if node.Type == html.ElementNode && node.Data == "a" {
			// Look through the tag's attributes for "href"
			for _, attr := range node.Attr {
				if attr.Key == "href" {
					link := strings.TrimSpace(attr.Val)

					// Skip empty links
					if link == "" {
						continue
					}

					// Skip javascript:, mailto:, tel:, and # links
					lowerLink := strings.ToLower(link)
					if strings.HasPrefix(lowerLink, "javascript:") ||
						strings.HasPrefix(lowerLink, "mailto:") ||
						strings.HasPrefix(lowerLink, "tel:") ||
						link == "#" {
						continue
					}

					// Convert relative URL to absolute
					absoluteURL, err := urlutil.ResolveURL(baseURL, link)
					if err != nil {
						continue // Skip URLs we can't resolve
					}

					// Normalize the URL (lowercase, remove fragment, etc.)
					normalizedURL, err := urlutil.NormalizeURL(absoluteURL)
					if err != nil {
						continue // Skip URLs we can't normalize
					}

					// Check if it's a valid http/https URL
					if !urlutil.IsValidURL(normalizedURL) {
						continue
					}

					// Only add if we haven't seen this URL on this page yet
					if !seen[normalizedURL] {
						seen[normalizedURL] = true
						links = append(links, normalizedURL)
					}

					break // Found href, no need to check other attributes
				}
			}
		}

		// Visit all child nodes (recursion)
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walkNode(child)
		}
	}

	// Start walking from the root of the document
	walkNode(doc)

	return links
}
