# 🕷️ GoCrawl

A concurrent, polite and fault-tolerant web crawler in Go. It explores a website breadth-first with a pool of worker goroutines, extracts clean text from every page, and streams the results to your code or to a JSONL file. It can be used as a CLI or imported as a library.

## Features

- **Concurrent crawling**: fixed worker pool sharing one frontier (mutex + `sync.Cond`)
- **Exact limits**: each URL queued once; a page slot is reserved before fetching, so the page limit never overshoots
- **Polite**: obeys `robots.txt` (RFC 9309, incl. `Crawl-delay`); per-domain rate limiter that hands out time slots
- **Fault tolerant**: retries timeouts, network errors, 429 and 5xx with exponential backoff, jitter and `Retry-After`
- **Stop and resume**: Ctrl-C stops cleanly; atomic checkpoints let a crawl continue without losing or repeating work
- **Redirect-aware**: follows redirects only within the site (the seed may move hosts, e.g. `golang.org` → `go.dev`); resolves links against the final URL and `<base href>`
- **Smart URL dedupe**: case, fragments, trailing slashes, index files, default ports, query order, tracking parameters, `www.`
- **Content extraction**: title + clean text, skipping scripts, styles, navigation and footers
- **JSONL output**: append-only, deduplicated across runs, repairs a line cut off by a crash
- **Library API**: `crawl.RunContext(ctx, cfg)` with an `OnDocument` callback

## Architecture

```
main.go / your program
        │
        ▼
crawl.RunContext ──► Crawler.RunContext ──► N workers
                                               │  ▲
                                         Next()│  │Push(new links)
                                               ▼  │
                                ┌──────────────────────────────┐
                                │ Frontier (mutex + sync.Cond) │──► checkpoint file
                                │ queue · seen · page budget   │
                                └──────────────────────────────┘

worker: rate-limit slot → fetch (+retry) → parse once → links → OnPage → push links
OnPage: extractor → OnDocument callback and/or DocStore (JSONL)
```

The crawl ends when nothing is queued and nothing is in flight, when the page limit is reached, or when it is cancelled.

## Project Structure

```
gocrawl/
├── main.go                 # CLI: flags, Ctrl-C handling, summary
├── crawl/                  # Public API: CrawlConfig, DefaultConfig, Run, RunContext
├── config/                 # Internal config + validation
├── crawler/
│   ├── crawler.go          # Engine: RunContext, Stop, site and robots checks
│   ├── worker.go           # Worker loop, page processing, retries
│   ├── frontier.go         # Queue: dedupe, page budget, end detection, snapshots
│   ├── ratelimiter.go      # Per-domain time slots
│   ├── checkpoint.go       # Atomic save / restore of crawl progress
│   └── types.go            # CrawlTask, Page, CrawlStats
├── fetcher/                # HTTP fetch: timeout, status, type, size, redirects
├── robots/                 # robots.txt parser + per-site cache
├── parser/                 # Link extraction from the HTML tree
├── urlutil/                # URL normalization, resolution, same-site check
├── extractor/              # HTML → Document (title, clean text, SHA-256 ID)
├── docstore/               # JSONL document store
└── doc/                    # Deep-dive guide
```

## Quick Start

Requires Go 1.23 or newer.

```bash
git clone https://github.com/Saurabh-1785/gocrawl.git
cd gocrawl
go build -o gocrawl .
./gocrawl -url https://books.toscrape.com -depth 1 -pages 20 -workers 3 -delay 500ms -output books.jsonl
```

## CLI Flags

| Flag | Default | Description |
|---|---|---|
| `-url` | *(required)* | Seed URL to start crawling |
| `-depth` | `2` | Maximum crawl depth (`0` = seed page only) |
| `-pages` | `50` | Maximum number of HTML pages to fetch |
| `-workers` | `3` | Number of concurrent worker goroutines |
| `-timeout` | `10s` | HTTP request timeout per page |
| `-delay` | `1s` | Minimum delay between requests to the same domain (`0` = none) |
| `-retries` | `2` | Retries for timeouts, 429 and 5xx (`0` = none) |
| `-output` | `output.jsonl` | Output file (appends; documents already in it are skipped) |
| `-checkpoint` | *(none)* | File to save crawl progress to |
| `-resume` | `false` | Continue from the checkpoint |
| `-ignore-robots` | `false` | Do not obey robots.txt (only for sites you own) |
| `-user-agent` | `GoCrawl/1.0` | User-Agent header |

