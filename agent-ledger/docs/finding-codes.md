# Verify finding codes

`agent-ledger verify` emits findings under the `agent-ledger.verify.v1`
schema. Each finding carries a stable `code`, a `severity` (`info`,
`warning`, `error`, or `fatal`), a scalar `path`, and a
`suggested_recovery` string. Constants live in
`internal/verify/verify.go`. The 14 SPEC §19.3 codes are the
long-stable Phase 1 set; `AUTO_ASSIGNED_TASK` (added in v0.1.5) is
an additive `warning` finding for adapter-derived assignments and is
not a SPEC §19.3 code.

## Codes

| Code | Severity (default) | Meaning | Suggested recovery |
| ---- | ------------------ | ------- | ------------------ |
| `UNCLAIMED_CHANGE` | `error` | A file changed in the working tree but no active intent claimed it. | Run `agent-ledger claim <path> --task <id> --reason ...` then `record` the change, or `adopt` it retroactively. |
| `FORBIDDEN_PATH_CHANGED` | `fatal` | A change touched a path that the assignment's `--forbid` list explicitly disallows. | Revert the change, or update the assignment if the scope was wrong. |
| `PATH_OUTSIDE_ASSIGNMENT` | `error` | A change touched a path that is not in the assignment's `--allow` set. | Add the path to the assignment, or revert the change. |
| `ACTIVE_CONFLICT` | `warning` | An open conflict record exists for the task. | Run `agent-ledger conflicts --task <id>` to inspect, then `conflicts acknowledge <id>` once resolved. |
| `STALE_INTENT` | `warning` | An active intent has not heartbeat within the GC window. | Run `agent-ledger heartbeat --intent <id>` if still active, or `gc --stale-after <window>` to orphan it. |
| `OPEN_INTENT` | `info` | An intent is open at verify time. Informational at end-of-task. | `close --intent <id> --outcome completed` (or `abandoned`). |
| `MISSING_REASON` | `error` | An assignment or intent has an empty `reason` field. | Re-create the assignment or intent with `--reason "<text>"`. |
| `MISSING_ASSIGNMENT` | `error` | A claim or change references a task that has no assignment record. | Create the assignment first with `agent-ledger assign --task <id> ...`. |
| `AUTO_ASSIGNED_TASK` | `warning` | An assignment exists for the task but was created by an adapter's auto-derivation path (`metadata.auto_assigned == true` or a `[auto-assigned by ...]` / `[harness-derived by ...]` reason marker) rather than by an explicit orchestrator. | If this session belongs to a known task, set `AGENT_LEDGER_TASK_ID` before launching the harness so the orchestrator declares the task explicitly. Otherwise informational; the audit trail is intact via the assignment metadata. |
| `AGENT_MISMATCH` | `error` | A change's agent identity does not match the assignment's `--agent` value. | Re-record under the assigned agent, or update the assignment if the wrong agent owned the task. |
| `REVIEW_ONLY_WRITE` | `error` | An intent declared `--access-mode read-only` but a `change.recorded` event exists for it. | Re-claim with `--access-mode read-write`, or revert the change. |
| `EXCLUSIVE_LOCK_HELD` | `warning` | An advisory file lock is held outside the expected exclusive-claim flow. | Run `agent-ledger doctor --json` to inspect lock sentinels; release the stale lock. |
| `SUMMARY_MISMATCH` | `fatal` | `verify --summary <file>` recomputed a path hash that disagrees with the value in the summary, or detected a tampered assignment hash. | Re-export the summary from the source ledger, or investigate which file changed after summary export. |
| `CONFIG_ERROR` | `fatal` | The verify run could not load configuration (bad pointer file, malformed policy, unresolvable ledger directory). Exit code 2. | Fix the configuration; `agent-ledger doctor --json` reports the same issue. |
| `STORAGE_ERROR` | `fatal` | The verify run could not read the ledger database. Exit code 3. | Inspect the ledger directory, run `migrate`, or restore from backup. |

## Status mapping

`verify` collapses findings into a single status:

| Status | Trigger |
| ------ | ------- |
| `passed` | No findings, or only `info` findings (such as `OPEN_INTENT` mid-task). |
| `failed` | One or more `error` or `fatal` findings of any code. |
| `needs-decision` | The only blocking findings are `ACTIVE_CONFLICT`. The orchestrator or a human resolves the conflict; the cycle continues. |
| `error` | The verify run itself failed (`CONFIG_ERROR` or `STORAGE_ERROR`). Implies exit code 2 or 3. |

## Stability

The 14 SPEC §19.3 codes above are part of the Phase 1 public
contract; `AUTO_ASSIGNED_TASK` is an additive v0.1.5 extension. Future
phases may add new codes; existing codes will not be renamed without a
schema version bump. Adapter authors should treat unknown codes as
non-fatal and surface them under the persona that emitted them.
