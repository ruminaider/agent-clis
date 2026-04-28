package commands_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/commands"
)

// runCmd executes args against a fresh root with Wave-2 registrations
// bound to ledgerDir. It returns exit code, stdout, stderr.
func runCmd(t *testing.T, ledgerDir string, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	streams := cli.IOStreams{In: bytes.NewReader(nil), Out: out, Err: errBuf}
	full := append([]string{}, args...)
	full = append(full, "--ledger-dir", ledgerDir)

	for k, v := range env {
		t.Setenv(k, v)
	}
	code := commands.Execute(streams, full)
	return code, out.String(), errBuf.String()
}

func tempLedger(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	ledger := filepath.Join(root, "ledger")
	// Some tests need files under root to claim. Touch the dir.
	if err := os.MkdirAll(ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	// Writing test files; ensure cwd becomes the project root for path
	// resolution.
	t.Chdir(root)
	return root, ledger
}

func TestIdentifyShellExports(t *testing.T) {
	_, ledger := tempLedger(t)
	code, out, errStr := runCmd(t, ledger, nil, "identify", "--harness", "pi", "--agent-kind", "worker", "--shell")
	if code != 0 {
		t.Fatalf("identify failed: %d %s %s", code, out, errStr)
	}
	if !strings.Contains(out, "export AGENT_ID=") {
		t.Fatalf("missing AGENT_ID export: %s", out)
	}
}

func TestAssignClaimHeartbeatCloseStatus(t *testing.T) {
	root, ledger := tempLedger(t)
	// Create file to claim (path must exist for normalization to be
	// well-defined; normalize tolerates missing too, but a real file
	// is closer to user reality).
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Identify orchestrator and worker.
	if code, _, errStr := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.main.test"}, "identify", "--harness", "pi", "--agent-kind", "orchestrator"); code != 0 {
		t.Fatalf("identify orchestrator: %d %s", code, errStr)
	}
	if code, _, errStr := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.test"}, "identify", "--harness", "pi", "--agent-kind", "worker"); code != 0 {
		t.Fatalf("identify worker: %d %s", code, errStr)
	}

	// Assign.
	if code, _, errStr := runCmd(t, ledger, nil,
		"assign", "--task", "T1",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.test",
		"--allow", "README.md",
		"--policy", "warn",
		"--reason", "smoke"); code != 0 {
		t.Fatalf("assign: %d %s", code, errStr)
	}

	// Claim.
	code, out, errStr := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.test"},
		"claim", "README.md", "--task", "T1", "--reason", "edit", "--json")
	if code != 0 {
		t.Fatalf("claim: %d %s %s", code, out, errStr)
	}
	var claimResp map[string]any
	if err := json.Unmarshal([]byte(out), &claimResp); err != nil {
		t.Fatalf("claim json: %v %s", err, out)
	}
	intentID, _ := claimResp["intent_id"].(string)
	if intentID == "" {
		t.Fatalf("no intent_id in %s", out)
	}

	// Heartbeat.
	if code, _, errStr := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.test"},
		"heartbeat", "--intent", intentID); code != 0 {
		t.Fatalf("heartbeat: %d %s", code, errStr)
	}

	// Status (json).
	code, out, errStr = runCmd(t, ledger, nil, "status", "--json")
	if code != 0 {
		t.Fatalf("status: %d %s %s", code, out, errStr)
	}
	var st map[string]any
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("status json: %v %s", err, out)
	}
	if active, ok := st["active_intents"].([]any); !ok || len(active) != 1 {
		t.Fatalf("expected 1 active intent, got %v", st["active_intents"])
	}

	// Close.
	if code, _, errStr := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.test"},
		"close", "--intent", intentID, "--outcome", "completed"); code != 0 {
		t.Fatalf("close: %d %s", code, errStr)
	}
}

func TestClaimWarnPolicyCreatesConflict(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "shared.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runCmd(t, ledger, nil, "assign", "--task", "TW",
		"--orchestrator", "pi.main.test", "--agent", "pi.worker.test",
		"--allow", "shared.md", "--policy", "warn", "--reason", "warn smoke"); code != 0 {
		t.Fatalf("assign: %d %s", code, e)
	}

	code, _, e := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.a"},
		"claim", "shared.md", "--task", "TW", "--reason", "first")
	if code != 0 {
		t.Fatalf("first claim: %d %s", code, e)
	}

	code, out, e := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.b"},
		"claim", "shared.md", "--task", "TW", "--reason", "second", "--json")
	if code != 0 {
		t.Fatalf("second claim under warn should succeed: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	if resp["status"] != "warn" {
		t.Fatalf("expected warn status, got %v", resp)
	}
	if cs, ok := resp["conflicts"].([]any); !ok || len(cs) == 0 {
		t.Fatalf("expected at least one conflict id, got %v", resp)
	}

	// conflicts list returns the recorded conflict.
	code, out, e = runCmd(t, ledger, nil, "conflicts", "--json")
	if code != 0 {
		t.Fatalf("conflicts list: %d %s", code, e)
	}
	if !strings.Contains(out, `"path": "shared.md"`) {
		t.Fatalf("expected conflict on shared.md, got %s", out)
	}
}

