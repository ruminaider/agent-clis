// Package integration_test holds Phase 1 end-to-end tests that drive a
// freshly built `agent-ledger` binary as a subprocess. Each test uses
// `t.TempDir()` for project root, ledger dir, and any helper paths so
// runs are isolated from $HOME and from each other. Long flows are
// guarded with `testing.Short()` so `go test -short ./...` stays fast.
package integration_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// builtBinary compiles the CLI once per test process and returns the
// absolute path. The binary is written under the test process's
// per-package GOTMPDIR via t.TempDir on first use, then reused.
func builtBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "agent-ledger-bin-")
		if err != nil {
			binErr = err
			return
		}
		out := filepath.Join(dir, "agent-ledger")
		cmd := exec.Command("go", "build", "-o", out,
			"github.com/ruminaider/agent-clis/agent-ledger/cmd/agent-ledger")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			binErr = err
			return
		}
		binPath = out
	})
	if binErr != nil {
		t.Fatalf("build binary: %v", binErr)
	}
	return binPath
}

// runResult captures the outcome of a subprocess invocation.
type runResult struct {
	Code   int
	Stdout string
	Stderr string
}

// mergeEnv merges base and override env slices so that for any key
// present in override, the override value wins and all base entries
// for that key are dropped. Base entries whose keys do not appear in
// override are preserved with duplicates reduced to the first
// occurrence. The result is base-first, overrides appended at the end.
//
// This is a pure function used by run() and tested directly by
// TestMergeEnv_* to ensure the precedence logic cannot regress
// silently. References: finding rv2-new-002, packet RV3-002.
func mergeEnv(base, override []string) []string {
	// Collect the set of keys the caller wants to override.
	overrideKeys := make(map[string]struct{}, len(override))
	for _, e := range override {
		if idx := strings.IndexByte(e, '='); idx > 0 {
			overrideKeys[e[:idx]] = struct{}{}
		}
	}
	// Strip base entries whose key matches an override, and reduce
	// any duplicate base keys to the first occurrence.
	seen := make(map[string]struct{}, len(base))
	filtered := make([]string, 0, len(base))
	for _, e := range base {
		key := e
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			key = e[:idx]
		}
		if _, skip := overrideKeys[key]; skip {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, e)
	}
	return append(filtered, override...)
}

// run executes the agent-ledger binary in dir with the given args.
// env entries are appended to os.Environ so callers can override
// AGENT_ID/HOME/etc. without polluting other tests. The shared
// per-test t.TempDir() based HOME isolates XDG state lookups.
//
// Keys listed in env take precedence: any entry in the inherited
// environment whose key matches an override key is stripped before
// the override is appended. This prevents duplicate-key ambiguity
// when the parent process already exports the same variable.
func run(t *testing.T, dir string, env []string, args ...string) runResult {
	t.Helper()
	bin := builtBinary(t)
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir

	cmd.Env = mergeEnv(os.Environ(), env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return runResult{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
}

// mustZero fails the test unless r.Code == 0.
func mustZero(t *testing.T, label string, r runResult) {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("%s exited %d\nstdout=%s\nstderr=%s", label, r.Code, r.Stdout, r.Stderr)
	}
}

// writeFile creates the parent directory and writes data.
func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readAll is a tiny os.ReadFile wrapper that lives in the test
// helpers so individual tests can pull file contents without
// re-importing os.
func readAll(p string) ([]byte, error) { return os.ReadFile(p) }

// freshLedger returns a writable ledger directory under t.TempDir().
func freshLedger(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ledger")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
