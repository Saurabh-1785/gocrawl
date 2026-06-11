package fetcher

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestFetchPageSuccess tests a successful HTML page fetch.
func TestFetchPageSuccess(t *testing.T) {
	// Create a test server that serves HTML
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html><body><h1>Hello World</h1></body></html>"))
	}))
	defer server.Close()

	body, statusCode, err := FetchPage(server.URL, 5*time.Second)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if statusCode != 200 {
		t.Errorf("Expected status 200, got %d", statusCode)
	}

	if body == "" {
		t.Error("Expected non-empty body")
	}

	if body != "<html><body><h1>Hello World</h1></body></html>" {
		t.Errorf("Unexpected body: %s", body)
	}
}

// TestFetchPageNon200 tests that non-200 status codes return an error.
func TestFetchPageNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, statusCode, err := FetchPage(server.URL, 5*time.Second)

	if err == nil {
		t.Error("Expected an error for 404 response")
	}

	if statusCode != 404 {
		t.Errorf("Expected status 404, got %d", statusCode)
	}
}

// TestFetchPageNonHTML tests that non-HTML content types are rejected.
func TestFetchPageNonHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"key": "value"}`))
	}))
	defer server.Close()

	_, _, err := FetchPage(server.URL, 5*time.Second)

	if err == nil {
		t.Error("Expected an error for non-HTML content type")
	}
}

// TestFetchPageTimeout tests that slow servers trigger a timeout.
func TestFetchPageTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wait longer than the timeout
		time.Sleep(2 * time.Second)
		w.Write([]byte("too slow"))
	}))
	defer server.Close()

	_, _, err := FetchPage(server.URL, 500*time.Millisecond)

	if err == nil {
		t.Error("Expected a timeout error")
	}
}

// TestFetchPageUserAgent tests that the User-Agent header is set.
func TestFetchPageUserAgent(t *testing.T) {
	var receivedUA string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html></html>"))
	}))
	defer server.Close()

	FetchPage(server.URL, 5*time.Second)

	if receivedUA != "GoCrawl/1.0" {
		t.Errorf("Expected User-Agent 'GoCrawl/1.0', got %q", receivedUA)
	}
}
