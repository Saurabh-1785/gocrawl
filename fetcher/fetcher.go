package fetcher

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxBodySize is the maximum page size we'll read (10 MB).
// This prevents a single huge page from using all our memory.
const maxBodySize = 10 * 1024 * 1024 // 10 MB

// FetchPage downloads a URL and returns the HTML body as a string.
//
// It returns:
//   - body: the HTML content of the page
//   - statusCode: the HTTP status code (e.g., 200, 404)
//   - err: any error that occurred
//
// The function will return an error if:
//   - The request times out
//   - The status code is not 200
//   - The content type is not HTML
//   - The page is larger than 10MB
func FetchPage(pageURL string, timeout time.Duration) (string, int, error) {
	// Step 1: Create an HTTP client with a timeout
	// The timeout covers the entire request: connecting, sending, reading response.
	client := &http.Client{
		Timeout: timeout,
	}

	// Step 2: Create the GET request
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create request: %v", err)
	}

	// Set a User-Agent header so websites know who's crawling them
	req.Header.Set("User-Agent", "GoCrawl/1.0")

	// Step 3: Send the request
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("failed to fetch URL: %v", err)
	}
	// Always close the response body when we're done
	defer resp.Body.Close()

	// Step 4: Check the status code — we only want successful responses
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, fmt.Errorf("non-200 status code: %d", resp.StatusCode)
	}

	// Step 5: Check the Content-Type — we only want HTML pages
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(contentType), "text/html") {
		return "", resp.StatusCode, fmt.Errorf("not an HTML page (content-type: %s)", contentType)
	}

	// Step 6: Read the body, but limit it to maxBodySize
	// io.LimitReader prevents reading more than 10MB
	limitedReader := io.LimitReader(resp.Body, maxBodySize)
	bodyBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", resp.StatusCode, fmt.Errorf("failed to read response body: %v", err)
	}

	return string(bodyBytes), resp.StatusCode, nil
}
