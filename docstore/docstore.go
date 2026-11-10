package docstore

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/Saurabh-1785/gocrawl/extractor"
)

// DocStore writes Documents to a JSONL file: one JSON object per line.
//
//   - Thread-safe: many workers can call Save at once.
//   - Append-only: the file is never truncated.
//   - Deduplicated by document ID, including documents written by EARLIER
//     runs: the IDs already in the file are loaded when it is opened.
//   - Crash-tolerant: each document is flushed to the OS right away, and a
//     line left half-written by a crash is closed off on the next open.
type DocStore struct {
	mu      sync.Mutex
	file    *os.File
	writer  *bufio.Writer
	seen    map[string]bool // IDs already in the file
	count   int             // documents written by this DocStore
	skipped int             // duplicates skipped by this DocStore
}

// NewDocStore opens (or creates) the JSONL file at path for appending.
func NewDocStore(path string) (*DocStore, error) {
	seen, needsNewline, err := loadIDs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read existing docstore file %s: %w", path, err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open docstore file %s: %w", path, err)
	}

	ds := &DocStore{file: file, writer: bufio.NewWriter(file), seen: seen}

	// A crash in the middle of a write leaves a last line with no "\n".
	// Close it off, so the next document starts on its own line.
	if needsNewline {
		if _, err := file.Write([]byte{'\n'}); err != nil {
			file.Close()
			return nil, fmt.Errorf("failed to repair docstore file %s: %w", path, err)
		}
	}
	return ds, nil
}

// loadIDs reads the IDs of documents already in the file.
// Lines that aren't valid JSON (e.g. cut off by a crash) are ignored.
// needsNewline is true when the file doesn't end with "\n".
func loadIDs(path string) (seen map[string]bool, needsNewline bool, err error) {
	seen = make(map[string]bool)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return seen, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n') // no line-length limit, unlike bufio.Scanner
		if len(line) > 0 {
			needsNewline = line[len(line)-1] != '\n'
			var doc struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(line, &doc) == nil && doc.ID != "" {
				seen[doc.ID] = true
			}
		}
		if err == io.EOF {
			return seen, needsNewline, nil
		}
		if err != nil {
			return nil, false, err
		}
	}
}

// Save writes one Document as a JSON line.
// A document whose ID is already in the file is skipped (returns nil).
func (ds *DocStore) Save(doc extractor.Document) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if ds.seen[doc.ID] {
		ds.skipped++
		return nil
	}

	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal document: %w", err)
	}
	data = append(data, '\n')

	if _, err := ds.writer.Write(data); err != nil {
		return fmt.Errorf("failed to write document: %w", err)
	}
	// Flush after each document: it then survives a crash of THIS program.
	// (Surviving a power cut needs fsync; Close does that once at the end.)
	if err := ds.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush: %w", err)
	}

	// Mark as seen only once it's really written, so a failed write
	// doesn't block a later retry of the same document.
	ds.seen[doc.ID] = true
	ds.count++
	return nil
}

// Close flushes, syncs the file to disk, and closes it.
func (ds *DocStore) Close() error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if err := ds.writer.Flush(); err != nil {
		ds.file.Close()
		return fmt.Errorf("failed to flush on close: %w", err)
	}
	if err := ds.file.Sync(); err != nil {
		ds.file.Close()
		return fmt.Errorf("failed to sync on close: %w", err)
	}
	return ds.file.Close()
}

// Count returns the number of documents written by this DocStore.
func (ds *DocStore) Count() int {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.count
}

// Skipped returns the number of duplicate documents skipped by this DocStore.
func (ds *DocStore) Skipped() int {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.skipped
}
