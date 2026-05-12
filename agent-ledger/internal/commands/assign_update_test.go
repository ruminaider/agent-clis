package commands_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAssignUpdate_AddAllow_HappyPath verifies the round-trip of the
// additive-only assignment update: the latest active row carries the
// merged allowed_paths, and the prior row is marked superseded.
func TestAssignUpdate_AddAllow_HappyPath(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "foo.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, e := runCmd(t, ledger, nil,
		"assign", "--task", "T-upd",
		"--orchestrator", "pi.main", "--agent", "pi.worker",
		"--allow", "src/foo.py", "--policy", "warn",
		"--reason", "initial scope",
	); code != 0 {
		t.Fatalf("seed assign failed: %d %s %s", code, out, e)
	}

	code, out, e := runCmd(t, ledger, nil,
		"assign", "update", "--task", "T-upd", "--agent", "pi.worker",
		"--add-allow", "src/bar.py",
		"--reason", "extend for continuation",
		"--json",
	)
	if code != 0 {
		t.Fatalf("assign update failed: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode update json: %v %s", err, out)
	}
	if resp["changed"] != true {
		t.Errorf("expected changed=true, got %v", resp["changed"])
	}
	if resp["reused"] != false {
		t.Errorf("expected reused=false, got %v", resp["reused"])
	}
	if resp["prior_assignment_id"] == "" || resp["prior_assignment_id"] == nil {
		t.Errorf("missing prior_assignment_id: %s", out)
	}
	allowed, _ := resp["allowed_paths"].([]any)
	if len(allowed) != 2 || allowed[0] != "src/foo.py" || allowed[1] != "src/bar.py" {
		t.Errorf("allowed_paths=%v want [src/foo.py src/bar.py]", allowed)
	}

	// Both rows visible via the assignments query (status all).
	code, out, e = runCmd(t, ledger, nil,
		"assignments", "--task", "T-upd", "--status", "all", "--json")
	if code != 0 {
		t.Fatalf("assignments query: %d %s %s", code, out, e)
	}
	var listResp map[string]any
	if err := json.Unmarshal([]byte(out), &listResp); err != nil {
		t.Fatalf("decode list json: %v %s", err, out)
	}
	rows, _ := listResp["assignments"].([]any)
	if len(rows) != 2 {
		t.Fatalf("expected 2 assignments (active + superseded), got %d", len(rows))
	}
	var sawActive, sawSuperseded bool
	for _, r := range rows {
		row, _ := r.(map[string]any)
		switch row["status"] {
		case "active":
			sawActive = true
		case "superseded":
			sawSuperseded = true
			meta, _ := row["metadata"].(map[string]any)
			if meta["superseded_by"] == "" || meta["superseded_by"] == nil {
				t.Errorf("superseded row missing metadata.superseded_by: %v", meta)
			}
		}
	}
	if !sawActive || !sawSuperseded {
		t.Fatalf("expected one active + one superseded, got rows=%v", rows)
	}
}

// TestAssignUpdate_Idempotent verifies that re-running with the same
// flags produces no new row and no event.
func TestAssignUpdate_Idempotent(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "foo.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runCmd(t, ledger, nil,
		"assign", "--task", "T-noop",
		"--orchestrator", "pi.main", "--agent", "pi.worker",
		"--allow", "src/foo.py", "--allow", "src/bar.py",
		"--policy", "warn", "--reason", "initial",
	); code != 0 {
		t.Fatalf("seed: %d %s", code, e)
	}

	code, out, e := runCmd(t, ledger, nil,
		"assign", "update", "--task", "T-noop", "--agent", "pi.worker",
		"--add-allow", "src/foo.py", // already present
		"--reason", "ensure-shape script",
		"--json",
	)
	if code != 0 {
		t.Fatalf("noop update failed: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v %s", err, out)
	}
	if resp["changed"] != false || resp["reused"] != true {
		t.Fatalf("expected changed=false reused=true, got %v / %v", resp["changed"], resp["reused"])
	}

	// Only one row should exist (no supersede happened).
	code, out, e = runCmd(t, ledger, nil,
		"assignments", "--task", "T-noop", "--status", "all", "--json")
	if code != 0 {
		t.Fatalf("assignments list: %d %s %s", code, out, e)
	}
	var listResp map[string]any
	if err := json.Unmarshal([]byte(out), &listResp); err != nil {
		t.Fatalf("decode list json: %v %s", err, out)
	}
	rows, _ := listResp["assignments"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (no supersede on no-op), got %d: %s", len(rows), out)
	}
}

