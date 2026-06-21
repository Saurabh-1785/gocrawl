package docstore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/Saurabh-1785/gocrawl/extractor"
)

// DocStore handles writing Document records to a JSONL file.
// It is thread-safe and appends to the file (never truncates).
// It also deduplicates documents by ID within a session.
//
// JSONL format: one JSON object per line, easy to stream and parse.
type DocStore struct {
	mu      sync.Mutex
	file    *os.File
	writer  *bufio.Writer
	seen    map[string]bool // tracks document IDs to prevent duplicates
	count   int             // number of documents written in this session
	skipped int             // number of duplicate documents skipped
}

// NewDocStore creates a new DocStore that appends to the given file path.
// If the file doesn't exist, it is created.
// If the file already exists, new documents are appended to the end.
func NewDocStore(filepath string) (*DocStore, error) {
	// Open file in append mode: O_APPEND ensures writes go to end,
	// O_CREATE creates the file if it doesn't exist,
	// O_WRONLY opens for writing only.
	file, err := os.OpenFile(filepath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open docstore file %s: %v", filepath, err)
	}

	return &DocStore{
		file:   file,
		writer: bufio.NewWriter(file),
		seen:   make(map[string]bool),
	}, nil
}

// Save writes a single Document as a JSON line to the file.
// Thread-safe: multiple workers can call Save concurrently.
// Skips documents with IDs that have already been written in this session.
func (ds *DocStore) Save(doc extractor.Document) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	// Skip duplicate documents (same normalized URL → same ID)
	if ds.seen[doc.ID] {
		ds.skipped++
		return nil
	}
	ds.seen[doc.ID] = true

	// Marshal the document to JSON (compact, single line)
	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal document: %v", err)
	}

	// Write the JSON line followed by a newline
	if _, err := ds.writer.Write(data); err != nil {
		return fmt.Errorf("failed to write document: %v", err)
	}
	if err := ds.writer.WriteByte('\n'); err != nil {
		return fmt.Errorf("failed to write newline: %v", err)
	}

	// Flush after each document to ensure data is written to disk
	// even if the process crashes. The mutex protects the flush too.
	if err := ds.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush: %v", err)
	}

	ds.count++
	return nil
}

// Close flushes any buffered data and closes the file.
func (ds *DocStore) Close() error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if err := ds.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush on close: %v", err)
	}

	log.Printf("[INFO]  DocStore: wrote %d documents, skipped %d duplicates", ds.count, ds.skipped)
	return ds.file.Close()
}

// Count returns the number of documents written in this session.
func (ds *DocStore) Count() int {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.count
}
