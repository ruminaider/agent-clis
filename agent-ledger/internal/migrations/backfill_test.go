package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/migrations"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/paths"
)

// openTestDB returns a freshly migrated SQLite database.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrations.Apply(context.Background(), db, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return db
}

func seedAgent(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO agents(agent_id, agent_kind, started_at) VALUES('a', 'worker', '2026-01-01T00:00:00.000Z')`); err != nil {
		t.Fatal(err)
	}
}

func seedActiveIntent(t *testing.T, db *sql.DB, intentID, eventID, path, pathHash string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO intents(intent_id, event_id, task_id, agent_id, access_mode, conflict_policy, reason, status, opened_at)
		VALUES(?, ?, 't', 'a', 'write', 'warn', 'r', 'active', '2026-01-01T00:00:00.000Z')
	`, intentID, eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO intent_paths(intent_id, path, realpath, path_hash, access_mode)
		VALUES(?, ?, ?, ?, 'write')
	`, intentID, path, "/abs/"+path, pathHash); err != nil {
		t.Fatal(err)
	}
}

func closeIntent(t *testing.T, db *sql.DB, intentID string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE intents SET status = 'closed', closed_at = '2026-01-02T00:00:00.000Z' WHERE intent_id = ?`, intentID); err != nil {
		t.Fatal(err)
	}
}

// TestBackfill_RefusesWithActiveIntents reproduces the safety gate from
// SPEC §14 #8: a backfill while intents are active risks orphaning lock
// sentinels, so the function must refuse without --force.
func TestBackfill_RefusesWithActiveIntents(t *testing.T) {
	db := openTestDB(t)
	seedAgent(t, db)
	seedActiveIntent(t, db, "int_1", "evt_1", "src/x.go", "deadbeef")

	res, err := migrations.BackfillCanonicalHash(context.Background(), db, migrations.BackfillOptions{})
	if !errors.Is(err, migrations.ErrActiveIntents) {
		t.Fatalf("err=%v want ErrActiveIntents", err)
	}
	if !res.Skipped {
		t.Fatal("expected Skipped=true when refusing")
	}
	if res.IntentPathsBackfilled != 0 {
		t.Fatalf("backfilled %d rows despite refusal", res.IntentPathsBackfilled)
	}
	// canonical_path_hash must still be NULL.
	var canon sql.NullString
	if err := db.QueryRow(`SELECT canonical_path_hash FROM intent_paths WHERE intent_id = 'int_1'`).Scan(&canon); err != nil {
		t.Fatal(err)
	}
	if canon.Valid {
		t.Fatalf("canonical_path_hash should still be NULL, got %q", canon.String)
	}
}

// TestBackfill_RewritesQuiescedRows asserts the happy path: with no
// active intents the backfill rewrites canonical_path_hash from path.
func TestBackfill_RewritesQuiescedRows(t *testing.T) {
	db := openTestDB(t)
	seedAgent(t, db)
	seedActiveIntent(t, db, "int_1", "evt_1", "src/x.go", "deadbeef")
	closeIntent(t, db, "int_1")

	res, err := migrations.BackfillCanonicalHash(context.Background(), db, migrations.BackfillOptions{})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if res.IntentPathsBackfilled != 1 {
		t.Fatalf("backfilled %d, want 1", res.IntentPathsBackfilled)
	}
	var canon string
	if err := db.QueryRow(`SELECT canonical_path_hash FROM intent_paths WHERE intent_id = 'int_1'`).Scan(&canon); err != nil {
		t.Fatal(err)
	}
	if canon != paths.CanonicalHash("src/x.go") {
		t.Fatalf("canonical_path_hash=%q want %q", canon, paths.CanonicalHash("src/x.go"))
	}
}

// TestBackfill_Idempotent re-runs the backfill and asserts already-
// populated rows are skipped (count == 0).
func TestBackfill_Idempotent(t *testing.T) {
	db := openTestDB(t)
	seedAgent(t, db)
	seedActiveIntent(t, db, "int_1", "evt_1", "src/x.go", "deadbeef")
	closeIntent(t, db, "int_1")

	if _, err := migrations.BackfillCanonicalHash(context.Background(), db, migrations.BackfillOptions{}); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	res, err := migrations.BackfillCanonicalHash(context.Background(), db, migrations.BackfillOptions{})
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if res.IntentPathsBackfilled != 0 {
		t.Fatalf("idempotent backfill rewrote %d rows", res.IntentPathsBackfilled)
	}
}

// TestBackfill_RejectsMalformedPath asserts the migration aborts with a
// manifest when a row's path is malformed (absolute, contains ..,
// non-NFC, etc.) rather than silently rehashing garbage.
func TestBackfill_RejectsMalformedPath(t *testing.T) {
	db := openTestDB(t)
	seedAgent(t, db)
	seedActiveIntent(t, db, "int_1", "evt_1", "/etc/passwd", "deadbeef")
	closeIntent(t, db, "int_1")

	res, err := migrations.BackfillCanonicalHash(context.Background(), db, migrations.BackfillOptions{})
	if !errors.Is(err, migrations.ErrMalformedPaths) {
		t.Fatalf("err=%v want ErrMalformedPaths", err)
	}
	if len(res.MalformedPaths) != 1 {
		t.Fatalf("malformed=%d want 1: %+v", len(res.MalformedPaths), res.MalformedPaths)
	}
	if res.MalformedPaths[0].Reason == "" {
		t.Fatal("MalformedPath.Reason should be non-empty")
	}
	// canonical_path_hash must still be NULL because the migration aborts.
	var canon sql.NullString
	if err := db.QueryRow(`SELECT canonical_path_hash FROM intent_paths WHERE intent_id = 'int_1'`).Scan(&canon); err != nil {
		t.Fatal(err)
	}
	if canon.Valid {
		t.Fatalf("canonical_path_hash should still be NULL when malformed rows abort, got %q", canon.String)
	}
}

// TestBackfill_ForceBypassesActiveGate asserts --force lets the backfill
// proceed even with active intents. This documents the lock-correctness
// trade-off the operator opts into.
func TestBackfill_ForceBypassesActiveGate(t *testing.T) {
	db := openTestDB(t)
	seedAgent(t, db)
	seedActiveIntent(t, db, "int_1", "evt_1", "src/x.go", "deadbeef")

	res, err := migrations.BackfillCanonicalHash(context.Background(), db, migrations.BackfillOptions{Force: true})
	if err != nil {
		t.Fatalf("forced backfill: %v", err)
	}
	if res.Skipped {
		t.Fatal("Skipped=true under --force is unexpected")
	}
	if res.IntentPathsBackfilled != 1 {
		t.Fatalf("backfilled %d, want 1", res.IntentPathsBackfilled)
	}
}
