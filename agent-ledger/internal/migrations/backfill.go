package migrations

// Backfill canonical_path_hash for rows written before SPEC §14 #8.
//
// Migration 0003 adds the column nullable. Every row written before
// the upgrade keeps a NULL canonical_path_hash; conflict detection
// falls back to the legacy path_hash branch in that state, so it still
// works within a single worktree but misses cross-worktree overlaps.
// BackfillCanonicalHash rewrites the column from each row's `path`
// using paths.CanonicalHash, after which the canonical branch handles
// every row.
//
// Safety:
//   - Refuses to run while any intent is `status='active'` unless
//     opts.Force is set. Active intents may hold lock sentinels keyed
//     by the legacy hash, and rewriting the equality key during their
//     lifetime can produce a brief lock-correctness gap.
//   - Hard-errors (does not rewrite) on rows whose `path` is empty,
//     absolute, contains "..", contains "\\" on POSIX, or is non-NFC.
//     These are unsafe to rehash because the stored path no longer
//     reflects a normalized display value. Operators must inspect and
//     repair such rows manually.
//   - Idempotent: rows with non-empty canonical_path_hash are skipped.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/paths"
)

// BackfillOptions controls BackfillCanonicalHash.
type BackfillOptions struct {
	// Force runs the backfill even when active intents exist. The
	// caller assumes responsibility for the lock-correctness gap
	// described above.
	Force bool
}

// BackfillResult summarizes how many rows were rewritten.
type BackfillResult struct {
	IntentPathsBackfilled int
	ChangePathsBackfilled int
	ConflictsBackfilled   int
	// Skipped is true when the function declined to run because of
	// active intents and Force was false. SkipReason explains why.
	Skipped    bool
	SkipReason string
	// MalformedPaths lists rows that could not be safely rehashed.
	// When non-empty, the function returns an error and writes nothing.
	MalformedPaths []MalformedPath
}

// MalformedPath identifies a row that cannot be safely backfilled.
type MalformedPath struct {
	Table  string
	RowKey string // primary or composite key for diagnostics
	Path   string
	Reason string
}

// ErrActiveIntents is returned when BackfillCanonicalHash refuses to
// run because at least one intent is active and Force is not set.
var ErrActiveIntents = errors.New("migrations: refusing to backfill canonical_path_hash while active intents exist; close them or pass Force=true")

// ErrMalformedPaths is returned when one or more rows have a `path`
// value that cannot be safely rehashed. The accompanying BackfillResult
// lists every offender in MalformedPaths.
var ErrMalformedPaths = errors.New("migrations: refusing to backfill canonical_path_hash because one or more rows have malformed `path` values")

