# GoCrawl: Project Deep-Dive (Interview Prep)

GoCrawl is a concurrent web crawler written in Go. You give it a starting URL. It visits the pages of that website, pulls out clean text, and hands each page to your code or saves it to a file. The search engine **go-search** uses it as a library to get its documents.

**How to use this doc:** read sections 1–4 to understand the system. Section 5 follows one real crawl from start to end. Sections 6–7 explain each part and the concurrency. Section 9 is the story of the bugs I found, measured and fixed, which is the strongest thing to talk about in an interview. Section 11 is the question bank, and section 12 is a one-page cheat sheet for the night before.

**Everything here was checked against the code.** Numbers marked "measured" came from running the real code against local test servers, so read timings as approximate.

---

## Contents

1. [The 30-second pitch](#1-the-30-second-pitch)
2. [The big picture](#2-the-big-picture)
3. [Project structure](#3-project-structure)
4. [How to run it](#4-how-to-run-it)
5. [One real crawl, step by step](#5-one-real-crawl-step-by-step)
6. [Component deep-dives](#6-component-deep-dives)
7. [Concurrency, end to end](#7-concurrency-end-to-end)
8. [Testing](#8-testing)
9. [Bugs I found and fixed](#9-bugs-i-found-and-fixed)
10. [Limits today, and how it would scale](#10-limits-today-and-how-it-would-scale)
11. [Interview Q&A](#11-interview-qa)
12. [Cheat sheet](#12-cheat-sheet)

---

## 1. The 30-second pitch

> "GoCrawl is a concurrent web crawler in Go. A fixed pool of **worker goroutines** takes URLs from a **frontier**: a queue protected by one mutex and a condition variable. Each worker fetches a page, extracts its links and pushes new ones back. The frontier dedupes every URL atomically, reserves a page slot before each fetch so the page limit is exact, and knows when the crawl is over. It's **polite**: it obeys **robots.txt** and a per-domain **rate limiter** that hands out time slots. It's **fault tolerant**: timeouts and 5xx errors are **retried with backoff**, Ctrl-C stops cleanly, and progress is saved to an **atomic checkpoint**, so a crawl can be **resumed** without losing or repeating work. Pages become clean documents that stream to a **callback** or a **JSONL file**. My search engine imports it as a library."

Then the story, because it shows how you work:

> "My first version passed all its tests, even with the race detector. When I tested it with many workers, I found the rate limiter let 94 requests per second through a limit of 10, and the page limit overshot by up to the number of workers. Those are logic races, which the race detector can't see. I measured six bugs like that, fixed them, and added tests that use many workers so they can't come back."

Numbers worth remembering:

| Fact | Value |
|---|---|
| Defaults | depth 2, 50 pages, 3 workers, 10s timeout, 1s delay, 2 retries |
| Queue | Frontier: no fixed size, each URL queued once |
| Max page body | 10 MB (bigger pages are rejected, not cut) |
| Retries | Timeouts, network errors, 429, 5xx; backoff 0.5s, 1s, 2s… plus jitter |
| Checkpoint | Every 10s and on stop; written to a temp file, then renamed |
| Document ID | SHA-256 of the normalized URL |
| Only external dependency | `golang.org/x/net/html` (robots.txt parser is my own) |
| Tests | 102 test functions, 129 including subtests; all pass with `-race` |
| Real data I crawled | 1,007 docs from books.toscrape.com, 102 from en.wikipedia.org |

---

## 2. The big picture

### 2.1 The analogy

Think of a team of **researchers mapping a library**.

| Library | GoCrawl |
|---|---|
| A supervisor's desk with a tray of index cards, each naming a book | **Frontier** |
| The supervisor's rule: "never write the same card twice" | Frontier's **seen** set |
| "Only 50 books today" — a book is booked out before anyone walks to the shelf | Frontier's **page budget** |
| Researchers who take a card, read the book, and hand in new cards | **Worker goroutines** |
| A sign-up sheet with one time slot per shelf every second | **Rate limiter** |
| The library's house rules on the door: "staff only in room B" | **robots.txt** |
| Walking to the shelf; trying again if it's locked for a moment | **Fetcher + retries** |
| Copying down every reference in the book | **Parser** |
| Writing every reference in one standard format | **URL normalizer** |
| Summarizing the book without the adverts and covers | **Extractor** |
| The notebook where summaries are kept | **DocStore** (JSONL file) |
| A photo of the desk every 10 seconds, so work can continue tomorrow | **Checkpoint** |

The tricky part: researchers both **take** cards and **add** cards. An empty tray doesn't mean done, because someone may be about to hand in more. The supervisor knows the job is done only when the tray is empty **and** nobody is out reading a book.

### 2.2 Architecture (draw this on a whiteboard)

```
  main.go (CLI flags, Ctrl-C)      go-search (imports it as a library)
          │                                │
          └───────────────┬────────────────┘
                          ▼
              crawl.RunContext(ctx, cfg)     defaults, validation, wires the pipeline
                          ▼
              Crawler.RunContext(ctx)        seed (or checkpoint), N workers, checkpoints
                          │
        ┌─────────────────┼─────────────────┐
        ▼                 ▼                 ▼
    Worker 1          Worker 2          Worker N
        │  Next()         │                 │      ▲ Push(new link)
        ▼                 ▼                 ▼      │
   ┌────────────────────────────────────────────────┴───┐
   │ FRONTIER  (one mutex + sync.Cond)                   │
   │   queue []CrawlTask   FIFO → breadth-first          │
   │   seen  map[key]bool  each URL queued once          │
   │   inFlight + done     page budget, end of crawl     │
   └──────────────────────────┬──────────────────────────┘
                              │ Snapshot() every 10s
                              ▼
                     crawl.state (checkpoint)

  What one worker does with one task:

  rate-limit slot → fetch (retry on 5xx/timeout) → parse HTML ONCE → links
                                                              │
                ┌─────────────────────────────────────────────┤
                ▼                                             ▼
        OnPage callback                              for each link:
                │                                    same site? not seen?
                ▼                                    depth OK? robots.txt OK?
     extractor.ExtractNode → Document                → frontier.Push
                │
       ┌────────┴────────┐
       ▼                 ▼
  OnDocument()     DocStore.Save()
  (your code)      (JSONL, deduped)
```

**The one sentence to remember:** a worker asks the frontier for a task, waits for its rate-limit slot, fetches (retrying temporary failures), parses once, hands the page on, and pushes new links back; the frontier dedupes, enforces the page limit and decides when the crawl ends.

### 2.3 What makes the design good

| Choice | Why |
|---|---|
| Worker pool, fixed N | Bounds open connections and memory |
| One structure owns the queue, seen set and budget | One lock means every check-then-act is atomic, and a checkpoint snapshot is consistent |
| `sync.Cond` instead of a channel | Push never blocks, so no deadlock and no dropped links |
| Callback for output | The crawler knows nothing about extractors or search indexes |
| Small packages | Fetcher, parser, robots, URL rules can each be tested alone |
| Context everywhere | One `cancel()` stops waits, sleeps, retries and HTTP requests |

---

## 3. Project structure

```
gocrawl/
├── main.go                 CLI: flags, Ctrl-C handling, summary
├── crawl/crawl.go          PUBLIC API: CrawlConfig, DefaultConfig, Run, RunContext
├── config/config.go        Internal Config (literal values) + ValidateConfig
├── crawler/                The engine
│   ├── crawler.go          Crawler, RunContext, Stop, site + robots checks
│   ├── worker.go           worker loop, processTask, consider, fetchWithRetry, backoff
│   ├── frontier.go         The queue: dedupe, page budget, end detection, snapshots
│   ├── ratelimiter.go      Per-domain time slots
│   ├── checkpoint.go       Save (atomic) / restore / periodic checkpoints
│   └── types.go            CrawlTask, Page, CrawlStats
├── fetcher/fetcher.go      HTTP GET: timeout, status, type, size, redirects, typed errors
├── robots/robots.go        robots.txt parser (RFC 9309) + per-site cache
├── parser/parser.go        Finds <a href> links (and <base href>)
├── urlutil/urlutil.go      Normalize, resolve, same-site check
├── extractor/extractor.go  HTML → Document (title + clean text + SHA-256 ID)
└── docstore/docstore.go    Appends Documents to a JSONL file, deduped across runs
```

**Dependency direction:**

```
main → crawl → { config, crawler, docstore, extractor, fetcher }
                     crawler → { config, fetcher, parser, robots, urlutil }
                     parser → urlutil        extractor → urlutil
                     docstore → extractor
```

No cycles. `crawl` is the only package an outside project needs.

---

## 4. How to run it

```bash
go build -o gocrawl .
./gocrawl -url https://books.toscrape.com -depth 1 -pages 20 -workers 3 -delay 500ms -output books.jsonl
```

| Flag | Default | Meaning |
|---|---|---|
| `-url` | required | Starting URL |
| `-depth` | `2` | How many links deep to follow (`0` = seed only) |
| `-pages` | `50` | Maximum HTML pages to fetch |
| `-workers` | `3` | Number of worker goroutines |
| `-timeout` | `10s` | Time limit per request |
| `-delay` | `1s` | Minimum gap between requests to one domain (`0` = none) |
| `-retries` | `2` | Extra attempts for timeouts, 429 and 5xx (`0` = none) |
| `-output` | `output.jsonl` | Output file. Appends; documents already in it are skipped |
| `-checkpoint` | none | File to save progress to, e.g. `crawl.state` |
| `-resume` | off | Continue from the checkpoint |
| `-ignore-robots` | off | Skip robots.txt (only for sites you own) |
| `-user-agent` | `GoCrawl/1.0` | User-Agent header |

### Stop and resume

```bash
./gocrawl -url https://books.toscrape.com -pages 200 -checkpoint books.state -output books.jsonl
# press Ctrl-C at any time → "CRAWL INTERRUPTED ... Resume with: -checkpoint books.state -resume"
./gocrawl -url https://books.toscrape.com -pages 200 -checkpoint books.state -output books.jsonl -resume
```

Measured: a 30-page site, interrupted after 13 pages, then resumed: 17 more pages, **30 unique documents, no duplicates**, and the checkpoint file was removed once the crawl was complete.

### Using it as a library

This is what go-search does (simplified):

```go
docCh := make(chan extractor.Document, 64)
go func() {
    for doc := range docCh {
        idx.AddDocument(doc.ID, doc.URL, doc.Title, doc.Content)
    }
}()

cfg := crawl.DefaultConfig()
cfg.SeedURL = "https://example.com"
cfg.MaxPages = 100
cfg.OnDocument = func(doc extractor.Document) { docCh <- doc }

result, err := crawl.RunContext(ctx, cfg) // cancel ctx to stop it
close(docCh) // safe: every callback has returned once Run returns
```

**Zero values, decided on purpose:**

| Field | `0` means | To turn it off |
|---|---|---|
| `MaxDepth` | seed page only (a real value) | — |
| `CrawlDelay` | polite default, 1s | `crawl.NoDelay` |
| `MaxRetries` | default, 2 | `crawl.NoRetries` |
| `MaxPages`, `Workers`, `Timeout` | defaults (0 makes no sense) | — |

Why the delay is different: a library caller who forgets to set it must get a polite crawler, not one that hammers a site. **Safety settings fail safe.**

`OnDocument` runs inside worker goroutines. It must be thread-safe, and if it's slow it holds up that worker. The channel gives natural **backpressure**.

---

## 5. One real crawl, step by step

A real run against a small local site:

```
/                   links to: /about (in <nav>), /about#team, /blog/,
                              https://other.com/x, mailto:a@b.c
/about              links to: /, /logo.png
/blog/              links to: post-1 (relative), /blog/post-1, /blog/index.html
/blog/post-1        links to: /blog/post-1/deep
/blog/post-1/deep   no links
```

```bash
./gocrawl -url http://localhost:8099/ -depth 2 -pages 50 -workers 1 -delay 100ms -output out.jsonl
```

### The real log

```
[INFO]  Config: depth=2, pages=50, workers=1, delay=100ms, retries=2, robots=true
[INFO]  [Worker 1] Crawling: http://localhost:8099/ (depth: 0)
[DEBUG] [Worker 1] Found 3 links on http://localhost:8099/
[INFO]  [Worker 1] Crawling: http://localhost:8099/about (depth: 1)
[DEBUG] [Worker 1] Found 2 links on http://localhost:8099/about
[INFO]  [Worker 1] Crawling: http://localhost:8099/blog/ (depth: 1)
[DEBUG] [Worker 1] Found 2 links on http://localhost:8099/blog/
[INFO]  [Worker 1] Crawling: http://localhost:8099/logo.png (depth: 2)
[INFO]  [Worker 1] Skipped http://localhost:8099/logo.png: not an HTML page (content-type: image/png)
[INFO]  [Worker 1] Crawling: http://localhost:8099/blog/post-1 (depth: 2)
[DEBUG] [Worker 1] Found 1 links on http://localhost:8099/blog/post-1

  Pages Visited:      4
  Pages Failed:       0
  Pages Skipped:      1
  Unique URLs:        5
  Duration:           408ms
```

Order: depth 0, 1, 1, 2, 2. That's breadth-first, from the FIFO queue.

### What happened at each page

| Task | Links found | What was done with them | Queue afterwards |
|---|---|---|---|
| `/` (d0) | `/about`, `/blog/`, `other.com/x` | Before this: robots.txt fetched once (404 → everything allowed). `mailto:` dropped by the parser. `/about#team` is the same key as `/about`, counted once. `other.com` dropped: not our site. | `/about`, `/blog/` |
| `/about` (d1) | `/`, `/logo.png` | `/` already seen. | `/blog/`, `/logo.png` |
| `/blog/` (d1) | `/blog/post-1`, `/blog/` | `post-1` resolved against `/blog/` → `/blog/post-1`. `/blog/index.html` has the same key as `/blog/`, counted once. | `/logo.png`, `/blog/post-1` |
| `/logo.png` (d2) | | Not HTML: counted as **skipped**, not error. Its page slot is given back. | `/blog/post-1` |
| `/blog/post-1` (d2) | `/blog/post-1/deep` | Would be depth 3: never queued. | empty, nothing in flight → crawl ends |

**Key detail:** the queue holds `/blog/` **with** its trailing slash, as the author wrote it. The dedupe key is `/blog` (normalized). Fetch URL and dedupe key are kept separate on purpose; mixing them was bug 6.

**Time check:** 408ms ≈ 4 gaps of 100ms (the first request doesn't wait).

**Run it again into the same file:** `Documents Written: 0, Already in File: 4`. The DocStore loads existing IDs when it opens.

### The real output (`out.jsonl`)

```json
{"id":"67fe89df224a...","url":"http://localhost:8099/","domain":"localhost","title":"Home","content":"Welcome Hello crawler.","status_code":200,"content_length":22,"timestamp":1793609344}
```

The home page had a `<nav>`, links and a `<script>`. None of that is in `content`.

---

## 6. Component deep-dives

### 6.1 `crawl.RunContext`: the public API ([crawl/crawl.go](../crawl/crawl.go))

1. Apply defaults (`toInternal`) and validate.
2. Open the DocStore if `OutputPath` is set.
3. Set `OnPage`: page → `extractor.ExtractNode` → `OnDocument` and/or `DocStore.Save`.
4. If `Save` fails (disk full), call `c.Stop(err)`: the crawl stops and returns the error, instead of losing every page after it.
5. Run, then build `CrawlResult` (visited, failed, skipped, robots-blocked, retries, interrupted…).

**Why two config structs?** `CrawlConfig` is the public contract, with defaults and "off" values (`NoDelay`). `config.Config` is literal: 0 means 0. The engine never has to guess.

### 6.2 `Crawler.RunContext`: starting and stopping ([crawler/crawler.go](../crawler/crawler.go#L88))

```go
ctx, cancel := context.WithCancel(ctx)        // Stop(err) calls cancel
if resuming { load checkpoint } else { check robots.txt, push seed }

stopClose := context.AfterFunc(ctx, c.frontier.Close) // cancel → frontier closes
var wg sync.WaitGroup                         // counts WORKERS, not tasks
for i := 1; i <= workers; i++ {
    wg.Add(1)
    go func(id int) { defer wg.Done(); c.worker(ctx, id) }(i)
}
stopCheckpoints := c.startCheckpoints()       // save every 10s
wg.Wait()                                     // all workers exited
stopCheckpoints()
c.finishCheckpoint()                          // complete → delete file; else save it
```

The worker loop is three lines:

```go
for {
    task, ok := c.frontier.Next()   // blocks; false = crawl over
    if !ok { return }
    c.frontier.Done(task, c.processTask(ctx, id, task))
}
```

### 6.3 The frontier: the heart of the design ([crawler/frontier.go](../crawler/frontier.go))

```go
type frontier struct {
    mu       sync.Mutex
    cond     *sync.Cond           // workers sleep here when there's nothing to do
    queue    []CrawlTask          // FIFO
    seen     map[string]bool      // every key ever queued
    inFlight map[string]CrawlTask // being processed right now
    done     int                  // successful pages
    maxPages int
    closed   bool
}
```

| Method | What it guarantees |
|---|---|
| `Push(task)` | Check-and-set on `seen` under the lock: **each URL is queued once**, however many pages link to it. Never blocks. |
| `Next()` | Hands out a task only while `done + inFlight < maxPages`: the **slot is reserved before fetching**, so the limit is exact. Blocks on the `Cond` otherwise. Returns false when the crawl is over. |
| `Done(task, outcome)` | `success` → `done++`. `failed` → the slot is freed for another page. `requeue` (stopped mid-task) → back in the queue for a resume. Wakes all waiters. |
| `Close()` | Stop handing out work (called on cancel). |
| `Snapshot()` | Copies queue + in-flight + seen + done **under the one lock**, so a checkpoint is consistent. |

**When is the crawl over?** `Next` returns false when:
- nothing is queued **and** nothing is in flight (an in-flight page might still add links), or
- `done` has reached `maxPages`, or
- `Close` was called.

**Why `sync.Cond`?** Workers need to sleep until "a task arrived" **or** "a slot was freed" **or** "the crawl ended". A channel can carry only the first. A condition variable lets each worker re-check the whole state after any change. The rule: always call `Wait` inside a `for` loop that re-checks the condition, because a wake-up only means "something changed".

### 6.4 `processTask` ([crawler/worker.go](../crawler/worker.go#L42))

```
defer recover()                        ← a panic (e.g. in OnPage) fails one page, not the crawl
1. fetchWithRetry                      → stopped? requeue.  not HTML / redirect refused? skipped.
                                         other error? failed.
2. Redirect landed on a seen page?     → skipped ("/blog" → "/blog/" processed once)
3. Seed redirected to another host?    → that host joins the site (golang.org → go.dev)
4. html.Parse ONCE                     ← shared by link extraction and the extractor
5. parser.Links(root, finalURL)        ← resolve against the FINAL URL
6. OnPage(page)
7. depth < MaxDepth → consider(link) for each link
8. stopped during this page?           → requeue (resume does it again); else success
```

`consider(link)`: normalize → same site? → already seen? → robots.txt allows? (if not, mark seen and count once) → `Push`.

### 6.5 `RateLimiter`: time slots ([crawler/ratelimiter.go](../crawler/ratelimiter.go#L57))

```go
r.mu.Lock()
now := time.Now()
slot := r.next[domain]
if slot.Before(now) { slot = now }
r.next[domain] = slot.Add(delay)   // RESERVE: the next caller queues behind this one
r.mu.Unlock()
sleep until slot, or return early if ctx is cancelled
```

Like taking a numbered ticket at a counter: you get your turn under the lock, then wait without holding it. A robots.txt `Crawl-delay` raises the gap for that host (capped at 30s).

**Insight to say out loud:** for a single site, throughput is capped at `1 / delay`, however many workers you add. Measured: 100ms delay → 10.9 req/s with 1, 3, 5 or 10 workers. Workers help only when page latency is bigger than the delay.

### 6.6 `Fetch` ([fetcher/fetcher.go](../fetcher/fetcher.go#L71))

| Step | Why |
|---|---|
| `http.NewRequestWithContext(ctx, …)` | Cancel stops the request |
| `Client.Timeout` | Covers the whole request, body included |
| `Transport` nil → `http.DefaultTransport` | Connections are pooled. And go-search installs an SSRF guard there, which we must not bypass |
| `CheckRedirect` → `AllowRedirect` | Each hop must stay on our site and pass robots.txt |
| Status ≠ 200 → `*StatusError` (with `Retry-After`) | Callers can tell 404 from 503 |
| Type check **before** reading body | A linked PDF costs almost nothing |
| Read `limit + 1` bytes | Tells "exactly 10 MB" from "too big" → `ErrTooLarge`, no silent cut |
| `resp.Request.URL` → `FinalURL` | The URL after redirects; links resolve against it |

`IsRetryable(err)`: network errors, timeouts, 429, 5xx → yes. 404, not HTML, too large, refused redirect, cancelled → no.

**Retries** ([`fetchWithRetry`](../crawler/worker.go#L151)): each attempt takes a new rate-limit slot, so retries stay polite. Wait = server's `Retry-After` if given, else 0.5s · 2ⁿ plus up to 50% random **jitter** (so workers don't retry in lock-step), capped at 30s.

### 6.7 `robots`: being a good citizen ([robots/robots.go](../robots/robots.go))

Own small parser for **RFC 9309**, no dependency:

| Rule | Example |
|---|---|
| Use groups naming our token (`GoCrawl`, any case); else the `*` group | |
| **Longest match wins**; on a tie, Allow wins | `Disallow: /private` + `Allow: /private/public` → `/private/public` allowed |
| `*` = any characters, `$` = end of path | `Disallow: /*.pdf$` blocks `/a.pdf`, not `/a.pdf?x=1` |
| `/robots.txt` itself is always allowed | |
| `Crawl-delay:` (not standard, but common) | raises the rate-limit gap |

**When robots.txt can't be read:**

| Answer | Meaning |
|---|---|
| 2xx | parse it |
| 4xx (404, 403) | no rules → everything allowed |
| 5xx, 429, network error | assume **everything disallowed** (the RFC's safe choice) |

The `Cache` fetches once per `scheme://host`. If 20 workers ask at the same moment, one fetches and 19 wait on a `ready` channel. Tested: 20 goroutines → 1 request.

### 6.8 `parser.Links` ([parser/parser.go](../parser/parser.go#L23))

Walks the parsed tree **depth-first** (recursion). For every `<a href>`: skip empty, `#…`, `javascript:`, `mailto:`, `tel:` → resolve against the page's final URL (or its `<base href>`) → keep http(s) with a host → strip the fragment → dedupe by normalized key → return the URL **as written**.

Two traversals in this project: the **page tree** depth-first by recursion, the **web graph** breadth-first by the queue.

### 6.9 `urlutil`: the URL rules ([urlutil/urlutil.go](../urlutil/urlutil.go))

`NormalizeURL` builds the **dedupe key**:

| Input | Key | Rule |
|---|---|---|
| `HTTP://Example.COM/Page#top` | `http://example.com/Page` | lowercase scheme + host, drop fragment; path keeps case |
| `http://example.com:80/a` | `http://example.com/a` | drop default port |
| `/blog/`, `/blog/index.html` | `/blog` | trailing slash, index files |
| `/a?b=2&a=1&utm_source=x` | `/a?a=1&b=2` | sort params, drop tracking (`utm_*`, `gclid`, `fbclid`…) |

`IsSameDomain` compares `SiteHost`: lowercase, default port removed, leading `www.` removed. So `www.example.com`, `example.com`, `http` and `https` are one site; `blog.example.com` is another.

### 6.10 `extractor` ([extractor/extractor.go](../extractor/extractor.go))

| List | Tags | Action |
|---|---|---|
| `skipTags` | `script style noscript template nav header footer aside iframe svg math select option button` | skip whole subtree |
| `contentTags` | `p h1–h6 li td th blockquote pre figcaption caption dt dd` | take all text inside |
| `looseTextParents` | `div section article main body span…` | keep text placed **directly** inside |
| `blockTags` | `br p div li td tr h1…` | add a space at the edges |

So `foo<br>bar` → `foo bar`, but `un<b>believ</b>able` → `unbelievable` (inline tags don't split, same as a browser). `<form>` isn't skipped, because ASP.NET-style sites wrap the whole page in one.

**ID = SHA-256 of the normalized URL:** deterministic (same URL → same ID in every run, on every machine, no counter needed), fixed length. Not for security.

### 6.11 `DocStore` ([docstore/docstore.go](../docstore/docstore.go))

| Feature | How |
|---|---|
| One JSON object per line | Streamable, appendable, a cut-off last line doesn't ruin the rest |
| Thread-safe | One mutex around `Save` |
| Dedupe **across runs** | On open, read every existing `id` (`bufio.Reader`, no line-length limit) |
| Crash repair | If the file doesn't end in `\n`, write one first |
| Durability | `Flush` per document (survives a program crash); `fsync` on `Close` (survives power loss at the end) |

### 6.12 Checkpoints ([crawler/checkpoint.go](../crawler/checkpoint.go))

```json
{"version":1, "seed":"https://example.com/", "sites":["example.com"],
 "done":13, "seen":[...], "queue":[{"url":"…","key":"…","depth":1}, ...]}
```

- **Atomic write:** temp file → `fsync` → `rename`. A crash leaves the old file or the new one, never half of one.
- **Consistent:** one `Snapshot()` under the frontier lock. In-flight tasks go first in `queue`, so nothing being worked on is lost.
- **When:** every 10s, and when the crawl stops. Deleted when the crawl runs out of work. Kept if it stopped at the page limit, so `-resume -pages 500` can extend it.
- **Safety:** wrong version or a different seed → error, not a silent mix-up.

**Guarantee:** at-least-once. A page interrupted halfway is done again on resume; `OnDocument` may see it twice, and the DocStore dedupes by ID.

---

## 7. Concurrency, end to end

### 7.1 Who shares what

| Shared thing | Protected by |
|---|---|
| Queue, seen set, page budget, in-flight set | Frontier: one `sync.Mutex` + `sync.Cond` |
| Allowed sites | `sync.RWMutex` (read on every link, written once) |
| Rate-limit slots | `sync.Mutex` |
| robots.txt cache | `sync.Mutex` + a `ready` channel per site |
| Stats counters | `sync.Mutex` |
| DocStore | `sync.Mutex` |
| Cancellation | `context.Context` |
| `OnDocument` callback | The caller |

### 7.2 How does it know when to stop?

Workers both consume and produce work, so "the producer is done" never happens by itself. The frontier tracks **in-flight** tasks:

```
queue empty AND inFlight == 0   → nobody can create new work → over
done == maxPages                 → over
Close() (Ctrl-C, Stop)           → over; in-flight tasks requeue themselves
```

Every one of these is checked under the same lock as `Push` and `Done`, so there's no window where a link is being pushed while the frontier decides it's empty.

**First version, for comparison:** a buffered channel plus a `WaitGroup` counting tasks (`Add` before send, `Done` after processing, close at zero). Correct for stopping, but a channel has a fixed size, so it either blocks (deadlock: all workers sending, nobody receiving; I tested it and it hung) or drops links (bug 4). The frontier removes both.

### 7.3 Data race vs logic race (strong interview point)

`go test -race` passed on the **buggy** first version. Bugs 2 and 3 below were concurrency bugs anyway.

- **Data race:** two goroutines touch the same memory, one writing, no synchronization. The race detector catches it.
- **Logic race (check-then-act):** every access is locked, but the *sequence* isn't atomic:

```go
if stats.GetCrawledCount() >= max { return }  // locked ✓
fetch page (100ms)                            // ← every worker passes the check here
stats.IncrementCrawled()                      // locked ✓
```

**Fix pattern:** make check + act one step under one lock. That's `frontier.Next` (check budget + reserve slot), `frontier.Push` (check seen + mark), and `RateLimiter` (check slot + reserve next).

### 7.4 Cancellation path

```
Ctrl-C → signal.NotifyContext cancels ctx
       → context.AfterFunc closes the frontier → idle workers' Next() returns false
       → busy workers: rate-limit wait / retry sleep / HTTP request return ctx.Err()
       → processTask returns requeue → task goes back in the queue
       → wg.Wait() returns → final checkpoint saved → DocStore flushed + fsynced
```

### 7.5 Is it really BFS?

With 1 worker, exactly. With many, **approximately**: tasks *start* in FIFO order but finish in any order, so a URL could first be found via a slightly longer path. Fine for a crawler; exact BFS would need a barrier per level (all of depth d before d+1), which leaves workers idle waiting on the slowest page of each level.

---

## 8. Testing

```bash
go test ./...          # all pass
go test -race ./...    # all pass, no data races
go test -cover ./...
go vet ./...           # clean
```

| Package | Test functions | Coverage |
|---|---|---|
| `crawler` | 32 | 86.3% |
| `extractor` | 20 | 93.7% |
| `fetcher` | 10 | 83.3% |
| `docstore` | 9 | 79.0% |
| `robots` | 9 | 89.2% |
| `parser` | 8 | 89.1% |
| `crawl` | 7 | 92.2% (was 0%) |
| `urlutil` | 7 (many table subtests) | 85.9% |

**Techniques:** `httptest` servers that record every request with a timestamp (so tests assert what the *server* saw), table-driven tests for URL and robots rules, `t.TempDir()` for files, and contexts with short timeouts for cancel tests.

**The lesson from the bugs:** the old tests passed because they used **one worker** "for deterministic behavior", and tested the engine but not the public API. The new tests deliberately run **5, 10 and 20 workers** and check limits from the server's side:

| Test | Asserts |
|---|---|
| `TestCrawlerPageLimitManyWorkers` | 20 workers, limit 10 → server sees exactly 10 page requests |
| `TestCrawlerRateLimitManyWorkers` | 10 workers → every gap between server hits ≥ delay |
| `TestRateLimiterNoBurst` | 8 goroutines released one delay apart |
| `TestFrontierConcurrentPush` | 100 goroutines, same 10 URLs → queued exactly 10 |
| `TestCrawlerDeadLinksDontStarveCrawl` | 10 dead links can't stop the crawl reaching 10 pages |
| `TestCrawlerInterruptAndResume` | cancel mid-crawl, resume → every page crawled |
| `TestRunDepthZeroMeansSeedOnly` | through the public API, not the engine |

---

## 9. Bugs I found and fixed

How I found them: ran the code against local test servers with many workers, and counted what the **server** received, instead of trusting the crawler's own counters.

### The six main bugs

| # | Bug | Cause | Before (measured) | Fix | After (measured) |
|---|---|---|---|---|---|
| 1 | `-depth 0`, `-delay 0s` ignored | Go zero value: "unset" and "0" look the same | depth 0 crawled 3 pages | `MaxDepth` literal, `DefaultConfig()`, `NoDelay`/`NoRetries` | 1 page; no-delay crawl instant |
| 2 | Rate limiter bursts | Timestamp updated *after* sleeping | 10 workers: **94 req/s** vs limit of 10 | Reserve the slot under the lock, then sleep | **10.9 req/s** for 1, 3, 5, 10 workers |
| 3 | Page limit overshoots | Check-then-act across a 100ms fetch | limit 10 → 14 / 19 / 29 pages with 5 / 10 / 20 workers | Frontier reserves a slot before fetch | exactly 10 every time |
| 4 | Full queue drops links | Fixed-size channel + non-blocking send; duplicates took slots | 10 dead links → **1 page** crawled, 90 good ones dropped | Unbounded frontier; mark seen on enqueue; failures free their slot | 10 pages crawled |
| 5 | Redirects not tracked | Final URL ignored | redirect to another site was crawled and saved under the wrong URL | `AllowRedirect` hook; use `resp.Request.URL` | off-site redirect refused; seed redirect followed correctly |
| 6 | `/blog/` broke relative links | Normalized URL used as the base for links | `post-1` → `/post-1` (404) | Separate fetch URL (as written) from dedupe key; resolve against final URL | `/blog/post-1` fetched |

**The rate-limiter fix in one picture:**

```
Before:  t=0 A goes, last=0 │ B: wait 200ms │ C: wait 199ms │ D: wait 198ms → B, C, D fire together
After:   A slot 0 │ B slot 200ms │ C slot 400ms │ D slot 600ms → evenly spaced
Measured release times (8 goroutines, 200ms):
         before 0 200 200 200 200 200 200 200     after 0 200 400 600 800 1000 1200 1400
```

### Smaller bugs fixed

| Bug | Before | After |
|---|---|---|
| Pages over 10 MB | silently cut to 10,485,760 bytes | `ErrTooLarge` |
| Words glued | `foo<br>bar` → `foobar` | `foo bar` |
| Whole-page `<form>`, loose `<div>` text | lost | kept |
| Dedupe only within one run | same crawl twice → 4 lines | 2 lines, `Already in File: 2` |
| `DocumentsSkipped` | always 0 | real count |
| Validation | `HTTP://x.com` rejected, `http://` accepted | the reverse |
| Page parsed twice | parser + extractor each parsed | parsed once, tree shared |
| Same URL rules written twice | `urlutil` and `extractor` | one implementation |
| Library printed to stdout | always | `Logger` option; report printed by the CLI |
| Images and PDFs counted as errors | `Errors` | `Skipped` |
| Panic in callback | crashed the program | recovered, counted, crawl continues |
| Disk full | logged per page, every page lost | crawl stops with the error |
| `www.` vs bare host, `:80`, `?utm_*`, param order | different pages / sites | same |

### Features added

| Feature | Detail |
|---|---|
| **robots.txt** | RFC 9309 rules, cached per site, `Crawl-delay`, checked on links and redirects |
| **Retries** | timeouts, network errors, 429, 5xx; exponential backoff + jitter; `Retry-After` |
| **Cancellation** | `RunContext(ctx)`; Ctrl-C in the CLI |
| **Resume** | atomic checkpoints every 10s and on stop; `-checkpoint` / `-resume` |
| **`<base href>`** | honored when resolving links |

---

## 10. Limits today, and how it would scale

### 10.1 What it still doesn't do

| Limit | Why it matters | Next step |
|---|---|---|
| One site per crawl | Can't crawl the web | Per-host queues; more workers then really help |
| One process, state in RAM | Seen set ~150 bytes/URL (rough): 1M URLs ≈ 150 MB | Disk-backed store (SQLite / RocksDB) |
| Checkpoint rewrites the whole state | Fine for thousands–millions of URLs, slow beyond | Append-only log, or a database |
| No JavaScript | Single-page apps look empty | Headless browser, only when needed |
| No near-duplicate detection | Same content at many URLs | SimHash on content |
| robots.txt cached for the crawl | Very long crawls should refresh it | Expire after 24h |
| 5xx robots.txt blocks the site for the whole crawl | One bad moment stops everything | Retry robots.txt later |
| SSRF guard lives in go-search, not here | A program accepting user URLs must add one | Optional private-IP blocking dialer |

Measured on a real site: 5 pages of books.toscrape.com with 3 workers in 6.5s (1s delay, robots.txt checked).

### 10.2 Scaling to 10 billion pages

**Math**, assuming 30 days and ~100 KB per page:

| Need | Estimate |
|---|---|
| Throughput | 10B / 2.6M seconds ≈ **3,900 pages/s** |
| Machines | 40–80 at 50–100 pages/s each (to be measured) |
| Bandwidth | ≈ 3 Gbps |
| Raw storage | ≈ **1 PB** before compression |
| Bloom filter, 10B URLs at 1% false positives | ≈ 12 GB, but 1% = 100M pages wrongly skipped |

```
          ┌── Frontier (priority + per-host queues) ◄───┐
          │                                             │
    hash(host) → consistent hashing                     │
          ▼                                             │
   Fetcher fleet ── robots cache, DNS cache             │
          │                                             │
          ▼                                             │
   S3 (raw pages) ──► Kafka ──► Parser fleet ──► URL-seen store
                                     │          (Bloom + RocksDB)
                                     ▼
                         Content dedupe → Indexer
```

| Today | At 10B | Why |
|---|---|---|
| In-memory frontier | Durable frontier (Mercator: front queues for priority, back queues per host) | Survives crashes, no RAM limit |
| `map` seen set | Bloom filter in front of a key-value store | Fast "probably new", exact answer avoids lost pages |
| Rate limiter in one process | `hash(host)` → one owner | Politeness stays local |
| Fetch + parse in one worker | Separate fleets joined by Kafka | Network and CPU scale separately |
| JSONL | S3 in WARC format | Reparse without refetching |
| Requeue on cancel | Lease + timeout + redelivery | A dead machine's tasks come back |
| One crawl | Recrawl scheduler | Pages change |

**The failure rule is the same idea as my checkpoint:** at-least-once delivery + idempotent writes keyed by URL hash. Doing work twice is harmless; losing it isn't.

**Three hard problems to name:**
1. **Politeness caps big sites:** 100M pages at 1 request/s takes 3.2 years. You need per-site crawl budgets and priority, not just more machines.
2. **Bad content:** spider traps and near-duplicates waste the budget. Per-host limits, SimHash.
3. **DNS:** a hidden bottleneck at this scale. Local caching resolvers.

---

## 11. Interview Q&A

Answer out loud first, then read.

### Design

**Q: Why a worker pool and not one goroutine per URL?**
Goroutines are cheap, but what they do isn't: one per URL means thousands of open connections and pages in memory. N workers is a hard cap.

**Q: Why a mutex + `sync.Cond` frontier, and not a channel?**
A channel has a fixed size. Full, it either blocks the sender (deadlock, since workers are also the only receivers; I tested it and it hung) or forces you to drop links. The frontier never blocks on push, and one lock covers queue, seen set and budget, so all the check-then-act steps are atomic and snapshots are consistent.

**Q: Why keep the fetch URL and the dedupe key separate?**
The key is aggressive (no trailing slash, sorted query) so duplicates merge. But the page must be fetched as written, because its relative links resolve against its real URL. Using the key as the base was bug 6.

**Q: Why a callback, not a returned slice?**
Streaming: a big crawl would hold every document in memory, and the caller couldn't start indexing early.

**Q: Why BFS?**
Important pages are usually near the homepage, so a page limit gives a broad, useful sample, and the depth limit stops spider traps early.

### Concurrency

**Q: How do you know the crawl is finished?**
The frontier ends it when the queue is empty and nothing is in flight, the page limit is reached, or it's closed. Checked under the same lock as `Push` and `Done`.

**Q: Two workers find the same URL at once?**
`Push` checks and marks `seen` under one lock. Exactly one succeeds.

**Q: How is the page limit exact with 20 workers?**
`Next` reserves the slot before the fetch: it only hands out work while `done + inFlight < max`. A failure gives the slot back.

**Q: Why did the old rate limiter burst?**
It recorded "last request time" after sleeping, so several workers read the same old time and woke together. Now each takes the next slot under the lock before sleeping.

**Q: Does `-race` passing mean it's correct?**
No: it catches data races, not logic races. My first version passed `-race` with two concurrency bugs.

**Q: Why `Wait` in a `for` loop?**
A wake-up only means "something changed". Another worker may have taken the task first, so re-check the condition.

**Q: What if `OnDocument` panics?**
Recovered in `processTask`: logged with a stack trace, counted as an error, the crawl continues.

### Failure

**Q: What happens when a fetch fails?**
Timeouts, network errors, 429 and 5xx are retried (2 by default) with exponential backoff, jitter and `Retry-After`, each through the rate limiter. 404, non-HTML, too large: not retried. A failed page frees its budget slot.

**Q: What happens on Ctrl-C?**
The context is cancelled: idle workers exit, busy ones abort their wait or request and requeue the task, the checkpoint is saved, and the output file is flushed and fsynced. `-resume` continues.

**Q: What if the process is killed (`kill -9`) or the machine dies?**
By design you lose at most ~10s: the last periodic checkpoint is intact thanks to temp file + rename. Pages done after it are fetched again on resume; the DocStore skips their documents by ID. (Tested: Ctrl-C and resume. Not tested: an actual `kill -9` mid-write.)

**Q: Why temp file + rename?**
Rename is atomic on POSIX. Writing in place could leave half a file if the crash comes mid-write. `fsync` before rename makes sure the data is on disk before the name points to it.

**Q: What if the disk fills up?**
`Save` fails → `Stop(err)` → crawl stops, returns the error, and the page is requeued so a resume redoes it.

**Q: Do you obey robots.txt?**
Yes, RFC 9309: longest match, Allow wins ties, `*` and `$`, our group or `*`. 404 means allow all; 5xx or unreachable means disallow all. Plus `Crawl-delay`, and checked on redirects too.

### Go-specific

**Q: What's the zero-value problem and how did you solve it?**
`MaxDepth: 0` and "not set" look the same in Go. `MaxDepth` is now literal, `DefaultConfig()` gives defaults, and for safety settings I used "0 = safe default, sentinel = off" (`NoDelay`), so forgetting a field never makes the crawler impolite.

**Q: What does `context.AfterFunc` do here?**
Runs `frontier.Close` when the context is cancelled, waking workers blocked on the `Cond`. A `Cond` can't wait on a channel, so this bridges the two.

**Q: Why parse once?**
`html.Parse` is the most expensive CPU step. The tree is shared by the link parser and the extractor, so it's done once per page instead of twice.

### Practice: "how would you change it?"

1. **Many domains at once.** Per-host queues so a slow site doesn't block workers sleeping on its rate limit.
2. **Exact BFS depth.** Level-by-level barrier, or keep the minimum depth per URL. What does it cost?
3. **Disk-backed frontier.** What goes in SQLite, and how do you claim a task safely? (A transaction that flips `pending` → `in_progress`.)
4. **Refresh robots.txt.** Where would the 24h expiry go?
5. **Near-duplicate pages.** Where would SimHash go: before `OnDocument`, or in the indexer?

---

## 12. Cheat sheet

**In one line:** worker pool + frontier (one lock + Cond) + slot rate limiter + robots.txt → fetch with retries, parse once, extract → stream to callback or JSONL, with atomic checkpoints.

**Worker flow:**
```
Next() (reserves slot) → rate slot → fetch (+retry) → parse once → links → OnPage → Push new links → Done(outcome)
```

**Stopping:** queue empty AND nothing in flight, OR page limit, OR Close. Cancel → requeue in-flight → save checkpoint.

**Six decisions to defend:**

| Decision | Reason | Cost |
|---|---|---|
| Fixed worker pool | Bounded resources | Tune N by hand |
| One-lock frontier + Cond | Atomic dedupe + budget, no deadlock, consistent snapshot | One lock is a bottleneck at huge scale |
| Reserve before act (pages, rate slots) | No logic races | Slight under-use if a reserved fetch fails |
| Fetch URL ≠ dedupe key | Relative links stay right | Two strings per task |
| Safe zero values (`NoDelay`) | Forgetting a field never hurts a site | Slightly unusual API |
| Temp file + fsync + rename | Crash-safe checkpoint | Whole state rewritten every 10s |

**Six bugs, before → after:**

| # | Bug | Before | After |
|---|---|---|---|
| 1 | depth 0 ignored | 3 pages | 1 |
| 2 | rate burst | 94 req/s | 10.9 req/s |
| 3 | page overshoot | 29 of 10 | 10 of 10 |
| 4 | dropped links | 1 page | 10 pages |
| 5 | redirects | off-site crawled | refused |
| 6 | `/blog/` relative links | 404 | correct |

**Golden lines:**

- "The race detector finds data races, not logic races. Both my worst bugs passed `-race`."
- "Reserve before you act: page slots, rate-limit slots, seen keys."
- "I test concurrency with many workers and count what the server saw."
- "Once the limiter is right, more workers don't speed up one site: throughput is capped at one over the delay."
- "Checkpoints are at-least-once plus idempotent writes; the same rule a 10-billion-page crawler uses."
