# GoCrawl: Project Deep-Dive (Interview Prep)

GoCrawl is a concurrent web crawler written in Go. You give it a starting URL. It visits the pages of that website, pulls out clean text, and hands each page to your code or saves it to a file. The search engine **go-search** uses it as a library to get its documents.

**How to use this doc:** read sections 1–4 to understand the system. Section 5 follows one real crawl from start to end. Sections 6–7 explain each part and the concurrency. Section 9 lists real bugs, which is what a good interviewer will find. Section 11 is the question bank, and section 12 is a one-page cheat sheet for the night before.

**Everything here was checked against the code at commit `5a85a93`.** Numbers marked "measured" came from running the real code against local test servers, so read timings as approximate. Where the code and the ideal design differ, this doc says so.

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
9. [Known bugs and limitations](#9-known-bugs-and-limitations)
10. [How it would scale](#10-how-it-would-scale)
11. [Interview Q&A](#11-interview-qa)
12. [Cheat sheet](#12-cheat-sheet)

---

## 1. The 30-second pitch

> "GoCrawl is a concurrent web crawler in Go. It uses a **worker pool**: a fixed number of goroutines pull URLs from a **buffered channel**, fetch the page, extract its links, and push new links back onto the same channel. Because the channel is first-in-first-out, the crawl goes roughly **breadth-first**. A **thread-safe visited set** stops a page being crawled twice, and a **per-domain rate limiter** avoids hammering the site. Each page goes through an **extractor** that strips the HTML and produces a clean document, which is streamed to a **callback** or appended to a **JSONL file**. It is also a library: my search engine imports it and indexes pages as they arrive."

Then add this, because knowing your own weak spots shows ownership:

> "When I tested it properly I found real concurrency bugs that the race detector can't catch. For example, with more workers the rate limiter lets requests through in bursts, and the page limit can overshoot. I can show you the cause and the fix."

Numbers worth remembering:

| Fact | Value |
|---|---|
| Defaults | depth 2, 50 pages, 3 workers, 10s timeout, 1s delay |
| Queue | Buffered channel, capacity = `MaxPages` |
| Max page body | 10 MB (extra bytes are cut off silently) |
| Document ID | SHA-256 of the normalized URL (64 hex characters) |
| Only external dependency | `golang.org/x/net/html` |
| Tests | 48 test functions, 75 including subtests; all pass with `-race` |
| Real data I crawled | 1,007 docs from books.toscrape.com, 102 from en.wikipedia.org |

---

## 2. The big picture

### 2.1 The analogy

Think of a team of **researchers mapping a library**.

| Library | GoCrawl |
|---|---|
| A tray of index cards, each naming a book to check | **Task channel** (`tasks`) |
| Researchers who take a card, read the book, and add new cards | **Worker goroutines** |
| A checklist of books already read | **Visited store** |
| "Only one visit to each shelf per second" rule | **Rate limiter** |
| Walking to the shelf and getting the book | **Fetcher** |
| Copying down every reference in the book | **Parser** |
| Writing every reference in one standard format | **URL normalizer** |
| Summarizing the book without the adverts and covers | **Extractor** |
| The notebook where summaries are kept | **DocStore** (JSONL file) |
| A counter of cards still out | **`sync.WaitGroup`** |

The tricky part: researchers both **take** cards and **add** cards. Knowing when the whole job is finished is harder than it sounds. An empty tray doesn't mean done, because someone may be about to add more. That's why the WaitGroup exists.

### 2.2 Architecture (draw this on a whiteboard)

```
  main.go (CLI flags)        go-search (imports it as a library)
          │                        │
          └───────────┬────────────┘
                      ▼
              crawl.Run(cfg)              defaults + validation, wires the pipeline
                      │
                      ▼
              crawler.Run()               creates the channel and N workers
                      │
      ┌───────────────┼───────────────┐
      ▼               ▼               ▼
  Worker 1        Worker 2        Worker N
      ▲               ▲               ▲
      └───────┬───────┴───────┬───────┘
              │               │
   take task  │               │  add new links
              │               │
        ┌─────┴───────────────┴─────┐
        │  tasks channel (FIFO)     │
        │  capacity = MaxPages      │
        └───────────────────────────┘

  What one worker does with one task:

  check limits → MarkVisited → rate limit wait → FetchPage → ExtractLinks
                                                                  │
                     ┌────────────────────────────────────────────┤
                     ▼                                            ▼
           OnPageFetched callback                        for each link:
                     │                                   same domain? not visited?
                     ▼                                   depth OK? → send to channel
           extractor.Extract → Document
                     │
           ┌─────────┴─────────┐
           ▼                   ▼
      OnDocument()      DocStore.Save()
      (your code)       (JSONL file)
```

**The one sentence to remember:** a task flows *channel → limits → visited check → rate limiter → fetch → parse links → extract text → store*, and the new links go back into the channel.

### 2.3 What makes the design good

| Choice | Why |
|---|---|
| Worker pool, fixed N | Bounds open connections and memory. One goroutine per URL would explode on a big site. |
| Channel as the queue | Go gives you a thread-safe FIFO for free. |
| Callback for output | The crawler doesn't know about extractors or search indexes. Anyone can plug in. |
| Small packages | Fetcher, parser and URL utils can each be tested alone. |

---

## 3. Project structure

```
gocrawl/
├── main.go                 CLI: parse flags, call crawl.Run, print summary
├── crawl/crawl.go          PUBLIC API: CrawlConfig, CrawlResult, Run()
├── config/config.go        Internal Config + ValidateConfig
├── crawler/                The engine
│   ├── crawler.go          Crawler struct, Run(), printReport()
│   ├── worker.go           worker loop and processTask (the heart of the project)
│   ├── visited.go          VisitedStore: thread-safe set of URLs
│   ├── ratelimiter.go      RateLimiter: per-domain delay
│   └── types.go            CrawlTask, CrawlStats
├── fetcher/fetcher.go      HTTP GET with timeout, status, content-type and size checks
├── parser/parser.go        Finds <a href> links in the HTML tree
├── urlutil/urlutil.go      Normalize, resolve, same-domain check, scheme check
├── extractor/extractor.go  HTML → Document (title + clean text + SHA-256 ID)
└── docstore/docstore.go    Appends Documents to a JSONL file
```

**Dependency direction:**

```
main → crawl → { config, crawler, docstore, extractor }
                     crawler → { config, fetcher, parser, urlutil }
                     parser → urlutil
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
| `-depth` | `2` | How many links deep to follow |
| `-pages` | `50` | Maximum pages to crawl |
| `-workers` | `3` | Number of worker goroutines |
| `-timeout` | `10s` | Time limit per page |
| `-delay` | `1s` | Minimum gap between requests to one domain |
| `-output` | `output.jsonl` | Output file. Appends, never overwrites |

**Warning:** `-depth 0` and `-delay 0s` don't work today. See [bug 1](#bug-1-zero-values-are-replaced-by-defaults).

### Using it as a library

This is what go-search does. It's simplified from `go-search/crawl_job.go`:

```go
docCh := make(chan extractor.Document, 64)
go func() {
    for doc := range docCh {
        idx.AddDocument(doc.ID, doc.URL, doc.Title, doc.Content)
    }
}()

result, err := crawl.Run(crawl.CrawlConfig{
    SeedURL:    "https://example.com",
    MaxPages:   100,
    OnDocument: func(doc extractor.Document) { docCh <- doc },
})
close(docCh) // safe: every callback has returned once Run returns
```

`OnDocument` runs inside the worker goroutine. So it must be thread-safe, and if it's slow, it holds up that worker. The channel above gives natural **backpressure**: when indexing falls behind, workers wait.

---

## 5. One real crawl, step by step

This is a real run of the code against a small local site.

```
/                   links to: /about (in <nav>), /about#team, /blog/,
                              https://other.com/x, mailto:a@b.c
/about              links to: /, /logo.png
/blog               links to: post-1 (relative), /blog/post-1, /blog/index.html
/blog/post-1        links to: /blog/post-1/deep
/blog/post-1/deep   no links
```

```bash
./gocrawl -url http://localhost:8099/ -depth 2 -pages 50 -workers 1 -delay 100ms -output out.jsonl
```

### The real log

```
[INFO]  [Worker 1] Crawling: http://localhost:8099/ (depth: 0)
[DEBUG] [Worker 1] Found 3 links on http://localhost:8099/
[INFO]  [Worker 1] Crawling: http://localhost:8099/about (depth: 1)
[DEBUG] [Worker 1] Found 2 links on http://localhost:8099/about
[INFO]  [Worker 1] Crawling: http://localhost:8099/blog (depth: 1)
[DEBUG] [Worker 1] Found 3 links on http://localhost:8099/blog
[INFO]  [Worker 1] Crawling: http://localhost:8099/logo.png (depth: 2)
[ERROR] [Worker 1] Error fetching http://localhost:8099/logo.png: not an HTML page (content-type: image/png)
[INFO]  [Worker 1] Crawling: http://localhost:8099/post-1 (depth: 2)
[ERROR] [Worker 1] Error fetching http://localhost:8099/post-1: non-200 status code: 404
[INFO]  [Worker 1] Crawling: http://localhost:8099/blog/post-1 (depth: 2)
[DEBUG] [Worker 1] Found 1 links on http://localhost:8099/blog/post-1

  Pages Crawled:  4
  Pages Skipped:  0
  Errors:         2
  Unique URLs:    6
  Time Elapsed:   505ms
```

Notice the order: depth 0, 1, 1, 2, 2, 2. That's breadth-first, and it comes from the FIFO channel.

### What happened at each page

| Task | Links found (after normalizing) | What was done with them | Queue afterwards |
|---|---|---|---|
| `/` (d0) | `/about`, `/blog`, `other.com/x` | `mailto:` removed by the parser. `/about#team` became `/about`, merged as a duplicate. `/blog/` became `/blog`. `other.com` dropped: different domain. | `/about`, `/blog` |
| `/about` (d1) | `/`, `/logo.png` | `/` already visited. | `/blog`, `/logo.png` |
| `/blog` (d1) | `/post-1`, `/blog/post-1`, `/blog` | `/blog/index.html` became `/blog`, already visited. | `/logo.png`, `/post-1`, `/blog/post-1` |
| `/logo.png` (d2) | | Wrong content type. Counted as an error. | `/post-1`, `/blog/post-1` |
| `/post-1` (d2) | | **404. This is a bug**, see below. | `/blog/post-1` |
| `/blog/post-1` (d2) | `/blog/post-1/deep` | Would be depth 3, over the limit, so never queued. | empty, so the crawl ends |

**The bug this run exposed:** the page at `/blog/` has a relative link `post-1`, which should mean `/blog/post-1`. But the crawler removed the trailing slash, so it used `/blog` as the base, and `post-1` resolved to `/post-1`, which doesn't exist. See [bug 6](#bug-6-removing-the-trailing-slash-breaks-relative-links).

**Time check:** 505ms is about 5 waits of 100ms each, because the first request doesn't wait. With one worker the rate limiter behaves exactly as designed.

**Why 6 unique URLs but 4 crawled?** A URL is marked visited *before* it's fetched, so failed URLs are in the set too.

### The real output (`out.jsonl`)

One JSON object per line:

```json
{"id":"67fe89df224a4f7347a89ab939082a6249bba763fb7d1d3665128cfa92d9afe6","url":"http://localhost:8099/","domain":"localhost","title":"Home","content":"Welcome Hello crawler.","status_code":200,"content_length":22,"timestamp":1793609344}
```

The home page HTML had a `<nav>`, links and a `<script>`. None of that is in `content`. Only `Welcome` (from `<h1>`) and `Hello crawler.` (from `<p>`) are kept.

---

## 6. Component deep-dives

### 6.1 `crawl.Run`: the public API ([crawl/crawl.go](../crawl/crawl.go))

It does four things:

1. Fill in defaults, then validate the config.
2. Open the DocStore if `OutputPath` is set.
3. Set `OnPageFetched`, the callback that connects crawler → extractor → your code and the file ([line 114](../crawl/crawl.go#L114)).
4. Run the crawl (this blocks), then build a `CrawlResult`.

**Why two config structs?** `CrawlConfig` is the public contract and includes callbacks. `config.Config` is what the engine needs. Keeping them separate lets the internals change without breaking users.

**Each `Run` builds its own crawler, visited set and rate limiter**, so two crawls can run at the same time without sharing state. One consequence: two crawls of the same site don't know about each other's rate limits.

### 6.2 `Crawler.Run`: starting and stopping ([crawler/crawler.go](../crawler/crawler.go))

```go
tasks := make(chan CrawlTask, c.config.MaxPages)
var wg sync.WaitGroup
done := make(chan bool)

for i := 1; i <= c.config.Workers; i++ {
    go c.worker(i, tasks, &wg)          // workers wait on the channel
}

wg.Add(1)                               // count the seed BEFORE sending it
tasks <- CrawlTask{URL: seedNormalized, Depth: 0}

go func() {
    wg.Wait()                           // counter = 0 means nothing queued or running
    close(tasks)                        // workers' "for range" loops end
    done <- true
}()
<-done
c.printReport()
```

The WaitGroup here counts **tasks, not workers**. That's the key idea, and section 7 explains why.

**Small cleanup:** the extra goroutine and `done` channel aren't needed. `wg.Wait(); close(tasks)` directly in `Run` does the same thing.

### 6.3 `processTask`: the heart of the project ([crawler/worker.go](../crawler/worker.go#L30))

```
defer wg.Done()                        ← runs on every return path
1. Page limit reached?                 → skip
2. Task deeper than MaxDepth?          → skip
3. MarkVisited returns false?          → skip (someone else has it)
4. Page limit check again              → skip
5. rateLimiter.Wait(url)               ← may sleep
6. FetchPage                           → on error: count it, return
7. stats.IncrementCrawled()
8. ExtractLinks(body, url)
9. OnPageFetched(url, body, status)    ← extraction and storage happen here
10. For each link:
      different domain?   → ignore
      already visited?    → ignore
      over page limit?    → stop the loop
      next depth too big? → ignore
      wg.Add(1), then try to send to the channel
      channel full?       → wg.Done(), count as skipped, drop the link
```

The final send is non-blocking:

```go
wg.Add(1)
select {
case tasks <- CrawlTask{URL: link, Depth: task.Depth + 1}:
default:           // channel full: drop the link instead of waiting
    wg.Done()
    c.stats.IncrementSkipped()
}
```

This `select` is the most important line for **deadlock safety**. Section 7.3 explains it.

### 6.4 `VisitedStore`: the "seen" set ([crawler/visited.go](../crawler/visited.go))

A `map[string]bool` protected by a `sync.RWMutex`.

```go
func (v *VisitedStore) MarkVisited(url string) bool {
    v.mu.Lock()
    defer v.mu.Unlock()
    if v.visited[url] { return false }
    v.visited[url] = true
    return true
}
```

**Why one method and not `IsVisited` followed by `Mark`?** Because the check and the write have to be **atomic**. With two separate calls, two workers could both see "not visited" and both fetch the page. Doing both under one lock means exactly one worker gets `true`.

The worker also calls `IsVisited` before queuing a link. That's only an optimization to avoid queuing obvious duplicates. `MarkVisited` is the real gatekeeper.

**Cost:** O(1) per check. Memory grows with every URL, and everything is lost when the process exits. There's no way to resume a crawl.

### 6.5 `RateLimiter`: being polite ([crawler/ratelimiter.go](../crawler/ratelimiter.go))

A `map[domain]lastRequestTime` plus a mutex. If the domain was hit recently, it sleeps for the remaining time. It correctly **releases the lock while sleeping**, so other domains don't wait.

The domain key is `host:port`. Since the crawler stays on one domain, this map really only ever has one entry.

This has a real bug under concurrency. See [bug 2](#bug-2-the-rate-limiter-bursts-with-more-than-one-worker).

### 6.6 `FetchPage` ([fetcher/fetcher.go](../fetcher/fetcher.go))

| Step | Why |
|---|---|
| `http.Client{Timeout: timeout}` | Covers the whole request including reading the body. A slow server can't hang a worker forever. |
| Set `User-Agent: GoCrawl/1.0` | Lets site owners identify the crawler. |
| Reject status other than 200 | No error pages in the data. |
| Reject non `text/html` | Checked **before** reading the body, so a linked PDF or image costs almost no bandwidth. |
| `io.LimitReader(body, 10MB)` | One giant page can't eat all the memory. |
| `defer resp.Body.Close()` | Otherwise connections leak. |

**A subtle detail people get wrong:** a new `http.Client` is created per request, which looks wasteful. It's fine here, because a client with no `Transport` set uses the shared `http.DefaultTransport`, so connections are still reused.

### 6.7 `ExtractLinks` ([parser/parser.go](../parser/parser.go))

`html.Parse` turns the page into a tree. A recursive function walks every node (depth-first). For every `<a href>` it:

```
trim → skip empty, javascript:, mailto:, tel:, "#"
     → ResolveURL (relative → absolute)
     → NormalizeURL
     → IsValidURL (http or https only)
     → add if not already seen on this page
```

**Why a real HTML parser and not a regex?** Real pages have broken HTML, comments, odd quoting, and `<a>` text inside scripts. `html.Parse` follows the HTML5 parsing rules and tolerates all of this.

**Fun fact to mention:** there are two traversals in this project. The **page tree** is walked depth-first by recursion, and the **web graph** is walked breadth-first by the queue.

### 6.8 `urlutil`: the URL rules ([urlutil/urlutil.go](../urlutil/urlutil.go))

**Why normalize?** To a crawler, `HTTP://Example.com/a#top` and `http://example.com/a` look like different strings, but they're the same page. Without normalizing you crawl pages twice.

Real outputs from the code:

| Input | Output | Rule |
|---|---|---|
| `HTTP://Example.COM/Page#section` | `http://example.com/Page` | Lowercase scheme and host, drop the fragment. The path keeps its case, because paths are case-sensitive. |
| `http://example.com` | `http://example.com/` | Empty path becomes `/` |
| `http://example.com/blog/` | `http://example.com/blog` | Remove trailing slash |
| `http://example.com/index.html` | `http://example.com/` | Remove index files |
| `http://example.com/blog/index.html` | `http://example.com/blog` | Same, in a subfolder |

**What it does NOT handle** (all measured):

| Input | Output | Problem |
|---|---|---|
| `http://example.com:80/a` | unchanged | Default port not removed |
| `?b=2&a=1` vs `?a=1&b=2` | both kept | Same page, crawled twice |
| `/a?utm_source=x` | unchanged | Tracking parameters create fake new pages |
| `http://` vs `https://` | both kept | Treated as different pages |
| `www.example.com` vs `example.com` | both kept | Treated as **different domains** |

`ResolveURL` uses Go's `ResolveReference`, which follows the URL standard:

| Base | Link | Result |
|---|---|---|
| `http://example.com/blog/post` | `../about` | `http://example.com/about` |
| `http://example.com/blog/post` | `next` | `http://example.com/blog/next` |
| `http://example.com/blog/post/` | `next` | `http://example.com/blog/post/next` |
| `http://example.com/blog/post` | `/root` | `http://example.com/root` |
| `http://example.com/blog/post` | `//cdn.com/x` | `http://cdn.com/x` |

Rows 2 and 3 are why the trailing slash matters.

### 6.9 `Extract`: HTML to Document ([extractor/extractor.go](../extractor/extractor.go))

```go
type Document struct {
    ID            string // SHA-256 of the normalized URL
    URL           string
    Domain        string
    Title         string // <title>, else first <h1>
    Content       string // clean text only
    StatusCode    int
    ContentLength int    // length of clean text, not raw HTML
    Timestamp     int64  // Unix seconds
}
```

**How text is chosen:** it walks the tree using two lists.

| List | Tags | Action |
|---|---|---|
| `skipTags` | `script style noscript nav header footer aside form iframe svg math` | Ignore the whole subtree |
| `contentTags` | `p h1–h6 li td th blockquote pre figcaption caption dt dd` | Take all text inside, don't go deeper |
| anything else (`div`, `body`) | | Keep walking into children |

Skipped tags are also checked *inside* content tags. Wikipedia puts `<style>` inside `<p>`, so without this, CSS leaked into the text. The test `hello<style>.a{}</style> world` gives `hello world`.

**Why SHA-256 for the ID?** It's **deterministic**: the same URL always gives the same ID, in any run or any machine, with no central counter. It's also a fixed length. It's not for security; a faster hash like xxHash would work too.

**Limits of this approach** (measured):

| HTML | Content result | Problem |
|---|---|---|
| `<td>foo<br>bar</td>` | `foobar` | Words stuck together |
| `<li><a>Home</a><a>About</a></li>` | `HomeAbout` | Same problem |
| `<form><p>whole page</p></form>` | *(empty)* | Some sites wrap the whole page in a form |
| `<div>plain text</div>` | *(empty)* | Text directly in a `div` is lost |
| `<header><h1>Big Title</h1></header>` | *(no h1 in content)* | Title is still found, content misses it |

This is a heuristic, like a very simple version of Mozilla Readability. It also can't see pages rendered by JavaScript, because it doesn't run a browser.

### 6.10 `DocStore`: the JSONL file ([docstore/docstore.go](../docstore/docstore.go))

**Why JSONL?** One JSON object per line means you can stream it, append to it, and read it line by line without loading it all. If a crash cuts off the last line, the other lines are still fine. A normal JSON array would need closing brackets and a full parse.

| Feature | How |
|---|---|
| Thread safety | One mutex around the whole `Save` |
| Never overwrites | `O_APPEND` |
| Dedupe | `seen map[id]bool`, **only within this run** |
| Crash safety | `Flush` after every document |

**Exact point on durability:** `bufio.Flush` hands the data to the operating system. So it survives the *program* crashing, but not a *power* failure. For that you'd need `file.Sync()` (fsync), which is much slower. Flushing every write also makes the buffer nearly pointless, but it's the safe and simple choice.

---

## 7. Concurrency, end to end

### 7.1 Who shares what

| Shared thing | Used by | Protected by |
|---|---|---|
| `tasks` channel | All workers | The channel itself |
| Tasks in flight | All workers | `sync.WaitGroup` |
| `VisitedStore` | All workers | `sync.RWMutex` |
| `RateLimiter` | All workers | `sync.Mutex` |
| `CrawlStats` | All workers | `sync.Mutex` (atomics would also work) |
| `DocStore` | All workers | `sync.Mutex` |
| `OnDocument` callback | All workers | **The caller's job** |

### 7.2 How does it know when to stop?

This is the most likely deep question. Workers both consume and produce work, so you can't just "close the channel when the producer finishes". Every worker is a producer.

The solution is to **count tasks**:

```
wg.Add(1)   before a task is put in the channel
wg.Done()   after a task is fully processed (by defer)
Counter 0   means nothing is queued and nothing is running
            → nobody can create new work
            → it's safe to close the channel
```

**Why must `Add` come before the send?** If it came after, this could happen: the worker gets the task and finishes it, the counter hits 0, the channel gets closed, and another worker's send panics with `send on closed channel`.

**Why is it safe to `Add` while `Wait` is running?** A worker calling `Add(1)` is itself processing a task, and that task's `Done` hasn't run yet. So the counter is at least 1, and it can't reach 0 at that moment.

**Why can't a send hit a closed channel?** Every sender is inside a task, and that task holds the counter above 0. The channel only closes at 0. So all senders have already finished.

**Bonus point:** `OnPageFetched` runs before `wg.Done()`. So once `Run` returns, every callback has finished. That's what makes `store.Close()` and go-search's `close(docCh)` safe.

### 7.3 Why the non-blocking send matters: deadlock

Suppose the send was just `tasks <- newTask`, and one worker finds more links than the channel can hold:

```
Worker fills the channel, then blocks trying to send one more.
The only one who could empty the channel is a worker.
All workers are stuck sending. Nobody is receiving. Forever.
```

**Measured:** I replaced the `select` with a plain send, on a scratch copy, with 1 worker, `MaxPages=5`, and 20 links on the page. The crawl hung and never finished.

The code comment says the buffer "prevents deadlocks". That's only half true. A buffer alone just delays the problem. The `select` with `default` is what prevents it. The cost is that links get dropped ([bug 4](#bug-4-a-full-queue-silently-drops-links)).

### 7.4 Data race vs logic race (a strong interview point)

`go test -race` passes on this code, yet bugs 2 and 3 below are concurrency bugs. **Measured: the race detector stayed silent while both bugs happened.**

- A **data race** is two goroutines touching the same memory, with at least one writing, and no synchronization. The race detector catches this.
- A **logic race** (check-then-act) is where every individual access is locked properly, but the sequence isn't atomic. The race detector can't see this.

```go
if c.stats.GetCrawledCount() >= c.config.MaxPages { return }  // locked ✓
// ...fetch page, takes 100ms...
c.stats.IncrementCrawled()                                    // locked ✓
// But the gap between them isn't protected.
```

`MarkVisited` is the right example of the fix: check and write under one lock.

### 7.5 Is it really BFS?

With 1 worker, yes, it's exact. With many workers it's **approximately** BFS: tasks *start* in FIFO order, but finish in any order. A slow page at depth 1 can finish after pages at depth 2 have begun.

| | BFS (this project) | DFS |
|---|---|---|
| Data structure | Queue | Stack / recursion |
| Finds | Pages near the homepage first, usually the important ones | Goes deep down one path |
| With a page limit | Gives a broad sample of the site | Can burn the budget in one corner |
| Spider traps (`/cal/2026/1/2/...`) | Depth limit stops it early | Falls right in |

Complexity: O(V + E) for pages and links, with O(1) visited lookups. The visited set takes O(V) memory.

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
| `crawler` | 12 | 78.9% |
| `extractor` | 16 | 95.7% |
| `docstore` | 6 | 80.0% |
| `fetcher` | 5 | 89.5% |
| `parser` | 5 | 84.4% |
| `urlutil` | 4 (many table subtests) | 85.4% |
| `crawl`, `config`, `main` | **0** | **0%** |

**Techniques to mention:** table-driven tests for URL rules, `httptest` fake servers so no real network is needed, and 100 goroutines hitting the visited store at once under `-race`.

### Why the tests missed the bugs

This is honest and impressive to say out loud:

| Bug | Why tests passed |
|---|---|
| `-depth 0` ignored | The depth test calls `Crawler` directly. The bug is in `crawl.Run`, which has zero tests. |
| Page limit overshoot | `TestCrawlerPageLimit` uses `Workers: 1` "for deterministic behavior", the only setting that hides it. |
| Rate limiter burst | `TestRateLimiterDelay` calls `Wait` one at a time, never concurrently. |

**The lesson:** a deterministic test of a concurrent system can hide exactly the bugs you care about. The tests I'd add: run the crawl with 10+ workers and assert the page limit and the request rate as seen by the server.

---

## 9. Known bugs and limitations

Every bug here was reproduced by running the code. Know this section best. It's what turns "I wrote it" into "I own it".

### Bug 1: Zero values are replaced by defaults

[crawl/crawl.go:153](../crawl/crawl.go#L153) and [line 165](../crawl/crawl.go#L165):

```go
if cfg.MaxDepth == 0 && cfg.SeedURL != "" { cfg.MaxDepth = 2 }
if cfg.CrawlDelay == 0 { cfg.CrawlDelay = 1 * time.Second }
```

In Go, an unset `int` is `0`, so the code can't tell "not set" from "the user said 0".

| Command | Expected | Measured |
|---|---|---|
| Depth 0 through `crawl.Run` | 1 page | **3 pages** (`/`, `/d1`, `/d2`) |
| Depth 0 through `Crawler` directly | 1 page | 1 page |
| Delay 0, 5 pages, through `crawl.Run` | Instant | **4 seconds** |

**Fix:** a `crawl.DefaultConfig()` constructor, so 0 keeps its real meaning. Or pointer fields (`*int`), where `nil` means unset.

### Bug 2: The rate limiter bursts with more than one worker

[crawler/ratelimiter.go:46-66](../crawler/ratelimiter.go#L46-L66). The timestamp is only updated *after* waking up:

```
t=0ms   Worker A: first visit, sets last=0, goes ahead
t=1ms   Worker B: elapsed 1ms → sleep 199ms (last is NOT updated)
t=2ms   Worker C: elapsed 2ms → sleep 198ms (last is NOT updated)
t=3ms   Worker D: same...
t=200ms B, C, D all wake and fire TOGETHER
```

**Measured**, 8 goroutines with a 200ms delay. Should be evenly spaced:

```
Actual:  0  200 200 200 200 200 200 200 ms
Fixed:   0  200 400 600 800 1000 1200 1400 ms
```

**Measured** real crawl with a 100ms delay (limit should be 10 requests/second):

| Workers | Before fix | After fix |
|---|---|---|
| 1 | 10.9 req/s | 10.9 req/s |
| 3 | 31.7 req/s | 10.9 req/s |
| 5 | 48.6 req/s | 10.9 req/s |
| 10 | **93.8 req/s** | 10.9 req/s |

**Fix: reserve a time slot under the lock, and then sleep.** Tested on a scratch copy; all existing tests pass with `-race`.

```go
r.mu.Lock()
now := time.Now()
slot := r.lastVisit[domain].Add(r.delay)
if slot.Before(now) {
    slot = now
}
r.lastVisit[domain] = slot   // the next caller queues behind this slot
r.mu.Unlock()
time.Sleep(slot.Sub(now))
```

**The insight to say out loud:** once the limiter works, more workers no longer speed up a single-site crawl. Throughput is about `min(workers / page latency, 1 / delay)`. With a 1s delay and pages that take 200ms, a second worker adds nothing. Workers would only really pay off if the crawler visited many domains. So the speed-up you see from adding workers today comes from this bug.

### Bug 3: The page limit overshoots

[crawler/worker.go:55](../crawler/worker.go#L55) checks the count, but [line 74](../crawler/worker.go#L74) increments it only after the fetch. All workers can pass the check during that gap. It's check-then-act (section 7.4).

**Measured** with `-pages 10`, on a site with 200 pages:

| Workers | Pages actually crawled |
|---|---|
| 1 | 10 |
| 5 | 14 |
| 10 | 19 |
| 20 | 26–29 |

The worst case is `MaxPages + Workers − 1`.

**Fix: reserve the slot atomically before fetching.** Tested on a scratch copy: exactly 10 pages for every worker count, all tests pass with `-race`.

```go
func (s *CrawlStats) TryReservePage(max int) bool {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.reserved >= max { return false }
    s.reserved++
    return true
}
// Worker calls this before fetching, and gives the slot back if the fetch fails.
```

### Bug 4: A full queue silently drops links

[crawler/worker.go:113-120](../crawler/worker.go#L113-L120). The queue holds `MaxPages` items. Links that don't fit are thrown away and never retried. Links are also only marked visited when taken from the queue, so the same URL can fill several slots.

**Measured:** a site with 100 links where the first 10 are dead, `-pages 10`, 1 worker:

```
Crawled: 1     Errors: 10     Skipped: 90     ← 90 good pages never visited
```

The queue filled with dead links, the good ones were dropped, and the crawl ended far under the limit.

**Fix:** a separate unbounded frontier, such as a slice guarded by a mutex, or one coordinator goroutine that owns the queue and feeds workers. Also mark URLs as seen when *queuing* them, so duplicates never take a slot. *(This is a design idea, not something I've tested.)*

### Bug 5: Redirects are not tracked

`http.Client` follows up to 10 redirects by itself, and the code never asks where it ended up.

**Measured:** seed on server A redirects to server B:

- A page on **another domain got crawled**, bypassing the same-domain rule.
- It was saved under **A's URL**, though the content is from B.
- B's relative link `/only-on-b` was resolved against **A**, giving a 404.

**Fix:** use `resp.Request.URL`, which is the final URL. Use it as the base for links, as the document URL, and for the domain check. *(Not tested.)*

### Bug 6: Removing the trailing slash breaks relative links

Seen in the section 5 trace. `/blog/` becomes `/blog`, and then `post-1` resolves to `/post-1` (404) and not `/blog/post-1`. Normalized URLs are fine as *dedupe keys*, but shouldn't also be used as the *base for resolving links*.

**Fix:** keep the original URL for resolving. Using the final URL from bug 5 also solves this for sites that redirect `/blog` to `/blog/`. *(Not tested.)*

### Smaller bugs

| Issue | Evidence | Fix |
|---|---|---|
| `CrawlResult.DocumentsSkipped` is always 0 | Never assigned ([crawl.go:137](../crawl/crawl.go#L137)) | Add `DocStore.Skipped()` |
| Dedupe only lasts for one run | Measured: same crawl twice → file has 2, then 4 lines | Load existing IDs on open |
| Pages over 10 MB are silently cut | Measured: 11 MB → 10,485,760 bytes, no error. The comment on `FetchPage` claims it returns an error | Read one extra byte to detect it, then return an error or log it |
| Words stuck together | Measured: `foo<br>bar` → `foobar` | Add a space at element boundaries |
| Valid URLs rejected, invalid accepted | Measured: `HTTP://x.com` rejected, `http://` accepted | Check the parsed scheme and host |
| Page parsed twice | `parser` and `extractor` each call `html.Parse` | Parse once, pass the tree |
| Same normalize logic written twice | `urlutil.NormalizeURL`, `extractor.normalizeForDedup` | Keep one |
| Library prints to stdout | `printReport` always prints, `log` always writes | Inject a logger |
| Non-HTML links counted as errors | `logo.png` counted in `Errors` | Separate "skipped" from "failed" |

### Missing features

| Missing | Why it matters | How I'd add it |
|---|---|---|
| **robots.txt** | The basic rule of polite crawling; the biggest gap | Fetch per domain, cache it, check before queuing, honor `Crawl-delay` |
| **Cancellation** | `Run` can't be stopped once started | Pass `context.Context` to `Run` and to `http.NewRequestWithContext` |
| **Retries** | One network blip loses a page forever | Retry timeouts and 5xx with exponential backoff and jitter; honor 429 `Retry-After` |
| **Resume** | A crash loses all progress | Save the frontier and visited set to disk or Redis |
| **Query cleanup** | `?utm_*` makes fake new pages | Remove known tracking parameters, sort the rest |
| **Content dedupe** | Different URLs can serve the same page | Hash the content; SimHash for near-duplicates |
| **JavaScript pages** | Single-page apps look empty | Headless browser, used only when needed because it's slow |
| **SSRF protection** | A crawler that accepts user URLs (go-search's `POST /crawl`) could reach `localhost` or cloud metadata at `169.254.169.254` | Block private IP ranges in a custom `DialContext`, which also catches redirects |

### Repo housekeeping

- `README.md` is out of date. It describes v1: no `-output`, no extractor, no docstore, no `crawl` package.
- Dead code: the `crawler.CrawlResult` type is never used.

---

## 10. How it would scale

"Design a crawler for 1 billion pages a month" is a classic question. That's about 400 pages per second, and a single process can't hold the state.

| Problem today | At scale |
|---|---|
| Queue is a Go channel in one process | **URL frontier** in Kafka, Redis, or a database |
| Visited set is a Go map | **Bloom filter** in front of a key-value store. Roughly 1.2 GB for 1B URLs at 1% false positives. The cost of a false positive is only a skipped page, which is usually acceptable. |
| Rate limiter is in one process | **Partition by host:** `hash(host) → worker`. All of one site's URLs go to one worker, so politeness stays local and simple. |
| JSONL file | **Object storage** (S3) for the raw pages, metadata in a database, Kafka to feed the indexer |
| Fixed page and depth limits | **Prioritization** by page importance and how often it changes |
| One crawl, then finished | **Recrawl scheduling** so popular pages get refreshed sooner |

```
            ┌──────────────┐
  seeds ──► │   Frontier   │◄───── new links (after dedupe)
            │ priority +   │                ▲
            │ per-host     │                │
            └──────┬───────┘         ┌──────┴───────┐
                   │ hash(host)      │ Visited +    │
       ┌───────────┼───────────┐     │ content dedupe│
       ▼           ▼           ▼     └──────▲───────┘
    Worker 1    Worker 2    Worker N        │
       └───────────┬───────────┘            │
                   ▼                        │
             Parse + extract ───────────────┘
                   │
                   ▼
         S3 + Kafka → search indexer
```

**Extra topics to name:** DNS caching (DNS lookups are a hidden bottleneck), spider traps (limit depth per host and watch for repeating URL patterns), crawl budget per domain, and the **Mercator** design, which has "front queues" for priority and "back queues" for per-host politeness.

**Start with the smallest next step:** before any of this, I'd fix the single-process bugs, add `context` and robots.txt, and make the crawler work across many domains, which is where more workers actually help.

---

## 11. Interview Q&A

Answer out loud first, and then read.

### Design

**Q: Why a worker pool and not one goroutine per URL?**
Goroutines are cheap, but what they do isn't. One per URL on a big site means thousands of open connections and pages in memory at once. N workers puts a hard cap on it.

**Q: Why a channel, and not a slice with a mutex?**
A channel is a thread-safe FIFO that also makes idle workers wait with no busy looping, and `close` gives a clean shutdown signal. Its weakness is a fixed capacity, which causes bug 4. A slice would fix that but needs more code.

**Q: Why a callback, and not returning all the documents at the end?**
Streaming. A big crawl would hold every document in memory until the end, and the caller couldn't see partial results. It also keeps the crawler independent from extraction and indexing.

**Q: Why normalize URLs?**
So one page has exactly one identity. Otherwise dedupe fails and the same page gets crawled many times.

**Q: Why stay on one domain?**
To keep the crawl focused and bounded. Without it, a few hops lead to the entire internet. It's also strict: `www.example.com` counts as a different domain to `example.com`.

**Q: Why BFS?**
Important pages are usually close to the homepage, so a page limit gets a useful sample. The depth limit also protects from infinite URL spaces.

### Concurrency

**Q: How do you know the crawl is finished?**
Count tasks with a WaitGroup: `Add` before queuing, `Done` after processing. At zero, nothing is queued or running, so no new work can appear. Then close the channel and the workers exit. (Section 7.2.)

**Q: What if two workers find the same URL at the same time?**
Both can queue it, since `IsVisited` is only a pre-check. But `MarkVisited` is atomic, so only one worker gets `true` and fetches it. The other skips.

**Q: Why `RWMutex` for visited, but `Mutex` for the limiter?**
The visited store has read-only methods (`IsVisited`, `Count`) that can run in parallel. The limiter's `Wait` always writes. To be honest, the gain from `RWMutex` is small here since every task calls the write path too.

**Q: Why not `sync.Map`?**
It would work: `LoadOrStore` gives the same atomic check. But `sync.Map` is built for keys that rarely change, and a crawler keeps adding new ones. A plain map with a mutex is simpler and clearer.

**Q: Could this deadlock?**
Not as written, because the send is non-blocking. With a plain blocking send, yes. I tested it, and it hangs. (Section 7.3.)

**Q: Does `-race` passing mean it's correct?**
No. It finds data races, not logic races. Bugs 2 and 3 pass `-race`. (Section 7.4.)

**Q: Does the rate limiter hold the lock while sleeping?**
No, it unlocks first, or every other domain would wait too. The bug is that it doesn't reserve a time slot before unlocking.

**Q: What happens if `OnDocument` panics?**
There's no `recover`, so the whole program crashes. If it's slow, it only holds that one worker, which is useful backpressure.

### Failure and behavior

**Q: What happens when a fetch fails?**
Logged, error counter goes up, and the task ends. No retry. The URL stays marked visited, so it won't be tried again in this run.

**Q: What if a site never responds?**
`Client.Timeout` ends the request, including slow body reads. One worker waits at most that long.

**Q: What if a page is huge?**
`LimitReader` stops at 10 MB. It's silently cut off, and the partial HTML is still parsed.

**Q: What if the process crashes mid-crawl?**
Documents already saved stay in the file, because of the flush after every write. The visited set and queue are lost, so a restart begins from scratch and appends duplicates.

**Q: What is a spider trap, and are you safe?**
A site that generates endless URLs, like a calendar with a "next month" link forever. The depth and page limits bound the damage, so it can't run forever, but it can waste the entire page budget.

**Q: Is your crawler polite?**
Partly. It has a delay per domain and a clear User-Agent. But it ignores robots.txt, and the delay has bug 2. I'd fix both before crawling any site I don't own.

### Go-specific

**Q: Why `defer wg.Done()`?**
`processTask` has many return paths. Missing one `Done` means `Wait` never returns and the crawl hangs. `defer` guarantees it. It also runs if the function panics.

**Q: Why does `Add` go before the send?**
Otherwise the counter could hit zero while there's still work, closing the channel under another worker. (Section 7.2.)

**Q: What is the zero-value problem?**
Go gives unset fields their zero value, so `MaxDepth: 0` looks the same as not setting it. That's bug 1. Fix it with a constructor, pointers, or the functional options pattern.

**Q: What's the size of the channel buffer in memory?**
Each `CrawlTask` is 24 bytes (16 for the string header, 8 for the int), allocated up front. So 1M capacity is about 24 MB, plus the URL strings it points to.

**Q: Do you need to close the response body?**
Yes, always. Otherwise the connection can't be reused and it leaks. Here it's `defer resp.Body.Close()`.

### Practice: "how would you change it?"

Good to walk through in code, as these are common live follow-ups:

1. **Add `context.Context`.** Where do you check it? In the worker loop, and pass it to the HTTP request.
2. **Add robots.txt.** Where does the cache go, and how do you stop two workers fetching it at once? (`sync.Once` per domain, or `singleflight`.)
3. **Add retries.** Which errors should retry? How do you keep them from breaking the rate limit?
4. **Allow many domains.** What changes in `IsSameDomain`? Do workers get blocked sleeping for one slow domain? (Yes, so you'd need per-domain queues.)
5. **Make it resumable.** What must be saved, and when?

---

## 12. Cheat sheet

**In one line:** worker pool + channel queue + visited set + rate limiter → fetch, parse, extract → stream to callback or JSONL.

**Worker flow:**
```
limits → MarkVisited → rate wait → fetch → links → callback → queue new links → wg.Done
```

**Stopping:** WaitGroup counts tasks. `Add` before send, `Done` after processing, close at zero.

**Five decisions to defend:**

| Decision | Reason | Cost |
|---|---|---|
| Fixed worker pool | Bounded resources | Tune N by hand |
| Non-blocking send | No deadlock | Drops links when full |
| Atomic `MarkVisited` | Exactly one worker fetches | Memory grows with URLs |
| Callback output | Streaming, decoupled | Caller must be thread-safe |
| JSONL + flush each write | Survives a program crash | Slower, not fsync-safe |

**Six bugs to know cold:**

| # | Bug | One-line cause | One-line fix |
|---|---|---|---|
| 1 | `-depth 0`, `-delay 0` ignored | Can't tell 0 from unset | `DefaultConfig()` or pointers |
| 2 | Rate limit bursts: 94 req/s vs limit of 10 | Timestamp set after sleep | Reserve the slot under the lock |
| 3 | 10-page limit crawls 19 | Check-then-act | Atomic reservation |
| 4 | Full queue drops links | Fixed capacity | Unbounded frontier, mark on enqueue |
| 5 | Redirects leave the domain | Final URL ignored | Use `resp.Request.URL` |
| 6 | `/blog/` breaks relative links | Normalized URL used as base | Resolve with the original URL |

**Top missing feature:** robots.txt. Then `context` cancellation, then retries.

**Golden lines:**

- "A WaitGroup counts tasks here, not workers."
- "The race detector finds data races, not logic races."
- "A deterministic test can hide the concurrency bug you care about. My page limit test used one worker."
- "Once the limiter is right, more workers don't speed up a single site. Throughput is capped at one over the delay."
- "For scale, I'd partition by host hash, so politeness stays local to a worker."
