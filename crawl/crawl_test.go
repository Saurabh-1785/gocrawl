package crawl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Saurabh-1785/gocrawl/extractor"
)

var quiet = log.New(io.Discard, "", 0)

// chainSite serves / → /d1 → /d2 → /d3.
func chainSite() *httptest.Server {
	next := map[string]string{"/": "/d1", "/d1": "/d2", "/d2": "/d3", "/d3": ""}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, ok := next[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		link := ""
		if n != "" {
			link = fmt.Sprintf(`<a href="%s">next</a>`, n)
		}
		fmt.Fprintf(w, "<html><head><title>%s</title></head><body><p>text</p>%s</body></html>", r.URL.Path, link)
	}))
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxDepth != 2 || cfg.MaxPages != 50 || cfg.Workers != 3 ||
		cfg.Timeout != 10*time.Second || cfg.CrawlDelay != time.Second || cfg.MaxRetries != 2 {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}

// MaxDepth 0 used to be silently replaced by 2.
func TestRunDepthZeroMeansSeedOnly(t *testing.T) {
	srv := chainSite()
	defer srv.Close()

	res, err := Run(CrawlConfig{SeedURL: srv.URL, MaxDepth: 0, CrawlDelay: NoDelay, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	if res.PagesVisited != 1 {
		t.Errorf("visited %d pages, want 1", res.PagesVisited)
	}
}

// NoDelay really means no delay; 0 means the polite 1s default.
func TestRunDelaySemantics(t *testing.T) {
	srv := chainSite()
	defer srv.Close()

	start := time.Now()
	Run(CrawlConfig{SeedURL: srv.URL, MaxDepth: 3, CrawlDelay: NoDelay, Logger: quiet})
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("NoDelay crawl of 4 pages took %v", d)
	}

	start = time.Now()
	Run(CrawlConfig{SeedURL: srv.URL, MaxDepth: 1, Logger: quiet}) // 2 pages, default 1s delay
	if d := time.Since(start); d < 900*time.Millisecond {
		t.Errorf("default delay not applied: 2 pages took %v", d)
	}
}

func TestRunStreamsDocumentsAndDedupesFile(t *testing.T) {
	srv := chainSite()
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "out.jsonl")

	var mu sync.Mutex
	var docs []extractor.Document
	cfg := CrawlConfig{
		SeedURL: srv.URL, MaxDepth: 3, CrawlDelay: NoDelay, Logger: quiet, OutputPath: out,
		OnDocument: func(d extractor.Document) { mu.Lock(); docs = append(docs, d); mu.Unlock() },
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 4 || res.DocumentsWritten != 4 || res.DocumentsSkipped != 0 {
		t.Errorf("first run: streamed=%d written=%d skipped=%d, want 4, 4, 0",
			len(docs), res.DocumentsWritten, res.DocumentsSkipped)
	}

	// Same crawl again into the same file: nothing new is written.
	res, err = Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.DocumentsWritten != 0 || res.DocumentsSkipped != 4 {
		t.Errorf("second run: written=%d skipped=%d, want 0 and 4", res.DocumentsWritten, res.DocumentsSkipped)
	}
}

func TestRunCancel(t *testing.T) {
	srv := chainSite()
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := RunContext(ctx, CrawlConfig{SeedURL: srv.URL, Logger: quiet})
	if !errors.Is(err, context.Canceled) || !res.Interrupted {
		t.Errorf("err=%v interrupted=%v, want context.Canceled and true", err, res.Interrupted)
	}
}

func TestRunInvalidConfig(t *testing.T) {
	for _, cfg := range []CrawlConfig{
		{},                             // no seed
		{SeedURL: "ftp://example.com"}, // wrong scheme
		{SeedURL: "http://"},           // no host
		{SeedURL: "http://a.com", MaxDepth: -1},
	} {
		cfg.Logger = quiet
		if _, err := Run(cfg); err == nil {
			t.Errorf("Run(%+v) should fail", cfg)
		}
	}
}

func TestRunOutputPathNotWritable(t *testing.T) {
	_, err := Run(CrawlConfig{SeedURL: "http://example.com", OutputPath: t.TempDir(), Logger: quiet}) // a directory
	if err == nil {
		t.Error("expected an error when OutputPath can't be opened")
	}
}
