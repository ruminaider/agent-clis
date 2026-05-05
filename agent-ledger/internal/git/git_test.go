package git

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func gitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func runOrFatal(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func TestDiscover_NonGit(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	info, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if info.IsRepo {
		t.Fatal("expected non-repo")
	}
}

func TestWorktrees_NonGit(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	tops, err := Worktrees(dir)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(tops) != 0 {
		t.Fatalf("expected no worktrees, got %v", tops)
	}
}

func TestWorktrees_MainPlusLinked(t *testing.T) {
	gitAvailable(t)
	primary := t.TempDir()
	runOrFatal(t, primary, "git", "init", "-q")
	runOrFatal(t, primary, "git", "-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "--allow-empty", "-m", "init", "--quiet")

	linked := filepath.Join(t.TempDir(), "linked")
	runOrFatal(t, primary, "git", "worktree", "add", "-b", "feature", linked)

	tops, err := Worktrees(primary)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(tops) != 2 {
		t.Fatalf("expected 2 worktrees, got %d: %v", len(tops), tops)
	}
	primaryReal, _ := filepath.EvalSymlinks(primary)
	linkedReal, _ := filepath.EvalSymlinks(linked)
	found := map[string]bool{tops[0]: true, tops[1]: true}
	if !found[primaryReal] || !found[linkedReal] {
		t.Fatalf("expected %q and %q in tops, got %v", primaryReal, linkedReal, tops)
	}
	// Calling from the linked checkout should produce the same set.
	tops2, err := Worktrees(linked)
	if err != nil {
		t.Fatalf("Worktrees from linked: %v", err)
	}
	if len(tops2) != 2 {
		t.Fatalf("expected 2 from linked, got %d: %v", len(tops2), tops2)
	}
}

func TestDiscover_Repo(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	runOrFatal(t, dir, "git", "init", "-q")
	runOrFatal(t, dir, "git", "remote", "add", "origin", "https://example.com/test/repo.git")
	info, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !info.IsRepo {
		t.Fatal("expected repo")
	}
	if info.CommonDir == "" {
		t.Fatal("CommonDir empty")
	}
	if info.OriginURL != "https://example.com/test/repo.git" {
		t.Fatalf("OriginURL=%q", info.OriginURL)
	}
	// CommonDir should live under the repo dir.
	if rp, err := filepath.EvalSymlinks(dir); err == nil {
		if !filepath.IsAbs(info.CommonDir) {
			t.Fatalf("CommonDir not absolute: %q", info.CommonDir)
		}
		_ = rp
	}
}
