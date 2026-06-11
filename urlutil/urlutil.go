package urlutil

import (
	"net/url"
	"strings"
)

// NormalizeURL cleans up a URL so we can compare URLs consistently.
// For example, "HTTP://Example.COM/page#section" becomes "http://example.com/page"
func NormalizeURL(rawURL string) (string, error) {
	// Step 1: Parse the URL into its components
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	// Step 2: Lowercase the scheme (http, https)
	parsedURL.Scheme = strings.ToLower(parsedURL.Scheme)

	// Step 3: Lowercase the host (domain name)
	parsedURL.Host = strings.ToLower(parsedURL.Host)

	// Step 4: Remove the fragment (#section)
	// Fragments are just for in-page navigation, same page either way
	parsedURL.Fragment = ""

	// Step 5: Build the cleaned URL string
	result := parsedURL.String()

	// Step 6: Remove trailing slash, but keep it for root paths like "http://example.com/"
	if strings.HasSuffix(result, "/") && parsedURL.Path != "/" && parsedURL.Path != "" {
		result = strings.TrimSuffix(result, "/")
	}

	return result, nil
}

// ResolveURL converts a relative URL to an absolute URL using a base URL.
// For example: base="http://example.com/blog", relative="../about"
// Result: "http://example.com/about"
func ResolveURL(base string, relative string) (string, error) {
	// Parse the base URL
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}

	// Parse the relative URL
	relativeURL, err := url.Parse(relative)
	if err != nil {
		return "", err
	}

	// Resolve: combine base + relative to get an absolute URL
	resolvedURL := baseURL.ResolveReference(relativeURL)

	return resolvedURL.String(), nil
}

// IsSameDomain checks if a given URL belongs to the same domain as the seed URL.
// This is used to keep the crawler within one website.
func IsSameDomain(seedURL string, checkURL string) bool {
	seedParsed, err := url.Parse(seedURL)
	if err != nil {
		return false
	}

	checkParsed, err := url.Parse(checkURL)
	if err != nil {
		return false
	}

	// Compare the hostnames (lowercased for consistency)
	return strings.ToLower(seedParsed.Host) == strings.ToLower(checkParsed.Host)
}

// IsValidURL checks if a URL uses the http or https scheme.
// We don't want to crawl mailto:, javascript:, ftp:, etc.
func IsValidURL(rawURL string) bool {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	scheme := strings.ToLower(parsedURL.Scheme)
	return scheme == "http" || scheme == "https"
}
