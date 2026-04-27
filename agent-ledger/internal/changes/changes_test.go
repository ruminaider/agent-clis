package changes

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeDiff(t *testing.T) {
	in := "line1  \r\nline2\t\nline3\n\n\n"
	got := NormalizeDiff([]byte(in))
	want := "line1\nline2\nline3\n"
	if string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestHashDiffStable(t *testing.T) {
	a := HashDiff([]byte("foo\nbar\n"))
	b := HashDiff([]byte("foo\nbar\r\n"))
	if a != b {
		t.Fatalf("expected CRLF and LF to hash equal: %s vs %s", a, b)
	}
	if HashDiff(nil) != "" {
		t.Fatal("empty diff should have empty hash")
	}
}

func TestWriteBlob(t *testing.T) {
	root := t.TempDir()
	rel, err := WriteBlob(root, []byte("hello world\n"))
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world\n" {
		t.Fatalf("blob content mismatch: %q", data)
	}
	// Idempotent re-write.
	rel2, err := WriteBlob(root, []byte("hello world\n"))
	if err != nil {
		t.Fatal(err)
	}
	if rel != rel2 {
		t.Fatalf("blob path changed: %s vs %s", rel, rel2)
	}
}

func TestParseValidation(t *testing.T) {
	cases := []struct {
		in     string
		cmd    string
		status string
		ok     bool
	}{
		{"go test ./...:passed", "go test ./...", "passed", true},
		{"uv run ruff check src/foo:bar.py:passed", "uv run ruff check src/foo:bar.py", "passed", true},
		{"cmd:failed", "cmd", "failed", true},
		{"cmd:bogus", "", "", false},
		{"missingstatus", "", "", false},
		{":passed", "", "", false},
	}
	for _, tc := range cases {
		cmd, status, err := ParseValidation(tc.in)
		if tc.ok && err != nil {
			t.Fatalf("ParseValidation(%q) unexpected error: %v", tc.in, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("ParseValidation(%q) expected error", tc.in)
		}
		if tc.ok {
			if cmd != tc.cmd || status != tc.status {
				t.Fatalf("ParseValidation(%q) = (%q, %q), want (%q, %q)", tc.in, cmd, status, tc.cmd, tc.status)
			}
		}
	}
}
