package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/config"
)

func skipIfNoGit(t *testing.T) {
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

func TestResolve_FlagWins(t *testing.T) {
	root := t.TempDir()
	want := t.TempDir()
	res, err := Resolve(Options{Root: root, LedgerDirFlag: want, EnvLedgerDir: "/should/be/ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if res.LedgerDirSource != SourceFlag {
		t.Fatalf("source=%s", res.LedgerDirSource)
	}
	if !strings.HasPrefix(res.LedgerDir, want) {
		t.Fatalf("LedgerDir=%q want prefix %q", res.LedgerDir, want)
	}
}

func TestResolve_EnvWinsOverPointer(t *testing.T) {
	root := t.TempDir()
	if err := config.WritePointer(root, config.Pointer{Version: 1, LedgerDir: "/from/pointer"}); err != nil {
		t.Fatal(err)
	}
	res, err := Resolve(Options{Root: root, EnvLedgerDir: "/from/env"})
	if err != nil {
		t.Fatal(err)
	}
	if res.LedgerDirSource != SourceEnv {
		t.Fatalf("source=%s", res.LedgerDirSource)
	}
	if !strings.HasSuffix(res.LedgerDir, "from/env") {
		t.Fatalf("LedgerDir=%q", res.LedgerDir)
	}
}

func TestResolve_PointerWinsOverXDG(t *testing.T) {
	root := t.TempDir()
	if err := config.WritePointer(root, config.Pointer{Version: 1, LedgerDir: "/from/pointer"}); err != nil {
		t.Fatal(err)
	}
	res, err := Resolve(Options{Root: root, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if res.LedgerDirSource != SourcePointer {
		t.Fatalf("source=%s LedgerDir=%s", res.LedgerDirSource, res.LedgerDir)
	}
}

func TestResolve_XDGFallback(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	res, err := Resolve(Options{Root: root, HomeDir: home, ProjectIDFlag: "example.com/x/y"})
	if err != nil {
		t.Fatal(err)
	}
	if res.LedgerDirSource != SourceXDG {
		t.Fatalf("source=%s", res.LedgerDirSource)
	}
	want := filepath.Join(home, ".local", "state", "agent-ledger", "repos", res.Identity.DirName())
	if res.LedgerDir != want {
		t.Fatalf("LedgerDir=%q want %q", res.LedgerDir, want)
	}
}

func TestResolve_XDGStateHomeRespected(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	res, err := Resolve(Options{Root: root, XDGStateHome: state, ProjectIDFlag: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.LedgerDir, state) {
		t.Fatalf("LedgerDir=%q does not use XDG_STATE_HOME=%q", res.LedgerDir, state)
	}
}

func TestResolve_GitCommonDirSharedAcrossWorktrees(t *testing.T) {
	skipIfNoGit(t)
	repo := t.TempDir()
	runOrFatal(t, repo, "git", "init", "-q")
	runOrFatal(t, repo, "git", "remote", "add", "origin", "https://example.com/foo/bar.git")
	runOrFatal(t, repo, "git", "-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "--allow-empty", "-m", "init", "--quiet")

	wtParent := t.TempDir()
	wt := filepath.Join(wtParent, "wt")
	runOrFatal(t, repo, "git", "worktree", "add", "-b", "wt", wt)

	r1, err := Resolve(Options{Root: repo, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Resolve(Options{Root: wt, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Identity.Fingerprint != r2.Identity.Fingerprint {
		t.Fatalf("worktrees should share fingerprint: %q vs %q", r1.Identity.Fingerprint, r2.Identity.Fingerprint)
	}
}

func TestResolve_NonGitFallback(t *testing.T) {
	root := t.TempDir()
	res, err := Resolve(Options{Root: root, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if res.GitInfo.IsRepo {
		t.Fatal("expected non-git")
	}
	if res.Identity.Root == "" {
		t.Fatal("expected non-git root populated")
	}
	if res.Identity.GitCommonDir != "" {
		t.Fatal("git common dir should be empty for non-git")
	}
}

func TestResolve_GitPointerFallback(t *testing.T) {
	skipIfNoGit(t)
	repo := t.TempDir()
	runOrFatal(t, repo, "git", "init", "-q")
	commonDir := filepath.Join(repo, ".git")
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(commonDir, GitPointerName)); err != nil {
		t.Fatal(err)
	}
	// Use a fresh, empty XDG location so the XDG path does not exist.
	res, err := Resolve(Options{Root: repo, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if res.LedgerDirSource != SourceGitPointer {
		t.Fatalf("expected git-pointer, got %s (LedgerDir=%s)", res.LedgerDirSource, res.LedgerDir)
	}
}
