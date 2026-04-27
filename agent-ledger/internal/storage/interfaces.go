// Package storage defines the shared interfaces every Phase 1 storage
// backend must satisfy. Task 002 only declares the surface; Task 003
// owns the SQLite + JSONL implementation.
//
// Why a shared package?
//   - Allows command code (init, doctor) and later domain code to depend
//     on a stable type without importing the heavy SQLite driver.
//   - Lets test code provide stubs that return nil/empty results.
//
// All methods that perform I/O accept a context.Context for cancellation
// and must honor it cooperatively. Implementations should return typed
// errors that map cleanly to SPEC §19.1 exit codes.
package storage

import (
	"context"
	"io"
)

// Store is the top-level handle for ledger persistence. It owns both the
// SQLite database and the JSONL audit mirror.
//
// Phase 1 surface: open, close, run migrations, expose an EventWriter.
// Domain-specific helpers (Assignments, Intents, Changes, ...) will be
// added in Wave 2 as separate sub-interfaces composed onto Store.
type Store interface {
	io.Closer

	// Migrator returns the Migrator bound to this store. The migrator
	// runs idempotently: invoking Up on an already-current store is a
	// no-op.
	Migrator() Migrator

	// Events returns the EventWriter bound to this store. Each call may
	// return the same instance.
	Events() EventWriter

	// LedgerDir reports the absolute path of the ledger directory this
	// store was opened against. Useful for diagnostics.
	LedgerDir() string
}

// Migrator applies schema migrations.
//
// SchemaVersion returns the highest applied version, or 0 when no
// migrations have run yet. Up applies all pending migrations within a
// transaction per migration; partial application is not allowed.
type Migrator interface {
	SchemaVersion(ctx context.Context) (int, error)
	Up(ctx context.Context) error
}

// EventWriter is the privacy-aware audit channel.
//
// Implementations must:
//   - Insert a row into the events table inside the same transaction as
//     the corresponding domain row.
//   - Append a JSONL line to the day's audit file as a best-effort
//     mirror; failure to mirror MUST NOT roll back the database write,
//     but MUST be surfaced via Doctor checks.
//   - Reject payloads larger than the configured cap and reject any
//     payload that names a forbidden key (env, headers, secrets, ...).
type EventWriter interface {
	// WriteEvent persists evt. Implementations choose whether to enrich
	// with timestamps and IDs; callers should fill any field they need
	// to be deterministic for tests.
	WriteEvent(ctx context.Context, evt Event) error
}

// Event is the privacy-safe record persisted by EventWriter. Field names
// match SPEC §11.10 / §12.
//
// PayloadJSON must be a JSON object with no env vars, command output,
// raw hook payloads, file contents, full diffs, headers, tokens, or
// secrets. Implementations enforce this at write time.
type Event struct {
	EventID      string
	Type         string
	AgentID      string
	TaskID       string
	IntentID     string
	AssignmentID string
	OccurredAt   string
	PayloadJSON  []byte
}

// OpenFunc is the constructor signature implementations expose. Task 003
// will provide a concrete sqlite.Open matching this shape.
type OpenFunc func(ctx context.Context, ledgerDir string) (Store, error)
