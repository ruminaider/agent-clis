package commands_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
)

// seedAssignForClose writes a single active assignment row and returns
// its assignment_id. Centralizing the seed keeps the close-specific
// tests focused on the close transition rather than re-asserting the
// assign surface.
func seedAssignForClose(t *testing.T, ledger, root, task, allow string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, allow), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, e := runCmd(t, ledger, nil,
		"assign", "--task", task,
		"--orchestrator", "pi.main", "--agent", "pi.worker",
		"--allow", allow, "--policy", "warn", "--reason", "seed for close",
		"--json",
	)
	if code != 0 {
		t.Fatalf("seed assign: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode seed assign: %v %s", err, out)
	}
	id, _ := resp["assignment_id"].(string)
	if id == "" {
		t.Fatalf("seed assign missing assignment_id: %s", out)
	}
	return id
}

// TestAssignClose_Completed verifies the happy path: an active
// assignment transitions to status=completed with closed_at set, an
// assignment.closed event is emitted, and the (task, agent) slot is
// freed for a fresh assign without an intervening update.
func TestAssignClose_Completed(t *testing.T) {
	root, ledger := tempLedger(t)
	asg := seedAssignForClose(t, ledger, root, "T-close", "foo.py")

	code, out, e := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.main"},
		"assign", "close", "--assignment", asg, "--reason", "scope satisfied", "--json")
	if code != 0 {
		t.Fatalf("assign close: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v %s", err, out)
	}
	if resp["assignment_id"] != asg {
		t.Errorf("assignment_id=%v, want %s", resp["assignment_id"], asg)
	}
	if resp["close_outcome"] != "completed" {
		t.Errorf("close_outcome=%v, want completed", resp["close_outcome"])
	}

	// Row state via the assignments query.
	code, out, e = runCmd(t, ledger, nil,
		"assignments", "--task", "T-close", "--status", "all", "--json")
	if code != 0 {
		t.Fatalf("assignments list: %d %s %s", code, out, e)
	}
	var list map[string]any
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("decode list: %v %s", err, out)
	}
	rows, _ := list["assignments"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 assignment row, got %d", len(rows))
	}
	row, _ := rows[0].(map[string]any)
	if row["status"] != "completed" {
		t.Errorf("status=%v, want completed", row["status"])
	}
	if meta, _ := row["metadata"].(map[string]any); meta != nil {
		if meta["close_outcome"] != "completed" {
			t.Errorf("metadata.close_outcome=%v, want completed", meta["close_outcome"])
		}
		if h, _ := meta["close_reason_sha256"].(string); len(h) != 64 {
			t.Errorf("metadata.close_reason_sha256 not a 64-char hash: %q", h)
		}
	}

	// The slot in the active-row unique index is free; a fresh assign
	// for the same (task, agent) pair succeeds without --if-absent
	// and without `assign update`.
	if code, _, e := runCmd(t, ledger, nil,
		"assign", "--task", "T-close",
		"--orchestrator", "pi.main", "--agent", "pi.worker",
		"--allow", "foo.py", "--policy", "warn", "--reason", "reopen after close",
	); code != 0 {
		t.Fatalf("reassign after close failed: %d %s", code, e)
	}
}

// TestAssignClose_Abandoned verifies the abandoned outcome path. The
// distinction matters for downstream gc and summary consumers, which
// key on metadata.close_outcome rather than on status alone.
func TestAssignClose_Abandoned(t *testing.T) {
	root, ledger := tempLedger(t)
	asg := seedAssignForClose(t, ledger, root, "T-aband", "bar.py")

	code, out, e := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.main"},
		"assign", "close", "--assignment", asg, "--outcome", "abandoned", "--json")
	if code != 0 {
		t.Fatalf("assign close abandoned: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode: %v %s", err, out)
	}
	if resp["close_outcome"] != "abandoned" {
		t.Errorf("close_outcome=%v, want abandoned", resp["close_outcome"])
	}
}

