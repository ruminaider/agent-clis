// Package audit writes the privacy-safe JSONL mirror of every event
// row. Files rotate daily by UTC date: `<ledger>/audit/YYYY-MM-DD.jsonl`.
//
// Audit lines are best-effort: failure to mirror MUST NOT roll back a
// database write. The Doctor command surfaces mirror failures via a
// stat counter exposed through Writer.LastError.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Clock is the time source used to pick the current day's file. Tests
// inject a fixed clock to assert rotation.
type Clock func() time.Time

// Writer is a thread-safe append-only JSONL writer with daily rotation.
type Writer struct {
	dir   string
	now   Clock
	mu    sync.Mutex
	day   string
	file  *os.File
	last  error
	count uint64
}

// NewWriter constructs an audit writer rooted at dir. The directory is
// created if absent. now defaults to time.Now.
func NewWriter(dir string, now Clock) (*Writer, error) {
	if dir == "" {
		return nil, fmt.Errorf("audit: empty dir")
	}
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("audit: mkdir %s: %w", dir, err)
	}
	return &Writer{dir: dir, now: now}, nil
}

// Append serializes obj as a single JSONL line into the day's file.
// Each call performs an O_APPEND write followed by Sync, so partial
// writes are not interleaved across goroutines or processes.
//
// Append never returns nil for a marshal failure; otherwise filesystem
// errors are returned and also retained in LastError.
func (w *Writer) Append(obj any) error {
	line, err := json.Marshal(obj)
	if err != nil {
		w.setLast(fmt.Errorf("audit: marshal: %w", err))
		return w.last
	}
	line = append(line, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()
	day := w.now().UTC().Format("2006-01-02")
	if w.file == nil || day != w.day {
		if err := w.rotate(day); err != nil {
			w.last = err
			return err
		}
	}
	if _, err := w.file.Write(line); err != nil {
		w.last = fmt.Errorf("audit: write: %w", err)
		return w.last
	}
	if err := w.file.Sync(); err != nil {
		w.last = fmt.Errorf("audit: sync: %w", err)
		return w.last
	}
	w.count++
	return nil
}

func (w *Writer) rotate(day string) error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	path := filepath.Join(w.dir, day+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("audit: open %s: %w", path, err)
	}
	w.file = f
	w.day = day
	return nil
}

// Close releases the current file handle. Subsequent Append calls
// reopen it.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// LastError returns the most recent mirror error, or nil if none.
func (w *Writer) LastError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last
}

// Count reports how many lines have been successfully appended.
func (w *Writer) Count() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}

func (w *Writer) setLast(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.last = err
}
