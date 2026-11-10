package docstore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Saurabh-1785/gocrawl/extractor"
)

func TestNewDocStore_CreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.jsonl")

	ds, err := NewDocStore(path)
	if err != nil {
		t.Fatalf("failed to create docstore: %v", err)
	}
	defer ds.Close()

	// File should exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected file to be created")
	}
}

func TestDocStore_SaveAndRead(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.jsonl")

	ds, err := NewDocStore(path)
	if err != nil {
		t.Fatalf("failed to create docstore: %v", err)
	}

	doc := extractor.Document{
		ID:            "abc123",
		URL:           "https://example.com/page",
		Domain:        "example.com",
		Title:         "Test Page",
		Content:       "Hello world content",
		StatusCode:    200,
		ContentLength: 1234,
		Timestamp:     1700000000,
	}

	if err := ds.Save(doc); err != nil {
		t.Fatalf("failed to save document: %v", err)
	}
	ds.Close()

	// Read the file and verify JSONL format
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	// Parse the JSON line
	var parsed extractor.Document
	if err := json.Unmarshal([]byte(lines[0]), &parsed); err != nil {
		t.Fatalf("failed to parse JSONL line: %v", err)
	}

	if parsed.Title != "Test Page" {
		t.Errorf("expected title 'Test Page', got '%s'", parsed.Title)
	}
	if parsed.URL != "https://example.com/page" {
		t.Errorf("expected URL 'https://example.com/page', got '%s'", parsed.URL)
	}
	if parsed.StatusCode != 200 {
		t.Errorf("expected status code 200, got %d", parsed.StatusCode)
	}
}

func TestDocStore_AppendMode(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.jsonl")

	// First session: write 2 documents
	ds1, err := NewDocStore(path)
	if err != nil {
		t.Fatalf("failed to create docstore: %v", err)
	}
	ds1.Save(extractor.Document{ID: "1", Title: "First"})
	ds1.Save(extractor.Document{ID: "2", Title: "Second"})
	ds1.Close()

	// Second session: write 1 more document (should append, not overwrite)
	ds2, err := NewDocStore(path)
	if err != nil {
		t.Fatalf("failed to create docstore: %v", err)
	}
	ds2.Save(extractor.Document{ID: "3", Title: "Third"})
	ds2.Close()

	// Read and verify all 3 documents are present
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (append mode), got %d", len(lines))
	}

	// Verify the third document
	var third extractor.Document
	json.Unmarshal([]byte(lines[2]), &third)
	if third.Title != "Third" {
		t.Errorf("expected third doc title 'Third', got '%s'", third.Title)
	}
}

func TestDocStore_ConcurrentWrites(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "concurrent.jsonl")

	ds, err := NewDocStore(path)
	if err != nil {
		t.Fatalf("failed to create docstore: %v", err)
	}

	// Simulate 50 concurrent workers writing documents
	var wg sync.WaitGroup
	numDocs := 50

	for i := 0; i < numDocs; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			doc := extractor.Document{
				ID:    fmt.Sprintf("doc-%d", id), // unique IDs so all get written
				Title: "Concurrent Doc",
			}
			if err := ds.Save(doc); err != nil {
				t.Errorf("concurrent save failed: %v", err)
			}
		}(i)
	}

	wg.Wait()
	ds.Close()

	// Verify all documents were written
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineCount := 0
	for scanner.Scan() {
		lineCount++
		// Each line should be valid JSON
		var doc extractor.Document
		if err := json.Unmarshal(scanner.Bytes(), &doc); err != nil {
			t.Errorf("line %d is not valid JSON: %v", lineCount, err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if lineCount != numDocs {
		t.Errorf("expected %d lines, got %d", numDocs, lineCount)
	}
}

func TestDocStore_Count(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "count.jsonl")

	ds, err := NewDocStore(path)
	if err != nil {
		t.Fatalf("failed to create docstore: %v", err)
	}
	defer ds.Close()

	if ds.Count() != 0 {
		t.Errorf("expected count 0, got %d", ds.Count())
	}

	ds.Save(extractor.Document{ID: "1"})
	ds.Save(extractor.Document{ID: "2"})

	if ds.Count() != 2 {
		t.Errorf("expected count 2, got %d", ds.Count())
	}
}

func TestDocStore_SkipsDuplicateIDs(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "dedup.jsonl")

	ds, err := NewDocStore(path)
	if err != nil {
		t.Fatalf("failed to create docstore: %v", err)
	}

	// Save 3 documents, two with the same ID
	ds.Save(extractor.Document{ID: "abc", Title: "First"})
	ds.Save(extractor.Document{ID: "def", Title: "Second"})
	ds.Save(extractor.Document{ID: "abc", Title: "First Again"}) // duplicate — should be skipped
	ds.Close()

	// Only 2 unique documents should be written
	if ds.Count() != 2 {
		t.Errorf("expected count 2 (dedup), got %d", ds.Count())
	}

	// Verify the file has exactly 2 lines
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (dedup), got %d", len(lines))
	}

	// Verify the second line is "Second", not "First Again"
	var second extractor.Document
	json.Unmarshal([]byte(lines[1]), &second)
	if second.Title != "Second" {
		t.Errorf("expected second doc to be 'Second', got '%s'", second.Title)
	}
}

// Documents written by an earlier run are not written again.
func TestDocStore_DedupesAcrossRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.jsonl")

	ds1, _ := NewDocStore(path)
	ds1.Save(extractor.Document{ID: "1"})
	ds1.Save(extractor.Document{ID: "2"})
	ds1.Close()

	ds2, err := NewDocStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ds2.Save(extractor.Document{ID: "1"}) // already in the file
	ds2.Save(extractor.Document{ID: "3"})
	ds2.Close()

	if ds2.Count() != 1 || ds2.Skipped() != 1 {
		t.Errorf("second run: count=%d skipped=%d, want 1 and 1", ds2.Count(), ds2.Skipped())
	}
	data, _ := os.ReadFile(path)
	if n := len(strings.Split(strings.TrimSpace(string(data)), "\n")); n != 3 {
		t.Errorf("file has %d lines, want 3", n)
	}
}

// A line cut off by a crash is closed off, so the next document starts on
// its own line and every complete line is still valid JSON.
func TestDocStore_RepairsCutOffLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.jsonl")
	os.WriteFile(path, []byte(`{"id":"1","title":"ok"}`+"\n"+`{"id":"2","tit`), 0644)

	ds, err := NewDocStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ds.Save(extractor.Document{ID: "1"}) // complete line: still deduped
	ds.Save(extractor.Document{ID: "3", Title: "new"})
	ds.Close()

	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(lines), data)
	}
	var last extractor.Document
	if err := json.Unmarshal([]byte(lines[2]), &last); err != nil || last.ID != "3" {
		t.Errorf("last line %q should be the new document", lines[2])
	}
	if ds.Count() != 1 || ds.Skipped() != 1 {
		t.Errorf("count=%d skipped=%d, want 1 and 1", ds.Count(), ds.Skipped())
	}
}

// Very long lines (a big page's text) are read back without a size limit.
func TestDocStore_LoadsLongLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.jsonl")
	ds, _ := NewDocStore(path)
	ds.Save(extractor.Document{ID: "big", Content: strings.Repeat("x", 3<<20)})
	ds.Close()

	ds2, _ := NewDocStore(path)
	ds2.Save(extractor.Document{ID: "big"})
	ds2.Close()
	if ds2.Skipped() != 1 {
		t.Error("a 3 MB line should still be read and deduped")
	}
}