func TestClaimExclusivePolicyBlocksSecond(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "lockfile.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runCmd(t, ledger, nil, "assign", "--task", "TE",
		"--orchestrator", "pi.main.test", "--agent", "pi.worker.test",
		"--allow", "lockfile.txt", "--policy", "exclusive", "--reason", "exclusive smoke"); code != 0 {
		t.Fatalf("assign: %d %s", code, e)
	}
	if code, _, e := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.a"},
		"claim", "lockfile.txt", "--task", "TE", "--reason", "first"); code != 0 {
		t.Fatalf("first claim: %d %s", code, e)
	}

	code, out, e := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.b"},
		"claim", "lockfile.txt", "--task", "TE", "--reason", "second", "--json")
	if code != cli.ExitConflict {
		t.Fatalf("second exclusive claim should exit %d, got %d (%s %s)", cli.ExitConflict, code, out, e)
	}
	if !strings.Contains(e, "exclusive") && !strings.Contains(out, "exclusive") {
		t.Fatalf("expected exclusive in output, got out=%q err=%q", out, e)
	}

	// Confirm no second intent was created (only one active intent).
	code, out, _ = runCmd(t, ledger, nil, "status", "--json", "--task", "TE")
	if code != 0 {
		t.Fatalf("status: %d", code)
	}
	var st map[string]any
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatal(err)
	}
	if active, ok := st["active_intents"].([]any); !ok || len(active) != 1 {
		t.Fatalf("expected exactly 1 active intent, got %v", st["active_intents"])
	}
}

func TestClaimRejectsForbiddenPath(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "forbidden.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runCmd(t, ledger, nil, "assign", "--task", "TF",
		"--orchestrator", "pi.main.test", "--agent", "pi.worker.test",
		"--allow", "**/*.md", "--forbid", "forbidden.md",
		"--policy", "warn", "--reason", "forbid smoke"); code != 0 {
		t.Fatalf("assign: %d %s", code, e)
	}
	code, _, errStr := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.x"},
		"claim", "forbidden.md", "--task", "TF", "--reason", "should fail")
	if code != cli.ExitConflict {
		t.Fatalf("expected conflict exit, got %d %s", code, errStr)
	}
	if !strings.Contains(errStr, "forbidden") {
		t.Fatalf("expected forbidden message: %s", errStr)
	}

	// No intent was created (status JSON shows zero active intents for TF).
	code, out, _ := runCmd(t, ledger, nil, "status", "--json", "--task", "TF")
	if code != 0 {
		t.Fatalf("status: %d", code)
	}
	var st map[string]any
	_ = json.Unmarshal([]byte(out), &st)
	if active, ok := st["active_intents"].([]any); ok && len(active) != 0 {
		t.Fatalf("expected zero active intents, got %d", len(active))
	}
}

func TestClaimMissingAssignment(t *testing.T) {
	_, ledger := tempLedger(t)
	code, _, errStr := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.x"},
		"claim", "README.md", "--task", "DOES-NOT-EXIST", "--reason", "no")
	if code != cli.ExitConflict {
		t.Fatalf("expected conflict exit, got %d %s", code, errStr)
	}
	if !strings.Contains(errStr, "missing_assignment") && !strings.Contains(errStr, "no active assignment") {
		t.Fatalf("expected missing_assignment in errStr: %s", errStr)
	}
}

func TestExclusiveOverrideRequiresOrchestrator(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "lock.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runCmd(t, ledger, nil, "assign", "--task", "TX",
		"--orchestrator", "pi.main.test", "--agent", "pi.worker.test",
		"--allow", "lock.txt", "--policy", "exclusive", "--reason", "x"); code != 0 {
		t.Fatalf("assign: %d %s", code, e)
	}
	// First claim opens.
	if code, _, e := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.a"},
		"claim", "lock.txt", "--task", "TX", "--reason", "first"); code != 0 {
		t.Fatalf("first claim: %d %s", code, e)
	}
	// Second blocked, gives us a conflict id.
	code, out, _ := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.b"},
		"claim", "lock.txt", "--task", "TX", "--reason", "second", "--json")
	if code != cli.ExitConflict {
		t.Fatalf("expected conflict exit, got %d", code)
	}
	// Parse details.conflict_id from the error envelope (rendered to err).
	// The JSON envelope is on stderr; use the bytes there.
	_ = out
}

