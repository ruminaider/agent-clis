// Package migrations owns the embedded SQL migrations for the agent-
// ledger SQLite database. Migrations are numbered, applied in order,
// and tracked in `schema_migrations`. Re-running Apply is idempotent.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed embed/*.sql
var fsys embed.FS

// Migration is a single numbered SQL migration.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// All returns the embedded migrations sorted ascending by Version.
func All() ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, "embed")
	if err != nil {
		return nil, fmt.Errorf("migrations: read embed dir: %w", err)
	}
	out := make([]Migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		// Filename shape: NNNN_name.sql
		stem := strings.TrimSuffix(e.Name(), ".sql")
		parts := strings.SplitN(stem, "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("migrations: bad filename %q (want NNNN_name.sql)", e.Name())
		}
		v, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("migrations: bad version in %q: %w", e.Name(), err)
		}
		body, err := fs.ReadFile(fsys, "embed/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("migrations: read %q: %w", e.Name(), err)
		}
		out = append(out, Migration{Version: v, Name: parts[1], SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if i > 0 && out[i-1].Version == m.Version {
			return nil, fmt.Errorf("migrations: duplicate version %d", m.Version)
		}
	}
	return out, nil
}

// CurrentVersion reads the highest applied version from the database.
// Returns 0 when the schema_migrations table is absent or empty.
func CurrentVersion(ctx context.Context, db *sql.DB) (int, error) {
	// Make sure schema_migrations exists; if not, version is 0.
	var present int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&present)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("migrations: probe schema_migrations: %w", err)
	}
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("migrations: read max version: %w", err)
	}
	return int(v.Int64), nil
}

// Clock supplies the timestamp recorded in schema_migrations.applied_at.
type Clock func() time.Time

// Apply runs all pending migrations in order, each in its own
// transaction. It is safe to call repeatedly; already-applied versions
// are skipped.
func Apply(ctx context.Context, db *sql.DB, now Clock) error {
	if now == nil {
		now = time.Now
	}
	migs, err := All()
	if err != nil {
		return err
	}
	cur, err := CurrentVersion(ctx, db)
	if err != nil {
		return err
	}
	for _, m := range migs {
		if m.Version <= cur {
			continue
		}
		if err := applyOne(ctx, db, m, now); err != nil {
			return fmt.Errorf("migrations: apply v%d (%s): %w", m.Version, m.Name, err)
		}
	}
	return nil
}

func applyOne(ctx context.Context, db *sql.DB, m Migration, now Clock) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	ts := now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
		m.Version, ts); err != nil {
		return err
	}
	return tx.Commit()
}
