package integration_test

// Tests for PR2 (canonical_path_hash). Two interlocking properties must
// hold end-to-end:
//
//  1. Two agents in two worktrees claiming the same logical file
//     produce a conflict event under `warn` policy. Before PR2 they
//     silently coexisted because path_hash embedded the realpath.
//  2. On a case-insensitive filesystem, claims that differ only in
//     case collide. Before PR2 the realpath-derived hash collapsed
//     them via EvalSymlinks; after PR2 the canonical hash folds case
//     directly so the property is preserved on case-insensitive AND
//     case-sensitive filesystems.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCrossWorktreeConflictDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess cross-worktree conflict test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	worktreeDir := filepath.Join(root, "linked")
	xdgState := filepath.Join(root, "xdg")
	env := []string{"XDG_STATE_HOME=" + xdgState}

	gitCmd(t, root, "init", primary)
	gitCmd(t, primary, "-C", primary, "config", "user.email", "test@test.invalid")
	gitCmd(t, primary, "-C", primary, "config", "user.name", "Test")
	writeFile(t, filepath.Join(primary, "README.md"), "# test\n")
	gitCmd(t, primary, "-C", primary, "add", ".")
	gitCmd(t, primary, "-C", primary, "commit", "-m", "init")
	gitCmd(t, primary, "-C", primary, "worktree", "add", worktreeDir)

	writeFile(t, filepath.Join(primary, "src", "shared.go"), "x")
	writeFile(t, filepath.Join(worktreeDir, "src", "shared.go"), "x")

	mustZero(t, "init", run(t, primary, env, "init"))

	// Two assignments, one per worker. allowed_paths overlap because
	// the conflict policy is what should detect the overlap, not the
	// assignment scope.
	mustZero(t, "assign-A", run(t, primary, env,
		"assign", "--task", "CW-1",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.A",
		"--allow", "src/**", "--policy", "warn",
		"--reason", "worker A on shared.go",
	))
	mustZero(t, "assign-B", run(t, primary, env,
		"assign", "--task", "CW-1",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.B",
		"--allow", "src/**", "--policy", "warn",
		"--reason", "worker B on shared.go",
	))

	// Worker A claims from inside the linked worktree.
	rA := run(t, worktreeDir, append([]string{"AGENT_ID=pi.worker.A"}, env...),
		"claim", "src/shared.go",
		"--task", "CW-1",
		"--reason", "from worktree",
		"--json",
	)
	mustZero(t, "claim A", rA)

	// Worker B claims the same logical file but from cwd in the main
	// checkout. PR2 makes this collide via canonical_path_hash even
	// though path_hash differs.
	rB := run(t, primary, append([]string{"AGENT_ID=pi.worker.B"}, env...),
		"claim", "src/shared.go",
		"--task", "CW-1",
		"--reason", "from main checkout",
		"--json",
	)
	if rB.Code != 0 {
		t.Fatalf("claim B failed: code=%d stderr=%s", rB.Code, rB.Stderr)
	}
	var respB map[string]any
	if err := json.Unmarshal([]byte(rB.Stdout), &respB); err != nil {
		t.Fatalf("claim B json: %v\n%s", err, rB.Stdout)
	}
	if status, _ := respB["status"].(string); status != "warn" {
		t.Fatalf("claim B status=%q want warn (cross-worktree conflict missed)\nstdout=%s", status, rB.Stdout)
	}
	conflicts, _ := respB["conflicts"].([]any)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %v\nstdout=%s", conflicts, rB.Stdout)
	}

	// conflicts list also surfaces it for the orchestrator.
	rC := run(t, primary, env, "conflicts", "list", "--task", "CW-1", "--json")
	mustZero(t, "conflicts list", rC)
	var listResp map[string]any
	if err := json.Unmarshal([]byte(rC.Stdout), &listResp); err != nil {
		t.Fatalf("conflicts list json: %v\n%s", err, rC.Stdout)
	}
	cs, _ := listResp["conflicts"].([]any)
	if len(cs) != 1 {
		t.Fatalf("expected 1 conflict from list, got %v", cs)
	}
}

// fsCaseInsensitive returns true when dir lives on a case-insensitive
// filesystem (typical macOS APFS, Windows NTFS). Without this probe a
// case-sensitivity test on Linux tmpfs would falsely fail.
func fsCaseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	lower := filepath.Join(dir, "case_probe.txt")
	if err := os.WriteFile(lower, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(lower)
	upper := filepath.Join(dir, "CASE_PROBE.txt")
	if _, err := os.Stat(upper); err == nil {
		return true
	}
	return false
}

func TestCanonicalHash_FoldsCase_OnCaseInsensitiveFS(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess case-insensitive test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	if !fsCaseInsensitive(t, root) {
		t.Skip("filesystem is case-sensitive; nothing to verify on this platform/CI")
	}

	primary := filepath.Join(root, "primary")
	xdgState := filepath.Join(root, "xdg")
	env := []string{"XDG_STATE_HOME=" + xdgState}

	gitCmd(t, root, "init", primary)
	gitCmd(t, primary, "-C", primary, "config", "user.email", "test@test.invalid")
	gitCmd(t, primary, "-C", primary, "config", "user.name", "Test")
	writeFile(t, filepath.Join(primary, "README.md"), "# test\n")
	gitCmd(t, primary, "-C", primary, "add", ".")
	gitCmd(t, primary, "-C", primary, "commit", "-m", "init")

	writeFile(t, filepath.Join(primary, "Foo.go"), "x")

	mustZero(t, "init", run(t, primary, env, "init"))
	mustZero(t, "assign", run(t, primary, env,
		"assign", "--task", "CI-1",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.case",
		"--allow", "**", "--policy", "warn",
		"--reason", "case test",
	))

	r1 := run(t, primary, append([]string{"AGENT_ID=pi.worker.case"}, env...),
		"claim", "Foo.go",
		"--task", "CI-1",
		"--reason", "uppercase",
		"--json",
	)
	mustZero(t, "claim Foo.go", r1)

	r2 := run(t, primary, append([]string{"AGENT_ID=pi.worker.case"}, env...),
		"claim", "foo.go",
		"--task", "CI-1",
		"--reason", "lowercase",
		"--json",
	)
	if r2.Code != 0 {
		t.Fatalf("claim foo.go failed: %s", r2.Stderr)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(r2.Stdout), &resp); err != nil {
		t.Fatalf("claim json: %v\n%s", err, r2.Stdout)
	}
	if status, _ := resp["status"].(string); status != "warn" {
		t.Fatalf("status=%q want warn (case-insensitive collision missed)\nstdout=%s", status, r2.Stdout)
	}
	conflicts, _ := resp["conflicts"].([]any)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %v", conflicts)
	}
}

// TestMigrate_BackfillCommandReportsCounts confirms the CLI surface for
// the canonical-hash backfill: schema version + per-table counts.
func TestMigrate_BackfillCommandReportsCounts(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess migrate backfill test")
	}
	root := t.TempDir()
	xdgState := filepath.Join(root, "xdg")
	env := []string{"XDG_STATE_HOME=" + xdgState}

	mustZero(t, "init", run(t, root, env, "init"))
	r := run(t, root, env, "migrate")
	mustZero(t, "migrate", r)
	if !strings.Contains(r.Stdout, "schema_version=") || !strings.Contains(r.Stdout, "backfill_intent_paths=") {
		t.Fatalf("migrate stdout missing expected fields: %s", r.Stdout)
	}
}