// TestAssignClose_Replay_NotActive verifies the second close returns
// ExitConflict (4) with code assignment_not_active rather than silently
// no-ooping. Replay-as-conflict is the contract that protects an
// orchestrator from believing a stale close completed work that a
// concurrent supersede had already redirected.
func TestAssignClose_Replay_NotActive(t *testing.T) {
	root, ledger := tempLedger(t)
	asg := seedAssignForClose(t, ledger, root, "T-replay", "baz.py")

	if code, _, e := runCmd(t, ledger, nil,
		"assign", "close", "--assignment", asg, "--json"); code != 0 {
		t.Fatalf("first close: %d %s", code, e)
	}
	code, out, e := runCmd(t, ledger, nil,
		"assign", "close", "--assignment", asg, "--json")
	if code != cli.ExitConflict {
		t.Fatalf("replay close expected exit %d, got %d: out=%s err=%s", cli.ExitConflict, code, out, e)
	}
	var errResp cli.Error
	if err := json.Unmarshal([]byte(e), &errResp); err != nil {
		t.Fatalf("stderr not JSON: %v %s", err, e)
	}
	if errResp.Code != "assignment_not_active" {
		t.Fatalf("code=%q, want assignment_not_active", errResp.Code)
	}
}

// TestAssignClose_NotFound verifies the missing-id path returns
// ExitNotFound (8) so a typo or a stale id surfaces distinctly from
// the replay-after-close case.
func TestAssignClose_NotFound(t *testing.T) {
	_, ledger := tempLedger(t)
	code, _, e := runCmd(t, ledger, nil,
		"assign", "close", "--assignment", "asg_DOES_NOT_EXIST", "--json")
	if code != cli.ExitNotFound {
		t.Fatalf("expected exit %d, got %d: %s", cli.ExitNotFound, code, e)
	}
	var errResp cli.Error
	if err := json.Unmarshal([]byte(e), &errResp); err != nil {
		t.Fatalf("stderr not JSON: %v %s", err, e)
	}
	if errResp.Code != "assignment_not_found" {
		t.Fatalf("code=%q, want assignment_not_found", errResp.Code)
	}
}

// TestAssignClose_RejectsSupersededOutcome verifies the CLI guard
// against outcome=superseded. SPEC §11.3.1 reserves supersede
// transitions for `assign update`, which inserts the replacement row
// in the same transaction so consumers can walk the chain via
// metadata.superseded_by. Allowing assign close to emit
// outcome=superseded without that replacement would strand the task.
func TestAssignClose_RejectsSupersededOutcome(t *testing.T) {
	root, ledger := tempLedger(t)
	asg := seedAssignForClose(t, ledger, root, "T-sup", "qux.py")

	code, _, e := runCmd(t, ledger, nil,
		"assign", "close", "--assignment", asg, "--outcome", "superseded", "--json")
	if code != cli.ExitUsage {
		t.Fatalf("expected exit %d, got %d: %s", cli.ExitUsage, code, e)
	}
	if !strings.Contains(e, "invalid_outcome") {
		t.Fatalf("expected invalid_outcome in stderr: %s", e)
	}
}

// TestAssignClose_LeavesActiveIntentsAlone verifies the lifecycle
// boundary: closing an assignment does not auto-close its outstanding
// intents. Worker-side intent lifecycle is independent of
// orchestrator-side assignment lifecycle; the worker still owns the
// terminal `agent-ledger close --intent` call.
func TestAssignClose_LeavesActiveIntentsAlone(t *testing.T) {
	root, ledger := tempLedger(t)
	asg := seedAssignForClose(t, ledger, root, "T-int", "live.py")

	// Open a worker intent under the assignment.
	code, out, e := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker"},
		"claim", "live.py", "--task", "T-int", "--reason", "edit", "--json")
	if code != 0 {
		t.Fatalf("claim: %d %s %s", code, out, e)
	}

	// Close the assignment.
	if code, _, e := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.main"},
		"assign", "close", "--assignment", asg, "--reason", "scope done"); code != 0 {
		t.Fatalf("assign close: %d %s", code, e)
	}

	// The intent must still be active. agent-ledger gc and aging are
	// the documented sweep paths for intents stranded under a closed
	// assignment; this command does not pre-empt them.
	code, out, _ = runCmd(t, ledger, nil, "status", "--json", "--task", "T-int")
	if code != 0 {
		t.Fatalf("status: %d", code)
	}
	var st map[string]any
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("status json: %v %s", err, out)
	}
	if active, ok := st["active_intents"].([]any); !ok || len(active) != 1 {
		t.Fatalf("expected 1 active intent surviving close, got %v", st["active_intents"])
	}
}
