package integration_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// verifyTopLevelKeys is the canonical key set the Phase 1 verify
// contract emits at the top level. Tests assert at least these keys
// (extras OK but missing keys are a regression).
var verifyTopLevelKeys = []string{
	"schema",
	"status",
	"mode",
	"generated_at",
	"summary",
	"findings",
}

var verifyCountsKeys = []string{
	"changed_paths",
	"claimed_paths",
	"unclaimed_paths",
	"forbidden_path_violations",
	"outside_assignment_paths",
	"active_conflicts",
	"open_intents",
	"stale_intents",
	"findings",
}

var summaryTopLevelKeys = []string{
	"schema",
	"generated_at",
	"project",
	"task",
	"agent",
	"assignment_snapshot",
	"assignment_hash",
	"changed_paths",
	"changes",
	"validations",
	"closed",
}

var assignmentSnapshotKeys = []string{
	"task_id",
	"allowed_paths",
	"forbidden_paths",
	"conflict_policy",
}

func assertHasKeys(t *testing.T, label string, m map[string]any, keys []string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("%s missing key %q (got keys %v)", label, k, mapKeys(m))
		}
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestSchema_VerifyJSON exercises the full verify flow and asserts
// the contract from SPEC §19: schema string, status enum, mode, and
// every counts field.
func TestSchema_VerifyJSON(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	root := t.TempDir()
	ledger := freshLedger(t)
	writeFile(t, filepath.Join(root, "src", "a.go"), "package src\n")

	mustZero(t, "assign", run(t, root, nil,
		"assign", "--task", "TV",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.v",
		"--allow", "src/**",
		"--policy", "warn",
		"--reason", "schema check",
		"--ledger-dir", ledger,
	))
	cl := run(t, root, []string{"AGENT_ID=pi.worker.v"},
		"claim", "src/a.go", "--task", "TV", "--reason", "edit", "--json",
		"--ledger-dir", ledger,
	)
	mustZero(t, "claim", cl)
	var resp map[string]any
	json.Unmarshal([]byte(cl.Stdout), &resp)
	intentID, _ := resp["intent_id"].(string)
	mustZero(t, "record", run(t, root, []string{"AGENT_ID=pi.worker.v"},
		"record", "src/a.go", "--intent", intentID, "--summary", "tweak",
		"--ledger-dir", ledger,
	))
	mustZero(t, "close", run(t, root, []string{"AGENT_ID=pi.worker.v"},
		"close", "--intent", intentID, "--outcome", "completed",
		"--ledger-dir", ledger,
	))

	v := run(t, root, nil, "verify", "--task", "TV", "--json",
		"--ledger-dir", ledger,
	)
	if v.Code != 0 {
		t.Fatalf("verify exit %d\nstdout=%s\nstderr=%s", v.Code, v.Stdout, v.Stderr)
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(v.Stdout), &rep); err != nil {
		t.Fatalf("verify json: %v\n%s", err, v.Stdout)
	}
	if rep["schema"] != "agent-ledger.verify.v1" {
		t.Fatalf("schema = %v", rep["schema"])
	}
	allowedStatus := map[string]bool{
		"passed": true, "failed": true, "config_error": true,
		"storage_error": true, "conflict": true,
	}
	if !allowedStatus[asStr(rep["status"])] {
		t.Fatalf("unexpected status %v", rep["status"])
	}
	allowedMode := map[string]bool{"project": true, "task": true, "summary": true}
	if !allowedMode[asStr(rep["mode"])] {
		t.Fatalf("unexpected mode %v", rep["mode"])
	}
	assertHasKeys(t, "verify report", rep, verifyTopLevelKeys)
	sumObj, _ := rep["summary"].(map[string]any)
	if sumObj == nil {
		t.Fatalf("summary missing")
	}
	counts, _ := sumObj["counts"].(map[string]any)
	if counts == nil {
		t.Fatalf("counts missing")
	}
	assertHasKeys(t, "verify counts", counts, verifyCountsKeys)
	for _, k := range verifyCountsKeys {
		if _, ok := counts[k].(float64); !ok {
			t.Errorf("counts.%s should be number, got %T", k, counts[k])
		}
	}
	if _, ok := rep["findings"].([]any); !ok {
		t.Fatalf("findings should be array, got %T", rep["findings"])
	}
}

