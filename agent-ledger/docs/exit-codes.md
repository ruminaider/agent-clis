# Exit codes

`agent-ledger` exit codes follow SPEC §19.1. Codes 0 through 5 are the
public contract used by `verify` and consumed by harnesses; codes 6
through 12 are documented internal extensions for non-verify commands
and may evolve over time. Constants live in
`internal/cli/exitcodes.go`.

## Public contract (SPEC §19.1)

| Code | Constant | Meaning |
| ---: | -------- | ------- |
| 0 | `ExitOK` | Success. For `verify`: verification passed. |
| 1 | `ExitGeneric` | Unspecified runtime failure. For `verify`: verification failed (one or more findings). |
| 2 | `ExitConfigError` | Configuration error: bad flags, invalid config, missing required arguments, unresolvable ledger directory, malformed pointer file. |
| 3 | `ExitStorageIO` | Storage, database, or filesystem I/O failure. |
| 4 | `ExitConflict` | Coordination conflict that requires an orchestrator or human decision. For `verify`: status `needs-decision`. |
| 5 | reserved | Reserved by SPEC §19.1 for future remote sync or authentication errors. Do not emit from new code. |

## Internal extensions (Phase 1, non-verify)

| Code | Constant | Meaning |
| ---: | -------- | ------- |
| 6 | `ExitValidation` | Input or schema validation failed (for example, a `--reason` value that contains a secret-shaped pattern, a malformed `--validation` argument, or a record path not in the claimed set). |
| 7 | `ExitScope` | Scope or policy violation detected by a non-verify command (for example, claiming a forbidden path). |
| 8 | `ExitNotFound` | Referenced entity does not exist (intent ID, task, conflict ID). |
| 9 | `ExitLockHeld` | A required advisory lock is held by another process. |
| 10 | `ExitStale` | Operation targeted stale or expired state. |
| 11 | `ExitInternal` | Internal invariant violation. Treat as a bug. |
| 12 | `ExitNotImplemented` | Stub command not yet wired up. Phase 1 stubs return this until their real handlers land. |

`ExitUsage` is a legacy alias for `ExitConfigError` retained for
internal call sites; both names map to code 2.

## Per-command quick reference

| Command | Common exits |
| ------- | ------------ |
| `init` | 0, 2, 3 |
| `identify` | 0, 2 |
| `assign` | 0, 2, 6 (secret in reason), 8 (orchestrator/agent unknown) |
| `claim` | 0, 2, 4 (exclusive overlap), 6 (secret in reason), 7 (forbidden/outside path) |
| `heartbeat` | 0, 2, 8 |
| `record` | 0, 1 (path not claimed), 2, 3, 6 |
| `close` | 0, 2, 8 |
| `status` | 0, 2 |
| `verify` | 0 (passed), 1 (failed), 2 (config), 3 (storage), 4 (needs-decision) |
| `conflicts` | 0, 2, 8 |
| `adopt` | 0, 2, 6 (secret in reason) |
| `export-summary` | 0, 2, 3, 8 |
| `gc` | 0, 2, 3 |
| `migrate` | 0, 2, 3 |
| `doctor` | 0, 2 (config issue surfaced), 3 (storage issue surfaced) |

`doctor` is a special case: when its checks raise a configuration or
storage issue, it returns the corresponding code so harnesses can
short-circuit on environment problems.

## Harness-author guidance

- Treat `verify` exit code 4 as a soft block (the orchestrator or a
  human decides). Treat 1, 2, and 3 as hard failures.
- Treat exit codes 6 through 12 as command-specific failures. Surface
  the JSON error envelope (`{status, code, message, details}`) to the
  user; it is stable across releases.
- `ExitNotImplemented` (12) should never appear in steady state. If
  you see it, you are running against a kernel where a Phase 1 command
  is unimplemented. File an issue with the version string from
  `agent-ledger --version`.
