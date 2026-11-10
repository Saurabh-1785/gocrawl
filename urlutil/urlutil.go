package urlutil

import (
	"net"
	"net/url"
	"sort"
	"strings"
)

// indexFiles are file names that mean the same page as their folder.
var indexFiles = []string{
	"/index.html", "/index.htm", "/index.php",
	"/default.html", "/default.htm",
}

// trackingParams are query parameters that only track clicks.
// They never change the page, so they are removed from the dedupe key.
var trackingParams = map[string]bool{
	"gclid": true, "fbclid": true, "msclkid": true,
	"mc_cid": true, "mc_eid": true, "_ga": true,
}

// NormalizeURL turns a URL into one canonical form, used as the dedupe key.
// Two URLs that point to the same page should normalize to the same string.
//
//	HTTP://Example.COM:80/blog/index.html?b=2&a=1&utm_source=x#top
//	  → http://example.com/blog?a=1&b=2
//
// The result is a KEY, not necessarily the exact URL to fetch. The crawler
// fetches the URL as the page author wrote it, so relative links keep working.
func NormalizeURL(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}

	// Scheme and host are case-insensitive; the path is not.
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = normalizeHost(u.Scheme, u.Host)

	// The fragment (#section) is in-page navigation: same page either way.
	u.Fragment = ""
	u.RawFragment = ""

	// "http://example.com" and "http://example.com/" are the same page.
	if u.Path == "" {
		u.Path = "/"
	}

	// "/blog/index.html" is the same page as "/blog/".
	pathLower := strings.ToLower(u.Path)
	for _, idx := range indexFiles {
		if strings.HasSuffix(pathLower, idx) {
			u.Path = u.Path[:len(u.Path)-len(idx)+1]
			u.RawPath = ""
			break
		}
	}

	// "/blog/" and "/blog" are treated as the same page (root "/" stays).
	if u.Path != "/" && strings.HasSuffix(u.Path, "/") {
		u.Path = strings.TrimRight(u.Path, "/")
		if u.Path == "" {
			u.Path = "/"
		}
		u.RawPath = ""
	}

	u.RawQuery = normalizeQuery(u.RawQuery)
	u.ForceQuery = false // "/a?" → "/a"

	return u.String(), nil
}

// normalizeHost lowercases the host and removes the default port
// (":80" for http, ":443" for https).
func normalizeHost(scheme, host string) string {
	host = strings.ToLower(host)
	h, port, err := net.SplitHostPort(host)
	if err != nil {
		return host // no port
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		if strings.Contains(h, ":") {
			return "[" + h + "]" // IPv6 literal needs brackets
		}
		return h
	}
	return host
}

// normalizeQuery sorts parameters and removes tracking ones,
// so "?b=2&a=1&utm_source=x" and "?a=1&b=2" give the same key.
func normalizeQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return rawQuery // keep odd queries as they are
	}
	for key := range values {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || trackingParams[lower] {
			delete(values, key)
		}
	}
	for _, v := range values {
		sort.Strings(v)
	}
	return values.Encode() // Encode sorts by key
}

// ResolveURL converts a relative URL to an absolute URL using a base URL.
// For example: base="http://example.com/blog/post", relative="../about"
// gives "http://example.com/about".
func ResolveURL(base string, relative string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	relativeURL, err := url.Parse(relative)
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(relativeURL).String(), nil
}

// SiteHost returns the part of a host that decides "same website":
// lowercase, default port removed, and a leading "www." removed.
// So "WWW.Example.com:443" (https) and "example.com" are the same site.
// Other subdomains stay different: "blog.example.com" is its own site.
func SiteHost(scheme, host string) string {
	return strings.TrimPrefix(normalizeHost(strings.ToLower(scheme), host), "www.")
}

// IsSameDomain reports whether checkURL is on the same website as seedURL.
// http and https count as the same site, and so do "www.example.com"
// and "example.com".
func IsSameDomain(seedURL string, checkURL string) bool {
	seed, err := url.Parse(seedURL)
	if err != nil {
		return false
	}
	check, err := url.Parse(checkURL)
	if err != nil {
		return false
	}
	return SiteHost(seed.Scheme, seed.Host) == SiteHost(check.Scheme, check.Host)
}

// IsValidURL reports whether a URL is http or https and has a host.
// mailto:, javascript:, ftp: and "http://" (no host) are rejected.
func IsValidURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Host != ""
}
