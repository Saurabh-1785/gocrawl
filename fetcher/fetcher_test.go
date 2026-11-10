package fetcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

func TestFetchTooLarge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(strings.Repeat("a", 2000)))
	}))
	defer server.Close()

	_, err := Fetch(context.Background(), server.URL, Options{MaxBodySize: 1000})
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge (a big page must not be cut silently)", err)
	}

	res, err := Fetch(context.Background(), server.URL, Options{MaxBodySize: 2000})
	if err != nil || len(res.Body) != 2000 {
		t.Errorf("page exactly at the limit: len=%d err=%v, want 2000 and nil", len(res.Body), err)
	}
}

func TestFetchFinalURLAfterRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/blog" {
			http.Redirect(w, r, "/blog/", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<p>blog</p>"))
	}))
	defer server.Close()

	res, err := Fetch(context.Background(), server.URL+"/blog", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalURL != server.URL+"/blog/" {
		t.Errorf("FinalURL = %q, want %q", res.FinalURL, server.URL+"/blog/")
	}
}

func TestFetchRedirectBlocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://elsewhere.invalid/", http.StatusFound)
	}))
	defer server.Close()

	_, err := Fetch(context.Background(), server.URL, Options{
		AllowRedirect: func(to *url.URL) bool { return to.Host != "elsewhere.invalid" },
	})
	if !errors.Is(err, ErrRedirectBlocked) {
		t.Errorf("err = %v, want ErrRedirectBlocked", err)
	}
}

func TestFetchErrorsAndRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/503":
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/404":
			w.WriteHeader(http.StatusNotFound)
		case "/pdf":
			w.Header().Set("Content-Type", "application/pdf")
		}
	}))
	defer server.Close()

	_, err := Fetch(context.Background(), server.URL+"/503", Options{})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 503 || se.RetryAfter != 7*time.Second || !IsRetryable(err) {
		t.Errorf("503: err=%v, want retryable StatusError with Retry-After 7s", err)
	}

	_, err = Fetch(context.Background(), server.URL+"/404", Options{})
	if IsRetryable(err) {
		t.Error("404 should not be retried")
	}

	_, err = Fetch(context.Background(), server.URL+"/pdf", Options{})
	if !errors.Is(err, ErrNotHTML) || IsRetryable(err) {
		t.Errorf("pdf: err=%v, want ErrNotHTML, not retryable", err)
	}

	_, err = Fetch(context.Background(), "http://127.0.0.1:1/", Options{Timeout: time.Second})
	if !IsRetryable(err) {
		t.Errorf("connection refused should be retryable: %v", err)
	}
}

func TestFetchContextCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Fetch(ctx, server.URL, Options{}); err == nil {
		t.Fatal("expected an error")
	}
	if time.Since(start) > time.Second {
		t.Error("cancelling the context should stop the request")
	}
}
