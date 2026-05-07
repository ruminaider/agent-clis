package commands

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

// runAssignCmd runs an agent-ledger command against a per-test
// ledger directory. It mirrors the runCmd helper used by the
// external commands_test package so the subagent regression cases
// below stay close in style to existing assignment tests, while
// keeping this file in package commands for direct access to
// assign internals.
func runAssignCmd(t *testing.T, ledgerDir string, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	streams := cli.IOStreams{In: bytes.NewReader(nil), Out: out, Err: errBuf}
	full := append([]string{}, args...)
	full = append(full, "--ledger-dir", ledgerDir)
	for k, v := range env {
		t.Setenv(k, v)
	}
	code := Execute(streams, full)
	return code, out.String(), errBuf.String()
}

// tempAssignLedger sets up a per-test root directory, ledger
// subdirectory, and chdirs into root so claim path resolution is
// well-defined. The cases below only need a single touchable file
// under root, which the caller writes after this helper returns.
func tempAssignLedger(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	ledger := filepath.Join(root, "ledger")
	if err := os.MkdirAll(ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root, ledger
}

// readAssignmentMetadataJSON returns the raw metadata_json column
// for the active assignment row matching taskID. Reading the raw
// column is the only way to assert that a JSON number stored in
// metadata round-trips as an unquoted integer literal rather than a
// quoted string. The decoded form would erase that distinction
// because both shapes deserialize into Go strings under json.Number.
func readAssignmentMetadataJSON(t *testing.T, ledgerDir, taskID string) string {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Clean(ledgerDir))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Migrator().Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var raw string
	if err := store.DB().QueryRowContext(context.Background(),
		`SELECT metadata_json FROM assignments WHERE task_id = ? AND status = 'active'`,
		taskID,
	).Scan(&raw); err != nil {
		t.Fatalf("select metadata_json: %v", err)
	}
	return raw
}

