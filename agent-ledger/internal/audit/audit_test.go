package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWriter_AppendsAndRotates(t *testing.T) {
	dir := t.TempDir()
	day1 := time.Date(2026, 4, 27, 23, 30, 0, 0, time.UTC)
	day2 := day1.Add(2 * time.Hour) // crosses UTC midnight
	cur := day1
	w, err := NewWriter(dir, func() time.Time { return cur })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := w.Append(map[string]any{"k": "a"}); err != nil {
		t.Fatal(err)
	}
	cur = day2
	if err := w.Append(map[string]any{"k": "b"}); err != nil {
		t.Fatal(err)
	}

	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(files) != 2 {
		t.Fatalf("want 2 daily files, got %v", files)
	}

	got := readJSONL(t, filepath.Join(dir, "2026-04-27.jsonl"))
	if len(got) != 1 || got[0]["k"] != "a" {
		t.Fatalf("day1: %v", got)
	}
	got = readJSONL(t, filepath.Join(dir, "2026-04-28.jsonl"))
	if len(got) != 1 || got[0]["k"] != "b" {
		t.Fatalf("day2: %v", got)
	}
}

func TestWriter_AppendOnly(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	w, err := NewWriter(dir, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for i := 0; i < 5; i++ {
		if err := w.Append(map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	got := readJSONL(t, filepath.Join(dir, "2026-04-27.jsonl"))
	if len(got) != 5 {
		t.Fatalf("want 5 lines, got %d", len(got))
	}
}

func TestWriter_Concurrent(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	w, err := NewWriter(dir, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := w.Append(map[string]int{"i": i}); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if got := w.Count(); got != 50 {
		t.Fatalf("want 50 lines, got %d", got)
	}
	got := readJSONL(t, filepath.Join(dir, "2026-04-27.jsonl"))
	if len(got) != 50 {
		t.Fatalf("want 50 in file, got %d", len(got))
	}
}

func readJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	s := bufio.NewScanner(f)
	for s.Scan() {
		var m map[string]any
		if err := json.Unmarshal(s.Bytes(), &m); err != nil {
			t.Fatalf("bad line %q: %v", s.Text(), err)
		}
		out = append(out, m)
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
