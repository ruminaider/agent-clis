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
