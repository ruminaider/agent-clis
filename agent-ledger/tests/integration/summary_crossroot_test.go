package integration_test

// TestSummaryCrossRoot exercises SPEC §20.1 and §32 acceptance: "Exported
// summary verification in a clean checkout". A summary is produced in
// project dir A, then verify --summary is run against the same summary
// from a separate temp directory that shares the same project files but
// lives at a completely different absolute path. The path_hash in the
// summary must be the portable form (sha256 of NFC-normalized relative
// path) so that the recomputed hash matches regardless of checkout root.
//
// Finding: wv1-f03 (summary-path-hash-portability), packet R-003.

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSummaryCrossRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess cross-root summary test")
	}

	// --- Dir A: the originating checkout. --------------------------------

	dirA := t.TempDir()
	ledgerA := freshLedger(t)

	// Create source files that will be tracked.
	writeFile(t, filepath.Join(dirA, "src", "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(dirA, "src", "util.go"), "package main\n\nfunc helper() {}\n")

	// Initialise the ledger in dir A.
	mustZero(t, "init A", run(t, dirA, nil,
		"init", "--ledger-dir", ledgerA,
	))

	// Create an assignment.
	mustZero(t, "assign", run(t, dirA, nil,
		"assign",
		"--task", "cross-root-task",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.x",
		"--allow", "src/**",
		"--policy", "warn",
		"--reason", "cross-root portability test",
		"--ledger-dir", ledgerA,
	))

	// Claim both files.
	cl := run(t, dirA, []string{"AGENT_ID=pi.worker.x"},
		"claim", "src/main.go", "src/util.go",
		"--task", "cross-root-task",
		"--reason", "implementing feature",
		"--json",
		"--ledger-dir", ledgerA,
	)
	mustZero(t, "claim", cl)
	var claimResp map[string]any
	if err := json.Unmarshal([]byte(cl.Stdout), &claimResp); err != nil {
		t.Fatalf("claim JSON: %v\n%s", err, cl.Stdout)
	}
	intentID, _ := claimResp["intent_id"].(string)
	if intentID == "" {
		t.Fatalf("claim response missing intent_id: %s", cl.Stdout)
	}

	// Record both changed paths.
	mustZero(t, "record", run(t, dirA, []string{"AGENT_ID=pi.worker.x"},
		"record", "src/main.go", "src/util.go",
		"--intent", intentID,
		"--summary", "initial implementation",
		"--ledger-dir", ledgerA,
	))

	// Close the intent.
	mustZero(t, "close", run(t, dirA, []string{"AGENT_ID=pi.worker.x"},
		"close",
		"--intent", intentID,
		"--outcome", "completed",
		"--ledger-dir", ledgerA,
	))

	// Export summary into dir A.
	summaryPath := filepath.Join(dirA, "summary-cross-root-task.json")
	mustZero(t, "export-summary", run(t, dirA, nil,
		"export-summary",
		"--task", "cross-root-task",
		"--output", summaryPath,
		"--ledger-dir", ledgerA,
	))

	summaryRaw, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}

	// --- Dir B: a fresh checkout at a different absolute path. -----------

	dirB := t.TempDir()

	// Copy the project files to dir B so they exist at the new path.
	if err := copyDir(t, filepath.Join(dirA, "src"), filepath.Join(dirB, "src")); err != nil {
		t.Fatalf("copy src to dir B: %v", err)
	}

	// Place the summary file in dir B.
	summaryB := filepath.Join(dirB, "summary-cross-root-task.json")
	if err := os.WriteFile(summaryB, summaryRaw, 0o644); err != nil {
		t.Fatalf("write summary in dir B: %v", err)
	}

	// Run verify --summary from dir B. No ledger DB is needed (mode=summary).
	// This is the cross-root acceptance test from SPEC §32.
	ledgerB := freshLedger(t)
	result := run(t, dirB, nil,
		"verify",
		"--summary", "summary-cross-root-task.json",
		"--json",
		"--ledger-dir", ledgerB,
	)
	if result.Code != 0 {
		t.Fatalf("verify --summary in fresh checkout (dir B) failed with code %d\nstdout=%s\nstderr=%s",
			result.Code, result.Stdout, result.Stderr)
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(result.Stdout), &rep); err != nil {
		t.Fatalf("verify output not JSON: %v\n%s", err, result.Stdout)
	}
	if rep["mode"] != "summary" {
		t.Errorf("mode = %v, want summary", rep["mode"])
	}
	if rep["status"] != "passed" {
		t.Errorf("status = %v, want passed\nfindings: %v", rep["status"], rep["findings"])
	}

	// Also assert that dir A (same root) still passes, to confirm backward
	// compatibility with the same-root case.
	ledgerA2 := freshLedger(t)
	sameRoot := run(t, dirA, nil,
		"verify",
		"--summary", "summary-cross-root-task.json",
		"--json",
		"--ledger-dir", ledgerA2,
	)
	if sameRoot.Code != 0 {
		t.Fatalf("verify --summary in original dir A failed: code=%d\nstdout=%s\nstderr=%s",
			sameRoot.Code, sameRoot.Stdout, sameRoot.Stderr)
	}
	var repA map[string]any
	if err := json.Unmarshal([]byte(sameRoot.Stdout), &repA); err != nil {
		t.Fatalf("verify (dir A) output not JSON: %v", err)
	}
	if repA["status"] != "passed" {
		t.Errorf("same-root status = %v, want passed", repA["status"])
	}
}

// copyDir recursively copies src into dst, creating dst and all
// intermediate directories as needed.
func copyDir(t *testing.T, src, dst string) error {
	t.Helper()
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target)
	})
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
