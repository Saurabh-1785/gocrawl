# 🧪 GoCrawl — Complete Testing Guide

> **Purpose**: This document is your single source of truth for verifying that every component of the GoCrawl project works correctly, every flow is connected end-to-end, and the crawler behaves as expected under various conditions.

---

## Table of Contents

1. [Prerequisites](#1-prerequisites)
2. [Quick Health Check (Build & Smoke Test)](#2-quick-health-check)
3. [Unit Tests — Per Package](#3-unit-tests--per-package)
   - 3.1 [urlutil](#31-urlutil-package)
   - 3.2 [fetcher](#32-fetcher-package)
   - 3.3 [parser](#33-parser-package)
   - 3.4 [crawler (unit + integration)](#34-crawler-package-unit--integration)
4. [Run ALL Unit Tests at Once](#4-run-all-unit-tests-at-once)
5. [Race Condition Detection](#5-race-condition-detection)
6. [Test Coverage Report](#6-test-coverage-report)
7. [End-to-End (E2E) Manual Tests](#7-end-to-end-e2e-manual-tests)
8. [Flow Connectivity Verification](#8-flow-connectivity-verification)
9. [Edge Case & Negative Tests](#9-edge-case--negative-tests)
10. [Postman Testing (HTTP Fetcher)](#10-postman-testing-http-fetcher)
11. [Testing Checklist (Print & Check Off)](#11-testing-checklist)
12. [Troubleshooting Common Issues](#12-troubleshooting-common-issues)

---

## 1. Prerequisites

Before running any tests, make sure you have:

| Requirement | Check Command | Expected Output |
|---|---|---|
| Go installed (1.21+) | `go version` | `go version go1.21.x ...` |
| Module dependencies | `go mod tidy` | No errors |
| Project builds | `go build ./...` | No errors |
| Internet access (for E2E) | `ping -c 1 google.com` | Response received |

```bash
# Run these in order from the project root (/home/Saurabh/gocrawl):
go version
go mod tidy
go build ./...
```

> **What this implies**: If `go build ./...` succeeds, all packages compile and all imports resolve correctly. This is your first confirmation that the code structure is valid and every package can see its dependencies.

---

## 2. Quick Health Check

### 2.1 Build the Binary

```bash
cd /home/Saurabh/gocrawl
go build -o gocrawl .
```

**✅ PASS**: Binary `gocrawl` is created without errors.  
**❌ FAIL**: Compilation errors indicate broken imports, syntax errors, or missing dependencies.

**What this implies**: The `main.go → config → crawler → fetcher/parser/urlutil` dependency chain is intact.

### 2.2 Smoke Test — Show Help

```bash
./gocrawl
```

**✅ Expected output**:
```
Error: seed URL is required (use -url flag)

Usage:
  -delay duration
        Delay between requests to the same domain (e.g., 1s, 500ms) (default 1s)
  -depth int
        Maximum crawl depth (0 = seed page only) (default 2)
  -pages int
        Maximum number of pages to crawl (default 50)
  -timeout duration
        HTTP request timeout (e.g., 10s, 30s) (default 10s)
  -url string
        Seed URL to start crawling (required)
  -workers int
        Number of concurrent workers (default 3)
```

**What this implies**: The `main.go` → `config.ValidateConfig()` flow is connected. Flag parsing works. Config validation catches the missing required field.

### 2.3 Smoke Test — Quick Crawl

```bash
./gocrawl -url https://go.dev -depth 1 -pages 5 -workers 2 -delay 500ms
```

**✅ Expected output** (similar to):
```
  ╔═══════════════════════════════════╗
  ║         🕷️  GoCrawl v1.0          ║
  ║   Concurrent Web Crawler in Go    ║
  ╚═══════════════════════════════════╝

[INFO]  Starting crawl from: https://go.dev
[INFO]  Config: depth=1, pages=5, workers=2, delay=500ms
[INFO]  [Worker X] Crawling: https://go.dev/ (depth: 0)
[INFO]  [Worker X] Fetched https://go.dev/ (status: 200, size: XXXX bytes)
...
===========================================
          CRAWL REPORT
===========================================
  Seed URL:       https://go.dev
  Pages Crawled:  5
  Pages Skipped:  ...
  Errors:         ...
  Unique URLs:    ...
  Time Elapsed:   ...
===========================================
```

**What this implies**: The **entire end-to-end flow** works:
- `main.go` → parses flags → builds `Config`
- `config.ValidateConfig()` → passes
- `crawler.NewCrawler()` → creates `VisitedStore`, `RateLimiter`, `CrawlStats`
- `crawler.Run()` → normalizes seed URL, starts workers, pushes seed task
- Workers → `fetcher.FetchPage()` → `parser.ExtractLinks()` → `urlutil` (resolve, normalize, validate, same-domain check)
- `VisitedStore` prevents re-visits
- `RateLimiter` delays between same-domain requests
- `CrawlStats` tracks counts
- Report prints correctly

---

## 3. Unit Tests — Per Package

### 3.1 `urlutil` Package

**File**: `urlutil/urlutil_test.go`  
**Functions tested**: `NormalizeURL`, `ResolveURL`, `IsSameDomain`, `IsValidURL`

```bash
cd /home/Saurabh/gocrawl
go test ./urlutil/ -v
```

**Expected**: All tests PASS:

| Test Name | What It Verifies |
|---|---|
| `TestNormalizeURL/lowercase_scheme_and_host` | `HTTP://EXAMPLE.COM/Page` → `http://example.com/Page` |
| `TestNormalizeURL/remove_fragment` | `#section` is stripped |
| `TestNormalizeURL/remove_trailing_slash` | `/page/` → `/page` |
| `TestNormalizeURL/keep_root_slash` | `http://example.com/` stays as is |
| `TestNormalizeURL/already_clean_URL` | No changes to clean URLs |
| `TestNormalizeURL/remove_fragment_and_trailing_slash` | Combined normalization |
| `TestResolveURL/simple_relative_path` | `../about` resolved correctly |
| `TestResolveURL/absolute_path_on_same_host` | `/contact` resolved to full URL |
| `TestResolveURL/already_absolute_URL` | Absolute URL passes through |
| `TestResolveURL/relative_in_same_directory` | `post2` resolved to sibling |
| `TestIsSameDomain/same_domain` | ✅ true for same host |
| `TestIsSameDomain/different_domain` | ❌ false for different host |
| `TestIsSameDomain/same_domain_different_scheme` | ✅ http vs https is same domain |
| `TestIsSameDomain/same_domain_different_case` | ✅ case-insensitive comparison |
| `TestIsSameDomain/subdomain_is_different` | ❌ `sub.example.com` ≠ `example.com` |
| `TestIsValidURL/*` | Only `http://` and `https://` are valid |

**What this implies**: URL manipulation logic is correct. The crawler will correctly normalize, resolve relative paths, and filter non-HTTP URLs.

---

### 3.2 `fetcher` Package

**File**: `fetcher/fetcher_test.go`  
**Function tested**: `FetchPage`

```bash
go test ./fetcher/ -v
```

**Expected**: All tests PASS:

| Test Name | What It Verifies |
|---|---|
| `TestFetchPageSuccess` | Successful fetch returns body + status 200 + nil error |
| `TestFetchPageNon200` | 404 response returns error + correct status code |
| `TestFetchPageNonHTML` | `application/json` content-type is rejected |
| `TestFetchPageTimeout` | Slow server triggers timeout error |
| `TestFetchPageUserAgent` | Outgoing request has `GoCrawl/1.0` User-Agent header |

> **Note**: These tests use Go's `httptest.NewServer` — they spin up local HTTP servers, so **no internet is required**.

**What this implies**: The fetcher correctly handles HTTP communication, enforces content-type checks, respects timeouts, and sets a proper User-Agent.

---

### 3.3 `parser` Package

**File**: `parser/parser_test.go`  
**Function tested**: `ExtractLinks`

```bash
go test ./parser/ -v
```

**Expected**: All tests PASS:

| Test Name | What It Verifies |
|---|---|
| `TestExtractLinks` | Finds 4 unique links, deduplicates `/about` |
| `TestExtractLinksFiltersJavascript` | Filters `javascript:`, `mailto:`, `tel:` — only real page survives |
| `TestExtractLinksEmptyHTML` | Empty/no-link HTML → 0 links |
| `TestExtractLinksRelativeURLs` | `../about`, `post2`, `/contact` all resolved correctly |
| `TestExtractLinksWithFragments` | `/page#section1` and `/page#section2` → 1 unique link (`/page`) |

**What this implies**: HTML parsing + link extraction + URL resolution + deduplication pipeline works correctly. The parser integrates correctly with `urlutil` functions.

---

### 3.4 `crawler` Package (Unit + Integration)

**File**: `crawler/crawler_test.go`  
**Functions tested**: `VisitedStore`, `RateLimiter`, full `Crawler.Run()` with httptest servers

```bash
go test ./crawler/ -v
```

**Expected**: All tests PASS:

#### Unit Tests (VisitedStore — in crawler package)

| Test Name | What It Verifies |
|---|---|
| `TestMarkVisited` | Atomic check-and-set works correctly |
| `TestIsVisited` | Read-only check works |
| `TestVisitedCount` | Count is accurate, ignores duplicates |
| `TestConcurrentVisitedAccess` | Thread-safety with 100 goroutines |
| `TestGetAll` | Returns all URLs |

#### Unit Tests (RateLimiter — in crawler package)

| Test Name | What It Verifies |
|---|---|
| `TestRateLimiterDelay` | Same-domain delay enforced |
| `TestRateLimiterDifferentDomains` | Cross-domain independence |
| `TestRateLimiterZeroDelay` | No delay when set to 0 |

#### Integration Tests (Full Crawler)

| Test Name | What It Verifies |
|---|---|
| `TestCrawlerFindsAllPages` | Discovers all 5 pages on test site (`/`, `/about`, `/blog`, `/blog/post1`, `/contact`) |
| `TestCrawlerDepthLimit` | With `maxDepth=1`, only visits 3 pages (depth 0 + depth 1). Does NOT visit `/contact` or `/blog/post1` |
| `TestCrawlerPageLimit` | With `maxPages=2`, crawls at most 2 pages |
| `TestCrawlerSameDomainOnly` | External URLs on different domains are NOT followed |

**What this implies**: The full crawler orchestration works:
- Worker goroutines pull from task channel
- Depth limiting stops deep crawls
- Page count limiting stops at the configured max
- Same-domain filtering prevents cross-site crawling
- Visited tracking prevents re-visiting pages
- The cycle (`/blog/post1` → `/` → `/about` → ...) doesn't cause infinite loops

---

## 4. Run ALL Unit Tests at Once

```bash
cd /home/Saurabh/gocrawl
go test ./... -v
```

**✅ PASS**: All tests across ALL packages pass.

**Quick version (without verbose output)**:
```bash
go test ./...
```

**What this implies**: Every individual package works AND they integrate correctly (since `parser` imports `urlutil`, `crawler` imports `fetcher`/`parser`/`urlutil`, etc.).

---

## 5. Race Condition Detection

Go has a built-in **race detector**. This is **critical** for GoCrawl because it uses goroutines, channels, mutexes, and shared state.

```bash
go test ./... -race -v
```

**✅ PASS**: No race conditions detected in any test.  
**❌ FAIL**: Output will show `WARNING: DATA RACE` with stack traces pointing to the problematic code.

### What to look for:
- `VisitedStore` uses `sync.RWMutex` — race detector verifies this is correct
- `CrawlStats` uses `sync.Mutex` — race detector verifies concurrent increments are safe
- `RateLimiter` uses `sync.Mutex` — race detector verifies domain locking works

**What this implies**: All concurrent data structures are properly synchronized. No goroutine is reading while another writes without proper locking.

---

## 6. Test Coverage Report

### Generate Coverage Numbers

```bash
go test ./... -cover
```

**Expected output** (example):
```
ok      github.com/Saurabh-1785/gocrawl/crawler       1.234s  coverage: 75.0% of statements
ok      github.com/Saurabh-1785/gocrawl/fetcher        0.567s  coverage: 85.0% of statements
ok      github.com/Saurabh-1785/gocrawl/parser          0.123s  coverage: 90.0% of statements
ok      github.com/Saurabh-1785/gocrawl/urlutil         0.123s  coverage: 92.0% of statements
```

### Generate HTML Coverage Report (Visual)

```bash
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html
```

Then open `coverage.html` in a browser. Green lines = covered, red lines = not covered.

**What this implies**: You can see exactly which lines of code are exercised by tests and which are not.

---

## 7. End-to-End (E2E) Manual Tests

These tests run the actual compiled binary against real websites. **Internet connection required.**

### E2E Test 1: Basic Crawl

```bash
./gocrawl -url https://go.dev -depth 1 -pages 5 -workers 2 -delay 1s
```

**✅ Check**:
- [ ] Banner prints
- [ ] Log shows `[INFO] Starting crawl from: https://go.dev`
- [ ] Workers log `[INFO] [Worker X] Crawling: ...`
- [ ] Workers log `[INFO] [Worker X] Fetched ... (status: 200, ...)`
- [ ] Workers log `[DEBUG] [Worker X] Found N links on ...`
- [ ] Final report shows `Pages Crawled`, `Pages Skipped`, `Errors`, `Unique URLs`, `Time Elapsed`
- [ ] List of visited URLs prints
- [ ] Program exits with `Crawl completed successfully!`

### E2E Test 2: Depth 0 (Seed Only)

```bash
./gocrawl -url https://example.com -depth 0 -pages 100 -workers 3
```

**✅ Check**:
- [ ] Only 1 page is crawled (the seed)
- [ ] Pages Crawled = 1
- [ ] No links followed (even if links exist on the page)

**What this implies**: `depth=0` means "crawl seed page only, follow no links."

### E2E Test 3: Page Limit

```bash
./gocrawl -url https://go.dev -depth 5 -pages 3 -workers 2 -delay 500ms
```

**✅ Check**:
- [ ] Pages Crawled ≤ 3
- [ ] Crawl stops and produces report after hitting the limit

### E2E Test 4: Single Worker

```bash
./gocrawl -url https://go.dev -depth 1 -pages 5 -workers 1 -delay 500ms
```

**✅ Check**:
- [ ] Only `[Worker 1]` appears in logs
- [ ] Crawl completes (single worker handles everything)

### E2E Test 5: Fast Crawl (No Delay)

```bash
./gocrawl -url https://go.dev -depth 1 -pages 5 -workers 3 -delay 0s
```

**✅ Check**:
- [ ] Crawl completes much faster than with 1s delay
- [ ] No errors from rate limiting

---

## 8. Flow Connectivity Verification

This section ensures every component is wired to the next correctly.

### Flow Diagram

```
main.go
  │
  ├── flag.Parse() → builds config.Config struct
  │
  ├── config.ValidateConfig(cfg) → validates all fields
  │
  ├── crawler.NewCrawler(cfg)
  │     ├── creates VisitedStore  (crawler/visited.go)
  │     ├── creates RateLimiter   (crawler/ratelimiter.go)
  │     └── creates CrawlStats    (crawler/types.go)
  │
  └── crawler.Run()
        │
        ├── urlutil.NormalizeURL(seedURL)     ← urlutil connected
        │
        ├── Starts N worker goroutines
        │
        └── worker(id, tasks, wg)
              │
              ├── stats.GetCrawledCount()     ← CrawlStats connected
              ├── visited.MarkVisited(url)    ← VisitedStore connected
              ├── rateLimiter.Wait(url)       ← RateLimiter connected
              │
              ├── fetcher.FetchPage(url, timeout)  ← Fetcher connected
              │     └── HTTP GET → reads body
              │
              ├── parser.ExtractLinks(body, url)   ← Parser connected
              │     ├── html.Parse()
              │     ├── urlutil.ResolveURL()    ← urlutil connected
              │     ├── urlutil.NormalizeURL()  ← urlutil connected
              │     └── urlutil.IsValidURL()    ← urlutil connected
              │
              └── for each link:
                    ├── urlutil.IsSameDomain()  ← urlutil connected
                    ├── visited.IsVisited()     ← VisitedStore connected
                    └── tasks <- CrawlTask{...} ← back to worker channel
```

### How to Verify Each Connection

| # | Connection | How to Verify | Command/Test |
|---|---|---|---|
| 1 | `main.go` → `config` | Run without `-url` flag | `./gocrawl` → shows validation error |
| 2 | `main.go` → `crawler` | Run with valid URL | `./gocrawl -url https://example.com -depth 0 -pages 1` |
| 3 | `crawler` → `urlutil.NormalizeURL` | Check logs show normalized URL | Seed URL in log should be lowercase, no fragment |
| 4 | `crawler` → `fetcher.FetchPage` | Worker logs show `Fetched ... (status: 200, size: ...)` | `./gocrawl -url https://example.com -depth 0` |
| 5 | `crawler` → `parser.ExtractLinks` | Worker logs show `Found N links on ...` | `./gocrawl -url https://go.dev -depth 1 -pages 3` |
| 6 | `parser` → `urlutil.ResolveURL` | Relative links get resolved in output | Visible in discovered link URLs |
| 7 | `crawler` → `urlutil.IsSameDomain` | External links not in visited list | Crawl report shows no external domains |
| 8 | `crawler` → `VisitedStore` | No duplicate URLs in visited list | Check "Visited URLs" list has no duplicates |
| 9 | `crawler` → `RateLimiter` | Visible delay between same-domain requests | Set `-delay 2s` and observe timing in logs |
| 10 | `crawler` → `CrawlStats` | Report numbers make sense | Crawled + Skipped + Errors ≈ total tasks processed |

### Quick Flow Verification Command

```bash
# This single command exercises the ENTIRE flow end-to-end:
./gocrawl -url https://go.dev -depth 2 -pages 10 -workers 2 -delay 500ms 2>&1 | head -50
```

**✅ If you see**: Banner → Config info → Worker crawling logs → Worker fetched logs → Link discovery logs → Final report  
**Then**: ALL flows are connected.

---

## 9. Edge Case & Negative Tests

### 9.1 Invalid Inputs (Config Validation)

```bash
# No URL
./gocrawl
# Expected: Error: seed URL is required

# Invalid URL (no scheme)
./gocrawl -url example.com
# Expected: Error: seed URL must start with http:// or https://

# Negative depth
./gocrawl -url https://example.com -depth -1
# Expected: Error: max depth must be 0 or greater

# Zero pages
./gocrawl -url https://example.com -pages 0
# Expected: Error: max pages must be at least 1

# Zero workers
./gocrawl -url https://example.com -workers 0
# Expected: Error: worker count must be at least 1

# Negative timeout
./gocrawl -url https://example.com -timeout -1s
# Expected: Error: timeout must be greater than 0

# Negative delay
./gocrawl -url https://example.com -delay -1s
# Expected: Error: crawl delay must be 0 or greater
```

**What this implies**: All validation rules in `config.ValidateConfig()` are enforced. Bad input is caught before crawling starts.

### 9.2 Unreachable URL

```bash
./gocrawl -url http://this-domain-does-not-exist-12345.com -depth 1 -pages 5
```

**✅ Check**:
- [ ] Error logged: `[ERROR] Error fetching ...`
- [ ] Errors count > 0 in report
- [ ] Program still exits cleanly (doesn't crash)

### 9.3 Non-HTML Page

```bash
./gocrawl -url https://jsonplaceholder.typicode.com/posts/1 -depth 0 -pages 1
```

**✅ Check**:
- [ ] Error about non-HTML content type
- [ ] Program exits cleanly

### 9.4 Very Short Timeout

```bash
./gocrawl -url https://go.dev -depth 1 -pages 5 -timeout 1ms
```

**✅ Check**:
- [ ] Timeout errors logged
- [ ] Program handles timeouts gracefully

---

## 10. Postman Testing (HTTP Fetcher)

> **Context**: GoCrawl is a CLI tool, NOT an HTTP API server. There is no REST API to call with Postman. However, you can use Postman to **understand what the fetcher does** and to **test the URLs the fetcher would call**.

### 10.1 Simulating What `fetcher.FetchPage()` Does

The fetcher sends a GET request with these specifics:
- **Method**: `GET`
- **Header**: `User-Agent: GoCrawl/1.0`
- **Timeout**: Configurable (default 10s)
- **Accepts**: Only `text/html` content types

#### Step-by-Step in Postman:

1. **Open Postman** → Click **"New Request"**

2. **Test a successful HTML page**:
   - Method: `GET`
   - URL: `https://example.com`
   - Headers tab: Add `User-Agent` = `GoCrawl/1.0`
   - Click **Send**
   - **✅ Check**: Status = `200`, Body has HTML, `Content-Type` header contains `text/html`

3. **Test a non-HTML page (JSON API)**:
   - Method: `GET`
   - URL: `https://jsonplaceholder.typicode.com/posts/1`
   - Headers tab: Add `User-Agent` = `GoCrawl/1.0`
   - Click **Send**
   - **✅ Check**: Status = `200`, but `Content-Type` = `application/json`
   - **Implication**: The fetcher would **reject** this page (returns error: "not an HTML page")

4. **Test a 404 page**:
   - Method: `GET`
   - URL: `https://example.com/nonexistent-page-12345`
   - Click **Send**
   - **✅ Check**: Status = `404`
   - **Implication**: The fetcher would return an error ("non-200 status code: 404")

5. **Test redirect behavior**:
   - Method: `GET`
   - URL: `http://google.com` (HTTP, not HTTPS)
   - Click **Send**
   - **✅ Check**: Postman follows the redirect to `https://www.google.com`
   - **Implication**: Go's `http.Client` follows redirects by default, so the fetcher handles this

### 10.2 Postman Collection Setup (Optional)

Create a **Postman Collection** called `GoCrawl Fetcher Tests`:

| Request Name | Method | URL | Expected Status | Key Check |
|---|---|---|---|---|
| HTML Page (Success) | GET | `https://example.com` | 200 | Content-Type contains `text/html` |
| JSON API (Rejected) | GET | `https://jsonplaceholder.typicode.com/posts/1` | 200 | Content-Type is `application/json` (fetcher rejects) |
| 404 Page | GET | `https://example.com/nonexistent-12345` | 404 | Fetcher returns error |
| Redirect | GET | `http://google.com` | 200 (after redirect) | Follows redirect to HTTPS |
| Slow Server | GET | (use any slow endpoint) | Timeout | Set Postman timeout to 1s |

### 10.3 Using Postman to Verify Crawl Results

After running a crawl, you can use Postman to manually verify each URL from the "Visited URLs" list:

1. Run: `./gocrawl -url https://go.dev -depth 1 -pages 5`
2. Copy each URL from the "Visited URLs" output
3. In Postman, GET each URL and verify:
   - It returns status 200
   - Content-Type is `text/html`
   - The page contains `<a href="...">` links (inspect body)

### 10.4 curl Alternative (No Postman Needed)

You can do the same tests with `curl`:

```bash
# Simulate what fetcher.FetchPage does:
curl -v -H "User-Agent: GoCrawl/1.0" https://example.com

# Check headers only:
curl -I -H "User-Agent: GoCrawl/1.0" https://example.com

# Test with timeout:
curl --max-time 1 -H "User-Agent: GoCrawl/1.0" https://example.com

# Test a JSON endpoint (fetcher rejects):
curl -I https://jsonplaceholder.typicode.com/posts/1
# Look for: Content-Type: application/json
```

---

## 11. Testing Checklist

Print this and check off each item:

### Build & Setup
- [ ] `go version` shows Go 1.21+
- [ ] `go mod tidy` completes without errors
- [ ] `go build ./...` compiles all packages
- [ ] `go build -o gocrawl .` creates the binary

### Unit Tests
- [ ] `go test ./urlutil/ -v` — ALL PASS
- [ ] `go test ./fetcher/ -v` — ALL PASS
- [ ] `go test ./parser/ -v` — ALL PASS
- [ ] `go test ./crawler/ -v` — ALL PASS
- [ ] `go test ./... -v` — ALL PASS (full suite)

### Race Detection
- [ ] `go test ./... -race` — NO DATA RACES

### Coverage
- [ ] `go test ./... -cover` — coverage numbers look reasonable (>70% ideally)

### Config Validation (Negative Tests)
- [ ] No URL → error
- [ ] Invalid URL scheme → error
- [ ] Negative depth → error
- [ ] Zero pages → error
- [ ] Zero workers → error
- [ ] Negative timeout → error
- [ ] Negative delay → error

### E2E Crawl Tests
- [ ] Depth 0 → only seed page crawled
- [ ] Depth 1 → seed + immediate links
- [ ] Page limit works → stops at N pages
- [ ] Single worker → still completes
- [ ] No delay → fast crawl
- [ ] External links NOT followed

### Flow Connectivity
- [ ] Banner prints → `main.go` works
- [ ] Config error shows → `config.ValidateConfig()` connected
- [ ] Normalized URL in logs → `urlutil.NormalizeURL()` connected
- [ ] `Fetched (status: 200)` in logs → `fetcher.FetchPage()` connected
- [ ] `Found N links` in logs → `parser.ExtractLinks()` connected
- [ ] No duplicates in visited list → `VisitedStore` connected
- [ ] Delay visible between requests → `RateLimiter` connected
- [ ] Report numbers correct → `CrawlStats` connected

### Edge Cases
- [ ] Unreachable domain → error logged, no crash
- [ ] Non-HTML page → rejected, no crash
- [ ] Very short timeout → timeout errors, no crash

### Postman / curl
- [ ] HTML page returns 200 + text/html
- [ ] JSON page returns 200 + application/json (fetcher would reject)
- [ ] 404 page returns 404 (fetcher would error)
- [ ] User-Agent header is sent correctly

---

## 12. Troubleshooting Common Issues

| Problem | Possible Cause | Fix |
|---|---|---|
| `go: module not found` | Missing `go.sum` or dependencies | Run `go mod tidy` |
| `cannot find package "golang.org/x/net/html"` | Dependency not downloaded | Run `go mod download` |
| Test hangs forever | Deadlock in channel/goroutine | Check if `wg.Add`/`wg.Done` are balanced; check channel buffer size |
| `DATA RACE` in test | Missing mutex lock | Check all shared state access has proper locking |
| `connection refused` in tests | httptest server not started | Ensure `httptest.NewServer()` is called before `FetchPage()` |
| Fetcher returns "not an HTML page" | Server doesn't set Content-Type | Ensure test server sets `Content-Type: text/html` |
| Crawler visits 0 pages | Seed URL normalization fails | Check `urlutil.NormalizeURL()` output matches expected |
| Too many pages crawled | Race in page count check | Run with `-race` flag to detect |
| `Pages Crawled` > `MaxPages` | Workers check count non-atomically | Known timing window — minor off-by-one acceptable with multiple workers |

---

## Summary of What Each Test Level Proves

| Level | What It Proves |
|---|---|
| **`go build`** | All code compiles, all imports resolve, all types match |
| **Unit Tests (`go test ./pkg/`)** | Each function works correctly in isolation |
| **Integration Tests (crawler_test.go)** | Multiple components work together (workers + fetcher + parser + visited + rate limiter) |
| **Race Detection (`-race`)** | No concurrent data access bugs |
| **E2E Tests (binary run)** | The full application works end-to-end with real websites |
| **Negative Tests (bad input)** | Error handling is robust, program doesn't crash on bad input |
| **Coverage Report** | How much code is exercised by tests |
| **Postman/curl** | Manual verification of HTTP behavior matches fetcher expectations |

---

> 💡 **Pro Tip**: Run this one-liner for a comprehensive automated check:
> ```bash
> go build ./... && go test ./... -race -cover -v
> ```
> If this passes with no races and good coverage, your project is solid! ✅
