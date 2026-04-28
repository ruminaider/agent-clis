// Package sqlite implements storage.Store on top of modernc.org/sqlite
// (pure Go) so the agent-ledger binary builds with CGO_ENABLED=0.
//
// Connection-level pragmas (WAL, synchronous=NORMAL, foreign_keys=ON,
// busy_timeout=5000) are applied to every new SQL connection through a
// custom driver registration so they cannot drift across goroutines or
// reconnects.
//
// The Store value is the concrete handle the rest of the codebase uses;
// it satisfies the storage.Store, storage.Migrator, and
// storage.EventWriter interfaces from internal/storage.
package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	sqlitelib "modernc.org/sqlite"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/audit"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/events"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/id"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/migrations"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage"
)

// driverName is the database/sql name we register with the pragma
// connector wrapper. We register lazily once per process.
const driverName = "sqlite-agent-ledger"

var registerOnce sync.Once

// pragmas applied on every new connection. Order matters: WAL must be
// set first so the rest take effect under WAL semantics.
var pragmas = []string{
	"PRAGMA journal_mode=WAL",
	"PRAGMA synchronous=NORMAL",
	"PRAGMA foreign_keys=ON",
	"PRAGMA busy_timeout=5000",
}

// pragmaDriver wraps the modernc sqlite driver so every Open applies
// our connection pragmas before returning the conn to database/sql.
type pragmaDriver struct{ inner driver.Driver }

