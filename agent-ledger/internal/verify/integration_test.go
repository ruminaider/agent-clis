package verify_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildBinary compiles the agent-ledger binary into a temp file and
// returns its absolute path. The build runs once per test binary thanks
// to t.TempDir caching at the test runner level (each call still
// recompiles, which is fast enough for Phase 1).
func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "agent-ledger")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/ruminaider/agent-clis/agent-ledger/cmd/agent-ledger")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}
	return bin
}

func runBin(t *testing.T, bin, dir string, env []string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if asExit(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run %s %v: %v", bin, args, err)
		}
	}
	return code, stdout.String(), stderr.String()
}

func asExit(err error, target **exec.ExitError) bool {
	for {
		if e, ok := err.(*exec.ExitError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
		if err == nil {
			return false
		}
	}
}

// TestVerify_Subprocess_HappyPath exercises the full clean flow
// through the binary: assign, claim, record, close, verify --task
// --json. The expected exit code is 0 and status is "passed".
func TestVerify_Subprocess_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in -short mode")
	}
	bin := buildBinary(t)
	root := t.TempDir()
	ledger := filepath.Join(t.TempDir(), "ledger")
	if err := os.MkdirAll(ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "foo.go"), []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mustZero := func(label string, code int, out, errOut string) {
		t.Helper()
		if code != 0 {
			t.Fatalf("%s exited %d\nstdout=%s\nstderr=%s", label, code, out, errOut)
		}
	}
	env := []string{"AGENT_ID=pi.worker.test"}

	c, o, e := runBin(t, bin, root, env, "assign", "--task", "T1", "--orchestrator", "pi.main.test", "--agent", "pi.worker.test", "--allow", "src/**", "--policy", "warn", "--reason", "smoke", "--ledger-dir", ledger)
	mustZero("assign", c, o, e)

	c, o, e = runBin(t, bin, root, env, "claim", "src/foo.go", "--task", "T1", "--reason", "edit", "--json", "--ledger-dir", ledger)
	mustZero("claim", c, o, e)
	var claimResp map[string]any
	if err := json.Unmarshal([]byte(o), &claimResp); err != nil {
		t.Fatalf("claim json: %v\n%s", err, o)
	}
	intentID, _ := claimResp["intent_id"].(string)
	if intentID == "" {
		t.Fatalf("no intent_id in %s", o)
	}

	c, o, e = runBin(t, bin, root, env, "record", "src/foo.go", "--intent", intentID, "--summary", "smoke", "--ledger-dir", ledger)
	mustZero("record", c, o, e)
	c, o, e = runBin(t, bin, root, env, "close", "--intent", intentID, "--outcome", "completed", "--ledger-dir", ledger)
	mustZero("close", c, o, e)

	c, o, e = runBin(t, bin, root, nil, "verify", "--task", "T1", "--json", "--ledger-dir", ledger)
	if c != 0 {
		t.Fatalf("verify happy expected 0, got %d\nstdout=%s\nstderr=%s", c, o, e)
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(o), &rep); err != nil {
		t.Fatalf("rep parse: %v\n%s", err, o)
	}
	if rep["status"] != "passed" {
		t.Fatalf("expected passed, got %v\n%s", rep["status"], o)
	}
	if rep["schema"] != "agent-ledger.verify.v1" {
		t.Fatalf("schema=%v", rep["schema"])
	}
}