// TestSchema_SummaryJSON exports a per-task summary and asserts the
// stable agent-ledger-summary.v1 schema and assignment_snapshot
// contents.
func TestSchema_SummaryJSON(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	root := t.TempDir()
	ledger := freshLedger(t)
	writeFile(t, filepath.Join(root, "src", "b.go"), "package src\n")

	mustZero(t, "assign", run(t, root, nil,
		"assign", "--task", "TQ",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.q",
		"--allow", "src/**",
		"--forbid", "src/secrets/**",
		"--policy", "exclusive",
		"--reason", "schema check",
		"--ledger-dir", ledger,
	))
	cl := run(t, root, []string{"AGENT_ID=pi.worker.q"},
		"claim", "src/b.go", "--task", "TQ", "--reason", "edit", "--json",
		"--ledger-dir", ledger,
	)
	mustZero(t, "claim", cl)
	var resp map[string]any
	json.Unmarshal([]byte(cl.Stdout), &resp)
	intentID, _ := resp["intent_id"].(string)
	mustZero(t, "record", run(t, root, []string{"AGENT_ID=pi.worker.q"},
		"record", "src/b.go", "--intent", intentID, "--summary", "tweak",
		"--validation", "go test ./...:passed",
		"--ledger-dir", ledger,
	))
	mustZero(t, "close", run(t, root, []string{"AGENT_ID=pi.worker.q"},
		"close", "--intent", intentID, "--outcome", "completed",
		"--ledger-dir", ledger,
	))
	out := filepath.Join(root, "summary-TQ.json")
	mustZero(t, "export-summary", run(t, root, nil,
		"export-summary", "--task", "TQ", "--output", out,
		"--ledger-dir", ledger,
	))

	raw, err := readAll(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("summary not JSON: %v\n%s", err, raw)
	}
	if doc["schema"] != "agent-ledger-summary.v1" {
		t.Fatalf("schema = %v", doc["schema"])
	}
	assertHasKeys(t, "summary doc", doc, summaryTopLevelKeys)
	snap, _ := doc["assignment_snapshot"].(map[string]any)
	if snap == nil {
		t.Fatal("assignment_snapshot missing")
	}
	assertHasKeys(t, "assignment_snapshot", snap, assignmentSnapshotKeys)
	if snap["conflict_policy"] != "exclusive" {
		t.Errorf("conflict_policy=%v", snap["conflict_policy"])
	}
	allowed, _ := snap["allowed_paths"].([]any)
	if len(allowed) == 0 {
		t.Errorf("allowed_paths empty")
	}
	forbidden, _ := snap["forbidden_paths"].([]any)
	if len(forbidden) == 0 {
		t.Errorf("forbidden_paths empty")
	}
	proj, _ := doc["project"].(map[string]any)
	if proj == nil || proj["fingerprint"] == "" {
		t.Errorf("project.fingerprint missing")
	}
	if doc["assignment_hash"] == nil {
		t.Errorf("assignment_hash missing")
	}
	cp, _ := doc["changed_paths"].([]any)
	if len(cp) != 1 {
		t.Errorf("changed_paths len=%d want 1", len(cp))
	}
	v, _ := doc["validations"].([]any)
	if len(v) != 1 {
		t.Errorf("validations len=%d want 1", len(v))
	}

	// Run verify --summary back against the same project root. The
	// path_hash field binds to the realpath of each changed file, so
	// the same root reproduces the hash deterministically. SPEC
	// §20.1 supports cross-checkout summary verification but the
	// Phase 1 path-hash binding (sha256 of realpath) is sensitive to
	// the on-disk root; cross-machine summary replay is tracked
	// separately.
	writeFile(t, filepath.Join(root, "summary.json"), string(raw))
	v2 := run(t, root, nil, "verify", "--summary", "summary.json", "--json",
		"--ledger-dir", ledger,
	)
	if v2.Code != 0 {
		t.Fatalf("verify --summary expected 0 same-root, got %d\nstdout=%s\nstderr=%s",
			v2.Code, v2.Stdout, v2.Stderr)
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(v2.Stdout), &rep); err != nil {
		t.Fatal(err)
	}
	if rep["mode"] != "summary" {
		t.Errorf("mode=%v", rep["mode"])
	}
	if rep["status"] != "passed" {
		t.Errorf("status=%v", rep["status"])
	}
}
