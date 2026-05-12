# Changelog

All notable changes to `agent-ledger` are recorded here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the project aims to follow [Semantic Versioning](https://semver.org/)
once a stable release is tagged. Phase 1 is pre-1.0; the
`agent-ledger.verify.v1` and `agent-ledger-summary.v1` schemas are
covered by their version suffix and will follow semver independently
of the binary version.

## [Unreleased]

### Added

- **`assign update` subcommand for additive scope extension.** Closes
  the kernel gap where an active assignment's allow-list was fixed
  for the lifetime of the task. Operators that wanted to extend an
  in-flight assignment had to choose between living with `warn` policy
  drift, editing SQLite by hand, or rotating to a new `task_id` and
  breaking the worker's `AGENT_LEDGER_TASK_ID` env. The new command
  supersedes the prior active row and inserts a fresh active row that
  merges new globs into the existing path lists, all inside one
  immediate transaction.

  - `agent-ledger assign update --task <id> --agent <agent-id>
    --add-allow <glob>... --reason "<why>"` is the surface.
    `--add-allow` is required and may be repeated.
  - Idempotent. Rerunning with the same flags returns
    `changed=false reused=true`, no new row, no event.
  - Lineage is recorded as `metadata.superseded_by` on the prior row
    and `metadata.superseded_assignment_id` on the new row. The
    `assignments --status all` query exposes both.
  - New event type `assignment.superseded` joins the MVP set
    (SPEC §12.1). It is a *replacement* event, not a *closure* event.
    Standalone `assignment.closed` (terminal closure with no
    replacement) remains post-MVP.
  - SPEC §11.3.1 documents the assignment-identity rule: an
    `assignment_id` identifies one immutable scope-contract instance,
    and the human concept of "the assignment for this (task, agent)"
    is a chain linked via the `superseded_by` and
    `superseded_assignment_id` metadata keys.
  - The MVP is allow-list extension only. Adding forbid globs,
    removing globs, replacing the full path lists, and changing the
    conflict policy are intentionally out of scope: any change that
    narrows what an in-flight intent may write (including a new
    forbid that overlaps an already-claimed path) can leave `record`
    accepting writes that `verify` later rejects, because `record`
    validates against intent path hashes, not current assignment
    scope. Close and re-`assign` for those cases.
  - Reserved metadata keys (`superseded_by`,
    `superseded_assignment_id`, `updated_from`) supplied via
    `--metadata` are stripped; the helper owns these and writes them
    with the correct values.

## [0.4.1] - 2026-05-09

### Added

- **Pointer file `default_task_id` and the `pointer` task source.**
  The local `.agent-ledger.toml` now accepts an optional
  `default_task_id` field. Adapters consult it during session
  bootstrap when the harness produces no task signal (no PR, no
  branch, no detached HEAD) and select `TASK_SOURCE=pointer` when the
  field is set. This is the right answer for non-git, ambient
  multi-agent projects where two or more concurrent sessions in the
  same directory should attribute to one task without per-session env
  wiring.

  - `agent-ledger init --write-pointer --default-task-id <id>`
    persists the value. Reruns of `init --write-pointer` carry an
    existing value forward when `--default-task-id` is not supplied.
  - New read-only `agent-ledger pointer show [--json]` command
    projects the parsed pointer for adapters and humans. Adapters use
    the JSON shape; the kernel remains the authoritative TOML parser.
  - `agent-ledger doctor` surfaces `default_task_id` when present.
  - The `[harness-derived ...]` audit marker now accepts
    `source=pointer` alongside `branch`, `pr`, `detached`, and
    `subagent`.

- **Auto-fallback toast names the cheapest fix.** When the adapter
  falls through to `TASK_SOURCE=auto`, the bootstrap now exports
  `AGENT_LEDGER_TASK_AUTO_REASON` so the pi UI toast can point at the
  cheapest fix. Current values: `not_in_git_repo`, `git_no_head`,
  `pointer_lacks_default`, `pointer_unreadable`, and
  `pointer_parser_unavailable`. The toast hint expands each token
  into actionable guidance (set `AGENT_LEDGER_TASK_ID`, declare
  `default_task_id`, fix a malformed `.agent-ledger.toml`, install
  python3 or node, or launch from inside a checkout).

### Fixed

- **Pointer-detection in the adapter bootstrap no longer hides errors.**
  Previously, `agent-ledger pointer show --json` was wrapped in
  `2>/dev/null || true`, which discarded the kernel exit code. A
  malformed `.agent-ledger.toml` silently fell through to a
  misleading `not_in_git_repo` / `git_no_head` auto-fallback hint.
  The bootstrap now captures the exit code and stderr separately and
  emits `AGENT_LEDGER_TASK_AUTO_REASON=pointer_unreadable` (with the
  kernel's stderr in the warning) when the file exists but cannot be
  parsed.
- **Pointer-detection now warns when no JSON parser is available.**
  Hosts with neither `python3` nor `node` on `PATH` previously
  ignored the declared `default_task_id` silently. The bootstrap now
  emits `AGENT_LEDGER_TASK_AUTO_REASON=pointer_parser_unavailable`
  with a stderr warning that names the missing dependency.
- **`agent-ledger init --default-task-id` validates before disk side
  effects.** The `--default-task-id requires --write-pointer` usage
  check now runs before `storage.EnsureLayout`, so a misuse no
  longer leaves a half-initialized ledger directory behind.

## [0.4.0] - 2026-05-07

### Added

- **pi adapter: subagent children now self-assign.** Each pi subagent
  child self-assigns its own task from its session bootstrap. When
  pi-subagents sets `PI_SUBAGENT_CHILD=1` in the child's environment,
  the extension runs its bootstrap eagerly at extension load (before
  any tool call) and writes a fresh assignment row. The parent
  extension's `subagent` hook becomes observation-only: it records the
  dispatch event for correlation and telemetry but does not mutate
  `process.env` or call `agent-ledger assign`.

  The practical consequences:

  - **Parallel dispatch works.** Two `subagent()` tool calls in the
    same assistant turn each produce separate assignment rows with
    distinct task ids and agent ids. Parallel `tasks: [...]` fan-outs,
    `count: N` expansions, and multiple independent `subagent()` calls
    are all supported without any serialization constraint.

  - **Child task ids are deterministic.** A child task id derives from
    four inherited inputs (parent task id, child agent name, run id,
    child index) with no random suffix and no timestamp:
    `<parent>/<agent>/<run_id>-<index>`. A retry of the same logical
    child reuses the same task id via `assign --if-absent`, keeping the
    audit trail clean.

  - **Child sessions have fresh agent identities.** The child `AGENT_ID`
    is `agent:pi:subagent:<run_id>:<index>`, distinct from the parent's
    `AGENT_ID`. Claims, records, heartbeats, and closes in the child are
    attributed to the child identity; `orchestrator_id` on the
    assignment row records the parent identity for cross-session audit.
    Verify recognizes the split: it does not raise `AGENT_MISMATCH` on a
    subagent-bootstrap row when the calling agent matches the child
    assignee, even though the orchestrator field holds the parent's
    identity. A retry of the same logical child also reuses the same
    deterministic child `AGENT_ID`, so the second spawn's claims and
    records do not trigger `AGENT_MISMATCH`.

  - **Async and background dispatch work correctly.** Children launched
    via `subagent({ async: true })` inherit `PI_SUBAGENT_CHILD=1` and
    bootstrap eagerly, so they self-assign the correct child task id
    instead of falling back to branch or auto detection.

  This supersedes the approach added in PR #21
  (`fix/pi-child-task-id-collision`). That PR added a random hex suffix
  to parent-minted child task ids and documented why dispatch had to
  be serialized. Under the new design the parent never mints child task
  ids, so the hex suffix, the serialization lock, and the background
  inheritance gap are all removed.

- **Verify finding `AUTO_ASSIGNED_TASK`** added to the SPEC section
  19.3 finding catalog. Severity: warning. Fires when an assignment
  row exists but was created by an adapter's auto-derivation path
  rather than an explicit orchestrator assignment. Pi subagent children
  are exempt: their assignment rows are written by the child's own
  bootstrap, but the dispatch that triggered them was
  orchestrator-initiated. The discriminator is
  `metadata.dispatch_origin = "pi-subagent-bootstrap"`. Complement to
  `MISSING_ASSIGNMENT` (no-row case).

### Changed

- pi adapter: removed the no-op `event.input.env` annotation.
  pi-subagents validates input against a TypeBox schema that strips
  unknown fields, so the annotation never reached the spawner. The
  comment claiming it was a forward-compatibility hook was misleading.

## [0.3.0] - 2026-05-04

### Added

- `claim`, `record`, and `adopt` accept absolute paths inside any
  worktree of the same git common dir. Path normalization now
  enumerates worktree toplevels via `git worktree list --porcelain`
  and picks the longest realpath-prefix match, so an orchestrator in
  the main checkout can claim a file inside a sibling worktree
  without `path_outside_project`. SPEC §8.1 and §14 #1.
- `canonical_path_hash` column on `intent_paths`, `change_paths`,
  and `conflicts`, derived from `sha256(NFC(case-fold(display)))`.
  This is the new equality key for conflict detection, lock sentinel
  naming, and lookups across worktrees of the same project. Case
  folding uses Unicode-aware folding (`golang.org/x/text/cases.Fold`)
  so two case-aliased paths on macOS APFS or Windows NTFS continue
  to collide. SPEC §14 #8.
- Schema migration `0003_canonical_path_hash` adds the column
  (nullable) and matching indexes. Schema version bumps from 2 to 3.
- `agent-ledger migrate` now runs a Go-side backfill that rewrites
  legacy rows from their stored `path` to populate
  `canonical_path_hash`. The backfill refuses while any intent is
  active (lock-correctness gap risk); pass `--force` to override.
  Hard-errors with a manifest on rows whose `path` is malformed
  (empty, absolute, contains `..`, contains `\` on POSIX, non-NFC).
  Idempotent.
- Verifier finding `SYMLINK_ALIAS` (warning) fires when two active
  intents share a realpath but have distinct `canonical_path_hash`
  values, surfacing the lost symlink-aliasing that the realpath hash
  provided for free. SPEC §19.3.

### Fixed

- The same logical file claimed from two different worktrees of the
  same repo no longer produces two distinct `path_hash` values that
  silently bypass conflict detection. Conflict detection joins on
  `canonical_path_hash` and falls back to `path_hash` for rows with
  NULL canonical, so cross-worktree conflicts surface for new claims
  even before the legacy backfill runs.

### Notes

- Old binaries running against new ledgers continue to work because
  the new column is nullable and the conflict-detection query keeps
  the legacy `path_hash` branch.
- Operators with active intents at upgrade time will see a deferred
  backfill: schema migrates cleanly, but the canonical column stays
  NULL on old rows until they close intents and run
  `agent-ledger migrate` (or run with `--force`).

## [0.2.3] - 2026-05-01

### Added

- `doctor` gains a `lock_sentinels` check that cross-references
  `<ledger-dir>/locks/*.lock` against active intents in the DB and
  reports stale sentinels as a `warn` with recovery hints. The
  authoritative report stays `agent-ledger verify`
  (`EXCLUSIVE_LOCK_HELD`); doctor surfaces the same signal at the
  hygiene layer so reviewers see lock state without running task-mode
  verify.

## [0.2.2] - 2026-05-01

### Fixed

- `verify` no longer reports `EXCLUSIVE_LOCK_HELD` for sentinels owned
  by an active intent. The pre-fix scanner built its "known" hash set
  from a no-op loop over intents, so every sentinel under
  `<ledger-dir>/locks/` was flagged regardless of ownership. The
  scanner now resolves `intent_paths` for each active intent and
  flags only sentinels whose hash has no live owner.
- `verify` no longer reports `AGENT_MISMATCH` for changes recorded
  under a since-superseded assignment by that assignment's assignee.
  The check now consults every assignment row attached to the task
  (status `all`) and prefers `change.assignment_id` when set, falling
  back to "agent ever held an assignment for this task". A change by
  an agent that never held any assignment on the task is still flagged.
- `agent-ledger close` removes the `<ledger-dir>/locks/<hash>.lock`
  sentinel for each path of an exclusive intent after a successful
  close. Best-effort: filesystem failures do not abort the close.
- `agent-ledger gc` removes the same sentinels when it orphans a
  stale exclusive intent, preventing carried-over `EXCLUSIVE_LOCK_HELD`
  findings across gc cycles.

## [0.2.1] - 2026-04-29

### Fixed

- Documented the orchestrator ordering rule for long-lived workers:
  run `agent-ledger assign --if-absent` and confirm an active
  assignment before sending a worker any task brief or claim command.
- The shared adapter bootstrap now fails early when explicit
  `AGENT_LEDGER_TASK_ID` or `--task-id` sessions have no active
  assignment. Emergency repair is opt-in via
  `AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT=1` plus an explicit
  `AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW` scope, and repair rows are
  marked with `metadata.explicit_missing_assignment == true`.
- The pi extension's subagent hook now sets the child ledger env on
  `process.env` for the duration of the subagent tool call, which is
  the environment current pi-subagents uses when spawning children.
  Management calls such as `subagent({ action: "list" })` no longer
  create child ledger assignments. Child assignments are written from
  the requested subagent cwd when supplied, and overlapping subagent
  env injections are blocked.

## [0.2.0] - 2026-04-29

### Adapters: stable

v0.2.0 promotes the **pi extension** out of release-candidate
status. The extension has been continuously dogfooded against
shima-enaga's real ledger across the v0.2.0-rc1, rc2, and rc3
windows; tool-call interception, subagent chain auto-assignment,
bash snapshot-and-diff, and structured-metadata writing all run
cleanly against the v0.1.5 kernel.

#### Stable surface (v0.2.x contract)

- **pi extension** (`adapters/pi/agent-ledger.ts`): `tool_call`
  and `tool_result` interception for `Edit`, `Write`, `MultiEdit`,
  `Bash`, and `subagent`. Pre-claims paths, blocks on claim
  failure, records after edits, snapshots `git status` around
  bash. Subagent dispatches auto-create child tasks and inject
  `AGENT_LEDGER_TASK_ID` / `AGENT_LEDGER_PARENT_TASK_ID` into the
  child env so the chain follows automatically.
- **Cross-harness env var contract** (`docs/adapters.md`):
  `AGENT_ID`, `AGENT_LEDGER_TASK_ID`, `AGENT_LEDGER_PARENT_TASK_ID`,
  `AGENT_LEDGER_DIR`, `AGENT_LEDGER_REQUIRE_TASK`,
  `AGENT_LEDGER_AUTO_ASSIGN_POLICY`, `AGENT_LEDGER_AUTO_ASSIGN_ALLOW`,
  `AGENT_LEDGER_REASON`, `AGENT_LEDGER_DETECT_PR`. Renames are
  breaking under semver.
- **Shared session bootstrap** (`adapters/shared/session-bootstrap.sh`):
  six-source resolution chain (flag, env, pr, branch, detached,
  auto), structured-metadata writing via `assign --metadata` when
  the kernel supports it, idempotent `assign --if-absent` retries.
- **Marker helpers** (`adapters/shared/marker.sh`, `marker.js`):
  reason-text audit markers (`[auto-assigned by ...]` and
  `[harness-derived by ...]`) preserved as forward-compat
  fallback for v0.1.0 ledgers.

#### Experimental, opt-in (NOT in v0.2.x contract)

- **babysitter wrapper** (`adapters/babysitter/define-ledger-task.js`)
  ships in the repo for users who want to invoke it from a
  Babysitter process file. The wrapper has not been dogfooded at
  scale; its CLI surface, env-var convention, and chain-of-tasks
  shape may change in v0.3+. v0.2.x makes no contract guarantee
  about it. Marked experimental in `adapters/README.md` and
  `adapters/babysitter/README.md`.

#### Recommended kernel pairing

v0.2.0 adapters work against `agent-ledger` v0.1.0 through v0.1.5.
Recommended pair is the latest stable kernel (v0.1.5 at v0.2.0
tag time, includes the `AUTO_ASSIGNED_TASK` verify finding for
adapter-derived assignments). Older kernels lose only the
structured-metadata audit surface; the reason-text marker
fallback remains.

## [0.1.5] - 2026-04-29

### Verify: AUTO_ASSIGNED_TASK finding

- New `AUTO_ASSIGNED_TASK` finding code (severity `warning`) fires
  when an assignment exists for a verified task but was created by
  an adapter's auto-derivation path. Detection keys on the v0.1.1+
  structured signal `metadata.auto_assigned == true`; falls back to
  a leading `[auto-assigned by ...]` or `[harness-derived by ...]`
  reason marker for ledgers written by older adapter versions.
- `MISSING_ASSIGNMENT` keeps its v0.1.0 semantics (no assignment
  row at all). The two findings are complementary: explicit-but-no-
  row → `MISSING_ASSIGNMENT`; row-exists-but-adapter-derived →
  `AUTO_ASSIGNED_TASK`.
- `docs/finding-codes.md` documents the new code as an additive
  v0.1.5 extension (not a SPEC §19.3 contract code).


## [0.1.4] - 2026-04-29

### Kernel: integrity scan command

- New `agent-ledger scan` top-level command walks every JSON-bearing
  column in the ledger (`agents`, `assignments`, `intents`,
  `changes`, `validations`, `conflicts`, `events`) and reports any
  row whose `metadata_json`, paths columns, or `payload_json` fails
  to decode. Aggregates issues across all rows instead of aborting
  on the first failure. Output as text (per-row detail with table /
  column / row id / decode message) or JSON
  (`agent-ledger.scan.v1`).
- Exits 0 on a clean scan, 3 (`ExitStorageIO`) when any corrupt row
  is found OR when the underlying query fails.
- Backed by new `domain.IntegrityScan(ctx)` returning a structured
  `IntegrityReport{Tables, Issues}`. Re-uses the typed
  `MetadataDecodeError` and `PathsDecodeError` introduced in v0.1.3
  so the scan reports the same root cause the routine readers
  surface.
- Tests: clean ledger → exit 0 + zero issues; deliberately corrupted
  rows across 5 tables → exit 3 + every corruption reported in one
  invocation; text output includes per-row detail.


## [0.2.0-rc3] - 2026-04-28

### Adapters: catch up with v0.1.1 kernel surface

v0.2.0-rc3 labels the adapter source that has been tracking main
since the v0.1.1 kernel landed. No new adapter functionality beyond
rc2; this rc records the bootstrap and test changes that landed
alongside the v0.1.1 kernel so dogfooders can pin a specific
adapter cut against the current kernel.

#### Changed

- `agent-ledger/adapters/shared/session-bootstrap.sh` writes
  structured metadata (`auto_assigned`, `auto_assigned_by`,
  `task_source`, `parent_task`) via the v0.1.1+ `assign --metadata`
  flag. Probe-and-fallback against v0.1.0 binaries: if the kernel
  does not advertise `--metadata`, the bootstrap omits it and the
  reason marker carries the audit signal as before.
- `agent-ledger/adapters/shared/session-bootstrap.sh` calls
  `assign --if-absent` for harness-derived and auto-fallback
  sources so repeated pi launches on the same branch do not create
  duplicate `task.assigned` events.
- `agent-ledger/adapters/tests/run.sh` exercises both behaviours
  end-to-end against a real (test-ledger) `agent-ledger` binary.
- Documentation refresh in `docs/adapters.md` aligned with the
  v0.1.1+ kernel surface.

#### Compatibility

Works against `agent-ledger` v0.1.0 (reason-marker-only audit) and
v0.1.1+ (structured metadata + reason marker). Recommended pair is
the latest stable kernel; current latest is `v0.1.3`.

## [0.1.3] - 2026-04-28

### Kernel: typed metadata decode error

- Assignment, intent, conflict, change, and validation readers in
  `internal/domain` now return a typed `*domain.MetadataDecodeError`
  when a row's `metadata_json` column fails to parse as JSON.
  Pre-v0.1.3 the kernel silently replaced the field with an empty
  map, hiding ledger corruption from reviewers.
- The error carries `Field` (table.column), `RowID` (the row's
  primary key), `Raw` (truncated payload, max 200 bytes), and the
  underlying decode error.
- CLI handlers (`claim`, `assign`, `assign --if-absent`, `adopt`,
  `assignments`) map the error to `ExitStorageIO` with code
  `metadata_decode_failed` and details pointing at the corrupted
  row.
- Empty or unset metadata still returns an empty map without raising
  the error, matching pre-v0.1.3 behaviour for the happy path.
- `verify`'s storage-error path inherits the propagated message so
  metadata corruption surfaces in `agent-ledger.verify.v1` reports
  with `status: "error"` / `code: "STORAGE_ERROR"`.

### Documentation

- `docs/adapters.md` audit-trail section restructured around the
  current kernel surface (v0.1.1+ structured metadata as the
  canonical query path; reason marker as forward-compat fallback).
  Removed pre-v0.1.1 "the v0.1 kernel does not yet have --metadata"
  and "v0.2 of the kernel will add --metadata" claims that became
  stale when v0.1.1 shipped.
- `docs/adapters.md` kernel dependencies section now records the
  v0.1.1, v0.1.2, and v0.1.3 deltas separately so a reader can
  trace which feature lives in which release.
- v0.1.0 "Known limitations" entry for `TestConcurrent_ExclusivePolicy`
  rewritten as past tense with a closure pointer to v0.1.2.

## [0.1.2] - 2026-04-28

### Kernel: claim race fix

Closes the concurrent-claim race under both `warn` and `exclusive`
policies. Pre-v0.1.2, two simultaneous `claim` calls on the same
path could both pass overlap detection before either wrote its
intent; under `exclusive` policy this produced two winners. The fix
moves overlap lookup, conflict resolution, and intent insert into a
single SQLite `BEGIN IMMEDIATE` transaction, so the second writer
sees the first writer's intent on its overlap query.

#### Changed

- `internal/storage/sqlite.Store` exposes `WriteDomainEventImmediate`
  and `ResolveAndInsertIntent`. The claim flow now runs through the
  latter so overlap detection, supersede ordering, conflict-record
  writes, and intent insert are all atomic against concurrent
  writers.
- Supersede ordering (C1 from PR #10 review): the `--supersede`
  step now runs inside the immediate transaction and only on
  non-Block decisions, so an aborted exclusive claim does not
  leave a closed-then-untaken superseded chain.
- `superseded_by` back-reference (C2): the forward link from the
  old intent to the new intent is restored atomically with the
  insert of the new intent.
- New shared `internal/policy` leaf package houses policy and
  conflict constants previously duplicated across packages (S3).
- `conflicts.Decision` gains an `Unset` zero value so error
  returns no longer fall through to a permissive Allow (S2).
- `tests/integration/concurrent_test.TestConcurrent_ExclusivePolicy`
  is no longer skipped and passes under `-race`.

## [0.1.1] - 2026-04-28

### Kernel: structured audit + assignment invariant

This is a kernel-only patch alongside the v0.2.0-rc2 adapters: it
adds the structured audit surface and assignment uniqueness
guarantee the adapters depend on. The adapter scaffold
(v0.2.0-rc2) keeps working against v0.1.0 binaries via the existing
reason-text marker fallback, but `agent-ledger >= 0.1.1` is
recommended on the same machine that runs the pi extension.

#### Added

- `agent-ledger assign --metadata <json>`: optional JSON object
  flag merged into the assignment's `metadata_json` column. Adapters
  use this to write structured `auto_assigned`, `auto_assigned_by`,
  `task_source`, and `parent_task` fields so reviewers can query
  the audit trail without regex-matching the reason text.
- `agent-ledger assignments [--task <id>] [--orchestrator <id>]
  [--agent <id>] [--status active|superseded|closed|all] [--limit
  <n>] [--json]`: new query command for the historical contract
  surface. JSON output includes a `reason_marker` field that
  classifies each row as `auto`, `harness-derived`, or `explicit`.
- `internal/migrations/embed/0002_unique_active_assignment.sql`:
  partial unique index on `assignments(task_id, assigned_agent_id)
  WHERE status='active'`. Migration includes a deterministic
  preflight that demotes pre-existing duplicate active rows to
  `status='superseded'` (keeping the most-recent row active) so
  ledgers carrying duplicates from the v0.1.0 F9 race upgrade
  cleanly.

#### Changed (breaking)

- Plain `agent-ledger assign` is now strict: a second active
  assignment for the same `(task_id, assigned_agent_id)` returns
  `ExitConflict (4)` with code `assignment_exists`. Use
  `--if-absent` for idempotent replay or close the prior assignment
  first. This closes F9 from PR #8 and matches SPEC §16's intent
  that an assignment is the orchestrator's contract for a task.
- `--if-absent`'s replay predicate now compares
  `(task, agent, orchestrator, policy, reason, allow, forbid,
  metadata)` rather than just `(task, agent, policy, allow, forbid)`.
  Audit-bearing metadata cannot be silently reused under a stale
  older row that lacked it. Programmatic callers that depended on
  the looser predicate must either supply matching metadata or
  close the prior assignment.
- `--if-absent` now retries its lookup if the unique index races it,
  so true concurrent bootstraps reuse the winner's assignment
  cleanly instead of one process exiting non-zero.

#### Adapter changes (compatible with rc2)

- `agent-ledger/adapters/shared/session-bootstrap.sh` probes
  `agent-ledger assign --help` for `--metadata` support and writes
  structured metadata when available. Falls back silently against
  v0.1.0 kernels (reason marker only).
- The bootstrap now passes `--if-absent` for harness-derived and
  auto-fallback assignments so idempotent replays do not multiply
  rows.
- `agent-ledger/docs/adapters.md` audit-query examples switched
  from raw SQLite reads to `agent-ledger assignments` invocations.

## [0.2.0-rc2] - 2026-04-27

### Phase 2 adapters: harness-aware task id resolution

The pi extension and shared bootstrap now derive the task id from
git context the harness already knows, rather than requiring the
human to pre-set `AGENT_LEDGER_TASK_ID` before launching pi. The
rc1 "orchestrator did not pre-assign" warning fired on every fresh
human session, even though the human IS the orchestrator and the
branch they were sitting on already named the task. This release
fixes that.

#### Changed

- `agent-ledger/adapters/shared/session-bootstrap.sh` now resolves
  the task id through a six-step chain: explicit flag, env var,
  optional PR detection (`--detect-pr 1` or
  `AGENT_LEDGER_DETECT_PR=1`), git branch, detached HEAD
  (`detached/<short-sha>`), then the auto-fallback as a true last
  resort. New `--cwd <dir>` flag scopes git detection to a specific
  directory so callers can drive bootstrap against a project root
  that differs from the script's cwd.
- The bootstrap exports `AGENT_LEDGER_TASK_SOURCE` with the
  resolution path used (`flag`, `env`, `pr`, `branch`, `detached`,
  or `auto`) and only sets `AGENT_LEDGER_AUTO_ASSIGNED=1` when the
  source is `auto`.
- `agent-ledger/adapters/pi/agent-ledger.ts` passes
  `--cwd $(process.cwd())` and (when `AGENT_LEDGER_DETECT_PR=1`)
  `--detect-pr 1` to bootstrap, reads
  `AGENT_LEDGER_TASK_SOURCE`, and only surfaces the warning toast
  when source=auto. Branch- and PR-derived sessions are silent
  beyond a single stderr log line documenting the source.
- The shared marker helpers (`marker.sh`, `marker.js`) accept a
  `--source` parameter. For `branch`, `pr`, and `detached` they emit
  a new `[harness-derived by <by> source=<source> ...]` reason
  prefix. The `[auto-assigned by ...]` prefix is preserved for the
  auto-fallback path so existing rc1 audit queries still work.

#### Why

The rc1 toast was content-correct but design-wrong. A human running
`pi` on a feature branch is not "forgetting" to assign; the branch
name and the PR number are the task. The harness should pull that
context for them.

The four-tier source taxonomy (explicit, harness-derived, auto,
blocked-by-require) gives reviewers a clean audit query:
`reason LIKE '[auto-assigned%'` finds true context-less sessions;
`reason LIKE '[harness-derived%'` finds normal harness-managed
sessions and groups them by source.

### Review remediation (PR #8)
- F1: PR detection now scopes `gh pr view` to the target cwd; `gh -R <path>` was misusing the repo flag.
- F2: shell-export mode emits `AGENT_LEDGER_AUTO_ASSIGNED=0|1` to match JSON mode.
- F3: adapter tests cover PR detection success and failure fallthrough.
- F4: branch detection uses `git symbolic-ref` first so unborn branches resolve.
- F5: clarified that `AGENT_LEDGER_REQUIRE_TASK=1` blocks only the auto fallback.
- F6: tightened `TaskSource` union and added tolerant `parseTaskSource`.
- F7: removed dead no-op in the explicit task source case.
- F8: renamed `buildAutoAssignedMarker` to `buildAssignmentMarker`.
- F9: added `agent-ledger assign --if-absent` and routed bootstrap through it for harness-derived sources.

## [0.2.0-rc1] - 2026-04-27

### Phase 2 adapters (scaffold)

Deterministic, workflow-wrapped enforcement of `agent-ledger`
discipline for pi and Babysitter. Drops the reliance on AGENTS.md
guidance for agents to remember the claim/record cycle.

#### Added

- `agent-ledger/adapters/pi/agent-ledger.ts`: pi extension that
  hooks `tool_call` and `tool_result` for `Edit`, `Write`,
  `MultiEdit`, `Bash`, and `subagent`. Pre-claims paths before edits
  (blocking on failure), records after edits, snapshots and diffs
  `git status` around bash calls, and auto-assigns child tasks for
  every dispatched subagent so the chain is followed without
  orchestrator intervention.
- `agent-ledger/adapters/pi/install.sh`: idempotent installer that
  symlinks the extension and shared helpers into
  `~/.pi/agent/extensions/`.
- `agent-ledger/adapters/babysitter/define-ledger-task.js`:
  higher-order replacement for the SDK's `defineTask`. Wraps every
  agent task with a shell pre-step (`agent-ledger assign`) and a
  shell post-step (`agent-ledger verify`); injects task identity into
  `execution.env` so worker subagents inherit the discipline.
- `agent-ledger/adapters/shared/session-bootstrap.sh`: idempotent
  identify + ensure-assignment shell helper used by every adapter.
- `agent-ledger/adapters/shared/marker.sh` and `marker.js`: shared
  auto-assignment reason marker helpers for v0.1 audit trails.
- `agent-ledger/adapters/tests/run.sh`: lightweight adapter smoke
  tests using `node --test` and shell stubs, wired into `make check`.
- `agent-ledger/docs/adapters.md`: cross-harness env var contract
  (`AGENT_ID`, `AGENT_LEDGER_TASK_ID`, `AGENT_LEDGER_PARENT_TASK_ID`,
  `AGENT_LEDGER_REQUIRE_TASK`, etc.), auto-assignment design, and
  per-adapter behaviour reference.

#### Design notes

- Missing task id is solved by auto-derivation with a v0.1 audit
  marker in the assignment reason, not by fail-closed-by-default.
  Operators who want strict enforcement opt in via
  `AGENT_LEDGER_REQUIRE_TASK=1`.
- Subagent inheritance is enforced by the parent pi extension
  intercepting the `subagent` tool call, auto-assigning a child
  task, and injecting `AGENT_LEDGER_TASK_ID` /
  `AGENT_LEDGER_PARENT_TASK_ID` into the subagent's env.
- Bash mutations are handled by warn, pre-snapshot, and post-scan by
  default (`git status --porcelain`); `AGENT_LEDGER_BASH_MODE=block`
  blocks bash entirely because shell mutation detection is incomplete.

## [0.1.0] - 2026-04-27

### Phase 1 kernel slice

The first complete kernel implementation. Harness-neutral: pi, Claude
Code, and Babysitter adapters arrive in later phases.

#### Added

- Go static CLI `agent-ledger` with all 15 Phase 1 subcommands:
  `init`, `identify`, `assign`, `claim`, `heartbeat`, `record`,
  `close`, `status`, `verify`, `conflicts`, `adopt`,
  `export-summary`, `gc`, `migrate`, `doctor`. Built with
  `spf13/cobra` and `CGO_ENABLED=0`.
- Pure-Go SQLite storage (`modernc.org/sqlite`) with WAL,
  `synchronous=NORMAL`, `foreign_keys=ON`, and `busy_timeout=5000`.
  Idempotent migrations cover every Phase 1 table from SPEC §11.
- Append-only JSONL audit mirror with daily UTC rotation. Every
  domain write produces a domain row, an `events` row, and an audit
  line in one transaction (audit best-effort post-commit per SPEC §28).
- ULID-based identifiers (`<type>_<ulid>`), RFC3339 UTC timestamps,
  64-character lowercase hex hashes.
- Privacy-safe persistence by default. `internal/privacy.AssertSafe`
  rejects secret-shaped values (AWS keys, GitHub PATs, OpenAI keys,
  bearer tokens, env-dump shapes) for `reason` inputs at the CLI
  layer, audit JSONL, and exported summaries. Full diffs are stored
  only when `--include-diff --yes` is passed and live in
  content-addressed blobs, not the database.
- `agent-ledger.verify.v1` JSON contract per SPEC §19.2/§19.3:
  flat summary, scalar `path`, `suggested_recovery`, exit codes 0/1
  (pass/fail), 2 (config), 3 (storage), 4 (needs-decision/conflict).
  All 14 finding codes from SPEC §19.3 are emitted.
- `agent-ledger-summary.v1` exported summary schema, with portable
  `path_hash` (NFC sha256 of project-relative display path with
  forward slashes). Cross-checkout summary verification works against
  any working tree without local ledger state.
- `agent-ledger.doctor.v1` health output covering project identity,
  ledger directory writability, git detection, pointer file validity,
  policy file validity, lock support, adapter env vars, SQLite
  pragmas, and migrations.
- `gc --stale-after <duration>` orphans active intents whose last
  heartbeat is older than the cutoff. Idempotent. History is never
  deleted.
- Project fingerprinting: 24-character lowercase hex SHA-256 prefix
  of git common-dir realpath plus origin metadata. Worktrees of one
  repository share a fingerprint; separate clones get distinct
  fingerprints.
- XDG-aware ledger resolution: `$AGENT_LEDGER_DIR` overrides the
  default `${XDG_STATE_HOME:-$HOME/.local/state}/agent-ledger/repos/<slug>-<fingerprint>/`.
  Local `.agent-ledger.toml` and `.agent-ledger-policy.toml` pointer
  files are honored.
- Exit code constants per SPEC §19.1 in `internal/cli/exitcodes.go`.
  Codes 0-5 follow the spec exactly; 6-12 are documented internal
  extensions outside the public contract.
- Best-effort advisory file locks via `gofrs/flock` for exclusive
  claim semantics. Lock state is reported by `verify` as
  `EXCLUSIVE_LOCK_HELD` when relevant.
- `make build` injects `Version`, `Commit`, and `BuildDate` via
  `-ldflags -X` from `git describe`, `git rev-parse --short HEAD`,
  and the build timestamp.
- GoReleaser snapshot config for darwin/linux × arm64/amd64 archives.
  Local-only (no publishing, no secrets). Homebrew tap and signing
  deferred to Phase 5.
- GitHub Actions workflows at the monorepo root, name-prefixed
  `agent-ledger-*` and `paths:`-scoped to `agent-ledger/`:
  `agent-ledger-ci.yml` runs `make check`, a 4-target cross-build
  matrix, and a `go test -race` job;
  `agent-ledger-release-snapshot.yml` exercises GoReleaser on every
  pull request without publishing;
  `agent-ledger-release.yml` publishes a real GitHub Release on `v*`
  tag push using the auto-provided `GITHUB_TOKEN`. No
  user-configured secrets are required at any stage.
- Subprocess integration test suite under `tests/integration/`
  covering concurrent claims (disjoint, warn, exclusive), secret
  fixture leak scans, stable verify and summary JSON schemas,
  separate-clone fingerprint distinctness, worktree pointer
  discovery, and cross-root summary portability.

#### Documentation

- README with concentric-circles structure (what / quick start /
  configuration / usage / development / architecture).
- `docs/packaging.md` for build, archive, and CI details.
- `docs/walkthrough.md` for the full lifecycle end-to-end.
- `docs/exit-codes.md` listing every exit code emitted by the CLI.
- `docs/finding-codes.md` listing every `verify` finding code.
- `SPEC.md` is the authoritative design spec for the kernel and the
  later adapter phases.

#### Known limitations

- Adapters for pi, Claude Code, and Babysitter are not shipped yet.
  Use the CLI directly. See SPEC §32 for the post-Phase-1 roadmap.
- The local SQLite store retains plain-text `reason` fields for
  debugging. `AssertSafe` is pattern-based, not cryptographic; agents
  must not embed secrets in reason fields regardless of the guard.
  A scrubbing pass is on the Phase 5 backlog.
- The `claim` command runs `conflicts.Resolve` outside the intent
  insert transaction, so two concurrent claims on the same path under
  `exclusive` can both pass the overlap check before either writes.
  Exclusive claims serialized through one orchestrator are unaffected;
  the bug surfaces only for true concurrent races. **Closed in
  v0.1.2**: conflict detection now runs inside the `InsertIntent`
  `BEGIN IMMEDIATE` transaction, and `TestConcurrent_ExclusivePolicy`
  passes under `-race`.
- Release archives are unsigned. Verify downloads against the
  published `*_checksums.txt` file. A Homebrew tap with signed
  binaries is a Phase 5 deliverable.

[Unreleased]: https://github.com/ruminaider/agent-clis/compare/v0.4.1...HEAD
[0.4.1]: https://github.com/ruminaider/agent-clis/releases/tag/v0.4.1
[0.4.0]: https://github.com/ruminaider/agent-clis/releases/tag/v0.4.0
[0.3.0]: https://github.com/ruminaider/agent-clis/releases/tag/v0.3.0
[0.2.0]: https://github.com/ruminaider/agent-clis/releases/tag/v0.2.0
[0.1.5]: https://github.com/ruminaider/agent-clis/releases/tag/v0.1.5
[0.1.4]: https://github.com/ruminaider/agent-clis/releases/tag/v0.1.4
[0.2.0-rc3]: https://github.com/ruminaider/agent-clis/releases/tag/v0.2.0-rc3
[0.1.3]: https://github.com/ruminaider/agent-clis/releases/tag/v0.1.3
[0.1.2]: https://github.com/ruminaider/agent-clis/releases/tag/v0.1.2
[0.1.1]: https://github.com/ruminaider/agent-clis/releases/tag/v0.1.1
[0.2.0-rc2]: https://github.com/ruminaider/agent-clis/releases/tag/v0.2.0-rc2
[0.2.0-rc1]: https://github.com/ruminaider/agent-clis/releases/tag/v0.2.0-rc1
[0.1.0]: https://github.com/ruminaider/agent-clis/releases/tag/v0.1.0
