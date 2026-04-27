// Package git provides best-effort git repository discovery used by ledger
// project identity. It shells out to the git binary; absence of git is a
// soft failure that callers handle as "non-git project".
package git

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// Info captures the slice of git metadata the ledger needs.
//
// Each field is a best-effort lookup; an empty value means git did not
// answer or the project is not a git repo.
type Info struct {
	// IsRepo is true when the working directory is inside a git work tree
	// or bare repo.
	IsRepo bool
	// CommonDir is the realpath of the git common dir (shared across
	// worktrees), if available.
	CommonDir string
	// TopLevel is the realpath of the working tree root, if available.
	TopLevel string
	// OriginURL is the configured remote.origin.url, if any.
	OriginURL string
}

// ErrNoGit is returned by Discover when the git binary is not on PATH.
var ErrNoGit = errors.New("git: binary not found on PATH")

// Discover queries git inside dir. It never returns an error for "this is
// not a git repo": it returns an Info with IsRepo=false. ErrNoGit signals
// that the caller cannot perform git-aware logic at all.
func Discover(dir string) (Info, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return Info{}, ErrNoGit
	}
	info := Info{}
	if out, ok := runGit(dir, "rev-parse", "--is-inside-work-tree"); ok && strings.TrimSpace(out) == "true" {
		info.IsRepo = true
	} else if out, ok := runGit(dir, "rev-parse", "--is-bare-repository"); ok && strings.TrimSpace(out) == "true" {
		info.IsRepo = true
	}
	if !info.IsRepo {
		return info, nil
	}
	if out, ok := runGit(dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); ok {
		info.CommonDir = realpath(strings.TrimSpace(out))
	}
	if out, ok := runGit(dir, "rev-parse", "--show-toplevel"); ok {
		info.TopLevel = realpath(strings.TrimSpace(out))
	}
	if out, ok := runGit(dir, "config", "--get", "remote.origin.url"); ok {
		info.OriginURL = strings.TrimSpace(out)
	}
	return info, nil
}

// runGit runs `git <args...>` with cwd=dir and returns trimmed stdout.
// The bool reports success; non-zero exit returns false.
func runGit(dir string, args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// realpath resolves symlinks in p; if it cannot, returns the absolute form.
func realpath(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		return rp
	}
	return p
}