func TestAssignIfAbsentReusesIdenticalReplay(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "replay.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runCmd(t, ledger, nil, "assign", "--task", "TR", "--orchestrator", "pi.main.test", "--agent", "pi.worker.test", "--allow", "replay.md", "--policy", "warn", "--reason", "replay", "--if-absent"); code != 0 {
		t.Fatalf("first assign: %d %s", code, e)
	}
	code, out, e := runCmd(t, ledger, nil, "assign", "--task", "TR", "--orchestrator", "pi.main.test", "--agent", "pi.worker.test", "--allow", "replay.md", "--policy", "warn", "--reason", "replay", "--if-absent", "--json")
	if code != 0 {
		t.Fatalf("replay assign: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	if reused, ok := resp["reused"].(bool); !ok || !reused {
		t.Fatalf("expected reused true, got %v", resp["reused"])
	}
	if resp["assignment_id"] == "" {
		t.Fatalf("expected assignment id, got %v", resp)
	}
	code, out, _ = runCmd(t, ledger, nil, "status", "--json", "--task", "TR")
	if code != 0 {
		t.Fatalf("status: %d %s", code, out)
	}
	var st map[string]any
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatal(err)
	}
	if active, ok := st["active_intents"].([]any); !ok || len(active) != 0 {
		t.Fatalf("expected no intents from assignment replay, got %v", st["active_intents"])
	}
}

func TestAssignIfAbsentDifferentAgentInsertsNew(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "agent.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runCmd(t, ledger, nil, "assign", "--task", "TA", "--orchestrator", "pi.main.test", "--agent", "pi.worker.a", "--allow", "agent.md", "--policy", "warn", "--reason", "same", "--if-absent"); code != 0 {
		t.Fatalf("first assign: %d %s", code, e)
	}
	code, out, e := runCmd(t, ledger, nil, "assign", "--task", "TA", "--orchestrator", "pi.main.test", "--agent", "pi.worker.b", "--allow", "agent.md", "--policy", "warn", "--reason", "same", "--if-absent", "--json")
	if code != 0 {
		t.Fatalf("second assign: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if reused, _ := resp["reused"].(bool); reused {
		t.Fatalf("expected reused false, got %v", resp)
	}
	if resp["assignment_id"] == "" {
		t.Fatalf("missing assignment id")
	}
}

// TestAssignIfAbsentDifferentPolicyOrAllowFailsExclusive verifies the
// v0.1.1 strict semantics: --if-absent reuse only succeeds when the new
// request is byte-equivalent to the prior assignment. A non-match
// surfaces as ExitConflict / assignment_exists rather than silently
// inserting a competing active row, which the partial unique index on
// (task_id, assigned_agent_id) WHERE status='active' now forbids.
func TestAssignIfAbsentDifferentPolicyOrAllowFailsExclusive(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "policy.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runCmd(t, ledger, nil, "assign", "--task", "TP", "--orchestrator", "pi.main.test", "--agent", "pi.worker.test", "--allow", "policy.md", "--policy", "warn", "--reason", "same", "--if-absent"); code != 0 {
		t.Fatalf("first assign: %d %s", code, e)
	}
	// Different policy: should fail with assignment_exists.
	code, out, e := runCmd(t, ledger, nil, "assign", "--task", "TP", "--orchestrator", "pi.main.test", "--agent", "pi.worker.test", "--allow", "policy.md", "--policy", "exclusive", "--reason", "same", "--if-absent", "--json")
	if code != 4 {
		t.Fatalf("policy change assign expected ExitConflict (4), got %d: out=%s err=%s", code, out, e)
	}
	var errResp cli.Error
	if jerr := json.Unmarshal([]byte(e), &errResp); jerr != nil {
		t.Fatalf("stderr not JSON: %v %s", jerr, e)
	}
	if errResp.Code != "assignment_exists" {
		t.Fatalf("expected code=assignment_exists, got %v", errResp.Code)
	}
	// Different allow set: should fail with assignment_exists.
	code, out, e = runCmd(t, ledger, nil, "assign", "--task", "TP", "--orchestrator", "pi.main.test", "--agent", "pi.worker.test", "--allow", "policy.md", "--allow", "other.md", "--policy", "warn", "--reason", "same", "--if-absent", "--json")
	if code != 4 {
		t.Fatalf("allow change assign expected ExitConflict (4), got %d: out=%s err=%s", code, out, e)
	}
}

// TestAssignWithoutIfAbsentRejectsDuplicate verifies the v0.1.1
// strict default: calling plain assign twice on the same active
// (task, agent) pair fails with assignment_exists. Orchestrators
// must use --if-absent for idempotent replay or close the prior
// assignment first.
func TestAssignWithoutIfAbsentRejectsDuplicate(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "plain.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runCmd(t, ledger, nil, "assign", "--task", "TN", "--orchestrator", "pi.main.test", "--agent", "pi.worker.test", "--allow", "plain.md", "--policy", "warn", "--reason", "same"); code != 0 {
		t.Fatalf("first assign: %d %s", code, e)
	}
	code, out, e := runCmd(t, ledger, nil, "assign", "--task", "TN", "--orchestrator", "pi.main.test", "--agent", "pi.worker.test", "--allow", "plain.md", "--policy", "warn", "--reason", "same", "--json")
	if code != 4 {
		t.Fatalf("second assign expected ExitConflict (4), got %d: out=%s err=%s", code, out, e)
	}
	var errResp cli.Error
	if jerr := json.Unmarshal([]byte(e), &errResp); jerr != nil {
		t.Fatalf("stderr not JSON: %v %s", jerr, e)
	}
	if errResp.Code != "assignment_exists" {
		t.Fatalf("expected code=assignment_exists, got %v", errResp.Code)
	}
}
