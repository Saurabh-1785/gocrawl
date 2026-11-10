package crawler

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const checkpointVersion = 1

// checkpointFile is what gets saved to CheckpointPath.
type checkpointFile struct {
	Version int       `json:"version"`
	Seed    string    `json:"seed"`  // normalized seed URL; must match on resume
	Sites   []string  `json:"sites"` // hosts we may crawl (the seed may have redirected)
	SavedAt time.Time `json:"saved_at"`
	snapshot
}

// saveCheckpoint writes the frontier to disk ATOMICALLY: write a temp file,
// fsync it, then rename it over the old one. A crash at any moment leaves
// either the old checkpoint or the new one, never a half-written file.
func (c *Crawler) saveCheckpoint() error {
	if c.CheckpointPath == "" {
		return nil
	}
	data, err := json.Marshal(checkpointFile{
		Version:  checkpointVersion,
		Seed:     c.seedKey,
		Sites:    c.siteList(),
		SavedAt:  time.Now(),
		snapshot: c.frontier.Snapshot(),
	})
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(c.CheckpointPath), filepath.Base(c.CheckpointPath)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.CheckpointPath) // atomic on POSIX
}

// restoreCheckpoint loads a checkpoint when Resume is set.
// It returns true if the crawl continues from one.
func (c *Crawler) restoreCheckpoint() (bool, error) {
	if !c.Resume || c.CheckpointPath == "" {
		return false, nil
	}
	data, err := os.ReadFile(c.CheckpointPath)
	if errors.Is(err, os.ErrNotExist) {
		c.log.Printf("[INFO]  No checkpoint at %s, starting fresh", c.CheckpointPath)
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading checkpoint: %w", err)
	}

	var cp checkpointFile
	if err := json.Unmarshal(data, &cp); err != nil {
		return false, fmt.Errorf("checkpoint %s is corrupt: %w", c.CheckpointPath, err)
	}
	if cp.Version != checkpointVersion {
		return false, fmt.Errorf("checkpoint version %d not supported (want %d)", cp.Version, checkpointVersion)
	}
	if cp.Seed != c.seedKey {
		return false, fmt.Errorf("checkpoint is for %s, not %s", cp.Seed, c.seedKey)
	}

	c.frontier.Restore(cp.snapshot)
	c.siteMu.Lock()
	for _, s := range cp.Sites {
		c.sites[s] = true
	}
	c.siteMu.Unlock()
	c.log.Printf("[INFO]  Resumed from %s: %d pages done, %d queued, %d URLs seen",
		c.CheckpointPath, cp.Done, len(cp.Queue), len(cp.Seen))
	return true, nil
}

// startCheckpoints saves a checkpoint every CheckpointEvery while the crawl
// runs. The returned function stops it and waits for it to finish.
func (c *Crawler) startCheckpoints() (stop func()) {
	if c.CheckpointPath == "" {
		return func() {}
	}
	every := c.CheckpointEvery
	if every <= 0 {
		every = 10 * time.Second
	}
	quit := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := c.saveCheckpoint(); err != nil {
					c.log.Printf("[ERROR] Saving checkpoint: %v", err)
				}
			case <-quit:
				return
			}
		}
	}()
	return func() {
		close(quit)
		<-finished
	}
}

// finishCheckpoint runs after all workers have stopped. If the crawl ran
// out of work, the checkpoint is no longer needed. Otherwise (page limit,
// Ctrl-C, error) it is saved so the crawl can be resumed.
func (c *Crawler) finishCheckpoint() {
	if c.CheckpointPath == "" {
		return
	}
	if c.frontier.Complete() {
		if err := os.Remove(c.CheckpointPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			c.log.Printf("[ERROR] Removing checkpoint: %v", err)
		}
		return
	}
	if err := c.saveCheckpoint(); err != nil {
		c.log.Printf("[ERROR] Saving checkpoint: %v", err)
		return
	}
	c.log.Printf("[INFO]  Checkpoint saved to %s (resume with -resume)", c.CheckpointPath)
}