// TestAssignUpdate_WhitespaceOnlyAddAllow_UsageError verifies that
// `--add-allow "   "` is rejected at the CLI boundary with
// ExitUsage/invalid_flag instead of producing a misleading
// changed=true row whose globs trim to nothing.
func TestAssignUpdate_WhitespaceOnlyAddAllow_UsageError(t *testing.T) {
	_, ledger := tempLedger(t)
	code, _, e := runCmd(t, ledger, nil,
		"assign", "update", "--task", "T", "--agent", "w",
		"--add-allow", "   ",
		"--reason", "whitespace path",
	)
	if code != 2 {
		t.Fatalf("expected ExitUsage(2), got %d (stderr=%s)", code, e)
	}
	if !strings.Contains(e, "invalid_flag") {
		t.Errorf("expected invalid_flag in stderr, got %s", e)
	}
}

// TestAssignUpdate_NoFlags_UsageError verifies that omitting --add-allow
// is a usage error.
func TestAssignUpdate_NoFlags_UsageError(t *testing.T) {
	_, ledger := tempLedger(t)
	code, _, e := runCmd(t, ledger, nil,
		"assign", "update", "--task", "T", "--agent", "w", "--reason", "noop",
	)
	if code != 2 {
		t.Fatalf("expected ExitUsage(2), got %d (stderr=%s)", code, e)
	}
	if !strings.Contains(e, "missing_flag") {
		t.Errorf("expected missing_flag in stderr, got %s", e)
	}
}

// TestAssignUpdate_NoActiveAssignment verifies the typed conflict
// when no active row exists for the (task, agent) pair.
func TestAssignUpdate_NoActiveAssignment(t *testing.T) {
	_, ledger := tempLedger(t)
	code, _, e := runCmd(t, ledger, nil,
		"assign", "update", "--task", "no-such-task", "--agent", "no-such-agent",
		"--add-allow", "src/foo.py", "--reason", "expects no row",
	)
	if code != 4 {
		t.Fatalf("expected ExitConflict(4), got %d (stderr=%s)", code, e)
	}
	if !strings.Contains(e, "no_active_assignment") {
		t.Errorf("expected no_active_assignment in stderr, got %s", e)
	}
}

// TestAssignUpdate_UnsafeReason verifies the privacy guard rejects a
// reason containing a known secret pattern.
func TestAssignUpdate_UnsafeReason(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runCmd(t, ledger, nil,
		"assign", "--task", "T-priv",
		"--orchestrator", "pi.main", "--agent", "pi.worker",
		"--allow", "x.md", "--policy", "warn", "--reason", "ok",
	); code != 0 {
		t.Fatalf("seed: %d %s", code, e)
	}
	code, _, e := runCmd(t, ledger, nil,
		"assign", "update", "--task", "T-priv", "--agent", "pi.worker",
		"--add-allow", "y.md",
		"--reason", "ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA leaked here",
	)
	if code != 2 {
		t.Fatalf("expected ExitConfigError(2), got %d (stderr=%s)", code, e)
	}
	if !strings.Contains(e, "reason_unsafe") {
		t.Errorf("expected reason_unsafe in stderr, got %s", e)
	}
}

// TestAssignUpdate_TextOutput verifies the human-readable text format.
func TestAssignUpdate_TextOutput(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "foo.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runCmd(t, ledger, nil,
		"assign", "--task", "T-text",
		"--orchestrator", "pi.main", "--agent", "pi.worker",
		"--allow", "src/foo.py", "--policy", "warn", "--reason", "initial",
	); code != 0 {
		t.Fatalf("seed: %d %s", code, e)
	}

	code, out, e := runCmd(t, ledger, nil,
		"assign", "update", "--task", "T-text", "--agent", "pi.worker",
		"--add-allow", "src/bar.py", "--reason", "extend",
	)
	if code != 0 {
		t.Fatalf("update: %d %s %s", code, out, e)
	}
	if !strings.Contains(out, "changed=true") || !strings.Contains(out, "superseded=") {
		t.Errorf("text output missing expected fields: %q", out)
	}
}
