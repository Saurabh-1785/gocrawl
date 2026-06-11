package urlutil

import (
	"testing"
)

// TestNormalizeURL tests that URLs are cleaned up consistently.
func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name     string // description of the test
		input    string // URL to normalize
		expected string // what we expect after normalization
	}{
		{
			name:     "lowercase scheme and host",
			input:    "HTTP://EXAMPLE.COM/Page",
			expected: "http://example.com/Page",
		},
		{
			name:     "remove fragment",
			input:    "http://example.com/page#section",
			expected: "http://example.com/page",
		},
		{
			name:     "remove trailing slash",
			input:    "http://example.com/page/",
			expected: "http://example.com/page",
		},
		{
			name:     "keep root slash",
			input:    "http://example.com/",
			expected: "http://example.com/",
		},
		{
			name:     "already clean URL",
			input:    "https://example.com/about",
			expected: "https://example.com/about",
		},
		{
			name:     "remove fragment and trailing slash",
			input:    "https://example.com/blog/#top",
			expected: "https://example.com/blog",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := NormalizeURL(tt.input)
			if err != nil {
				t.Fatalf("NormalizeURL(%q) returned error: %v", tt.input, err)
			}
			if result != tt.expected {
				t.Errorf("NormalizeURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestResolveURL tests converting relative URLs to absolute.
func TestResolveURL(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		relative string
		expected string
	}{
		{
			name:     "simple relative path",
			base:     "http://example.com/blog/post1",
			relative: "../about",
			expected: "http://example.com/about",
		},
		{
			name:     "absolute path on same host",
			base:     "http://example.com/blog/post1",
			relative: "/contact",
			expected: "http://example.com/contact",
		},
		{
			name:     "already absolute URL",
			base:     "http://example.com/",
			relative: "http://other.com/page",
			expected: "http://other.com/page",
		},
		{
			name:     "relative in same directory",
			base:     "http://example.com/blog/",
			relative: "post2",
			expected: "http://example.com/blog/post2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveURL(tt.base, tt.relative)
			if err != nil {
				t.Fatalf("ResolveURL(%q, %q) returned error: %v", tt.base, tt.relative, err)
			}
			if result != tt.expected {
				t.Errorf("ResolveURL(%q, %q) = %q, want %q", tt.base, tt.relative, result, tt.expected)
			}
		})
	}
}

// TestIsSameDomain tests domain comparison.
func TestIsSameDomain(t *testing.T) {
	tests := []struct {
		name     string
		seed     string
		check    string
		expected bool
	}{
		{
			name:     "same domain",
			seed:     "http://example.com/",
			check:    "http://example.com/about",
			expected: true,
		},
		{
			name:     "different domain",
			seed:     "http://example.com/",
			check:    "http://other.com/about",
			expected: false,
		},
		{
			name:     "same domain different scheme",
			seed:     "http://example.com/",
			check:    "https://example.com/page",
			expected: true,
		},
		{
			name:     "same domain different case",
			seed:     "http://Example.COM/",
			check:    "http://example.com/page",
			expected: true,
		},
		{
			name:     "subdomain is different",
			seed:     "http://example.com/",
			check:    "http://sub.example.com/page",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsSameDomain(tt.seed, tt.check)
			if result != tt.expected {
				t.Errorf("IsSameDomain(%q, %q) = %v, want %v", tt.seed, tt.check, result, tt.expected)
			}
		})
	}
}

// TestIsValidURL tests HTTP/HTTPS validation.
func TestIsValidURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{name: "http URL", input: "http://example.com", expected: true},
		{name: "https URL", input: "https://example.com", expected: true},
		{name: "ftp URL", input: "ftp://example.com", expected: false},
		{name: "javascript URL", input: "javascript:alert(1)", expected: false},
		{name: "mailto URL", input: "mailto:user@example.com", expected: false},
		{name: "empty string", input: "", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidURL(tt.input)
			if result != tt.expected {
				t.Errorf("IsValidURL(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