## Usage Examples

```bash
# Crawl only the seed page
./gocrawl -url https://example.com -depth 0

# Long crawl that can be stopped with Ctrl-C and resumed later
./gocrawl -url https://books.toscrape.com -pages 500 -checkpoint books.state -output books.jsonl
./gocrawl -url https://books.toscrape.com -pages 500 -checkpoint books.state -output books.jsonl -resume

# Your own test server: no delay, no robots.txt
./gocrawl -url http://localhost:8080 -delay 0 -ignore-robots
```

### Sample Output

```
[INFO]  Config: depth=1, pages=5, workers=3, delay=1s, retries=2, robots=true
[INFO]  [Worker 3] Crawling: http://books.toscrape.com (depth: 0)
[INFO]  [Worker 3] Fetched http://books.toscrape.com (status: 200, size: 51294 bytes)
[DEBUG] [Worker 3] Found 73 links on http://books.toscrape.com
...
===========================================
          CRAWL COMPLETE
===========================================
  Pages Visited:      5
  Pages Failed:       0
  Pages Skipped:      0
  Robots Blocked:     0
  Retries:            0
  Unique URLs:        73
  Documents Written:  5
  Already in File:    0
  Duration:           6.532s
  Output:             books.jsonl
===========================================
```

## Library Usage

```go
cfg := crawl.DefaultConfig()
cfg.SeedURL = "https://example.com"
cfg.MaxPages = 100
cfg.OnDocument = func(doc extractor.Document) {
    // index it, store it... called from worker goroutines: must be thread-safe
}
result, err := crawl.RunContext(ctx, cfg) // cancel ctx to stop
```

Zero values: `MaxDepth: 0` means seed page only. `CrawlDelay: 0` and `MaxRetries: 0` mean the polite defaults (1s, 2); use `crawl.NoDelay` / `crawl.NoRetries` to turn them off. `Transport` defaults to `http.DefaultTransport`.

## How It Works

1. The seed URL is checked against robots.txt and pushed to the **frontier**.
2. **N workers** call `frontier.Next()`. It hands out a task only if a page slot is free, and reports when the crawl is over.
3. Each worker:
   - waits for its **rate-limit slot** for the domain
   - fetches the page (status 200, `text/html`, max 10 MB), **retrying** temporary failures
   - skips it if a redirect landed on a page already seen
   - parses the HTML **once**, extracts links against the final URL
   - hands the page to the extractor → callback / JSONL
   - pushes new links that are on the same site, unseen, within depth, and allowed by robots.txt
   - reports success (uses a page slot), failure (frees it), or "requeue" if the crawl was stopped
4. A **checkpoint** is written every 10s and when the crawl stops; it is removed once the crawl completes.

## Testing

```bash
go test ./...           # all tests
go test -race ./...     # with the race detector
go test -cover ./...    # coverage
```

102 test functions (129 with subtests). Concurrency tests run with 5–20 workers and count what the test server actually received, so limits and rate limits are checked from the outside.

| Package | Tests | What's covered |
|---|---|---|
| `crawler` | 32 | Frontier, rate limiter, page/rate limits with many workers, redirects, robots, retries, cancel, resume |
| `extractor` | 20 | Title/content extraction, skipped tags, spacing, dedupe IDs |
| `fetcher` | 10 | Status, content type, size limit, redirects, timeouts, retryable errors |
| `docstore` | 9 | JSONL writes, concurrency, cross-run dedupe, crash repair |
| `robots` | 9 | RFC 9309 matching, groups, status handling, caching |
| `parser` | 8 | Link extraction, filtering, relative URLs, `<base href>` |
| `crawl` | 7 | Defaults, zero values, streaming, file dedupe, cancel, validation |
| `urlutil` | 7 | Normalization, resolution, same-site rules |

## Tech Stack

- **Language**: Go 1.23+
- **HTML parsing**: [`golang.org/x/net/html`](https://pkg.go.dev/golang.org/x/net/html)
- **HTTP**: standard library `net/http`
- **Concurrency**: goroutines, `sync.Mutex`, `sync.RWMutex`, `sync.Cond`, `sync.WaitGroup`, `context`
- **Testing**: `testing`, `net/http/httptest`

## License

This project is open source and available under the [MIT License](LICENSE).
