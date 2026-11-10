package crawler

import (
	"sort"
	"sync"
)

// outcome is what happened to a task, reported back to the frontier.
type outcome int

const (
	success outcome = iota // page fetched: counts toward the page limit
	failed                 // gave up on it: does NOT use up the page limit
	requeue                // crawl was stopped mid-task: put it back for resume
)

// frontier is the crawl queue: every URL waiting to be crawled.
//
// It replaces the old "buffered channel + WaitGroup" design and fixes its
// problems:
//
//   - Push never blocks and the queue has no fixed size, so workers can't
//     deadlock and links are never dropped because the queue is full.
//   - Push is an atomic check-and-set on "seen": each URL is queued once,
//     no matter how many pages link to it or how many workers find it.
//   - Next hands out a task only while  done + inFlight < maxPages.
//     A page slot is reserved BEFORE fetching, so the page limit can't
//     overshoot. A failed task gives its slot back.
//   - It knows when the crawl is over: nothing queued AND nothing in flight
//     (an in-flight task may still add new links), or the page limit is
//     reached, or Close was called.
//
// One mutex guards all of it, so a snapshot for a checkpoint is consistent.
// Workers wait on a sync.Cond instead of spinning.
type frontier struct {
	mu       sync.Mutex
	cond     *sync.Cond
	queue    []CrawlTask          // FIFO: gives breadth-first order
	head     int                  // index of the next task in queue
	seen     map[string]bool      // every key ever queued or marked
	inFlight map[string]CrawlTask // tasks a worker is processing now
	done     int                  // successful pages, all runs included
	maxPages int
	closed   bool
}

func newFrontier(maxPages int) *frontier {
	f := &frontier{
		seen:     make(map[string]bool),
		inFlight: make(map[string]CrawlTask),
		maxPages: maxPages,
	}
	f.cond = sync.NewCond(&f.mu)
	return f
}

// Push queues a task if its key is new. It returns false if already seen.
// Tasks pushed after Close are still recorded, so a checkpoint keeps them.
func (f *frontier) Push(t CrawlTask) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen[t.Key] {
		return false
	}
	f.seen[t.Key] = true
	f.queue = append(f.queue, t)
	f.cond.Signal() // wake one waiting worker
	return true
}

// MarkSeen records a key without queuing it (e.g. blocked by robots.txt,
// or the target of a redirect). It returns true if the key was new.
func (f *frontier) MarkSeen(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen[key] {
		return false
	}
	f.seen[key] = true
	return true
}

// Seen reports whether a key was ever queued or marked.
func (f *frontier) Seen(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen[key]
}

// Next blocks until there is a task to do, and returns it.
// It returns false when the crawl is over.
func (f *frontier) Next() (CrawlTask, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for {
		if f.closed {
			return CrawlTask{}, false
		}
		pending := len(f.queue) - f.head
		if f.done >= f.maxPages || (pending == 0 && len(f.inFlight) == 0) {
			f.closed = true
			f.cond.Broadcast() // wake every waiting worker so they exit too
			return CrawlTask{}, false
		}
		// Hand out work only if a page slot is free. Otherwise wait: an
		// in-flight task may fail and give its slot back.
		if pending > 0 && f.done+len(f.inFlight) < f.maxPages {
			t := f.queue[f.head]
			f.queue[f.head] = CrawlTask{} // let the GC free the strings
			f.head++
			f.compact()
			f.inFlight[t.Key] = t
			return t, true
		}
		f.cond.Wait() // releases the lock while waiting
	}
}

// Done reports that a task returned by Next has finished.
func (f *frontier) Done(t CrawlTask, out outcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.inFlight, t.Key)
	switch out {
	case success:
		f.done++
	case requeue:
		f.queue = append(f.queue, t) // still seen; resume will pick it up
	}
	// Something changed (a slot freed, or maybe the crawl is over):
	// let every waiting worker re-check.
	f.cond.Broadcast()
}

// Close stops handing out tasks. Workers blocked in Next return false.
func (f *frontier) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.cond.Broadcast()
}

// Complete reports whether the crawl ran out of work (nothing queued,
// nothing in flight), as opposed to stopping at the page limit or Close.
func (f *frontier) Complete() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queue)-f.head == 0 && len(f.inFlight) == 0
}

// compact drops the already-handed-out front of the queue once it is
// more than half of the slice, so memory doesn't grow forever.
func (f *frontier) compact() {
	if f.head > 1024 && f.head*2 > len(f.queue) {
		f.queue = append([]CrawlTask(nil), f.queue[f.head:]...)
		f.head = 0
	}
}

// snapshot is the frontier's state, saved in a checkpoint.
type snapshot struct {
	Done  int         `json:"done"`
	Seen  []string    `json:"seen"`
	Queue []CrawlTask `json:"queue"` // in-flight tasks first, then the queue
}

// Snapshot copies the state under the lock, so it is consistent:
// every key in Queue is in Seen, and nothing in flight is lost.
func (f *frontier) Snapshot() snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()

	s := snapshot{Done: f.done}
	for t := range f.seen {
		s.Seen = append(s.Seen, t)
	}
	sort.Strings(s.Seen)

	inFlight := make([]CrawlTask, 0, len(f.inFlight))
	for _, t := range f.inFlight {
		inFlight = append(inFlight, t)
	}
	sort.Slice(inFlight, func(i, j int) bool { return inFlight[i].Key < inFlight[j].Key })
	s.Queue = append(inFlight, f.queue[f.head:]...)
	return s
}

// Restore loads a snapshot into an empty frontier.
func (f *frontier) Restore(s snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.done = s.Done
	for _, k := range s.Seen {
		f.seen[k] = true
	}
	for _, t := range s.Queue {
		f.seen[t.Key] = true
		f.queue = append(f.queue, t)
	}
}

// SeenCount returns the number of unique URLs seen.
func (f *frontier) SeenCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

// SeenKeys returns all seen keys, sorted. Used by tests and reports.
func (f *frontier) SeenKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(f.seen))
	for k := range f.seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
