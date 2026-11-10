package fetcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MaxBodySize is the largest page we accept (10 MB).
// It stops one huge page from using all our memory.
const MaxBodySize = 10 * 1024 * 1024

// DefaultUserAgent tells site owners who is crawling them.
const DefaultUserAgent = "GoCrawl/1.0"

// Errors that callers can check with errors.Is.
var (
	ErrNotHTML         = errors.New("not an HTML page")
	ErrTooLarge        = errors.New("page is larger than the size limit")
	ErrRedirectBlocked = errors.New("redirect target not allowed")
)

// StatusError is returned when the server answers with a status other than 200.
type StatusError struct {
	Code       int
	RetryAfter time.Duration // from the Retry-After header, 0 if absent
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("non-200 status code: %d", e.Code)
}

// Temporary reports whether trying again later might work:
// 429 Too Many Requests and 5xx server errors.
func (e *StatusError) Temporary() bool {
	return e.Code == http.StatusTooManyRequests || e.Code >= 500
}

// Options control one fetch. The zero value is usable.
type Options struct {
	Timeout     time.Duration     // whole request incl. body; 0 = 10s
	UserAgent   string            // "" = DefaultUserAgent
	MaxBodySize int64             // 0 = MaxBodySize
	Transport   http.RoundTripper // nil = http.DefaultTransport

	// AllowRedirect is asked before following each redirect.
	// Return false to stop (for example, the target is on another site,
	// or robots.txt forbids it). nil allows every redirect.
	AllowRedirect func(to *url.URL) bool
}

// Result is a successfully fetched HTML page.
type Result struct {
	Body       string
	StatusCode int
	FinalURL   string // URL after redirects; relative links resolve against this
}

// Fetch downloads one HTML page.
//
// It returns an error if the request fails or times out, the status is not
// 200 (*StatusError), the page is not HTML (ErrNotHTML), the page is over the
// size limit (ErrTooLarge), or a redirect was refused (ErrRedirectBlocked).
// Cancelling ctx stops the request.
func Fetch(ctx context.Context, pageURL string, opt Options) (Result, error) {
	if opt.Timeout <= 0 {
		opt.Timeout = 10 * time.Second
	}
	if opt.UserAgent == "" {
		opt.UserAgent = DefaultUserAgent
	}
	if opt.MaxBodySize <= 0 {
		opt.MaxBodySize = MaxBodySize
	}

	// A Client is cheap. With Transport nil it uses http.DefaultTransport,
	// so connections are still pooled and reused across requests.
	client := &http.Client{
		Timeout:   opt.Timeout,
		Transport: opt.Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if opt.AllowRedirect != nil && !opt.AllowRedirect(req.URL) {
				return fmt.Errorf("%w: %s", ErrRedirectBlocked, req.URL)
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return Result{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", opt.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer func() {
		// Read a little of what's left so the connection can be reused.
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
	}()

	res := Result{StatusCode: resp.StatusCode, FinalURL: resp.Request.URL.String()}

	if resp.StatusCode != http.StatusOK {
		return res, &StatusError{Code: resp.StatusCode, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}

	// Check the type BEFORE reading the body: a linked PDF costs almost nothing.
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml+xml") {
		return res, fmt.Errorf("%w (content-type: %s)", ErrNotHTML, contentType)
	}

	// Read one byte more than the limit, so we can tell "exactly at the
	// limit" from "too big" instead of silently cutting the page.
	body, err := io.ReadAll(io.LimitReader(resp.Body, opt.MaxBodySize+1))
	if err != nil {
		return res, fmt.Errorf("failed to read response body: %w", err)
	}
	if int64(len(body)) > opt.MaxBodySize {
		return res, fmt.Errorf("%w (%d bytes)", ErrTooLarge, opt.MaxBodySize)
	}

	res.Body = string(body)
	return res, nil
}

// FetchPage is the simple form of Fetch: no context, default options.
// It returns the body, the status code, and an error.
func FetchPage(pageURL string, timeout time.Duration) (string, int, error) {
	res, err := Fetch(context.Background(), pageURL, Options{Timeout: timeout})
	return res.Body, res.StatusCode, err
}

// IsRetryable reports whether a failed fetch is worth trying again.
// Network errors, timeouts, 429 and 5xx are; 404, wrong content type,
// too large, blocked redirects and cancellation are not.
func IsRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Temporary()
	}
	if errors.Is(err, ErrNotHTML) || errors.Is(err, ErrTooLarge) || errors.Is(err, ErrRedirectBlocked) {
		return false
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return true // connection refused/reset, DNS failure, timeout
	}
	return false
}

// retryAfter parses a Retry-After header: seconds, or an HTTP date.
func retryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