func (d pragmaDriver) Open(name string) (driver.Conn, error) {
	c, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	if err := applyPragmas(c); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func applyPragmas(c driver.Conn) error {
	exec, ok := c.(driver.ExecerContext)
	if !ok {
		return errors.New("sqlite: driver missing ExecerContext")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, p := range pragmas {
		if _, err := exec.ExecContext(ctx, p, nil); err != nil {
			return fmt.Errorf("sqlite: pragma %q: %w", p, err)
		}
	}
	return nil
}

func register() {
	registerOnce.Do(func() {
		sql.Register(driverName, pragmaDriver{inner: &sqlitelib.Driver{}})
	})
}

// Store is the concrete agent-ledger SQLite store.
type Store struct {
	db     *sql.DB
	dir    string
	dbPath string
	clock  func() time.Time
	idgen  *id.Generator
	audit  *audit.Writer
}

// Options control optional behavior. All fields are optional; sensible
// defaults are used.
type Options struct {
	// Now is the clock injected into IDs, audit rotation, and the
	// migrator. Defaults to time.Now.
	Now func() time.Time
	// IDGen overrides the ID generator. Defaults to a generator that
	// uses Now.
	IDGen *id.Generator
}

// Open creates the ledger directory layout if needed and opens the
// SQLite database. It does NOT run migrations: callers must call
// Migrator().Up to bring the schema up to date.
func Open(ctx context.Context, ledgerDir string) (*Store, error) {
	return OpenWithOptions(ctx, ledgerDir, Options{})
}

// OpenWithOptions is Open with explicit dependencies for tests.
func OpenWithOptions(ctx context.Context, ledgerDir string, opt Options) (*Store, error) {
	if ledgerDir == "" {
		return nil, errors.New("sqlite: empty ledger dir")
	}
	layout, err := storage.EnsureLayout(ledgerDir)
	if err != nil {
		return nil, fmt.Errorf("sqlite: ensure layout: %w", err)
	}
	register()
	dsn := "file:" + layout.SQLitePath() + "?_pragma=busy_timeout(5000)"
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	// Limit max open connections to a small number; SQLite serializes
	// writes anyway, and a single writer keeps WAL contention low.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	gen := opt.IDGen
	if gen == nil {
		gen = id.NewGenerator(now, nil)
	}
	w, err := audit.NewWriter(layout.AuditDir(), now)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{
		db:     db,
		dir:    layout.Dir,
		dbPath: layout.SQLitePath(),
		clock:  now,
		idgen:  gen,
		audit:  w,
	}, nil
}

// DB returns the underlying *sql.DB. Exposed for advanced callers and
// tests that need raw access; domain code should use the typed helpers.
func (s *Store) DB() *sql.DB { return s.db }

// LedgerDir reports the absolute ledger directory.
func (s *Store) LedgerDir() string { return s.dir }

// Path returns the SQLite file path.
func (s *Store) Path() string { return s.dbPath }

// Audit returns the audit writer for direct access (e.g. doctor).
func (s *Store) Audit() *audit.Writer { return s.audit }

// IDGen returns the ID generator bound to this store.
func (s *Store) IDGen() *id.Generator { return s.idgen }

// Clock returns the time source.
func (s *Store) Clock() func() time.Time { return s.clock }

// Close releases the database handle and flushes the audit writer.
func (s *Store) Close() error {
	var firstErr error
	if err := s.audit.Close(); err != nil {
		firstErr = err
	}
	if err := s.db.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// Migrator returns the storage.Migrator bound to this store.
func (s *Store) Migrator() storage.Migrator { return migrator{s: s} }

// Events returns the storage.EventWriter bound to this store.
func (s *Store) Events() storage.EventWriter { return eventWriter{s: s} }

// migrator adapts the migrations package to the storage.Migrator
// interface.
type migrator struct{ s *Store }

func (m migrator) SchemaVersion(ctx context.Context) (int, error) {
	v, err := migrations.CurrentVersion(ctx, m.s.db)
	if err != nil {
		return 0, mapStorageError(err)
	}
	return v, nil
}

func (m migrator) Up(ctx context.Context) error {
	if err := migrations.Apply(ctx, m.s.db, m.s.clock); err != nil {
		return mapStorageError(err)
	}
	return nil
}

// eventWriter adapts events to storage.EventWriter. Domain helpers in
// later tasks should prefer the WriteDomainEvent transactional helper
// rather than calling this directly.
type eventWriter struct{ s *Store }

func (e eventWriter) WriteEvent(ctx context.Context, ev storage.Event) error {
	if err := events.ValidatePayload(ev.PayloadJSON); err != nil {
		return err
	}
	row, err := e.s.fillEventDefaults(ev)
	if err != nil {
		return err
	}
	tx, err := e.s.db.BeginTx(ctx, nil)
	if err != nil {
		return mapStorageError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertEvent(ctx, tx, row); err != nil {
		return mapStorageError(err)
	}
	if err := tx.Commit(); err != nil {
		return mapStorageError(err)
	}
	// Audit mirror is best-effort: failure does not roll back the DB.
	_ = e.s.mirrorEvent(row)
	return nil
}

// WriteDomainEvent persists a domain row (via the supplied callback),
// an `events` row, and a JSONL audit line in one SQL transaction. The
// audit mirror is best-effort and will not abort the DB write on
// failure; the most recent error is exposed via Audit().LastError().
//
// domainInsert receives the open transaction. It must not call Commit
// or Rollback; this helper owns the transaction lifecycle.
func (s *Store) WriteDomainEvent(
	ctx context.Context,
	ev storage.Event,
	domainInsert func(ctx context.Context, tx *sql.Tx) error,
) error {
	if err := events.ValidatePayload(ev.PayloadJSON); err != nil {
		return err
	}
	row, err := s.fillEventDefaults(ev)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mapStorageError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if domainInsert != nil {
		if err := domainInsert(ctx, tx); err != nil {
			return err
		}
	}
	if err := insertEvent(ctx, tx, row); err != nil {
		return mapStorageError(err)
	}
	if err := tx.Commit(); err != nil {
		return mapStorageError(err)
	}
	_ = s.mirrorEvent(row)
	return nil
}

// WriteDomainEventImmediate mirrors WriteDomainEvent, but it pins a
// connection and issues BEGIN IMMEDIATE explicitly so claim-specific
// callers can acquire the writer lock before any read-then-write
// overlap check. The callback returns the event rows to persist after
// the domain work succeeds. modernc.org/sqlite supports
// _txlock=immediate in the DSN, but this helper keeps the opt-in local
// instead of changing the whole pool's default transaction mode.
func (s *Store) WriteDomainEventImmediate(
	ctx context.Context, fn func(ctx context.Context, conn *sql.Conn) ([]storage.Event, error),
) error {
	var rows []storage.Event
	if err := s.withImmediateConn(ctx, func(ctx context.Context, conn *sql.Conn) error {
		var err error
		rows, err = fn(ctx, conn)
		if err != nil {
			return err
		}
		if rows == nil {
			return nil
		}
		for i := range rows {
			if err := events.ValidatePayload(rows[i].PayloadJSON); err != nil {
				return err
			}
			row, err := s.fillEventDefaults(rows[i])
			if err != nil {
				return err
			}
			rows[i] = row
			if err := insertEvent(ctx, conn, row); err != nil {
				return mapStorageError(err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, row := range rows {
		_ = s.mirrorEvent(row)
	}
	return nil
}

func (s *Store) withImmediateConn(ctx context.Context, fn func(ctx context.Context, conn *sql.Conn) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return mapStorageError(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return mapStorageError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if err := fn(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return mapStorageError(err)
	}
	committed = true
	return nil
}

// fillEventDefaults assigns event_id and occurred_at when blank.
func (s *Store) fillEventDefaults(ev storage.Event) (storage.Event, error) {
	if ev.EventID == "" {
		newID, err := s.idgen.New(id.PrefixEvent)
		if err != nil {
			return ev, err
		}
		ev.EventID = newID
	}
	if ev.OccurredAt == "" {
		ev.OccurredAt = id.FormatTimestamp(s.clock())
	}
	return ev, nil
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertEvent(ctx context.Context, exec sqlExecer, ev storage.Event) error {
	_, err := exec.ExecContext(ctx, `
		INSERT INTO events(event_id, schema, event_type, created_at, agent_id, task_id, payload_json)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		ev.EventID,
		events.Schema,
		ev.Type,
		ev.OccurredAt,
		nullable(ev.AgentID),
		nullable(ev.TaskID),
		string(ev.PayloadJSON),
	)
	return err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// mirrorEvent appends a JSONL line for ev. Best-effort: returns the
// error so callers can decide, but Store.WriteDomainEvent ignores it.
func (s *Store) mirrorEvent(ev storage.Event) error {
	rec := map[string]any{
		"schema":     events.Schema,
		"event_id":   ev.EventID,
		"event_type": ev.Type,
		"created_at": ev.OccurredAt,
		"agent_id":   ev.AgentID,
		"task_id":    ev.TaskID,
		"payload":    rawJSON(ev.PayloadJSON),
	}
	return s.audit.Append(rec)
}

// rawJSON ensures PayloadJSON is embedded as a JSON object, not a
// double-encoded string.
func rawJSON(raw []byte) any {
	// We trust the validator to have ensured this is a JSON object.
	return jsonRaw(raw)
}

// jsonRaw is a helper type that marshals as the underlying bytes.
type jsonRaw []byte

func (j jsonRaw) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return []byte(j), nil
}

// mapStorageError translates SQLite errors into typed storage errors.
// SQLITE_BUSY surfaces as ErrBusy after the connection-level
// busy_timeout has elapsed.
func mapStorageError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "database is locked"),
		strings.Contains(msg, "SQLITE_BUSY"):
		return &StorageError{Kind: "busy", Err: err}
	case strings.Contains(msg, "FOREIGN KEY constraint failed"):
		return &StorageError{Kind: "foreign_key", Err: err}
	case strings.Contains(msg, "UNIQUE constraint failed"):
		return &StorageError{Kind: "unique", Err: err}
	}
	return &StorageError{Kind: "io", Err: err}
}

// StorageError is the typed error returned by the SQLite store.
// Callers can inspect Kind to decide on recovery vs failure.
type StorageError struct {
	Kind string
	Err  error
}

func (e *StorageError) Error() string { return "storage: " + e.Kind + ": " + e.Err.Error() }
func (e *StorageError) Unwrap() error { return e.Err }

// IsBusy reports whether err is a busy-timeout exhaustion error.
func IsBusy(err error) bool {
	var s *StorageError
	if errors.As(err, &s) {
		return s.Kind == "busy"
	}
	return false
}
