package commands_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

// TestMigrateStatusFlag verifies migrate --status reports schema_version
// and is read-only (idempotent: running twice still reports schema_version=1).
func TestMigrateStatusFlag(t *testing.T) {
	_, ledger := tempLedger(t)
	// Apply migrations once via the migrate command itself.
	code, out, errStr := runCmd(t, ledger, nil, "migrate")
	if code != 0 {
		t.Fatalf("migrate: %d %s %s", code, out, errStr)
	}
	if !strings.Contains(out, "schema_version=1") {
		t.Fatalf("expected schema_version=1, got %q", out)
	}
	// --status mode: read-only, JSON.
	code, out, _ = runCmd(t, ledger, nil, "migrate", "--status", "--json")
	if code != 0 {
		t.Fatalf("migrate --status: %d", code)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	if v, _ := resp["schema_version"].(float64); int(v) != 1 {
		t.Fatalf("schema_version = %v", resp["schema_version"])
	}
	applied, ok := resp["applied"].([]any)
	if !ok || len(applied) == 0 {
		t.Fatalf("expected applied list, got %v", resp["applied"])
	}
	if pending, _ := resp["pending"].([]any); len(pending) != 0 {
		t.Fatalf("expected zero pending, got %v", pending)
	}
}

// TestMigrateIdempotent runs migrate twice and confirms the second run
// is a no-op (still reports schema_version=1, no error).
func TestMigrateIdempotent(t *testing.T) {
	_, ledger := tempLedger(t)
	for i := 0; i < 2; i++ {
		code, _, errStr := runCmd(t, ledger, nil, "migrate")
		if code != 0 {
			t.Fatalf("migrate iter %d: %d %s", i, code, errStr)
		}
	}
}

// TestGCMarksStaleIntents seeds a stale intent directly in the DB,
// runs `gc --stale-after=1h`, and confirms the intent is orphaned.
func TestGCMarksStaleIntents(t *testing.T) {
	_, ledger := tempLedger(t)
	// Migrate first.
	if code, _, e := runCmd(t, ledger, nil, "migrate"); code != 0 {
		t.Fatalf("migrate: %d %s", code, e)
	}
	// Seed a stale intent: opened 48h ago, no heartbeat.
	store, err := sqlite.Open(context.Background(), ledger)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	staleTS := now.Add(-48 * time.Hour).Format("2006-01-02T15:04:05.000Z07:00")
	if _, err := store.DB().ExecContext(context.Background(),
		`INSERT INTO agents(agent_id, agent_kind, started_at) VALUES('agent.test','worker',?)`,
		staleTS); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(context.Background(),
		`INSERT INTO events(event_id, schema, event_type, created_at, agent_id, task_id, payload_json)
		 VALUES('evt_stale','agent-ledger.v1','intent.opened',?,?,?, '{"intent_id":"int_stale"}')`,
		staleTS, "agent.test", "T1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(context.Background(),
		`INSERT INTO intents(intent_id,event_id,task_id,agent_id,access_mode,conflict_policy,reason,status,opened_at)
		 VALUES('int_stale','evt_stale','T1','agent.test','write','warn','stale-test','active',?)`,
		staleTS); err != nil {
		t.Fatal(err)
	}
	store.Close()

	// gc --stale-after 1h --json
	code, out, errStr := runCmd(t, ledger, nil, "gc", "--stale-after", "1h", "--json")
	if code != 0 {
		t.Fatalf("gc: %d %s %s", code, out, errStr)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	orphaned, _ := resp["orphaned"].([]any)
	if len(orphaned) != 1 || orphaned[0] != "int_stale" {
		t.Fatalf("orphaned = %v", resp["orphaned"])
	}

	// Second run is idempotent.
	code, out, _ = runCmd(t, ledger, nil, "gc", "--stale-after", "1h", "--json")
	if code != 0 {
		t.Fatalf("gc second run: %d", code)
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if c, _ := resp["candidates"].(float64); int(c) != 0 {
		t.Fatalf("expected 0 candidates on second run, got %v", resp["candidates"])
	}
}

func TestGCRejectsInvalidDuration(t *testing.T) {
	_, ledger := tempLedger(t)
	code, _, errStr := runCmd(t, ledger, nil, "gc", "--stale-after", "garbage")
	if code == 0 {
		t.Fatalf("expected non-zero exit for bad duration, got 0")
	}
	if !strings.Contains(errStr, "invalid_duration") && !strings.Contains(errStr, "duration") {
		t.Fatalf("expected duration error, got %s", errStr)
	}
}

func TestGCMissingDuration(t *testing.T) {
	_, ledger := tempLedger(t)
	code, _, errStr := runCmd(t, ledger, nil, "gc")
	if code == 0 {
		t.Fatalf("expected non-zero exit when --stale-after missing")
	}
	if !strings.Contains(errStr, "stale-after") {
		t.Fatalf("expected stale-after in error: %s", errStr)
	}
}

// TestDoctorJSONShape exercises the doctor command end to end and
// confirms the JSON schema, overall, and checks fields.
func TestDoctorJSONShape(t *testing.T) {
	_, ledger := tempLedger(t)
	if code, _, e := runCmd(t, ledger, nil, "migrate"); code != 0 {
		t.Fatalf("migrate: %d %s", code, e)
	}
	code, out, _ := runCmd(t, ledger, nil, "doctor", "--json")
	if code != 0 {
		t.Fatalf("doctor: %d %s", code, out)
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v %s", err, out)
	}
	if rep["schema"] != "agent-ledger.doctor.v1" {
		t.Fatalf("schema = %v", rep["schema"])
	}
	if _, ok := rep["overall"]; !ok {
		t.Fatalf("missing overall: %s", out)
	}
	checks, ok := rep["checks"].([]any)
	if !ok || len(checks) == 0 {
		t.Fatalf("missing checks: %s", out)
	}
}

// TestDoctorReportsBrokenPolicy stages an invalid policy file and
// expects doctor to exit non-zero with an actionable status.
func TestDoctorReportsBrokenPolicy(t *testing.T) {
	root, ledger := tempLedger(t)
	if code, _, e := runCmd(t, ledger, nil, "migrate"); code != 0 {
		t.Fatalf("migrate: %d %s", code, e)
	}
	if err := os.WriteFile(filepath.Join(root, ".agent-ledger-policy.toml"),
		[]byte("not = valid = toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ := runCmd(t, ledger, nil, "doctor", "--json")
	if code == 0 {
		t.Fatalf("expected non-zero exit for invalid policy")
	}
}
