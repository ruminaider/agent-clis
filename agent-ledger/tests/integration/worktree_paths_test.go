package integration_test

// Tests for PR1 (worktree-aware path scope). When the cwd of agent-ledger
// is in checkout A, an absolute path inside sibling worktree B of the
// same repo must be accepted by `claim` and produce a display path
// relative to B's toplevel. Regression for the bug surfaced by
// pi-subagents worktree dispatch (issue: "agent-ledger refused claim").

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestClaim_AcceptsAbsolutePathInSiblingWorktree(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess worktree path test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	worktreeDir := filepath.Join(root, "linked")
	xdgState := filepath.Join(root, "xdg")
	env := []string{"XDG_STATE_HOME=" + xdgState}

	// Initialise the primary repo with one commit so worktree add works.
	gitCmd(t, root, "init", primary)
	gitCmd(t, primary, "-C", primary, "config", "user.email", "test@test.invalid")
	gitCmd(t, primary, "-C", primary, "config", "user.name", "Test")
	writeFile(t, filepath.Join(primary, "README.md"), "# test\n")
	gitCmd(t, primary, "-C", primary, "add", ".")
	gitCmd(t, primary, "-C", primary, "commit", "-m", "init")

	// Add a linked worktree.
	gitCmd(t, primary, "-C", primary, "worktree", "add", worktreeDir)

	// Create an apps/ subdir in the primary checkout (an arbitrary cwd
	// from which an orchestrator might run). Also place a target file
	// inside the linked worktree.
	writeFile(t, filepath.Join(primary, "apps", "marker.txt"), "x")
	writeFile(t, filepath.Join(worktreeDir, "apps", "worldbuilder", "tests", "conftest.py"), "x\n")

	// Init from the primary checkout. Resolves the shared ledger.
	mustZero(t, "init", run(t, primary, env, "init"))

	// Open an assignment from the primary cwd.
	mustZero(t, "assign", run(t, primary, env,
		"assign", "--task", "WT-1",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.wt",
		"--allow", "apps/**",
		"--policy", "warn",
		"--reason", "worktree path coverage",
	))

	// Claim from cwd=primary/apps using an absolute path that lives in
	// the linked worktree. This is the failure mode from the bug report.
	apps := filepath.Join(primary, "apps")
	target := filepath.Join(worktreeDir, "apps", "worldbuilder", "tests", "conftest.py")
	r := run(t, apps, append([]string{"AGENT_ID=pi.worker.wt"}, env...),
		"claim", target,
		"--task", "WT-1",
		"--reason", "absolute path inside sibling worktree",
		"--json",
	)
	if r.Code != 0 {
		t.Fatalf("claim failed: code=%d\nstdout=%s\nstderr=%s", r.Code, r.Stdout, r.Stderr)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &resp); err != nil {
		t.Fatalf("claim json: %v\n%s", err, r.Stdout)
	}
	gotPaths, _ := resp["paths"].([]any)
	if len(gotPaths) != 1 {
		t.Fatalf("expected 1 path in response, got %v", resp["paths"])
	}
	first, _ := gotPaths[0].(map[string]any)
	display, _ := first["path"].(string)
	if display != "apps/worldbuilder/tests/conftest.py" {
		t.Fatalf("display path %q, want apps/worldbuilder/tests/conftest.py", display)
	}
}

// TestClaim_RejectsPathOutsideAllWorktrees confirms the OutsideProject
// guard still fires when an absolute path escapes every worktree of the
// repo. This is the safety net that PR1 must not weaken.
func TestClaim_RejectsPathOutsideAllWorktrees(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess worktree path test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	stranger := filepath.Join(root, "stranger")
	xdgState := filepath.Join(root, "xdg")
	env := []string{"XDG_STATE_HOME=" + xdgState}

	gitCmd(t, root, "init", primary)
	gitCmd(t, primary, "-C", primary, "config", "user.email", "test@test.invalid")
	gitCmd(t, primary, "-C", primary, "config", "user.name", "Test")
	writeFile(t, filepath.Join(primary, "README.md"), "# test\n")
	gitCmd(t, primary, "-C", primary, "add", ".")
	gitCmd(t, primary, "-C", primary, "commit", "-m", "init")
	writeFile(t, filepath.Join(stranger, "x.txt"), "x")

	mustZero(t, "init", run(t, primary, env, "init"))
	mustZero(t, "assign", run(t, primary, env,
		"assign", "--task", "WT-2",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.wt",
		"--allow", "**",
		"--policy", "warn",
		"--reason", "scope check",
	))

	r := run(t, primary, append([]string{"AGENT_ID=pi.worker.wt"}, env...),
		"claim", filepath.Join(stranger, "x.txt"),
		"--task", "WT-2",
		"--reason", "should be rejected",
		"--json",
	)
	if r.Code == 0 {
		t.Fatalf("claim should have failed for out-of-project path; stdout=%s stderr=%s", r.Stdout, r.Stderr)
	}
	// --json sends the error envelope to stderr (see cmd/agent-ledger/main.go).
	var resp map[string]any
	if err := json.Unmarshal([]byte(r.Stderr), &resp); err != nil {
		t.Fatalf("error json: %v\nstderr=%s\nstdout=%s", err, r.Stderr, r.Stdout)
	}
	if code, _ := resp["code"].(string); code != "path_outside_project" {
		t.Fatalf("error code=%q want path_outside_project; resp=%s", code, r.Stderr)
	}
}