// TestVerify_Subprocess_Unclaimed exercises the unclaimed-change flow.
func TestVerify_Subprocess_Unclaimed(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := buildBinary(t)
	root := t.TempDir()
	ledger := filepath.Join(t.TempDir(), "ledger")
	if err := os.MkdirAll(ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	// Initialize a git repo so verify discovers the changed file via
	// `git status`.
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "rogue.txt"), []byte("oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, o, e := runBin(t, bin, root, nil, "verify", "--task", "T1", "--json", "--ledger-dir", ledger)
	if c != 1 {
		t.Fatalf("expected exit 1 for unclaimed, got %d\nstdout=%s\nstderr=%s", c, o, e)
	}
	if !strings.Contains(o, "UNCLAIMED_CHANGE") {
		t.Fatalf("expected UNCLAIMED_CHANGE in output: %s", o)
	}
}

// TestVerify_Subprocess_Forbidden checks the forbidden-path failure.
func TestVerify_Subprocess_Forbidden(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := buildBinary(t)
	root := t.TempDir()
	ledger := filepath.Join(t.TempDir(), "ledger")
	if err := os.MkdirAll(ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, "uv.lock"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := []string{"AGENT_ID=pi.worker.test"}
	if c, o, e := runBin(t, bin, root, env, "assign",
		"--task", "TF",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.test",
		"--allow", "uv.lock",
		"--forbid", "uv.lock",
		"--policy", "warn",
		"--reason", "smoke",
		"--ledger-dir", ledger); c != 0 {
		t.Fatalf("assign: %d %s %s", c, o, e)
	}
	// Claim won't be possible (forbidden); record a change manually
	// via adopt to simulate the post-edit state. The test's intent is
	// to assert FORBIDDEN_PATH on a changed working-tree file.
	c, o, e := runBin(t, bin, root, nil, "verify", "--task", "TF", "--json", "--ledger-dir", ledger)
	if c != 1 {
		t.Fatalf("expected 1, got %d\nout=%s err=%s", c, o, e)
	}
	if !strings.Contains(o, "FORBIDDEN_PATH") {
		t.Fatalf("expected FORBIDDEN_PATH, got %s", o)
	}
}

// TestVerify_Subprocess_SummaryMismatch builds a summary and tampers
// with it to confirm SUMMARY_MISMATCH fires with exit 1.
func TestVerify_Subprocess_SummaryMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := buildBinary(t)
	root := t.TempDir()
	ledger := filepath.Join(t.TempDir(), "ledger")
	if err := os.MkdirAll(ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := []string{"AGENT_ID=pi.worker.test"}
	mustZero := func(c int, o, e, label string) {
		if c != 0 {
			t.Fatalf("%s: %d %s %s", label, c, o, e)
		}
	}
	c, o, e := runBin(t, bin, root, env, "assign", "--task", "TS", "--orchestrator", "pi.main.test", "--agent", "pi.worker.test", "--allow", "f.txt", "--policy", "warn", "--reason", "x", "--ledger-dir", ledger)
	mustZero(c, o, e, "assign")
	c, o, e = runBin(t, bin, root, env, "claim", "f.txt", "--task", "TS", "--reason", "edit", "--json", "--ledger-dir", ledger)
	mustZero(c, o, e, "claim")
	var resp map[string]any
	json.Unmarshal([]byte(o), &resp)
	intentID, _ := resp["intent_id"].(string)
	c, o, e = runBin(t, bin, root, env, "record", "f.txt", "--intent", intentID, "--summary", "x", "--ledger-dir", ledger)
	mustZero(c, o, e, "record")
	c, o, e = runBin(t, bin, root, env, "close", "--intent", intentID, "--outcome", "completed", "--ledger-dir", ledger)
	mustZero(c, o, e, "close")

	sumPath := filepath.Join(root, "summary.json")
	c, o, e = runBin(t, bin, root, env, "export-summary", "--task", "TS", "--output", "summary.json", "--ledger-dir", ledger)
	mustZero(c, o, e, "export-summary")

	// Tamper.
	raw, err := os.ReadFile(sumPath)
	if err != nil {
		t.Fatal(err)
	}
	var docMap map[string]any
	if err := json.Unmarshal(raw, &docMap); err != nil {
		t.Fatal(err)
	}
	docMap["assignment_hash"] = "sha256:deadbeef"
	tampered, _ := json.MarshalIndent(docMap, "", "  ")
	if err := os.WriteFile(sumPath, tampered, 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify --summary in a *clean* directory (no ledger state).
	clean := t.TempDir()
	if err := os.WriteFile(filepath.Join(clean, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clean, "summary.json"), tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	c, o, e = runBin(t, bin, clean, nil, "verify", "--summary", "summary.json", "--json")
	if c != 1 {
		t.Fatalf("expected exit 1, got %d\nstdout=%s stderr=%s", c, o, e)
	}
	if !strings.Contains(o, "SUMMARY_MISMATCH") {
		t.Fatalf("expected SUMMARY_MISMATCH, got %s", o)
	}
}

// TestVerify_Subprocess_ConfigError forces a malformed pointer file
// and asserts exit 2.
func TestVerify_Subprocess_ConfigError(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := buildBinary(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".agent-ledger.toml"), []byte("garbage = [[[ not toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, o, e := runBin(t, bin, root, nil, "verify", "--json")
	if c != 2 {
		t.Fatalf("expected exit 2, got %d\nstdout=%s\nstderr=%s", c, o, e)
	}
	if !strings.Contains(o, "config_error") {
		t.Fatalf("expected config_error, got %s", o)
	}
}

// TestVerify_Subprocess_StorageError corrupts the sqlite file and
// asserts exit 3.
func TestVerify_Subprocess_StorageError(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bin := buildBinary(t)
	root := t.TempDir()
	ledger := filepath.Join(t.TempDir(), "ledger")
	if err := os.MkdirAll(ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ledger, "ledger.sqlite"), []byte("not a sqlite db"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, o, e := runBin(t, bin, root, nil, "verify", "--json", "--ledger-dir", ledger)
	if c != 3 {
		t.Fatalf("expected exit 3, got %d\nstdout=%s\nstderr=%s", c, o, e)
	}
	if !strings.Contains(o, "storage_error") {
		t.Fatalf("expected storage_error, got %s", o)
	}
}

// gitInit creates an empty git repo at root so `git status` works.
func gitInit(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}
