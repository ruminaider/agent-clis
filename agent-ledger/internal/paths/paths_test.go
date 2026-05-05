package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestNormalize_Basic(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a/b"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(root, "a", "b", "File.go")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := Normalize(root, "a/b/File.go")
	if err != nil {
		t.Fatal(err)
	}
	if n.Display != "a/b/File.go" {
		t.Fatalf("Display=%q", n.Display)
	}
	if !strings.Contains(n.RealPath, filepath.Join("a", "b", "File.go")) {
		t.Fatalf("RealPath=%q", n.RealPath)
	}
	if len(n.PathHash) != 64 {
		t.Fatalf("PathHash=%q", n.PathHash)
	}
}

func TestNormalize_Outside(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	f := filepath.Join(other, "x")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Normalize(root, f)
	if !IsOutsideProject(err) {
		t.Fatalf("expected OutsideProjectError, got %v", err)
	}
}

func TestNormalize_Symlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, "real", "f.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.go")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	n1, err := Normalize(root, "link.go")
	if err != nil {
		t.Fatal(err)
	}
	n2, err := Normalize(root, "real/f.go")
	if err != nil {
		t.Fatal(err)
	}
	if n1.PathHash != n2.PathHash {
		t.Fatalf("symlink hash mismatch: %q vs %q", n1.PathHash, n2.PathHash)
	}
}

func TestNormalize_UnicodeNFC(t *testing.T) {
	root := t.TempDir()
	// "café" with combining accent (NFD) vs precomposed (NFC).
	nfd := "caf\u0065\u0301.txt"
	nfc := norm.NFC.String(nfd)
	if nfc == nfd {
		t.Fatal("test fixture not actually NFD")
	}
	if err := os.WriteFile(filepath.Join(root, nfc), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := Normalize(root, nfd)
	if err != nil {
		t.Fatal(err)
	}
	if n.RealPath != norm.NFC.String(n.RealPath) {
		t.Fatal("RealPath not NFC")
	}
	if !strings.HasSuffix(n.Display, nfc) {
		t.Fatalf("Display not NFC: %q", n.Display)
	}
}

func TestNormalize_NonexistentPath(t *testing.T) {
	root := t.TempDir()
	n, err := Normalize(root, "does/not/exist.go")
	if err != nil {
		t.Fatal(err)
	}
	if n.Display != "does/not/exist.go" {
		t.Fatalf("Display=%q", n.Display)
	}
}

func TestNormalize_PreservesCase(t *testing.T) {
	root := t.TempDir()
	n, err := Normalize(root, "MixedCase.GO")
	if err != nil {
		t.Fatal(err)
	}
	if n.Display != "MixedCase.GO" {
		t.Fatalf("Display=%q", n.Display)
	}
}

func TestNormalize_SlashSeparators(t *testing.T) {
	root := t.TempDir()
	n, err := Normalize(root, filepath.Join("a", "b", "c.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(n.Display, '\\') {
		t.Fatalf("backslash in display: %q", n.Display)
	}
}

// TestNormalizeAt_LongestPrefixWins covers the multi-root scope expansion
// used for git worktrees (PR1: worktree-aware paths). When the same input
// could resolve under two roots, the longer realpath prefix wins, so a
// path under /repo/.worktrees/foo/sub picks the worktree root rather than
// the main checkout root.
func TestNormalizeAt_LongestPrefixWins(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "main")
	wt := filepath.Join(base, "main", ".worktrees", "feature")
	if err := os.MkdirAll(filepath.Join(wt, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(wt, "src", "x.go")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Main is listed first; if matching were first-wins instead of
	// longest-prefix, the display would be ".worktrees/feature/src/x.go".
	n, err := NormalizeAt([]string{main, wt}, f)
	if err != nil {
		t.Fatal(err)
	}
	if n.Display != "src/x.go" {
		t.Fatalf("Display=%q want src/x.go", n.Display)
	}
}

func TestNormalizeAt_FallbackToFirstRootForRelative(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "f.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := NormalizeAt([]string{root, other}, "a/f.go")
	if err != nil {
		t.Fatal(err)
	}
	if n.Display != "a/f.go" {
		t.Fatalf("Display=%q", n.Display)
	}
}

func TestNormalizeAt_OutsideAllRoots(t *testing.T) {
	r1 := t.TempDir()
	r2 := t.TempDir()
	stray := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NormalizeAt([]string{r1, r2}, stray)
	if !IsOutsideProject(err) {
		t.Fatalf("expected OutsideProjectError, got %v", err)
	}
}

func TestNormalizeAt_EmptyRootsRejected(t *testing.T) {
	if _, err := NormalizeAt(nil, "/tmp"); err == nil {
		t.Fatal("expected error for empty roots")
	}
	if _, err := NormalizeAt([]string{"", "  "}, "/tmp"); err == nil {
		t.Fatal("expected error when every root is blank")
	}
}

func TestNormalizeAt_SingleRootMatchesNormalize(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(root, "a", "f.go")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	n1, err := Normalize(root, "a/f.go")
	if err != nil {
		t.Fatal(err)
	}
	n2, err := NormalizeAt([]string{root}, "a/f.go")
	if err != nil {
		t.Fatal(err)
	}
	if n1.Display != n2.Display || n1.PathHash != n2.PathHash {
		t.Fatalf("single-root NormalizeAt diverged from Normalize: %#v vs %#v", n1, n2)
	}
}

// TestPortableHash_NoBackslashCoercion asserts that PortableHash does NOT
// fold backslashes to forward slashes (RV3-001). On POSIX, backslash is a
// valid filename character; aliasing "a\\b" to "a/b" would silently corrupt
// summary keying for files whose names contain backslashes. Callers that
// receive platform-native paths must apply filepath.ToSlash before calling
// PortableHash. SPEC §32.
func TestPortableHash_NoBackslashCoercion(t *testing.T) {
	forward := PortableHash(`a/b`)
	backward := PortableHash(`a\b`)
	if forward == backward {
		t.Fatalf("PortableHash should differ: forward=%q backward=%q", forward, backward)
	}
	if len(forward) != 64 {
		t.Fatalf("expected 64-char hex, got %q", forward)
	}
}
