package commands_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAssignMetadataRoundTrip verifies that --metadata is accepted as
// JSON, merged with the existing branch metadata, and surfaced in the
// assignment row's metadata_json column.
func TestAssignMetadataRoundTrip(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := `{"auto_assigned":true,"auto_assigned_by":"pi-extension","source":"branch"}`
	code, out, e := runCmd(t, ledger, nil,
		"assign", "--task", "TM",
		"--orchestrator", "pi.main", "--agent", "pi.worker",
		"--allow", "x.md", "--policy", "warn",
		"--reason", "metadata test", "--metadata", meta,
		"--branch", "feature/x",
	)
	if code != 0 {
		t.Fatalf("assign with metadata: %d %s %s", code, out, e)
	}
	// Pull back the assignment via the assignments command and confirm
	// metadata round-tripped exactly.
	code, out, e = runCmd(t, ledger, nil, "assignments", "--task", "TM", "--json")
	if code != 0 {
		t.Fatalf("assignments query: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	rows, _ := resp["assignments"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(rows))
	}
	row, _ := rows[0].(map[string]any)
	gotMeta, _ := row["metadata"].(map[string]any)
	if gotMeta["auto_assigned"] != true {
		t.Errorf("metadata.auto_assigned = %v, want true", gotMeta["auto_assigned"])
	}
	if gotMeta["auto_assigned_by"] != "pi-extension" {
		t.Errorf("metadata.auto_assigned_by = %v, want pi-extension", gotMeta["auto_assigned_by"])
	}
	if gotMeta["source"] != "branch" {
		t.Errorf("metadata.source = %v, want branch", gotMeta["source"])
	}
	// Branch field from --branch flag should also be preserved.
	if gotMeta["branch"] != "feature/x" {
		t.Errorf("metadata.branch = %v, want feature/x", gotMeta["branch"])
	}
}

// TestAssignMetadataRejectsArrayTopLevel verifies parseMetadataFlag
// rejects non-object JSON payloads with ExitUsage.
func TestAssignMetadataRejectsArrayTopLevel(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, e := runCmd(t, ledger, nil,
		"assign", "--task", "TBAD",
		"--orchestrator", "pi.main", "--agent", "pi.worker",
		"--allow", "x.md", "--policy", "warn",
		"--reason", "bad meta", "--metadata", `["not","an","object"]`,
	)
	if code != 2 {
		t.Fatalf("expected ExitUsage(2) for array metadata, got %d (stderr=%s)", code, e)
	}
	if !strings.Contains(e, "invalid_metadata") {
		t.Errorf("expected invalid_metadata in stderr, got %s", e)
	}
}

// TestAssignMetadataRejectsMalformedJSON verifies parseMetadataFlag
// rejects unparseable JSON with ExitUsage.
func TestAssignMetadataRejectsMalformedJSON(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, e := runCmd(t, ledger, nil,
		"assign", "--task", "TBAD2",
		"--orchestrator", "pi.main", "--agent", "pi.worker",
		"--allow", "x.md", "--policy", "warn",
		"--reason", "bad meta", "--metadata", `{not json`,
	)
	if code != 2 {
		t.Fatalf("expected ExitUsage(2) for malformed metadata, got %d (stderr=%s)", code, e)
	}
}

// TestAssignmentsCommandFiltersAndMarkers exercises the assignments
// query command end-to-end with several filter combinations. Verifies
// the reason_marker classifier picks the right kind for explicit,
// auto-assigned, and harness-derived rows.
func TestAssignmentsCommandFiltersAndMarkers(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Explicit (no marker prefix in reason).
	if code, _, e := runCmd(t, ledger, nil,
		"assign", "--task", "T-explicit",
		"--orchestrator", "pi.main", "--agent", "pi.worker",
		"--allow", "a.md", "--policy", "warn",
		"--reason", "explicit ticket reference",
	); code != 0 {
		t.Fatalf("explicit assign: %d %s", code, e)
	}
	// Harness-derived (reason starts with [harness-derived ...]).
	if code, _, e := runCmd(t, ledger, nil,
		"assign", "--task", "T-harness",
		"--orchestrator", "pi.adapter", "--agent", "pi.worker",
		"--allow", "a.md", "--policy", "warn",
		"--reason", "[harness-derived by pi-adapter source=branch task=feature-x] derived",
	); code != 0 {
		t.Fatalf("harness assign: %d %s", code, e)
	}
	// Auto-assigned (reason starts with [auto-assigned ...]).
	if code, _, e := runCmd(t, ledger, nil,
		"assign", "--task", "T-auto",
		"--orchestrator", "pi.adapter", "--agent", "pi.worker",
		"--allow", "a.md", "--policy", "warn",
		"--reason", "[auto-assigned by pi-adapter auto-derived task=auto/x/y] auto-fallback",
	); code != 0 {
		t.Fatalf("auto assign: %d %s", code, e)
	}

	// Filter by orchestrator should return 2 rows (pi.adapter).
	code, out, e := runCmd(t, ledger, nil, "assignments", "--orchestrator", "pi.adapter", "--json")
	if code != 0 {
		t.Fatalf("orchestrator filter: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	if int(resp["count"].(float64)) != 2 {
		t.Fatalf("orchestrator filter count = %v, want 2", resp["count"])
	}

	// Filter by task should return 1 row.
	code, out, _ = runCmd(t, ledger, nil, "assignments", "--task", "T-explicit", "--json")
	if code != 0 {
		t.Fatalf("task filter: %d", code)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	rows, _ := resp["assignments"].([]any)
	if len(rows) != 1 {
		t.Fatalf("task filter count = %d, want 1", len(rows))
	}
	row := rows[0].(map[string]any)
	if row["reason_marker"] != "explicit" {
		t.Errorf("explicit task reason_marker = %v, want explicit", row["reason_marker"])
	}

	// Verify marker classification on the auto and harness rows.
	for _, kind := range []string{"auto", "harness-derived"} {
		taskID := "T-" + kind
		if kind == "harness-derived" {
			taskID = "T-harness"
		}
		code, out, _ = runCmd(t, ledger, nil, "assignments", "--task", taskID, "--json")
		if code != 0 {
			t.Fatalf("%s filter: %d", kind, code)
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("%s json: %v", kind, err)
		}
		rows = resp["assignments"].([]any)
		if len(rows) != 1 {
			t.Fatalf("%s row count = %d, want 1", kind, len(rows))
		}
		got := rows[0].(map[string]any)["reason_marker"]
		if got != kind {
			t.Errorf("%s reason_marker = %v, want %v", kind, got, kind)
		}
	}
}

// TestAssignmentsCommandRejectsBadStatusFlag confirms the --status
// validator rejects unknown values.
func TestAssignmentsCommandRejectsBadStatusFlag(t *testing.T) {
	_, ledger := tempLedger(t)
	code, _, e := runCmd(t, ledger, nil, "assignments", "--status", "garbage")
	if code != 2 {
		t.Fatalf("expected ExitUsage(2), got %d (stderr=%s)", code, e)
	}
	if !strings.Contains(e, "invalid_status") {
		t.Errorf("expected invalid_status in stderr, got %s", e)
	}
}