// BackfillCanonicalHash rewrites canonical_path_hash for every row in
// intent_paths, change_paths, and conflicts where it is currently NULL
// or empty. The rewrite uses paths.CanonicalHash on the stored `path`
// value (already a project-relative display string with forward slashes).
//
// The function runs in a single transaction so it is atomic with
// respect to concurrent readers. It is safe to call repeatedly; rows
// already populated are not touched.
func BackfillCanonicalHash(ctx context.Context, db *sql.DB, opts BackfillOptions) (BackfillResult, error) {
	res := BackfillResult{}

	// 0003 must already be applied; otherwise the column does not exist.
	if !columnExists(ctx, db, "intent_paths", "canonical_path_hash") {
		return res, errors.New("migrations: canonical_path_hash column missing; run Apply first")
	}

	// Quiesce gate.
	if !opts.Force {
		var active int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM intents WHERE status = 'active'`).Scan(&active); err != nil {
			return res, fmt.Errorf("migrations: probe active intents: %w", err)
		}
		if active > 0 {
			res.Skipped = true
			res.SkipReason = fmt.Sprintf("%d active intents", active)
			return res, ErrActiveIntents
		}
	}

	// Pre-flight: scan every row that needs backfill and validate its
	// `path` value before we touch any data. Any malformed row aborts
	// the migration with a manifest.
	type pending struct {
		table, rowKey, path string
	}
	var rows []pending
	scanTable := func(query string) error {
		r, err := db.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		defer r.Close()
		for r.Next() {
			var table, rowKey, path string
			if err := r.Scan(&table, &rowKey, &path); err != nil {
				return err
			}
			rows = append(rows, pending{table, rowKey, path})
		}
		return r.Err()
	}
	if err := scanTable(`
		SELECT 'intent_paths' AS t, intent_id || '|' || path_hash AS k, path
		FROM intent_paths WHERE canonical_path_hash IS NULL OR canonical_path_hash = ''
	`); err != nil {
		return res, err
	}
	if err := scanTable(`
		SELECT 'change_paths' AS t, change_id || '|' || path_hash AS k, path
		FROM change_paths WHERE canonical_path_hash IS NULL OR canonical_path_hash = ''
	`); err != nil {
		return res, err
	}
	if err := scanTable(`
		SELECT 'conflicts' AS t, conflict_id AS k, path
		FROM conflicts WHERE canonical_path_hash IS NULL OR canonical_path_hash = ''
	`); err != nil {
		return res, err
	}

	for _, p := range rows {
		if reason := classifyMalformed(p.path); reason != "" {
			res.MalformedPaths = append(res.MalformedPaths, MalformedPath{
				Table: p.table, RowKey: p.rowKey, Path: p.path, Reason: reason,
			})
		}
	}
	if len(res.MalformedPaths) > 0 {
		return res, ErrMalformedPaths
	}

	// All rows clean: write canonical hashes in one transaction.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback() }()

	upd := func(table, key string, args ...any) error {
		_, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET canonical_path_hash = ? WHERE %s`, table, key), args...)
		return err
	}
	for _, p := range rows {
		hash := paths.CanonicalHash(p.path)
		switch p.table {
		case "intent_paths":
			parts := strings.SplitN(p.rowKey, "|", 2)
			if len(parts) != 2 {
				return res, fmt.Errorf("migrations: bad intent_paths key %q", p.rowKey)
			}
			if err := upd("intent_paths", "intent_id = ? AND path_hash = ?", hash, parts[0], parts[1]); err != nil {
				return res, err
			}
			res.IntentPathsBackfilled++
		case "change_paths":
			parts := strings.SplitN(p.rowKey, "|", 2)
			if len(parts) != 2 {
				return res, fmt.Errorf("migrations: bad change_paths key %q", p.rowKey)
			}
			if err := upd("change_paths", "change_id = ? AND path_hash = ?", hash, parts[0], parts[1]); err != nil {
				return res, err
			}
			res.ChangePathsBackfilled++
		case "conflicts":
			if err := upd("conflicts", "conflict_id = ?", hash, p.rowKey); err != nil {
				return res, err
			}
			res.ConflictsBackfilled++
		default:
			return res, fmt.Errorf("migrations: unknown table %q", p.table)
		}
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	return res, nil
}

// classifyMalformed returns a non-empty reason when the stored `path`
// cannot be safely backfilled. Rules mirror SPEC §14: paths must be
// project-relative, NFC-normalized, and use forward slashes.
func classifyMalformed(p string) string {
	if p == "" {
		return "empty path"
	}
	if isAbsolutePath(p) {
		return "absolute path"
	}
	if p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") || strings.HasSuffix(p, "/..") {
		return "contains parent (..) component"
	}
	if runtime.GOOS != "windows" && strings.Contains(p, `\`) {
		return "contains backslash on POSIX"
	}
	if !norm.NFC.IsNormalString(p) {
		return "not NFC-normalized"
	}
	return ""
}

// isAbsolutePath checks for both POSIX absolute (`/foo`) and Windows
// drive-letter absolute (`C:\foo`, `C:/foo`) forms, since stored values
// may have been written on either platform.
func isAbsolutePath(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	if len(p) >= 2 && p[1] == ':' {
		c := p[0]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return true
		}
	}
	return false
}

// columnExists reports whether table has a column named col. Used to
// guard the backfill against running before the 0003 ALTER TABLE has
// been applied.
func columnExists(ctx context.Context, db *sql.DB, table, col string) bool {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name == col {
			return true
		}
	}
	return false
}
