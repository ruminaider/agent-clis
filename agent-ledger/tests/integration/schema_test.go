package integration_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/verify"
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

// verifySummaryKeys are the SPEC §19.2 canonical summary keys, plus
// the additive fields the implementation documents on top.
var verifySummaryKeys = []string{
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

// verifyCanonicalCodes is the SPEC §19.3 finding-code registry
// snapshotted into the integration suite. Adding or removing entries
// from SPEC §19.3 must update this list in lock-step. Drift here
// fails fast so future code rename regressions cannot ship silently.
var verifyCanonicalCodes = []string{
	"UNCLAIMED_CHANGE",
	"FORBIDDEN_PATH_CHANGED",
	"PATH_OUTSIDE_ASSIGNMENT",
	"ACTIVE_CONFLICT",
	"STALE_INTENT",
	"OPEN_INTENT",
	"MISSING_REASON",
	"MISSING_ASSIGNMENT",
	"AGENT_MISMATCH",
	"REVIEW_ONLY_WRITE",
	"EXCLUSIVE_LOCK_HELD",
	"SUMMARY_MISMATCH",
	"SYMLINK_ALIAS",
	"CONFIG_ERROR",
	"STORAGE_ERROR",
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
	// SPEC §19.2: passed | failed | needs-decision | error.
	allowedStatus := map[string]bool{
		"passed": true, "failed": true,
		"needs-decision": true, "error": true,
	}
	if !allowedStatus[asStr(rep["status"])] {
		t.Fatalf("unexpected status %v", rep["status"])
	}
	allowedMode := map[string]bool{"project": true, "task": true, "summary": true}
	if !allowedMode[asStr(rep["mode"])] {
		t.Fatalf("unexpected mode %v", rep["mode"])
	}
	assertHasKeys(t, "verify report", rep, verifyTopLevelKeys)
	// SPEC §19.2: summary is flat. The legacy {"counts": {...}}
	// wrapper is gone; counts live directly on summary.
	sumObj, _ := rep["summary"].(map[string]any)
	if sumObj == nil {
		t.Fatalf("summary missing")
	}
	if _, present := sumObj["counts"]; present {
		t.Fatalf("summary.counts wrapper must be removed (SPEC §19.2)")
	}
	assertHasKeys(t, "verify summary", sumObj, verifySummaryKeys)
	for _, k := range verifySummaryKeys {
		if _, ok := sumObj[k].(float64); !ok {
			t.Errorf("summary.%s should be number, got %T", k, sumObj[k])
		}
	}
	if _, ok := rep["findings"].([]any); !ok {
		t.Fatalf("findings should be array, got %T", rep["findings"])
	}
}

// TestSchema_VerifyCodeRegistry snapshots the SPEC §19.3 finding-code
// list and asserts the in-tree verify package exposes exactly that
// set as exported Code* constants. Adding a SPEC code without wiring
// it (or renaming an in-tree constant away from SPEC) fails this
// test, providing the tripwire required by remediation packet R-002
// (finding wv1-f04).
func TestSchema_VerifyCodeRegistry(t *testing.T) {
	codes := map[string]bool{
		verify.CodeUnclaimedChange:       true,
		verify.CodeForbiddenPathChanged:  true,
		verify.CodePathOutsideAssignment: true,
		verify.CodeActiveConflict:        true,
		verify.CodeStaleIntent:           true,
		verify.CodeOpenIntent:            true,
		verify.CodeMissingReason:         true,
		verify.CodeMissingAssignment:     true,
		verify.CodeAgentMismatch:         true,
		verify.CodeReviewOnlyWrite:       true,
		verify.CodeExclusiveLockHeld:     true,
		verify.CodeSummaryMismatch:       true,
		verify.CodeSymlinkAlias:          true,
		verify.CodeConfigError:           true,
		verify.CodeStorageError:          true,
	}
	if len(codes) != len(verifyCanonicalCodes) {
		t.Fatalf("verify package exports %d codes, SPEC §19.3 lists %d",
			len(codes), len(verifyCanonicalCodes))
	}
	for _, want := range verifyCanonicalCodes {
		if !codes[want] {
			t.Errorf("SPEC §19.3 code %q is not exported by internal/verify", want)
		}
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

	// Run verify --summary back against the same project root. Since
	// R-003 (e120d9a) path_hash is sha256(NFC(project-relative path
	// with forward slashes)), making it stable across checkouts and
	// machines. The same root reproduces the hash deterministically
	// because the relative path is invariant. SPEC §20.1, §32.
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
