package commands_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

// TestScan_CleanLedgerExitsZero verifies that a fresh ledger with no
// corruption returns exit 0, schema=agent-ledger.scan.v1, and an
// empty issue list.
func TestScan_CleanLedgerExitsZero(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runCmd(t, ledger, nil, "assign", "--task", "T1", "--orchestrator", "op", "--agent", "worker", "--allow", "x.md", "--policy", "warn", "--reason", "test"); code != 0 {
		t.Fatalf("seed assign: %d %s", code, e)
	}
	code, out, e := runCmd(t, ledger, nil, "scan", "--json")
	if code != 0 {
		t.Fatalf("scan on clean ledger expected 0, got %d (out=%s err=%s)", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("json parse: %v %s", err, out)
	}
	if resp["schema"] != "agent-ledger.scan.v1" {
		t.Errorf("schema = %v, want agent-ledger.scan.v1", resp["schema"])
	}
	if int(resp["issue_count"].(float64)) != 0 {
		t.Errorf("issue_count = %v, want 0", resp["issue_count"])
	}
	if int(resp["row_total"].(float64)) < 1 {
		t.Errorf("row_total = %v, want at least 1 (the seeded assignment)", resp["row_total"])
	}
}

// TestScan_AggregatesAcrossTablesAndExitsThree seeds corruption in
// every JSON-bearing table the scanner walks, runs scan, and verifies
// that the report aggregates ALL issues (not just the first one) and
// that the command exits with ExitStorageIO=3.
func TestScan_AggregatesAcrossTablesAndExitsThree(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Seed an assignment, intent, change, validation, and conflict so
	// the scan has rows in each table to find corruption in.
	if code, _, e := runCmd(t, ledger, nil, "assign", "--task", "TC", "--orchestrator", "op", "--agent", "worker", "--allow", "x.md", "--policy", "warn", "--reason", "seed"); code != 0 {
		t.Fatalf("assign: %d %s", code, e)
	}
	cl := claimJSON(t, ledger, root, "x.md", "TC", "worker", "edit")
	intent := cl["intent_id"].(string)
	if code, _, e := runCmd(t, ledger, map[string]string{"AGENT_ID": "worker"}, "record", "x.md", "--intent", intent, "--summary", "edit"); code != 0 {
		t.Fatalf("record: %d %s", code, e)
	}

	// Corrupt one row in each interesting table.
	store, err := sqlite.Open(context.Background(), ledger)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.DB()
	corruptions := []string{
		`UPDATE assignments SET metadata_json = '{not json'`,
		`UPDATE assignments SET allowed_paths_json = '[1,2'`,
		`UPDATE intents SET metadata_json = 'null'`,
		`UPDATE changes SET metadata_json = '"a string is not an object"'`,
		`UPDATE events SET payload_json = '{partial' WHERE event_type = 'task.assigned'`,
	}
	for _, q := range corruptions {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("corrupt %q: %v", q, err)
		}
	}

	code, out, e := runCmd(t, ledger, nil, "scan", "--json")
	if code != 3 {
		t.Fatalf("scan on corrupt ledger expected ExitStorageIO(3), got %d (out=%s err=%s)", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	issues, _ := resp["issues"].([]any)
	if len(issues) < 5 {
		t.Fatalf("expected >=5 issues (one per corruption), got %d: %v", len(issues), issues)
	}
	// Verify each corruption shows up at least once. Indexed by
	// "<table>.<column>".
	want := map[string]bool{
		"assignments.metadata_json":      false,
		"assignments.allowed_paths_json": false,
		"intents.metadata_json":          false,
		"changes.metadata_json":          false,
		"events.payload_json":            false,
	}
	for _, raw := range issues {
		i := raw.(map[string]any)
		k := i["table"].(string) + "." + i["column"].(string)
		want[k] = true
	}
	for k, found := range want {
		if !found {
			t.Errorf("expected scan to report corruption in %s, did not", k)
		}
	}
}

// TestScan_TextOutputListsCorruptedRows verifies the human-friendly
// output mode includes per-row detail rather than just a count.
func TestScan_TextOutputListsCorruptedRows(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runCmd(t, ledger, nil, "assign", "--task", "TT", "--orchestrator", "op", "--agent", "worker", "--allow", "x.md", "--policy", "warn", "--reason", "seed"); code != 0 {
		t.Fatalf("assign: %d %s", code, e)
	}
	store, err := sqlite.Open(context.Background(), ledger)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().Exec(`UPDATE assignments SET metadata_json = '{not json'`); err != nil {
		t.Fatal(err)
	}

	code, out, _ := runCmd(t, ledger, nil, "scan")
	if code != 3 {
		t.Fatalf("expected exit 3, got %d", code)
	}
	if !strings.Contains(out, "assignments.metadata_json") {
		t.Errorf("expected text output to mention assignments.metadata_json: %s", out)
	}
	if !strings.Contains(out, "row=") {
		t.Errorf("expected text output to include row=<id>: %s", out)
	}
	if !strings.Contains(out, "rows examined") {
		t.Errorf("expected text output to include rows-examined summary: %s", out)
	}
}

// claimJSON is a small helper that runs `claim` with --json and
// returns the decoded response. Used by tests that need to drive
// the lifecycle past assignment.
func claimJSON(t *testing.T, ledger, root, path, task, agent, reason string) map[string]any {
	t.Helper()
	code, out, e := runCmd(t, ledger, map[string]string{"AGENT_ID": agent},
		"claim", path, "--task", task, "--reason", reason, "--json",
	)
	if code != 0 {
		t.Fatalf("claim: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("claim json: %v %s", err, out)
	}
	return resp
}
