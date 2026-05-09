package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPointer_Missing(t *testing.T) {
	p, err := LoadPointer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Fatalf("expected nil pointer, got %+v", p)
	}
}

func TestLoadPointer_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	want := Pointer{Version: 1, ProjectID: "x/y", LedgerDir: "/tmp/foo", PolicyFile: ".agent-ledger-policy.toml"}
	if err := WritePointer(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPointer(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestLoadPointer_DefaultTaskID(t *testing.T) {
	dir := t.TempDir()
	body := `version = 1
project_id = "scratch/example"
ledger_dir = "/tmp/foo"
default_task_id = "exploration-2026-05"
`
	if err := os.WriteFile(filepath.Join(dir, PointerFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPointer(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected pointer")
	}
	if got.DefaultTaskID != "exploration-2026-05" {
		t.Fatalf("DefaultTaskID = %q, want %q", got.DefaultTaskID, "exploration-2026-05")
	}
}

func TestLoadPointer_DefaultTaskID_AbsentIsZero(t *testing.T) {
	dir := t.TempDir()
	body := `version = 1
project_id = "scratch/example"
ledger_dir = "/tmp/foo"
`
	if err := os.WriteFile(filepath.Join(dir, PointerFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPointer(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.DefaultTaskID != "" {
		t.Fatalf("expected empty DefaultTaskID, got %+v", got)
	}
}

func TestLoadPointer_DefaultTaskID_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	want := Pointer{Version: 1, ProjectID: "x/y", LedgerDir: "/tmp/foo", DefaultTaskID: "feature-z"}
	if err := WritePointer(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPointer(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestLoadPointer_BadVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, PointerFileName), []byte("version = 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPointer(dir); err == nil {
		t.Fatal("expected error for bad version")
	}
}

func TestLoadPolicy_Missing(t *testing.T) {
	pol, err := LoadPolicy(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if pol != nil {
		t.Fatalf("expected nil policy, got %+v", pol)
	}
}

func TestLoadPolicy_Defaults(t *testing.T) {
	dir := t.TempDir()
	body := `version = 1
[defaults]
conflict_policy = "warn"
heartbeat_seconds = 30
stale_after_seconds = 120
`
	if err := os.WriteFile(filepath.Join(dir, PolicyFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pol, err := LoadPolicy(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pol == nil || pol.Defaults.ConflictPolicy != "warn" || pol.Defaults.HeartbeatSeconds != 30 {
		t.Fatalf("unexpected: %+v", pol)
	}
}
