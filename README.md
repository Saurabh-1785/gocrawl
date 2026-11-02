# 🕷️ GoCrawl

A concurrent web crawler built in Go that explores websites using goroutines, channels, and a worker pool architecture. It performs BFS traversal of the web graph with configurable depth limits, page limits, and per-domain rate limiting.

## Features

- **Concurrent Crawling** — Configurable worker pool using goroutines and channels
- **BFS Web Traversal** — Explores pages level-by-level with depth control
- **Thread-Safe URL Deduplication** — Hash set with `sync.RWMutex` and atomic check-and-set
- **Per-Domain Rate Limiting** — Prevents overwhelming any single server
- **HTML Link Extraction** — Recursive DOM tree traversal using `golang.org/x/net/html`
- **URL Normalization** — Consistent URL handling (case, fragments, trailing slashes, relative resolution)
- **Same-Domain Filtering** — Stays within the seed URL's domain
- **Configurable via CLI Flags** — Depth, pages, workers, timeout, delay

## Architecture

```
main.go → Config Validation → Crawler Engine
                                    │
                        ┌───────────┼───────────┐
                        ▼           ▼           ▼
                    Worker 1    Worker 2    Worker N
                        │           │           │
                        ▼           ▼           ▼
                    ┌────────────────────────────────┐
                    │       Shared Task Channel       │
                    │        (Buffered FIFO Queue)     │
                    └────────────────────────────────┘
                        │           │           │
                   ┌────┘     ┌─────┘     ┌─────┘
                   ▼          ▼           ▼
              Fetcher     Parser      URL Util
              (HTTP)    (HTML DOM)   (Normalize)
```

Each worker pulls a task → checks limits → fetches the page → extracts links → pushes new tasks back into the channel.

## Project Structure

```
gocrawl/
├── main.go                 # Entry point, CLI flag parsing
├── config/
│   └── config.go           # Configuration validation
├── crawler/
│   ├── crawler.go          # Crawler engine, Run() orchestration
│   ├── worker.go           # Worker goroutine logic
│   ├── visited.go          # Thread-safe visited URL tracker
│   ├── ratelimiter.go      # Per-domain rate limiter
│   ├── types.go            # Crawler-specific types
│   └── crawler_test.go     # Unit + integration tests
├── fetcher/
│   ├── fetcher.go          # HTTP page fetcher with timeout & content-type checks
│   └── fetcher_test.go     # Fetcher tests with httptest mock servers
├── parser/
│   ├── parser.go           # HTML link extraction via DOM traversal
│   └── parser_test.go      # Parser tests
└── urlutil/
    ├── urlutil.go          # URL normalization, resolution, validation
    └── urlutil_test.go     # URL utility tests
```

## Quick Start

### Prerequisites

- Go 1.21 or higher

### Build

```bash
git clone https://github.com/Saurabh-1785/gocrawl.git
cd gocrawl
go mod tidy
go build -o gocrawl .
```

### Run

```bash
./gocrawl -url https://example.com -depth 2 -pages 50 -workers 3 -delay 1s
```

## CLI Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `-url` | string | *(required)* | Seed URL to start crawling |
| `-depth` | int | `2` | Maximum crawl depth (0 = seed page only) |
| `-pages` | int | `50` | Maximum number of pages to crawl |
| `-workers` | int | `3` | Number of concurrent worker goroutines |
| `-timeout` | duration | `10s` | HTTP request timeout per page |
| `-delay` | duration | `1s` | Minimum delay between requests to the same domain |

## Usage Examples

```bash
# Crawl only the seed page
./gocrawl -url https://example.com -depth 0

# Fast crawl with more workers and no delay
./gocrawl -url https://go.dev -depth 2 -pages 100 -workers 5 -delay 0s

# Conservative crawl with longer timeout
./gocrawl -url https://books.toscrape.com -depth 3 -pages 500 -workers 2 -timeout 30s -delay 2s
```

### Sample Output

```
  ╔═══════════════════════════════════╗
  ║         🕷️  GoCrawl v1.0          ║
  ║   Concurrent Web Crawler in Go    ║
  ╚═══════════════════════════════════╝

[INFO]  Starting crawl from: https://example.com
[INFO]  Config: depth=2, pages=50, workers=3, delay=1s
[INFO]  [Worker 1] Crawling: https://example.com/ (depth: 0)
[INFO]  [Worker 1] Fetched https://example.com/ (status: 200, size: 1256 bytes)
[DEBUG] [Worker 1] Found 5 links on https://example.com/
...

===========================================
          CRAWL REPORT
===========================================
  Seed URL:       https://example.com
  Pages Crawled:  12
  Pages Skipped:  3
  Errors:         0
  Unique URLs:    12
  Time Elapsed:   8.234s
===========================================

Visited URLs:
  1. https://example.com/
  2. https://example.com/about
  ...
```

## How It Works

1. **Seed URL** is normalized and pushed into a buffered channel (task queue)
2. **N worker goroutines** consume tasks from the channel
3. Each worker:
   - Checks page limit, depth limit, and visited status
   - Waits for the rate limiter (per-domain delay)
   - Fetches the page via HTTP GET (with timeout and User-Agent header)
   - Validates the response (status 200, content-type `text/html`, max 10MB body)
   - Parses the HTML DOM tree to extract `<a href="...">` links
   - Normalizes and resolves each link (relative → absolute)
   - Filters out non-HTTP, external-domain, and already-visited URLs
   - Pushes new tasks back into the channel
4. When all tasks are processed (`WaitGroup` counter hits 0), the channel closes and workers exit
5. A **crawl report** prints final statistics

## Data Structures & Techniques

| Technique | Usage |
|---|---|
| **Hash Map** (`map[string]bool`) | O(1) URL deduplication |
| **Buffered Channel** | Thread-safe FIFO task queue (BFS) |
| **Worker Pool** | Fixed N goroutines processing shared queue |
| **Mutex / RWMutex** | Thread-safe shared state (visited set, stats, rate limiter) |
| **Recursive DFS** | HTML DOM tree traversal for link extraction |
| **BFS** | Web graph exploration (level-by-level via task queue) |
| **Producer-Consumer** | Workers both consume and produce tasks |

## Testing

### Run All Tests

```bash
go test ./... -v
```

### Run with Race Detector

```bash
go test ./... -race -v
```

### Test Coverage

```bash
go test ./... -cover
```

### Generate HTML Coverage Report

```bash
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html
```

### Test Summary

| Package | Tests | What's Covered |
|---|---|---|
| `urlutil` | 15 | URL normalization, resolution, domain comparison, validation |
| `fetcher` | 5 | HTTP fetch, status codes, content-type, timeout, User-Agent |
| `parser` | 5 | Link extraction, filtering, deduplication, relative URLs, fragments |
| `crawler` | 12 | Visited store, rate limiter, full crawl integration (httptest servers) |

## Tech Stack

- **Language**: Go 1.21+
- **HTML Parsing**: [`golang.org/x/net/html`](https://pkg.go.dev/golang.org/x/net/html)
- **HTTP**: Standard library `net/http`
- **Concurrency**: Goroutines, channels, `sync.Mutex`, `sync.RWMutex`, `sync.WaitGroup`
- **Testing**: `testing`, `net/http/httptest`

## License

This project is open source and available under the [MIT License](LICENSE).
