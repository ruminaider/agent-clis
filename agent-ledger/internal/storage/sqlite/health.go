package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/migrations"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage"
)

// HealthReport summarizes the live state of the SQLite database.
// Doctor renders this; verify and gc consume the booleans.
type HealthReport struct {
	// PingOK is true when a trivial query round-tripped.
	PingOK bool
	// PingErr captures the ping failure reason when PingOK is false.
	PingErr string

	// JournalMode is the active PRAGMA journal_mode value (e.g. "wal").
	JournalMode string
	// ForeignKeysOn is true when PRAGMA foreign_keys=ON is in effect.
	ForeignKeysOn bool
	// SynchronousLevel is the active PRAGMA synchronous mode (numeric).
	SynchronousLevel int

	// SchemaVersion is the highest applied migration. 0 means none.
	SchemaVersion int
	// Applied lists every applied migration in version order.
	Applied []AppliedMigration
	// Pending lists every embedded migration not yet applied.
	Pending []migrations.Migration
}

// AppliedMigration is one row from schema_migrations enriched with the
// migration name from the embedded source set, when known.
type AppliedMigration struct {
	Version   int    `json:"version"`
	Name      string `json:"name,omitempty"`
	AppliedAt string `json:"applied_at"`
}

// Health collects pragma values, ping status, and migration state.
// It does not modify the database. Each subsection records its own
// error rather than aborting the report so doctor can show partial
// success when one probe fails.
func (s *Store) Health(ctx context.Context) HealthReport {
	r := HealthReport{}
	if err := s.db.PingContext(ctx); err != nil {
		r.PingErr = err.Error()
	} else {
		r.PingOK = true
	}

	r.JournalMode = scalarString(ctx, s.db, "PRAGMA journal_mode")
	r.SynchronousLevel = scalarInt(ctx, s.db, "PRAGMA synchronous")
	r.ForeignKeysOn = scalarInt(ctx, s.db, "PRAGMA foreign_keys") == 1

	if v, err := migrations.CurrentVersion(ctx, s.db); err == nil {
		r.SchemaVersion = v
	}

	all, err := migrations.All()
	if err == nil {
		applied := loadApplied(ctx, s.db, all)
		r.Applied = applied
		appliedSet := make(map[int]struct{}, len(applied))
		for _, a := range applied {
			appliedSet[a.Version] = struct{}{}
		}
		for _, m := range all {
			if _, ok := appliedSet[m.Version]; !ok {
				r.Pending = append(r.Pending, m)
			}
		}
	}
	return r
}

func loadApplied(ctx context.Context, db *sql.DB, all []migrations.Migration) []AppliedMigration {
	names := make(map[int]string, len(all))
	for _, m := range all {
		names[m.Version] = m.Name
	}
	// schema_migrations may not exist yet (fresh DB).
	var present int
	if err := db.QueryRowContext(ctx,
		`SELECT 1 FROM sqlite_master WHERE type='table' AND name='schema_migrations'`,
	).Scan(&present); err != nil {
		return nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT version, applied_at FROM schema_migrations ORDER BY version ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []AppliedMigration
	for rows.Next() {
		var a AppliedMigration
		if err := rows.Scan(&a.Version, &a.AppliedAt); err != nil {
			return out
		}
		a.Name = names[a.Version]
		out = append(out, a)
	}
	return out
}

func scalarString(ctx context.Context, db *sql.DB, q string) string {
	var v string
	if err := db.QueryRowContext(ctx, q).Scan(&v); err != nil {
		return ""
	}
	return v
}

func scalarInt(ctx context.Context, db *sql.DB, q string) int {
	var v int
	if err := db.QueryRowContext(ctx, q).Scan(&v); err != nil {
		return -1
	}
	return v
}

// LockSentinels returns the names of *.lock sentinel files under the
// layout's locks directory. The list is advisory: a sentinel may be
// present even when no process currently holds the OS-level flock.
// Doctor surfaces the count so operators can spot stuck workers.
func (s *Store) LockSentinels() []string {
	layout := storage.Layout{Dir: s.dir}
	entries, err := os.ReadDir(layout.LocksDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".lock" {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}
