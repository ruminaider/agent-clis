package commands_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
)

func TestClaimReportsCorruptAssignmentPaths(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errStr := runCmd(t, ledger, nil,
		"assign", "--task", "T-PATHS",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.test",
		"--allow", "README.md",
		"--policy", "warn",
		"--reason", "path corruption test",
		"--json",
	)
	if code != 0 {
		t.Fatalf("assign: %d %s %s", code, out, errStr)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("assign json: %v %s", err, out)
	}
	assignmentID, _ := resp["assignment_id"].(string)
	if assignmentID == "" {
		t.Fatalf("missing assignment_id in %s", out)
	}

	store := openTestStore(t, ledger)
	if _, err := store.DB().ExecContext(context.Background(),
		`UPDATE assignments SET forbidden_paths_json = ? WHERE assignment_id = ?`,
		`null`, assignmentID,
	); err != nil {
		store.Close()
		t.Fatal(err)
	}
	store.Close()

	code, _, errStr = runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.test"},
		"claim", "README.md", "--task", "T-PATHS", "--reason", "use")
	if code != cli.ExitStorageIO {
		t.Fatalf("expected storage exit, got %d: %s", code, errStr)
	}
	if !strings.Contains(errStr, "paths_corrupt") {
		t.Fatalf("expected paths_corrupt, got %s", errStr)
	}
	if !strings.Contains(errStr, "assignments.forbidden_paths_json") {
		t.Fatalf("expected forbidden_paths_json field, got %s", errStr)
	}
}

func TestClaimReportsCorruptOverrideConflictMetadata(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errStr := runCmd(t, ledger, nil,
		"assign", "--task", "T-OVERRIDE",
		"--orchestrator", "orch",
		"--agent", "worker",
		"--allow", "README.md",
		"--policy", "warn",
		"--reason", "override test",
	); code != 0 {
		t.Fatalf("assign: %d %s", code, errStr)
	}

	store := openTestStore(t, ledger)
	d := domain.New(store)
	conflict, err := d.InsertConflict(context.Background(), domain.Conflict{
		Path:             "README.md",
		PathHash:         "path-hash",
		ExistingIntentID: "intent-old",
		NewIntentID:      "intent-new",
		Policy:           domain.PolicyWarn,
		Status:           domain.ConflictDetected,
	})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := d.AcknowledgeConflict(context.Background(), conflict.ConflictID, "orch", "override", true, store.Clock()()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(context.Background(),
		`UPDATE conflicts SET metadata_json = ? WHERE conflict_id = ?`,
		`{"ok":true} trailing-junk`, conflict.ConflictID,
	); err != nil {
		store.Close()
		t.Fatal(err)
	}
	store.Close()

	code, _, errStr := runCmd(t, ledger, map[string]string{"AGENT_ID": "orch"},
		"claim", "README.md", "--task", "T-OVERRIDE", "--reason", "use", "--override-conflict", conflict.ConflictID)
	if code != cli.ExitStorageIO {
		t.Fatalf("expected storage exit, got %d: %s", code, errStr)
	}
	if !strings.Contains(errStr, "metadata_corrupt") {
		t.Fatalf("expected metadata_corrupt, got %s", errStr)
	}
	if !strings.Contains(errStr, "conflicts.metadata_json") {
		t.Fatalf("expected conflicts.metadata_json field, got %s", errStr)
	}
}

func TestStatusReportsCorruptIntentAndConflictMetadata(t *testing.T) {
	t.Run("intent lookup", func(t *testing.T) {
		root, ledger := tempLedger(t)
		if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}
		intentID := setupClaim(t, root, ledger, "T-STATUS-INTENT", "pi.worker.status", "doc.md")

		store := openTestStore(t, ledger)
		if _, err := store.DB().ExecContext(context.Background(),
			`UPDATE intents SET metadata_json = ? WHERE intent_id = ?`,
			`{"ok":true} trailing-junk`, intentID,
		); err != nil {
			store.Close()
			t.Fatal(err)
		}
		store.Close()

		code, _, errStr := runCmd(t, ledger, nil, "status", "--intent", intentID)
		if code != cli.ExitStorageIO {
			t.Fatalf("expected storage exit, got %d: %s", code, errStr)
		}
		if !strings.Contains(errStr, "metadata_corrupt") {
			t.Fatalf("expected metadata_corrupt, got %s", errStr)
		}
		if !strings.Contains(errStr, "intents.metadata_json") {
			t.Fatalf("expected intents.metadata_json field, got %s", errStr)
		}
	})

	t.Run("conflict list", func(t *testing.T) {
		_, ledger := tempLedger(t)
		store := openTestStore(t, ledger)
		d := domain.New(store)
		conflict, err := d.InsertConflict(context.Background(), domain.Conflict{
			Path:             "doc.md",
			PathHash:         "path-hash",
			ExistingIntentID: "intent-old",
			NewIntentID:      "intent-new",
			Policy:           domain.PolicyWarn,
			Status:           domain.ConflictDetected,
		})
		if err != nil {
			store.Close()
			t.Fatal(err)
		}
		if _, err := store.DB().ExecContext(context.Background(),
			`UPDATE conflicts SET metadata_json = ? WHERE conflict_id = ?`,
			`null`, conflict.ConflictID,
		); err != nil {
			store.Close()
			t.Fatal(err)
		}
		store.Close()

		code, _, errStr := runCmd(t, ledger, nil, "status")
		if code != cli.ExitStorageIO {
			t.Fatalf("expected storage exit, got %d: %s", code, errStr)
		}
		if !strings.Contains(errStr, "metadata_corrupt") {
			t.Fatalf("expected metadata_corrupt, got %s", errStr)
		}
		if !strings.Contains(errStr, "conflicts.metadata_json") {
			t.Fatalf("expected conflicts.metadata_json field, got %s", errStr)
		}
	})
}

func TestExportSummaryReportsCorruptChangeMetadata(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "sum.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	intentID := setupClaim(t, root, ledger, "T-SUM-CORRUPT", "pi.worker.sum", "src/sum.go")

	code, out, errStr := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.sum"},
		"record", "src/sum.go",
		"--intent", intentID,
		"--summary", "Summary build test",
		"--validation", "go test ./...:passed",
		"--json",
	)
	if code != 0 {
		t.Fatalf("record: %d %s %s", code, out, errStr)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("record json: %v %s", err, out)
	}
	changeID, _ := resp["change_id"].(string)
	if changeID == "" {
		t.Fatalf("missing change_id in %s", out)
	}

	store := openTestStore(t, ledger)
	if _, err := store.DB().ExecContext(context.Background(),
		`UPDATE changes SET metadata_json = ? WHERE change_id = ?`,
		`{"ok":true}{"other":false}`, changeID,
	); err != nil {
		store.Close()
		t.Fatal(err)
	}
	store.Close()

	outPath := filepath.Join(root, "tasks", "agent-ledger", "T-SUM-CORRUPT.json")
	code, _, errStr = runCmd(t, ledger, nil, "export-summary", "--task", "T-SUM-CORRUPT", "--output", outPath)
	if code != cli.ExitStorageIO {
		t.Fatalf("expected storage exit, got %d: %s", code, errStr)
	}
	if !strings.Contains(errStr, "metadata_corrupt") {
		t.Fatalf("expected metadata_corrupt, got %s", errStr)
	}
	if !strings.Contains(errStr, "changes.metadata_json") {
		t.Fatalf("expected changes.metadata_json field, got %s", errStr)
	}
}