// countActiveAssignments returns the number of active assignment
// rows for taskID. The --if-absent idempotency test pins this at 1
// after a duplicate replay.
func countActiveAssignments(t *testing.T, ledgerDir, taskID string) int {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Clean(ledgerDir))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Migrator().Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var n int
	if err := store.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM assignments WHERE task_id = ? AND status = 'active'`,
		taskID,
	).Scan(&n); err != nil {
		t.Fatalf("count assignments: %v", err)
	}
	return n
}

func TestRecoverIfAbsentAssignmentBranchesOnLookupErrors(t *testing.T) {
	wanted := domain.Assignment{TaskID: "task-1", AssignedAgentID: "agent-1", OrchestratorID: "orch", ConflictPolicy: domain.PolicyWarn, Reason: "same"}
	lookup := func(context.Context, string, string) (domain.Assignment, error) {
		return domain.Assignment{}, errors.New("lookup blew up")
	}

	_, _, err := recoverIfAbsentAssignment(context.Background(), lookup, wanted, wanted.TaskID, wanted.AssignedAgentID)
	if err == nil {
		t.Fatal("expected lookup error")
	}
	ce, ok := err.(*cli.Error)
	if !ok {
		t.Fatalf("expected *cli.Error, got %T", err)
	}
	if ce.ExitCode != cli.ExitStorageIO || ce.Code != "assign_lookup_failed" {
		t.Fatalf("expected assign_lookup_failed storage error, got %+v", ce)
	}

	lookup = func(context.Context, string, string) (domain.Assignment, error) {
		return domain.Assignment{}, sql.ErrNoRows
	}
	_, _, err = recoverIfAbsentAssignment(context.Background(), lookup, wanted, wanted.TaskID, wanted.AssignedAgentID)
	if err == nil {
		t.Fatal("expected conflict error")
	}
	ce, ok = err.(*cli.Error)
	if !ok {
		t.Fatalf("expected *cli.Error, got %T", err)
	}
	if ce.ExitCode != cli.ExitConflict || ce.Code != "assignment_exists" {
		t.Fatalf("expected assignment_exists conflict, got %+v", ce)
	}
}

// TestAssignAcceptsDistinctOrchestratorAndChildAgent pins the
// kernel contract that the subagent bootstrap relies on:
// --orchestrator and --agent are independent fields, so a child
// can record its own assignment with the parent agent id as
// orchestrator and the derived child agent id as the assigned
// worker without overloading either column. The audit confirmed
// assign.go upserts the two ids separately and never reads
// AGENT_ID, so this test pins that observed shape against
// regressions.
func TestAssignAcceptsDistinctOrchestratorAndChildAgent(t *testing.T) {
	root, ledger := tempAssignLedger(t)
	if err := os.WriteFile(filepath.Join(root, "child.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	const (
		parentTask = "parent-task-1"
		childTask  = "parent-task-1/scout/run-7-0"
		parentID   = "agent:pi:main:parent-1"
		childID    = "agent:pi:subagent:run-7:0"
	)
	code, out, e := runAssignCmd(t, ledger, nil,
		"assign", "--task", childTask,
		"--orchestrator", parentID, "--agent", childID,
		"--allow", "child.md", "--policy", "warn",
		"--reason", "[harness-derived by pi-adapter source=subagent parent_task="+parentTask+"] subagent child",
		"--metadata", `{"parent_task":"`+parentTask+`","dispatch_origin":"pi-subagent-bootstrap"}`,
		"--if-absent",
	)
	if code != 0 {
		t.Fatalf("assign: code=%d out=%s err=%s", code, out, e)
	}

	code, out, e = runAssignCmd(t, ledger, nil, "assignments", "--task", childTask, "--json")
	if code != 0 {
		t.Fatalf("assignments: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	rows, _ := resp["assignments"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(rows))
	}
	row := rows[0].(map[string]any)
	if row["orchestrator_id"] != parentID {
		t.Errorf("orchestrator_id = %v, want %s", row["orchestrator_id"], parentID)
	}
	if row["assigned_agent"] != childID {
		t.Errorf("assigned_agent = %v, want %s", row["assigned_agent"], childID)
	}
	if row["orchestrator_id"] == row["assigned_agent"] {
		t.Fatalf("expected distinct orchestrator and assigned agent ids, got both = %v", row["orchestrator_id"])
	}
}

// TestAssignSubagentMetadataPayloadPreservesIntegerChildIndex pins
// the locked decision 7 schema for subagent-created child rows:
// six required fields with subagent_child_index serialized as a
// JSON number, not a quoted string, and dispatch_origin as the
// literal "pi-subagent-bootstrap" discriminator the verify command
// reads to suppress AUTO_ASSIGNED_TASK warnings.
func TestAssignSubagentMetadataPayloadPreservesIntegerChildIndex(t *testing.T) {
	root, ledger := tempAssignLedger(t)
	if err := os.WriteFile(filepath.Join(root, "meta.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	const (
		parentTask = "parent-task-meta"
		childTask  = "parent-task-meta/planner/run-9-3"
		parentID   = "agent:pi:main:meta-1"
		childID    = "agent:pi:subagent:run-9:3"
	)
	meta := `{` +
		`"parent_task":"` + parentTask + `",` +
		`"parent_agent_id":"` + parentID + `",` +
		`"subagent_run_id":"run-9",` +
		`"subagent_child_index":3,` +
		`"subagent_child_agent":"planner",` +
		`"dispatch_origin":"pi-subagent-bootstrap"` +
		`}`
	code, out, e := runAssignCmd(t, ledger, nil,
		"assign", "--task", childTask,
		"--orchestrator", parentID, "--agent", childID,
		"--allow", "meta.md", "--policy", "warn",
		"--reason", "[harness-derived by pi-adapter source=subagent parent_task="+parentTask+"] subagent child",
		"--metadata", meta,
		"--if-absent",
	)
	if code != 0 {
		t.Fatalf("assign: code=%d out=%s err=%s", code, out, e)
	}

	// Read the raw metadata_json column. Confirms the integer is
	// stored as an unquoted JSON number.
	raw := readAssignmentMetadataJSON(t, ledger, childTask)
	if !strings.Contains(raw, `"subagent_child_index":3`) {
		t.Errorf("raw metadata_json should contain unquoted subagent_child_index:3, got %s", raw)
	}
	if strings.Contains(raw, `"subagent_child_index":"3"`) {
		t.Errorf("raw metadata_json must not quote subagent_child_index, got %s", raw)
	}
	if !strings.Contains(raw, `"dispatch_origin":"pi-subagent-bootstrap"`) {
		t.Errorf("raw metadata_json missing dispatch_origin discriminator, got %s", raw)
	}

	// Round-trip through assignments --json. Decode with UseNumber
	// so the test asserts the stored shape kept the integer kind.
	code, out, e = runAssignCmd(t, ledger, nil, "assignments", "--task", childTask, "--json")
	if code != 0 {
		t.Fatalf("assignments: %d %s %s", code, out, e)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	dec.UseNumber()
	var resp map[string]any
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	rows, _ := resp["assignments"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(rows))
	}
	gotMeta, _ := rows[0].(map[string]any)["metadata"].(map[string]any)
	if gotMeta == nil {
		t.Fatalf("missing metadata in assignments JSON: %s", out)
	}
	for _, key := range []string{
		"parent_task", "parent_agent_id", "subagent_run_id",
		"subagent_child_index", "subagent_child_agent", "dispatch_origin",
	} {
		if _, ok := gotMeta[key]; !ok {
			t.Errorf("metadata missing required field %q: %v", key, gotMeta)
		}
	}
	num, ok := gotMeta["subagent_child_index"].(json.Number)
	if !ok {
		t.Fatalf("subagent_child_index = %T (%v), want json.Number", gotMeta["subagent_child_index"], gotMeta["subagent_child_index"])
	}
	i, err := num.Int64()
	if err != nil {
		t.Fatalf("subagent_child_index not an integer: %v (raw %q)", err, num.String())
	}
	if i != 3 {
		t.Errorf("subagent_child_index = %d, want 3", i)
	}
	if gotMeta["dispatch_origin"] != "pi-subagent-bootstrap" {
		t.Errorf("dispatch_origin = %v, want pi-subagent-bootstrap", gotMeta["dispatch_origin"])
	}
}

// TestAssignIfAbsentReusesSubagentRowOnRespawn pins the
// deterministic-id reuse policy for subagent children: the same
// child task id, child agent id, run id, and child index produce a
// byte-equivalent assign --if-absent call, and the second call
// must reuse the existing row rather than insert a duplicate. This
// is what makes pi-subagents internal retries safe under the
// option-d design.
func TestAssignIfAbsentReusesSubagentRowOnRespawn(t *testing.T) {
	root, ledger := tempAssignLedger(t)
	if err := os.WriteFile(filepath.Join(root, "respawn.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	const (
		parentTask = "parent-task-respawn"
		childTask  = "parent-task-respawn/worker/run-12-1"
		parentID   = "agent:pi:main:respawn"
		childID    = "agent:pi:subagent:run-12:1"
	)
	meta := `{` +
		`"parent_task":"` + parentTask + `",` +
		`"parent_agent_id":"` + parentID + `",` +
		`"subagent_run_id":"run-12",` +
		`"subagent_child_index":1,` +
		`"subagent_child_agent":"worker",` +
		`"dispatch_origin":"pi-subagent-bootstrap"` +
		`}`
	args := []string{
		"assign", "--task", childTask,
		"--orchestrator", parentID, "--agent", childID,
		"--allow", "respawn.md", "--policy", "warn",
		"--reason", "[harness-derived by pi-adapter source=subagent parent_task=" + parentTask + "] subagent child",
		"--metadata", meta,
		"--if-absent",
	}

	code, out, e := runAssignCmd(t, ledger, nil, append(args, "--json")...)
	if code != 0 {
		t.Fatalf("first assign: %d %s %s", code, out, e)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(out), &first); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	if reused, _ := first["reused"].(bool); reused {
		t.Fatalf("first call must not be reused, got %v", first)
	}
	firstID, _ := first["assignment_id"].(string)
	if firstID == "" {
		t.Fatalf("first call missing assignment_id: %v", first)
	}
	rawBefore := readAssignmentMetadataJSON(t, ledger, childTask)

	// Replay the identical command. The deterministic child id
	// guarantees the second invocation has a byte-equivalent
	// payload, so --if-absent must be a no-op.
	code, out, e = runAssignCmd(t, ledger, nil, append(args, "--json")...)
	if code != 0 {
		t.Fatalf("replay assign: %d %s %s", code, out, e)
	}
	var second map[string]any
	if err := json.Unmarshal([]byte(out), &second); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	if reused, _ := second["reused"].(bool); !reused {
		t.Fatalf("replay must report reused=true, got %v", second)
	}
	if second["assignment_id"] != firstID {
		t.Fatalf("replay assignment_id = %v, want %s", second["assignment_id"], firstID)
	}

	if n := countActiveAssignments(t, ledger, childTask); n != 1 {
		t.Fatalf("expected exactly 1 active assignment after replay, got %d", n)
	}
	rawAfter := readAssignmentMetadataJSON(t, ledger, childTask)
	if rawBefore != rawAfter {
		t.Errorf("replay altered metadata_json:\nbefore=%s\nafter =%s", rawBefore, rawAfter)
	}
}

// TestAssignDoesNotConflateCallerAgentIDWithOrchestrator pins the
// audit finding that assign.go reads no caller-identity input: it
// has no AGENT_ID lookup, no implicit orchestrator inference, and
// no field that could overwrite --orchestrator from process state.
// A bootstrapping child sets AGENT_ID to its own derived id; the
// resulting assignment row must still record --orchestrator as the
// inherited parent agent. If a future change ever adds a caller
// path that overrides --orchestrator, this test will fail.
func TestAssignDoesNotConflateCallerAgentIDWithOrchestrator(t *testing.T) {
	root, ledger := tempAssignLedger(t)
	if err := os.WriteFile(filepath.Join(root, "caller.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	const (
		childTask = "parent-task-caller/worker/run-3-0"
		parentID  = "agent:pi:main:caller"
		childID   = "agent:pi:subagent:run-3:0"
	)
	// Simulate the child-side environment: AGENT_ID is the child's
	// freshly derived id, while --orchestrator carries the
	// inherited parent agent id.
	code, out, e := runAssignCmd(t, ledger, map[string]string{"AGENT_ID": childID},
		"assign", "--task", childTask,
		"--orchestrator", parentID, "--agent", childID,
		"--allow", "caller.md", "--policy", "warn",
		"--reason", "[harness-derived by pi-adapter source=subagent parent_task=parent-task-caller] subagent child",
		"--metadata", `{"dispatch_origin":"pi-subagent-bootstrap"}`,
		"--if-absent",
	)
	if code != 0 {
		t.Fatalf("assign: code=%d out=%s err=%s", code, out, e)
	}

	code, out, e = runAssignCmd(t, ledger, nil, "assignments", "--task", childTask, "--json")
	if code != 0 {
		t.Fatalf("assignments: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	rows, _ := resp["assignments"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(rows))
	}
	row := rows[0].(map[string]any)
	if row["orchestrator_id"] != parentID {
		t.Errorf("orchestrator_id = %v, want parent %s (caller AGENT_ID must not overwrite it)",
			row["orchestrator_id"], parentID)
	}
	if row["assigned_agent"] != childID {
		t.Errorf("assigned_agent = %v, want %s", row["assigned_agent"], childID)
	}
}
