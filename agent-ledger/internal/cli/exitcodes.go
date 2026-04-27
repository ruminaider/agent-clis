// Package cli holds shared CLI building blocks: exit codes, error envelopes,
// and command construction helpers used by cmd/agent-ledger.
package cli

// Exit codes defined by SPEC §19.1.
//
// These are the only exit codes agent-ledger emits. Command handlers must
// return an *Error wrapping one of these so the top-level runner can map it
// to the process exit status.
const (
	// ExitOK indicates successful execution.
	ExitOK = 0
	// ExitGeneric indicates an unspecified runtime failure.
	ExitGeneric = 1
	// ExitUsage indicates a CLI usage error: bad flags, missing args, etc.
	ExitUsage = 2
	// ExitNotImplemented indicates a stub command not yet wired up.
	ExitNotImplemented = 3
	// ExitConflict indicates a coordination conflict that needs a decision.
	ExitConflict = 4
	// ExitStorageIO indicates a storage or filesystem I/O failure.
	ExitStorageIO = 5
	// ExitValidation indicates input or schema validation failed.
	ExitValidation = 6
	// ExitScope indicates a scope or policy violation.
	ExitScope = 7
	// ExitNotFound indicates a referenced entity does not exist.
	ExitNotFound = 8
	// ExitLockHeld indicates a required lock is currently held.
	ExitLockHeld = 9
	// ExitStale indicates the operation targeted stale or expired state.
	ExitStale = 10
	// ExitInternal indicates an internal invariant violation.
	ExitInternal = 11
)
