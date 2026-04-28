package migrations_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/migrations"
)

// TestMigration0002_DedupesExistingDuplicateActiveRows seeds a database
// at version 1 with TWO active assignment rows for the same
// (task_id, assigned_agent_id) pair (the F9 race outcome). It then
// runs Apply and confirms migration 0002 demotes the older row to
// status='superseded' and keeps the newer one active. This guards
// against the migration failing on real ledgers that already
// accumulated duplicates from the v0.1.0 race.
func TestMigration0002_DedupesExistingDuplicateActiveRows(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ledger.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	// Apply only migration 0001 by stopping after it.
	migs, err := migrations.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) < 2 {
		t.Fatalf("expected >=2 migrations, got %d", len(migs))
	}
	// Apply 0001 manually (without going through Apply, which would
	// run all pending migrations including 0002).
	if _, err := db.ExecContext(ctx, migs[0].SQL); err != nil {
		t.Fatalf("apply 0001: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES(1, '2026-01-01T00:00:00.000Z')`,
	); err != nil {
		t.Fatal(err)
	}

	// Seed two duplicate active assignments and one unrelated active
	// row. The older duplicate should be demoted; the newer duplicate
	// and the unrelated row should remain active.
	insert := `INSERT INTO assignments(
		assignment_id, event_id, task_id, orchestrator_id, assigned_agent_id,
		allowed_paths_json, forbidden_paths_json, conflict_policy, reason,
		status, created_at, metadata_json
	) VALUES(?,?,?,?,?, '["**"]','[]','warn','test','active',?, '{}')`
	if _, err := db.ExecContext(ctx, insert,
		"asg_old", "evt_old", "task-X", "orch", "agent-A",
		"2026-01-01T00:00:00.000Z",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, insert,
		"asg_new", "evt_new", "task-X", "orch", "agent-A",
		"2026-01-01T01:00:00.000Z",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, insert,
		"asg_other", "evt_other", "task-Y", "orch", "agent-B",
		"2026-01-01T02:00:00.000Z",
	); err != nil {
		t.Fatal(err)
	}

	// Apply pending migrations (which is just 0002).
	if err := migrations.Apply(ctx, db, nil); err != nil {
		t.Fatalf("apply 0002: %v", err)
	}

	// asg_old should be superseded; asg_new and asg_other should be
	// active. The unique index should now exist and reject a third
	// active row for (task-X, agent-A).
	statuses := map[string]string{}
	rows, err := db.QueryContext(ctx,
		`SELECT assignment_id, status FROM assignments ORDER BY assignment_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			t.Fatal(err)
		}
		statuses[id] = status
	}
	if statuses["asg_old"] != "superseded" {
		t.Errorf("asg_old status = %q, want superseded", statuses["asg_old"])
	}
	if statuses["asg_new"] != "active" {
		t.Errorf("asg_new status = %q, want active", statuses["asg_new"])
	}
	if statuses["asg_other"] != "active" {
		t.Errorf("asg_other status = %q, want active", statuses["asg_other"])
	}

	// Verify the unique index now blocks a fresh duplicate insert.
	_, err = db.ExecContext(ctx, insert,
		"asg_third", "evt_third", "task-X", "orch", "agent-A",
		"2026-01-01T03:00:00.000Z",
	)
	if err == nil {
		t.Fatal("expected UNIQUE constraint failure on duplicate active insert, got nil")
	}
	// Idempotency: re-running Apply on an already-migrated database is
	// a no-op (no error, no row mutation).
	if err := migrations.Apply(ctx, db, nil); err != nil {
		t.Fatalf("rerun apply: %v", err)
	}
}

// TestMigration0002_LeavesClosedRowsAlone confirms the dedupe step
// does NOT touch already-closed assignments; only active duplicates
// are eligible for the unique index, and only their older instances
// are demoted.
func TestMigration0002_LeavesClosedRowsAlone(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ledger.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	migs, _ := migrations.All()
	if _, err := db.ExecContext(ctx, migs[0].SQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, applied_at) VALUES(1, '2026-01-01T00:00:00.000Z')`,
	); err != nil {
		t.Fatal(err)
	}

	// One closed and one active row for the same (task, agent).
	insert := `INSERT INTO assignments(
		assignment_id, event_id, task_id, orchestrator_id, assigned_agent_id,
		allowed_paths_json, forbidden_paths_json, conflict_policy, reason,
		status, created_at, metadata_json
	) VALUES(?,?,?,?,?, '["**"]','[]','warn','test',?,?,'{}')`
	if _, err := db.ExecContext(ctx, insert,
		"asg_closed", "evt_closed", "task-Z", "orch", "agent-C",
		"closed", "2026-01-01T00:00:00.000Z",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, insert,
		"asg_active", "evt_active", "task-Z", "orch", "agent-C",
		"active", "2026-01-01T01:00:00.000Z",
	); err != nil {
		t.Fatal(err)
	}

	if err := migrations.Apply(ctx, db, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var status string
	if err := db.QueryRowContext(ctx,
		`SELECT status FROM assignments WHERE assignment_id='asg_closed'`,
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "closed" {
		t.Errorf("asg_closed status = %q, want closed (untouched)", status)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT status FROM assignments WHERE assignment_id='asg_active'`,
	).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Errorf("asg_active status = %q, want active", status)
	}
}
