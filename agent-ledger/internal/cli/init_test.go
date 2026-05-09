package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/config"
)

// TestInit_DefaultTaskID_RequiresWritePointer asserts that the usage
// error fires before EnsureLayout, so a misuse leaves no ledger
// directory behind on the operator's disk.
func TestInit_DefaultTaskID_RequiresWritePointer(t *testing.T) {
	wd := t.TempDir()
	chdir(t, wd)
	t.Setenv(`AGENT_LEDGER_DIR`, ``)
	t.Setenv(`XDG_STATE_HOME`, ``)
	ledgerDir := filepath.Join(t.TempDir(), `ledger`)

	streams, _, _ := newTestStreams()
	code := Execute(streams, []string{
		`init`,
		`--ledger-dir`, ledgerDir,
		`--default-task-id`, `should-fail`,
	})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}

	for _, sub := range []string{"audit", "blobs", "locks"} {
		p := filepath.Join(ledgerDir, sub)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("usage error created %s (err=%v); want side-effect-free validation", p, err)
		}
	}
}

// TestInit_WritePointer_CarriesForwardDefaultTaskID asserts that
// rerunning `init --write-pointer` without --default-task-id preserves
// an existing default_task_id in the pointer file. Without this carry-
// forward, a rerun (e.g. to refresh project_id) would silently erase
// the ambient task id used by the adapter session bootstrap.
func TestInit_WritePointer_CarriesForwardDefaultTaskID(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	t.Setenv(`AGENT_LEDGER_DIR`, ``)
	t.Setenv(`XDG_STATE_HOME`, ``)
	ledgerDir := filepath.Join(t.TempDir(), `ledger`)

	// Seed an existing pointer with a default_task_id.
	seed := config.Pointer{
		Version:       config.PointerVersion,
		ProjectID:     "scratch/example",
		LedgerDir:     ledgerDir,
		DefaultTaskID: "ambient-2026-05",
	}
	if err := config.WritePointer(root, seed); err != nil {
		t.Fatalf("seed pointer: %v", err)
	}

	streams, _, _ := newTestStreams()
	code := Execute(streams, []string{
		`init`,
		`--ledger-dir`, ledgerDir,
		`--project-id`, `scratch/example`,
		`--write-pointer`,
	})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}

	got, err := config.LoadPointer(root)
	if err != nil {
		t.Fatalf("LoadPointer: %v", err)
	}
	if got == nil {
		t.Fatalf("LoadPointer returned nil pointer")
	}
	if got.DefaultTaskID != "ambient-2026-05" {
		t.Errorf("DefaultTaskID = %q, want %q (carry-forward dropped)", got.DefaultTaskID, "ambient-2026-05")
	}
}

// TestInit_WritePointer_OverridesDefaultTaskID asserts that an explicit
// --default-task-id on a rerun overrides a previously persisted value.
func TestInit_WritePointer_OverridesDefaultTaskID(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	t.Setenv(`AGENT_LEDGER_DIR`, ``)
	t.Setenv(`XDG_STATE_HOME`, ``)
	ledgerDir := filepath.Join(t.TempDir(), `ledger`)

	seed := config.Pointer{
		Version:       config.PointerVersion,
		ProjectID:     "scratch/example",
		LedgerDir:     ledgerDir,
		DefaultTaskID: "old-task",
	}
	if err := config.WritePointer(root, seed); err != nil {
		t.Fatalf("seed pointer: %v", err)
	}

	streams, _, _ := newTestStreams()
	code := Execute(streams, []string{
		`init`,
		`--ledger-dir`, ledgerDir,
		`--project-id`, `scratch/example`,
		`--write-pointer`,
		`--default-task-id`, `new-task`,
	})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}

	got, err := config.LoadPointer(root)
	if err != nil {
		t.Fatalf("LoadPointer: %v", err)
	}
	if got == nil {
		t.Fatalf("LoadPointer returned nil pointer")
	}
	if got.DefaultTaskID != "new-task" {
		t.Errorf("DefaultTaskID = %q, want %q (explicit override dropped)", got.DefaultTaskID, "new-task")
	}
}
