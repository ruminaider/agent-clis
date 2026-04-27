// Package cli holds shared CLI building blocks: exit codes, error envelopes,
// and command construction helpers used by cmd/agent-ledger.
package cli

// SPEC §19.1 exit codes.
//
// These six values (0-5) are the canonical, externally documented
// process exit codes agent-ledger emits. They are the contract
// orchestrators and shells rely on:
//
//	| Code | Meaning                                                    |
//	|------|------------------------------------------------------------|
//	|  0   | Verification passed / success.                             |
//	|  1   | Verification failed / unspecified runtime failure.         |
//	|  2   | Configuration error (bad flags, bad config, missing args). |
//	|  3   | Storage or database error.                                 |
//	|  4   | Conflict requiring orchestrator or human decision.         |
//	|  5   | Sync or authentication error. Reserved for future remote   |
//	|      | sync; agent-ledger does not emit it today.                 |
//
// Command handlers must return an *Error wrapping one of the codes
// declared below so the top-level runner can map it to the process
// exit status.
const (
	// ExitOK indicates successful execution / verification passed
	// (SPEC §19.1 code 0).
	ExitOK = 0
	// ExitGeneric indicates an unspecified runtime failure, or a
	// failed verification (SPEC §19.1 code 1).
	ExitGeneric = 1
	// ExitConfigError indicates a configuration error: bad flags,
	// invalid config, or missing required arguments
	// (SPEC §19.1 code 2).
	ExitConfigError = 2
	// ExitUsage is a legacy alias for ExitConfigError used by
	// command handlers that detect bad CLI usage. Both names map
	// to SPEC §19.1 code 2.
	ExitUsage = ExitConfigError
	// ExitStorageIO indicates a storage, database, or filesystem
	// I/O failure (SPEC §19.1 code 3).
	ExitStorageIO = 3
	// ExitConflict indicates a coordination conflict that needs an
	// orchestrator or human decision (SPEC §19.1 code 4).
	ExitConflict = 4
	// Code 5 is reserved by SPEC §19.1 for sync or authentication
	// errors against future remote sync. No constant is defined
	// because agent-ledger does not emit it today.
)

// Internal extension exit codes (NOT part of SPEC §19.1).
//
// These codes are private to the agent-ledger CLI. They give
// orchestrators and humans finer-grained signal beyond the six
// codes SPEC §19.1 defines, but they are not part of the public
// exit-code contract. Adding a new code here MUST be paired with
// either a SPEC amendment promoting it into §19.1 or explicit
// documentation that the code is an implementation-private
// extension.
const (
	// ExitValidation indicates input or schema validation failed.
	ExitValidation = 6
	// ExitScope indicates a scope or policy violation.
	ExitScope = 7
	// ExitNotFound indicates a referenced entity does not exist.
	ExitNotFound = 8
	// ExitLockHeld indicates a required lock is currently held.
	ExitLockHeld = 9
	// ExitStale indicates the operation targeted stale or expired
	// state.
	ExitStale = 10
	// ExitInternal indicates an internal invariant violation.
	ExitInternal = 11
	// ExitNotImplemented indicates a stub command not yet wired up.
	// Phase 1 stubs return this until their real handlers land.
	ExitNotImplemented = 12
)
