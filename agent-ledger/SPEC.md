# Agent Ledger Implementation Spec

## 1. Purpose

Agent Ledger is a local coordination kernel for agentic coding workflows. It records which agent was assigned a task, which files the agent intended to edit, which files changed, why they changed, and whether those changes stayed within scope.

The tool targets pi, Claude Code, Babysitter, and generic agent harnesses. The core must stay harness-neutral. Harness integrations are adapters around the same CLI, storage model, and verification contract.

## 2. Goals

Scope terms used throughout this spec:

- **Phase 1 kernel slice:** the first buildable CLI-only release.
- **Product MVP:** the usable pre-hardening product that includes the kernel, pi adapter, Babysitter summary flow, and Claude Code adapter.
- **Post-MVP:** hardening and expansion after the Product MVP.

1. Attribute local file changes to an agent, task, and reason.
2. Detect overlapping work before it becomes a merge or review surprise.
3. Let gate reviewers and CI verify that changed files match assigned scope.
4. Work across multiple git worktrees and avoid collisions across multiple local clones in MVP. Full cross-clone coordination is post-MVP.
5. Preserve privacy by default. Do not store full diffs unless explicitly requested.
6. Recover cleanly from crashes, missed claims, stale claims, human edits, and formatter edits.
7. Provide a fast static CLI suitable for frequent hook invocations.
8. Expose a stable `verify --json` contract for pi, Claude Code, Babysitter, and other harnesses.

## 3. Non-goals

1. Agent Ledger is not a tamper-proof security audit log in MVP.
2. Agent Ledger does not prevent malicious agents from forging `AGENT_ID`.
3. Agent Ledger does not replace git history, code review, or test validation.
4. Agent Ledger does not implement line-level ownership in MVP.
5. Agent Ledger does not require a remote service in MVP.

## 4. Trust model

MVP targets honest-but-forgetful agents and humans. `AGENT_ID` is cooperative metadata, usually supplied through environment variables or a harness adapter. A determined actor can forge it.

Later versions may add event signing, OS keychain-backed session keys, hash-chained audit logs, or a remote append-only service. These are out of scope for MVP.

## 5. Terminology

| Term | Meaning |
|---|---|
| Agent | A coding agent, reviewer, orchestrator, or harness-controlled process. |
| Orchestrator | The process that assigns tasks and owns scope decisions. Examples: pi main agent, Babysitter. |
| Assignment | The orchestrator-written contract for a task: allowed paths, forbidden paths, assigned agent, and policy. |
| Intent | A worker-written pre-edit claim for a task. It references an assignment when an orchestrator has created one. |
| Change | A post-edit record with path hashes, patch hash, summary, and validations. |
| Claim | The active state created by an intent. Claims may heartbeat and later close. |
| Conflict | An overlapping claim or change that violates or challenges the active policy. |
| Ledger | The SQLite database plus append-only audit mirror and blob store. |

## 6. Architecture

Agent Ledger has three layers:

1. **Kernel CLI:** a static executable named `agent-ledger`.
2. **Adapters:** pi extension, Claude Code hooks, Babysitter scripts, and generic shell wrappers.
3. **Storage:** SQLite WAL primary store, JSONL audit mirror, and optional content-addressed blobs.

The kernel owns all schema, validation, conflict detection, path normalization, and verification. Adapters must not implement their own rules except for harness-specific event capture.

## 7. Implementation language

Build the MVP in Go.

Reasons:

1. Single static binary distribution.
2. Low startup latency for PreToolUse/PostToolUse hooks.
3. Straightforward cross-platform packaging.
4. Good SQLite, file-locking, JSON, and CLI support.

Rust is out of scope for MVP unless this spec is amended. Python and Node should not be used for the hook-critical kernel because startup latency will be paid on every edit/write operation.

## 8. Storage resolution

Resolve the ledger directory in this order:

1. `$AGENT_LEDGER_DIR`, if set.
2. `ledger_dir` in the local `.agent-ledger.toml` pointer at the project root.
3. XDG state directory keyed by project fingerprint.
4. Git common dir fallback or symlink for discoverability.

Default XDG path:

```text
$XDG_STATE_HOME/agent-ledger/repos/<project-slug>-<project-fingerprint>/
```

If `XDG_STATE_HOME` is unset:

```text
~/.local/state/agent-ledger/repos/<project-slug>-<project-fingerprint>/
```

On macOS, an installer may choose platform-native state storage, but the CLI must support the XDG path consistently.

### 8.1 Project fingerprint

Compute the project fingerprint from shared project identity, not from an individual worktree root. Git worktrees that share a common git directory must share one ledger. Separate clones intentionally get separate local ledgers in the Product MVP.

A single ledger spans every worktree of the same git common dir. Path scope checks (§14) accept any path inside any worktree, and `canonical_path_hash` collapses the same logical file across worktrees into a single equality key so conflict detection works regardless of which checkout an agent edits from.

Inputs:

1. Optional explicit `project_id` from the local `.agent-ledger.toml` pointer or committed `.agent-ledger-policy.toml`.
2. Git remote origin URL, if present.
3. Git common dir realpath, if present.
4. Project root realpath only for non-git projects.

Canonical string:

```text
project_id=<project-id-or-empty>\norigin=<origin-url-or-empty>\ngit_common_dir=<git-common-dir-realpath-or-empty>\nroot=<realpath-root-for-non-git-projects-only-or-empty>\n
```

Fingerprint:

```text
sha256(canonical-string)[0:24]
```

Store `project_fingerprint` as the raw 24-character hash. Store a separate `project_slug` for display, sanitized from `project_id` or origin. Example:

```text
project_fingerprint = "4bd6f86196d0f41a76aa1a88"
project_slug = "recora-health-shima-enaga"
```

### 8.2 Project pointer file

Create a gitignored project pointer file:

```toml
# .agent-ledger.toml
version = 1
project_id = "github.com/recora-health/shima-enaga"
ledger_dir = "/Users/albert/.local/state/agent-ledger/repos/recora-health-shima-enaga-4bd6f86196d0f41a76aa1a88"
# Optional. When set and no harness-derived task id is available
# (no PR, branch, or detached HEAD), adapters use this value as
# the session task id and mark TASK_SOURCE=pointer. Right answer
# for non-git ambient projects where multiple concurrent sessions
# should attribute to one task.
default_task_id = "exploration-2026-05"
```

The pointer makes the ledger discoverable from the working tree. It must not contain secrets.

### 8.3 Git common dir pointer

When inside a git repo, create a best-effort pointer under the git common dir:

```text
$(git rev-parse --git-common-dir)/agent-ledger -> <resolved-ledger-dir>
```

This pointer is shared by all worktrees for the same git common dir. It exists for discoverability only; the XDG ledger directory remains the primary store. If symlinks are unavailable, write a small `pointer.toml` file instead.

## 9. Storage layout

```text
<ledger-dir>/
  ledger.sqlite
  audit/
    2026-04-27.jsonl
  blobs/
    sha256/
      ab/
        abc123...
  locks/
    <path-hash>.lock
  config.toml
```

SQLite is the queryable source of truth. JSONL is an append-only audit mirror for debugging and manual inspection. Blobs store optional large payloads, such as scrubbed patch text, when explicitly enabled.

## 10. SQLite configuration

Use WAL mode:

```sql
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
PRAGMA foreign_keys=ON;
PRAGMA busy_timeout=5000;
```

Every CLI command that writes events must use a transaction. Commands must retry transient busy errors until `busy_timeout` expires, then fail with a storage error.

## 11. SQLite schema

### 11.1 `schema_migrations`

```sql
CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);
```

### 11.2 `agents`

```sql
CREATE TABLE agents (
  agent_id TEXT PRIMARY KEY,
  agent_kind TEXT NOT NULL,
  harness TEXT,
  model TEXT,
  parent_agent_id TEXT,
  orchestrator_id TEXT,
  started_at TEXT NOT NULL,
  ended_at TEXT,
  metadata_json TEXT NOT NULL DEFAULT '{}'
);
```

### 11.3 `assignments`

```sql
CREATE TABLE assignments (
  assignment_id TEXT PRIMARY KEY,
  event_id TEXT NOT NULL UNIQUE,
  task_id TEXT NOT NULL,
  orchestrator_id TEXT NOT NULL,
  assigned_agent_id TEXT,
  harness_run_id TEXT,
  branch TEXT,
  base_sha TEXT,
  allowed_paths_json TEXT NOT NULL,
  forbidden_paths_json TEXT NOT NULL DEFAULT '[]',
  conflict_policy TEXT NOT NULL,
  reason TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  created_at TEXT NOT NULL,
  closed_at TEXT,
  metadata_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_assignments_task_id ON assignments(task_id);
CREATE INDEX idx_assignments_agent_id ON assignments(assigned_agent_id);
```

Allowed statuses:

```text
active, completed, abandoned, superseded
```

In the Phase 1 kernel slice, only `active` is reachable through CLI commands. Assignment closure is post-MVP through `assignment.closed`.

### 11.4 `intents`

```sql
CREATE TABLE intents (
  intent_id TEXT PRIMARY KEY,
  event_id TEXT NOT NULL UNIQUE,
  assignment_id TEXT,
  task_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  access_mode TEXT NOT NULL,
  conflict_policy TEXT NOT NULL,
  reason TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  opened_at TEXT NOT NULL,
  last_heartbeat_at TEXT,
  heartbeat_expires_at TEXT,
  closed_at TEXT,
  close_outcome TEXT,
  close_reason TEXT,
  metadata_json TEXT NOT NULL DEFAULT '{}',
  FOREIGN KEY (assignment_id) REFERENCES assignments(assignment_id),
  FOREIGN KEY (agent_id) REFERENCES agents(agent_id)
);

CREATE INDEX idx_intents_task_id ON intents(task_id);
CREATE INDEX idx_intents_agent_id ON intents(agent_id);
CREATE INDEX idx_intents_status ON intents(status);
```

Allowed access modes:

```text
observe, read, write, review-only
```

Allowed statuses:

```text
active, closed, orphaned
```

`completed`, `abandoned`, and `superseded` are close outcomes stored in `close_outcome`, not intent statuses. Supersession writes `intent.superseded`, closes the old intent with `close_outcome = superseded`, and may open a replacement intent.

### 11.5 `intent_paths`

```sql
CREATE TABLE intent_paths (
  intent_id TEXT NOT NULL,
  path TEXT NOT NULL,
  realpath TEXT NOT NULL,
  path_hash TEXT NOT NULL,
  access_mode TEXT NOT NULL,
  PRIMARY KEY (intent_id, path_hash),
  FOREIGN KEY (intent_id) REFERENCES intents(intent_id)
);

CREATE INDEX idx_intent_paths_path_hash ON intent_paths(path_hash);
CREATE INDEX idx_intent_paths_path ON intent_paths(path);
```

### 11.6 `changes`

```sql
CREATE TABLE changes (
  change_id TEXT PRIMARY KEY,
  event_id TEXT NOT NULL UNIQUE,
  intent_id TEXT,
  assignment_id TEXT,
  task_id TEXT NOT NULL,
  agent_id TEXT,
  actor_kind TEXT NOT NULL,
  summary TEXT NOT NULL,
  created_at TEXT NOT NULL,
  commit_sha TEXT,
  metadata_json TEXT NOT NULL DEFAULT '{}',
  FOREIGN KEY (intent_id) REFERENCES intents(intent_id),
  FOREIGN KEY (assignment_id) REFERENCES assignments(assignment_id)
);

CREATE INDEX idx_changes_task_id ON changes(task_id);
CREATE INDEX idx_changes_agent_id ON changes(agent_id);
CREATE INDEX idx_changes_created_at ON changes(created_at);
```

Allowed actor kinds:

```text
agent, human, unknown, formatter, ide, hook
```

### 11.7 `change_paths`

```sql
CREATE TABLE change_paths (
  change_id TEXT NOT NULL,
  path TEXT NOT NULL,
  realpath TEXT NOT NULL,
  path_hash TEXT NOT NULL,
  before_sha256 TEXT,
  after_sha256 TEXT,
  patch_sha256 TEXT,
  line_ranges_json TEXT NOT NULL DEFAULT '[]',
  status TEXT NOT NULL,
  PRIMARY KEY (change_id, path_hash),
  FOREIGN KEY (change_id) REFERENCES changes(change_id)
);

CREATE INDEX idx_change_paths_path_hash ON change_paths(path_hash);
CREATE INDEX idx_change_paths_path ON change_paths(path);
```

Allowed path statuses:

```text
added, modified, deleted, renamed, copied, unknown
```

### 11.8 `validations`

```sql
CREATE TABLE validations (
  validation_id TEXT PRIMARY KEY,
  change_id TEXT,
  task_id TEXT NOT NULL,
  command TEXT NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT,
  completed_at TEXT,
  exit_code INTEGER,
  output_ref TEXT,
  metadata_json TEXT NOT NULL DEFAULT '{}',
  FOREIGN KEY (change_id) REFERENCES changes(change_id)
);

CREATE INDEX idx_validations_task_id ON validations(task_id);
```

Allowed statuses:

```text
passed, failed, skipped, unknown
```

### 11.9 `conflicts`

```sql
CREATE TABLE conflicts (
  conflict_id TEXT PRIMARY KEY,
  event_id TEXT NOT NULL UNIQUE,
  path TEXT NOT NULL,
  path_hash TEXT NOT NULL,
  existing_intent_id TEXT,
  new_intent_id TEXT,
  policy TEXT NOT NULL,
  status TEXT NOT NULL,
  detected_at TEXT NOT NULL,
  acknowledged_at TEXT,
  acknowledged_by_agent_id TEXT,
  resolution TEXT,
  metadata_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_conflicts_path_hash ON conflicts(path_hash);
CREATE INDEX idx_conflicts_status ON conflicts(status);
```

Allowed statuses:

```text
detected, acknowledged, ignored, escalated, resolved
```

In the Phase 1 kernel slice, only `detected` and `acknowledged` are reachable through CLI commands. `ignored`, `escalated`, and `resolved` are pre-provisioned for post-MVP workflows.

### 11.10 `events`

```sql
CREATE TABLE events (
  event_id TEXT PRIMARY KEY,
  schema TEXT NOT NULL,
  event_type TEXT NOT NULL,
  created_at TEXT NOT NULL,
  agent_id TEXT,
  task_id TEXT,
  payload_json TEXT NOT NULL,
  payload_ref TEXT,
  payload_sha256 TEXT
);

CREATE INDEX idx_events_type ON events(event_type);
CREATE INDEX idx_events_agent_id ON events(agent_id);
CREATE INDEX idx_events_task_id ON events(task_id);
CREATE INDEX idx_events_created_at ON events(created_at);
```

Every domain table write must also insert one row in `events` and one line in the audit JSONL mirror. `payload_json` is a normalized, privacy-safe domain payload. It must never contain raw hook inputs, raw tool payloads, command output, environment variables, file contents, full diffs, headers, tokens, or secrets.

All `*_at` timestamp columns store RFC 3339 / ISO 8601 UTC strings with a `Z` suffix. All `*_sha256` columns store full 64-character lowercase hex digests. `path_hash` stores the full 64-character lowercase hex digest of `sha256(realpath-normalized)`.

## 12. Event schema

All events share this envelope:

```json
{
  "schema": "agent-ledger.v1",
  "event_id": "evt_01HY...",
  "event_type": "task.assigned",
  "created_at": "2026-04-27T02:30:00Z",
  "project_id": "github.com/recora-health/shima-enaga",
  "project_fingerprint": "4bd6f86196d0f41a76aa1a88",
  "project_slug": "recora-health-shima-enaga",
  "worktree": "/Users/albert/Work/shima-enaga",
  "branch": "worldbuilder/tonic-api-credential-client",
  "agent_id": "pi.main.8c91",
  "task_id": "W2-A",
  "payload": {}
}
```

Use stored IDs shaped as `<type>_<ulid>`, for example `evt_01HY...`, `asg_01HY...`, `int_01HY...`, `chg_01HY...`, and `cfl_01HY...`. The suffix is a ULID, which provides time ordering and low collision risk. `agent-ledger identify` uses the same ULID suffix strategy for the `<short-id>` segment unless an adapter supplies its own stable session suffix.

### 12.1 Required event types for MVP

```text
orchestration.opened
task.assigned
intent.opened
intent.heartbeat
change.recorded
intent.closed
conflict.detected
conflict.acknowledged
intent.orphaned
change.adopted
validation.recorded
intent.superseded
```

### 12.2 Post-MVP event types

```text
conflict.escalated
conflict.resolved
assignment.closed
human.change.detected
unknown.change.detected
commit.created
sync.started
sync.completed
sync.failed
```

## 13. Agent identity

Agent sessions use IDs shaped like:

```text
<harness>.<agent-kind>.<short-id>
```

Examples:

```text
pi.main.8c91
pi.worker.7f3a
pi.gate-reviewer.b12e
claude-code.worker.339b
babysitter.workflow.004a
human.operator.local
```

Adapters should set:

```bash
AGENT_ID=pi.worker.7f3a
AGENT_KIND=worker
AGENT_HARNESS=pi
AGENT_TASK_ID=W2-A
AGENT_ORCHESTRATOR_ID=pi.main.8c91
BABYSITTER_RUN_ID=bsr_123
```

The kernel must not require all variables. Missing values should degrade to `unknown` actor records where possible, except when a command explicitly requires an assignment or task.

## 14. Path normalization

Every path must be normalized before storage.

Rules:

1. Resolve project scope. For git repos, scope is every worktree of the same git common dir; the input path is matched against every worktree toplevel by longest realpath prefix. For non-git projects, scope is the resolved project root.
2. Convert input path to absolute path.
3. Resolve symlinks where possible.
4. Convert to display path relative to the matched scope root, preserving case.
5. Normalize Unicode to NFC.
6. Normalize separators to `/`.
7. Preserve case in `path` (display) and `realpath` columns.
8. Store `canonical_path_hash = sha256(NFC(case-fold(display)))` and use it as the equality key for conflict detection, lock sentinel naming, and lookups across worktrees of the same project.
9. Continue to store the legacy `path_hash = sha256(NFC(realpath))` column as a per-checkout forensic artifact. It is preserved by migrations but is not used as an equality key.

Case-folding uses Unicode case folding, not ASCII lowercase, so it matches the case-insensitivity behavior agents observe on macOS APFS and Windows NTFS by default. Two distinct logical files on a case-insensitive filesystem (`Foo.go` and `foo.go`) cannot coexist; folding the hash matches that constraint instead of silently splitting them into separate ledger rows.

Symlinks within a project that point to another file in the same project no longer collapse under the canonical hash because that hash is computed from display, not realpath. The verifier emits a `SYMLINK_ALIAS` finding when two active rows share a `realpath` value but have distinct `canonical_path_hash` values, so this regression is observable rather than silent.

The verifier must detect paths outside every project scope root and report them as scope violations unless explicitly allowed by assignment metadata.

## 15. Conflict policies

| Policy | Default behavior | Intended use |
|---|---|---|
| `none` | Do not warn or block on overlapping claims. | Low-value generated scratch files or explicitly uncoordinated work. |
| `warn` | Warn on overlap. Allow with acknowledgement. | Most source files. |
| `exclusive` | Block overlapping writes unless orchestrator overrides. Hold OS lock where supported. | Lockfiles, migrations, schema, generated files, PR descriptions, shared config. |

Default policy is `warn`.

`access_mode` describes what the intent wants to do, for example `observe`, `read`, `write`, or `review-only`. `conflict_policy` describes how other overlapping work should be handled, for example `none`, `warn`, or `exclusive`. The enums are intentionally separate: `review-only` is an access constraint, not a conflict policy.

## 16. Claim and conflict behavior

### 16.1 Claim creation

`agent-ledger claim` must:

1. Load the task assignment, if one exists.
2. If no assignment exists and `[defaults].allow_unassigned_intents = false`, write no event, return exit code 4, and emit a `MISSING_ASSIGNMENT` finding to stderr or JSON. If unassigned intents are allowed, create the intent with `metadata_json.assignment_confidence = "unassigned"`; `verify` reports `MISSING_ASSIGNMENT` as a warning unless project policy treats it as allowed.
3. If an assignment exists, verify requested paths are allowed and not forbidden. If any requested path is forbidden or outside `allowed_paths`, write no event, return exit code 4, and emit `FORBIDDEN_PATH_CHANGED` for forbidden paths or `PATH_OUTSIDE_ASSIGNMENT` for paths outside the assignment.
4. Normalize paths.
5. Check active intents for overlapping path hashes.
6. Apply conflict policy.
7. For `exclusive` overlap without orchestrator override: write `conflict.detected`, return exit code 4, and do not create `intent.opened`.
8. For `exclusive` overlap with `--override-conflict <conflict-id>`: require `AGENT_ID` or `--agent` to match the assignment `orchestrator_id`, require the conflict to be acknowledged with override resolution, then create `intent.opened`.
9. For `warn` overlap: create `intent.opened`, write `conflict.detected`, and require acknowledgement before verification passes.
10. For no overlap, create `intent.opened`.
11. For active `exclusive` intents, acquire OS locks for path sentinels before returning success.

### 16.2 Heartbeats

Long-running agents must renew claims:

```bash
agent-ledger heartbeat --intent <intent-id>
```

Adapters should heartbeat automatically every 30 seconds while the agent is active. Default expiration is 2 minutes after the last heartbeat unless configured otherwise.

### 16.3 Crash recovery

If heartbeat expires, the intent becomes stale but not deleted. `agent-ledger gc` or `agent-ledger verify` may mark it:

```text
intent.orphaned
```

Stale claims are recoverable through adoption, closure, or supersession.

## 17. Privacy policy

Default records must not store full diffs, file contents, environment variables, command output, headers, tokens, secrets, raw hook payloads, or raw tool payloads.

Default change records store:

1. Path.
2. Before SHA256.
3. After SHA256.
4. Patch SHA256, if available.
5. Optional line ranges.
6. Human summary.
7. Validation command names and statuses.

Full patch storage requires explicit opt-in:

```bash
agent-ledger record --include-diff
```

When full diff capture is enabled, the CLI must warn unless `--yes` is supplied. Post-MVP should add a configurable secret scrubber.

## 18. CLI commands

### 18.1 `init`

```bash
agent-ledger init [--project-id <id>] [--ledger-dir <path>] [--write-pointer]
```

Creates storage, migrations, config, and optional `.agent-ledger.toml` pointer. Default `--write-pointer` is false for safety; installers and adapters may pass it explicitly.

### 18.2 `identify`

```bash
agent-ledger identify [--agent-kind worker] [--harness pi] [--parent <agent-id>] [--shell]
```

Creates or prints an agent session identity. Outputs shell exports with `--shell`.

### 18.3 `assign`

```bash
agent-ledger assign \
  --task W2-A \
  --orchestrator pi.main.8c91 \
  --agent pi.worker.7f3a \
  --allow src/worldbuilder/credentials/tonic_api.py \
  --forbid 'tests/**' \
  --policy warn \
  --reason 'Implement approved W2-A packet'
```

Writes `task.assigned`. This command is for orchestrators and adapters.

When a pi subagent child writes its own assignment from its session bootstrap, the child uses `--agent <child-agent-id>` for its own identity and `--orchestrator <parent-agent-id>` for the inherited parent identity, and supplies the structured metadata schema described in section 21.1. The child uses only its own `<child-agent-id>` for subsequent `claim`, `record`, `heartbeat`, and `close` events.

### 18.4 `claim`

```bash
agent-ledger claim <path>... \
  --task W2-A \
  --reason 'Edit status mapping' \
  [--access-mode write] \
  [--policy warn] \
  [--supersede <intent-id>] \
  [--override-conflict <conflict-id>] \
  [--agent <agent-id>] \
  [--json]
```

Writes `intent.opened`. Returns `intent_id`. Default `--access-mode` is `write`. If `--supersede` is supplied, also writes `intent.superseded`, closes the old intent with `close_outcome = superseded`, sets `intents.metadata_json.superseded_by = <new-intent-id>` on the old intent, and sets `intents.metadata_json.superseded_intent_id = <old-intent-id>` on the replacement intent.

### 18.5 `heartbeat`

```bash
agent-ledger heartbeat --intent <intent-id>
```

Writes `intent.heartbeat` and extends expiration.

### 18.6 `record`

```bash
agent-ledger record <path>... \
  --intent <intent-id> \
  --summary 'Mapped Tonic statuses to credential errors' \
  [--validation 'uv run ruff check src/worldbuilder/credentials/tonic_api.py:passed'] \
  [--include-diff] \
  [--yes]
```

Writes `change.recorded`. `record` fails with exit code 1 and writes no event if any supplied path is not in the intent's claimed paths; the agent must claim that path first or use `adopt` for retroactive recovery. If `--validation` is supplied, also writes `validation.recorded` for each validation result. The `--validation` argument format is `<command>:<status>`, where status is one of `passed|failed|skipped|unknown`; parse status after the last colon so commands may contain colons. The flag may be repeated. Full diffs from `--include-diff` are written to `blobs/sha256/...`; `--yes` suppresses the privacy warning.

### 18.7 `close`

```bash
agent-ledger close --intent <intent-id> --outcome completed
```

Writes `intent.closed`, sets `intents.status = closed`, stores the supplied value in `close_outcome`, and releases locks.

Allowed close outcomes:

```text
completed, abandoned, superseded
```

### 18.8 `status`

```bash
agent-ledger status
agent-ledger status --path src/foo.py
agent-ledger status --task W2-A
agent-ledger status --json
```

Shows active claims, recent changes, conflicts, and stale intents.

### 18.9 `verify`

```bash
agent-ledger verify --task W2-A --json
agent-ledger verify --summary tasks/agent-ledger/W2-A.json --json
agent-ledger verify --json
```

Produces the stable verification contract. With `--task`, verification is scoped to one task. With `--summary`, verification uses a CI-visible summary file. With neither, verification checks the current project working tree against all active and recently closed intents. See section 19.

### 18.10 `conflicts`

```bash
agent-ledger conflicts
agent-ledger conflicts acknowledge --conflict <id> --reason 'Parallel edits approved by orchestrator' [--as-override]
```

Lists or updates conflict state. `--as-override` marks the conflict as an orchestrator-approved override that a later `claim --override-conflict <id>` may consume by setting `conflicts.resolution = "override"` and `conflicts.metadata_json.override = true`.

### 18.11 `adopt`

```bash
agent-ledger adopt <path>... \
  --task W2-A \
  --agent pi.worker.7f3a \
  --reason 'Backfill missed claim after verifier found unclaimed change'
```

Writes `change.adopted` and a `changes` row with `metadata_json.retroactive = true`. It does not also emit `change.recorded`; adoption is the change event for retroactive recovery. Adoption must be explicit and must preserve that the claim was retroactive.

### 18.12 `export-summary`

```bash
agent-ledger export-summary --task W2-A --output tasks/agent-ledger/W2-A.json
```

Exports a privacy-safe task summary for CI or review.

### 18.13 `gc`

```bash
agent-ledger gc --stale-after 24h
```

Marks stale active intents as orphaned. It must not delete audit history.

### 18.14 `migrate`

```bash
agent-ledger migrate
```

Applies schema migrations.

### 18.15 `doctor`

```bash
agent-ledger doctor
agent-ledger doctor --json
```

Checks storage resolution, SQLite health, git detection, project pointer validity, policy file validity, lock support, and adapter environment variables.

### 18.16 `exec`

```bash
agent-ledger exec --task W2-A --actor-kind formatter -- make format
```

Post-MVP. Runs a command under ledger observation. The command may record before and after working-tree state so formatters, code generators, and other mutating commands can be attributed. In MVP, adapters should warn on mutating commands whose file effects cannot be predicted.

### 18.17 MVP command matrix

| Command | Phase 1 kernel slice | Notes |
|---|---:|---|
| `init` | yes | Creates storage and pointer. |
| `identify` | yes | Creates or prints an agent identity. |
| `assign` | yes | Writes orchestrator assignment. |
| `claim` | yes | Supports `--supersede` in MVP. |
| `heartbeat` | yes | Renews active intents. |
| `record` | yes | Records privacy-safe changes and validation status. |
| `close` | yes | Closes intents with `close_outcome`. |
| `status` | yes | Query active work and recent changes. |
| `verify` | yes | Stable JSON contract. |
| `conflicts` | yes | List and acknowledge conflicts. |
| `adopt` | yes | Retroactive recovery flow. |
| `export-summary` | yes | CI-visible Babysitter summaries. |
| `gc` | yes | Marks stale intents orphaned. |
| `migrate` | yes | Applies schema migrations. |
| `doctor` | yes | Environment and storage diagnostics. |
| `exec` | no | Post-MVP observation wrapper. |

## 19. Verification contract

`agent-ledger verify --json` is the universal contract for adapters and CI.

### 19.1 Exit codes

| Code | Meaning |
|---:|---|
| 0 | Verification passed. |
| 1 | Verification failed. |
| 2 | Configuration error. |
| 3 | Storage or database error. |
| 4 | Conflict requires orchestrator or human decision. |
| 5 | Sync or authentication error. Reserved for future remote sync. |

### 19.2 JSON schema

```json
{
  "schema": "agent-ledger.verify.v1",
  "status": "failed",
  "project_id": "github.com/recora-health/shima-enaga",
  "project_fingerprint": "4bd6f86196d0f41a76aa1a88",
  "project_slug": "recora-health-shima-enaga",
  "task_id": "W2-A",
  "generated_at": "2026-04-27T02:45:00Z",
  "summary": {
    "changed_paths": 2,
    "claimed_paths": 1,
    "unclaimed_paths": 1,
    "forbidden_path_violations": 1,
    "active_conflicts": 0,
    "open_intents": 0,
    "stale_intents": 0
  },
  "findings": [
    {
      "severity": "error",
      "code": "FORBIDDEN_PATH_CHANGED",
      "path": "uv.lock",
      "message": "uv.lock changed but task W2-A forbids this path.",
      "suggested_recovery": "Revert the file or route to orchestrator."
    }
  ]
}
```

Allowed statuses:

```text
passed, failed, needs-decision, error
```

Allowed severities:

```text
info, warning, error, fatal
```

### 19.3 MVP finding codes

```text
UNCLAIMED_CHANGE
FORBIDDEN_PATH_CHANGED
PATH_OUTSIDE_ASSIGNMENT
ACTIVE_CONFLICT
STALE_INTENT
OPEN_INTENT
MISSING_REASON
MISSING_ASSIGNMENT
AGENT_MISMATCH
REVIEW_ONLY_WRITE
EXCLUSIVE_LOCK_HELD
SUMMARY_MISMATCH
SYMLINK_ALIAS
AUTO_ASSIGNED_TASK
CONFIG_ERROR
STORAGE_ERROR
```

`SYMLINK_ALIAS` warns that two active intents reference different display paths that resolve through symlinks to the same realpath but compute distinct `canonical_path_hash` values. SPEC §14 #8: switching the equality key from realpath to display lost free symlink-aliasing, so this finding makes the regression observable. Operators should pick one canonical display path or close one of the intents.

`AUTO_ASSIGNED_TASK` (severity: warning) fires when a verified task has an assignment row but that row was created by an adapter's auto-derivation path rather than an explicit orchestrator assignment. Detection keys on `metadata.auto_assigned == true`; falls back to a leading `[auto-assigned by ...]` or `[harness-derived by ...]` reason marker for rows written by older adapter versions. Assignments whose `metadata.dispatch_origin` is `"pi-subagent-bootstrap"` are explicitly exempt: pi subagent children write their own assignment rows from their session bootstrap, but their dispatches are orchestrator-initiated, not adapter fallbacks. This finding is additive with `MISSING_ASSIGNMENT`: the no-row case still reports `MISSING_ASSIGNMENT`; `AUTO_ASSIGNED_TASK` fires only when a row exists but was adapter-derived. See section 21.1 for the subagent bootstrap contract and the verify suppression rule.

## 20. Babysitter integration

Babysitter needs CI-visible state. Local XDG state is not visible in GitHub Actions.

### 20.1 MVP strategy: committed per-task summary

Each task exports a privacy-safe summary:

```text
tasks/agent-ledger/<task-id>.json
```

Example:

```json
{
  "schema": "agent-ledger-summary.v1",
  "task_id": "W2-A",
  "agent_id": "pi.worker.7f3a",
  "assignment_hash": "sha256:abc123",
  "assignment_snapshot": {
    "assignment_id": "asg_01HY...",
    "task_id": "W2-A",
    "assigned_agent_id": "pi.worker.7f3a",
    "allowed_paths": [
      "src/worldbuilder/credentials/tonic_api.py"
    ],
    "forbidden_paths": [
      "tests/**",
      "pyproject.toml",
      "uv.lock"
    ],
    "conflict_policy": "warn",
    "reason_sha256": "sha256:def456"
  },
  "changed_paths": [
    "src/worldbuilder/credentials/tonic_api.py"
  ],
  "changes": [
    {
      "path": "src/worldbuilder/credentials/tonic_api.py",
      "before_sha256": "7ab...",
      "after_sha256": "91c...",
      "patch_sha256": "1bf..."
    }
  ],
  "validations": [
    {
      "command": "uv run ruff check src/worldbuilder/credentials/tonic_api.py",
      "status": "passed"
    }
  ],
  "closed": true
}
```

The summary must not include full diffs by default. It must include a privacy-safe `assignment_snapshot` so CI can enforce allowed paths, forbidden paths, assigned agent, and conflict policy without local XDG ledger state.

### 20.2 Babysitter workflow hooks

1. On run creation: write `orchestration.opened`.
2. On task dispatch: write `task.assigned`.
3. During execution: adapter records intent and changes.
4. Before gate review: export task summary.
5. In CI: run `agent-ledger verify --summary <summary> --json`. CI must use the kernel for policy decisions. A lightweight helper may parse summary files, but it must not reimplement scope, conflict, or attribution rules.
6. In wave review: summarize task summaries and active conflicts.

### 20.3 Post-MVP strategy: git ref sync

Add optional sync to:

```text
refs/agent-ledger/<branch>
```

This avoids committed summary files but requires clearer fetch/push UX.

## 21. pi adapter

The pi adapter is the first Product MVP harness adapter.

Requirements:

1. Create agent IDs for main agent and subagents.
2. Export agent identity variables into subagent environments.
3. Write `task.assigned` when dispatching work with known scope.
4. Auto-claim before `edit` and `write` tool calls when possible.
5. Auto-record after `edit` and `write` tool calls when possible.
6. Warn or block on forbidden paths according to policy.
7. Provide a fallback skill that instructs agents to use the CLI manually when tool interception is unavailable.
8. Surface `agent-ledger status --path` in conflict messages.
9. Run `agent-ledger verify --json` before gate-reviewer marks a task complete.

If pi cannot intercept edit/write at the tool layer, MVP may ship with a pi extension that wraps supported tools plus a documented limitation.

### 21.1 Pi subagent child bootstrap invariant

pi-subagents spawns child processes that load the pi extension afresh. Children inherit the parent's environment, including `AGENT_LEDGER_TASK_ID` and `AGENT_ID`, and pi-subagents adds `PI_SUBAGENT_CHILD=1`, `PI_SUBAGENT_CHILD_AGENT`, `PI_SUBAGENT_RUN_ID`, and `PI_SUBAGENT_CHILD_INDEX` to the child's environment. A pi subagent child must self-assign its own task instead of inheriting one from the parent.

The invariant is:

> For a pi subagent child, no `claim`, `record`, `heartbeat`, or `close`
> may execute until a durable assignment exists for that child task in
> the same ledger, and every child-side event must use a child `AGENT_ID`
> while the assignment's `orchestrator_id` remains the parent agent
> identity. Define explicitly whether assignment visibility is required
> at dispatch time or only before first child action.

This spec resolves the open clause: assignment visibility is required at extension load time in the child, before the child issues any tool call. A child cannot `claim`, `record`, `heartbeat`, or `close` until its own durable assignment row exists.

The contract has six parts. They are user-approved decisions; see `tasks/option-d-context.md` for the full rationale. The format strings and metadata schema below are byte-for-byte authoritative.

1. **New task source `subagent`.** When the pi extension loads in a child process and `process.env.PI_SUBAGENT_CHILD === "1"`, the bootstrap selects `task_source=subagent`. This source preempts the chain `flag`, `env`, `pr`, `branch`, `detached`, `auto` and short-circuits it. (`tasks/option-d-context.md` decision 1 also defines how `verify` treats this source: `AUTO_ASSIGNED_TASK` is suppressed for assignments whose `metadata.dispatch_origin = "pi-subagent-bootstrap"`. The warning continues to fire for true adapter-derived self-bootstrap such as `branch`, `detached`, `auto`, and explicit-repair.)

2. **Eager bootstrap.** The bootstrap runs at extension load, not on the first tool call. The assignment row exists before the child can issue any `claim`, `record`, `heartbeat`, or `close`. This is the "before first child action" half of the invariant. Trade-off: every child pays one `agent-ledger assign --if-absent` round-trip even if it never edits anything. Benefit: audit chronology stays clean (`task.assigned` precedes any later `intent.opened`), and zero-tool children still leave an assignment row. (`tasks/option-d-context.md` decision 2.)

3. **Hard-fail on missing parent task.** If `PI_SUBAGENT_CHILD=1` is set but the inherited parent task id (`AGENT_LEDGER_TASK_ID`), `PI_SUBAGENT_RUN_ID`, `PI_SUBAGENT_CHILD_INDEX`, or `PI_SUBAGENT_CHILD_AGENT` is missing, bootstrap exits non-zero with a clear diagnostic. It does not fall back to `branch`, `auto`, or any other task source. (`tasks/option-d-context.md` decision 3.)

4. **Deterministic child task id.** The child task id is derived from four inputs: the inherited parent task id (`parent_task`), the child agent name (`child_agent`), the run id (`run_id`), and the child index (`child_index`). The format is fixed:

   ```
   <parent_task>/<child_agent>/<run_id>-<child_index>
   ```

   Inputs:
   - `parent_task`: the inherited `AGENT_LEDGER_TASK_ID`.
   - `child_agent`: `PI_SUBAGENT_CHILD_AGENT`.
   - `run_id`: `PI_SUBAGENT_RUN_ID`.
   - `child_index`: `PI_SUBAGENT_CHILD_INDEX` rendered as a decimal integer string with no padding.

   No random suffix, no timestamp. A retry of the same logical child (same `run_id`, same `child_index`) produces the same task id, which lets `agent-ledger assign --if-absent` reuse the existing assignment row instead of creating a duplicate. A genuinely new dispatch (different `run_id` or different `child_index`) produces a fresh task id. (`tasks/option-d-context.md` decision 5.)

5. **Deterministic child `AGENT_ID`, distinct from the parent.** The child `AGENT_ID` is derived from the same run inputs:

   ```
   agent:pi:subagent:<run_id>:<child_index>
   ```

   This child `AGENT_ID` is the value the child uses for every child-side event: `claim`, `record`, `heartbeat`, and `close`. It is distinct from the inherited parent `AGENT_ID`. The bootstrap captures the inherited parent `AGENT_ID` separately and passes it to `agent-ledger assign --orchestrator <parent-agent-id>`, so the assignment row's `orchestrator_id` records the parent identity while `assigned_agent_id` records the child identity. A retry of the same logical child reuses the same child `AGENT_ID`, so `assign --if-absent` is a no-op and `verify` does not surface `AGENT_MISMATCH` on the second spawn's claims and records. (`tasks/option-d-context.md` decision 6.)

6. **Locked assignment metadata schema.** The `agent-ledger assign --metadata` JSON payload for a subagent child row must include exactly these fields:

   - `parent_task`: string. Inherited parent task id.
   - `parent_agent_id`: string. Inherited parent `AGENT_ID`.
   - `subagent_run_id`: string. `PI_SUBAGENT_RUN_ID` verbatim.
   - `subagent_child_index`: number (decimal integer, JSON number type). `PI_SUBAGENT_CHILD_INDEX` parsed as int.
   - `subagent_child_agent`: string. `PI_SUBAGENT_CHILD_AGENT` verbatim.
   - `dispatch_origin`: string literal `"pi-subagent-bootstrap"`.

   `dispatch_origin` is the discriminator that `verify` reads to suppress `AUTO_ASSIGNED_TASK` per part 1. Reason text remains an audit hint. Metadata is the authoritative surface for programmatic readers (verify, audit, cross-tool correlation). (`tasks/option-d-context.md` decision 7.)

The parent-side `subagent` `tool_call` hook in the pi extension is observation-only. It records that a dispatch was initiated (parent task id, child agent name, dispatch timestamp) for correlation and telemetry. It does not mutate `process.env`, does not call `agent-ledger assign`, and does not block the dispatch. The child does its own assignment from its own session bootstrap. (`tasks/option-d-context.md` decision 4.)

When a child resolves a different ledger than its parent (different `cwd` per task), the child writes its assignment row to its own resolved ledger. In that case `metadata.parent_task` is informational cross-ledger linkage. It is not a relational guarantee, and a `verify` run inside the child's ledger cannot follow `metadata.parent_task` back to the parent assignment in the parent's ledger.

## 22. Claude Code adapter

Phase 4 Product MVP adapter.

Requirements:

1. Use PreToolUse hooks for Edit, Write, MultiEdit, NotebookEdit, and mutating Bash where possible.
2. Use PostToolUse hooks to record after-state hashes.
3. Pull identity from environment variables or adapter-generated session files.
4. Fail closed for `exclusive` policy conflicts if project config requires it.
5. Degrade to warnings for commands whose file effects cannot be predicted.

## 23. Generic harness adapter

MVP generic harness support is manual because `agent-ledger exec` is post-MVP. For generic file edits, document manual commands:

```bash
agent-ledger claim src/foo.py --task W2-A --reason '...'
# edit file
agent-ledger record src/foo.py --intent <intent-id> --summary '...'
agent-ledger close --intent <intent-id> --outcome completed
```

Generic support is cooperative unless the harness supports hooks.

Post-MVP, generic harnesses may use:

```bash
agent-ledger exec --task W2-A -- make test
```

That mode is an observation wrapper, not part of Phase 1.

## 24. Human, IDE, and formatter changes

The verifier must detect working-tree changes that lack an agent claim.

Attribution rules:

1. If a known formatter command was run through a post-MVP `agent-ledger exec` change-recording mode, actor kind is `formatter`.
2. If a known IDE integration reports the change, actor kind is `ide`.
3. If a human identity is configured, actor kind may be `human`.
4. If a change was recorded directly by an Agent Ledger hook without agent identity, actor kind is `hook`.
5. Otherwise actor kind is `unknown`.

Unowned changes should be reported clearly. Depending on policy, they may warn or fail. The default for source files is warning plus suggested adoption. The default for forbidden paths is failure.

## 25. Recovery flows

### 25.1 Missed claim

Verifier finding:

```text
UNCLAIMED_CHANGE
```

Recovery:

```bash
agent-ledger adopt src/foo.py --task W1-A --reason 'Backfill missed claim found during verification'
```

This writes `change.adopted` and creates a change row with `changes.metadata_json.retroactive = true`.

### 25.2 Stale active intent

Verifier finding:

```text
STALE_INTENT
```

Recover with one of:

```bash
agent-ledger close --intent <id> --outcome abandoned
agent-ledger heartbeat --intent <id>
agent-ledger claim <path> --task <task> --reason 'Supersede stale claim' --supersede <id>
```

### 25.3 Exclusive conflict

Verifier finding:

```text
ACTIVE_CONFLICT
```

Recover with:

```bash
agent-ledger conflicts acknowledge --conflict <id> --reason 'Orchestrator approved sequential merge order'
```

Or stop and route back to orchestrator.

## 26. Configuration

Local pointer config lives in `.agent-ledger.toml` and should be gitignored. Shared project policy may live in a committed `.agent-ledger-policy.toml` when a team wants common defaults. The local pointer may reference the policy file, but the two responsibilities must stay separate.

Local pointer example:

```toml
version = 1
project_id = "github.com/recora-health/shima-enaga"
ledger_dir = "/Users/albert/.local/state/agent-ledger/repos/recora-health-shima-enaga-4bd6..."

policy_file = ".agent-ledger-policy.toml"
```

Committed policy example:

```toml
version = 1

[defaults]
conflict_policy = "warn"
allow_unassigned_intents = false
heartbeat_seconds = 30
stale_after_seconds = 120
store_full_diffs = false

[policies]
exclusive = [
  "uv.lock",
  "package-lock.json",
  "pnpm-lock.yaml",
  "db/migrate/**",
  "**/schema.sql",
  ".github/workflows/**"
]
review_only_agents = [
  "*.reviewer.*",
  "*.gate-reviewer.*"
]
```

Config values must never include secrets. The local pointer owns machine-specific paths. The policy file owns team defaults.

## 27. Performance requirements

1. `agent-ledger claim` should complete in under 20ms on a warm cache for normal projects.
2. `agent-ledger record` should complete in under 50ms for fewer than 10 files, excluding git diff computation.
3. `agent-ledger status --path` should be indexed and avoid full audit scans.
4. `agent-ledger verify --task` should complete in under 500ms for typical task scopes.
5. JSONL audit writes must not block SQLite commits for long-running file operations.

## 28. OS portability

MVP targets:

1. macOS arm64 and amd64.
2. Linux amd64 and arm64.

Windows support is post-MVP unless a contributor needs it immediately. The design must avoid needless POSIX-only assumptions in the database schema and event model.

File locking:

1. Use platform-specific locking behind an abstraction.
2. `exclusive` locks should be best-effort on platforms without reliable advisory locks.
3. Verification must not rely only on OS locks. The database state remains authoritative for policy decisions.

## 29. Rotation and retention

Audit JSONL files rotate daily:

```text
audit/YYYY-MM-DD.jsonl
```

Retention defaults:

1. SQLite events: keep indefinitely until explicit prune.
2. JSONL audit files: keep 90 days by default.
3. Blobs: keep 30 days by default unless referenced by committed summaries.

`gc` may compact old audit files and orphan unreferenced blobs. It must not delete SQLite event rows unless an explicit destructive prune command is added later.

## 30. Packaging

MVP packaging targets:

1. Static binary release archives.
2. Homebrew tap.
3. pi extension package that depends on or bundles the binary.
4. Adapter packages as they ship. The pi extension is MVP; the Claude Code hook package ships with Phase 4.

The binary must support:

```bash
agent-ledger --version
agent-ledger doctor
```

`doctor` checks storage resolution, SQLite health, git detection, project pointer validity, lock support, and adapter environment variables.

## 31. Test plan

### 31.1 Unit tests

1. Path normalization, including symlinks and Unicode NFC.
2. Project fingerprint computation.
3. Assignment path allow/forbid matching.
4. Conflict policy resolution.
5. Event schema validation.
6. Verify JSON output and exit codes.
7. Privacy defaults, especially no full diff content.
8. Migration idempotency.

### 31.2 Integration tests

1. Multiple processes concurrently claiming different files.
2. Multiple processes claiming the same file under `warn`.
3. Multiple processes claiming the same file under `exclusive`.
4. Stale heartbeat recovery.
5. Missed claim adoption.
6. Git worktree pointer discovery.
7. Separate clone project fingerprint behavior.
8. Exported summary verification in a clean checkout.

### 31.3 Adapter tests

1. pi adapter auto-claims before edit/write.
2. pi adapter records after edit/write.
3. gate-reviewer consumes `verify --json`.
4. Babysitter summary export is privacy-safe and CI-readable.
5. Claude Code hook detects Edit/Write and mutating Bash where supported.

## 32. Product MVP roadmap

### Phase 1: Kernel spec and CLI

Deliver:

1. Go CLI skeleton.
2. Storage resolution.
3. SQLite migrations.
4. JSONL audit mirror.
5. `init`, `identify`, `assign`, `claim`, `heartbeat`, `record`, `close`, `status`, `verify --json`, `conflicts`, `adopt`, `export-summary`, `gc`, `migrate`, `doctor`.
6. Unit tests and core integration tests.

Acceptance:

1. Two local agents can claim and record disjoint files.
2. Overlapping claims produce deterministic conflict output.
3. `verify --json` reports unclaimed and forbidden changes.
4. No full diff content is stored by default.

### Phase 2: pi adapter

Deliver:

1. pi extension package.
2. Agent identity injection.
3. Assignment creation for dispatched tasks.
4. Auto-claim and auto-record for edit/write where possible.
5. Fallback skill instructions for manual use.
6. gate-reviewer integration through `verify --json`.

Acceptance:

1. A pi worker editing an assigned file produces assignment, intent, change, and close records.
2. A pi reviewer attempting to write under `review-only` fails verification.
3. A conflict message shows the owning agent, task, path, and reason.

### Phase 3: Babysitter integration

Deliver:

1. `export-summary` integration.
2. CI verification for per-task summaries.
3. Wave summary support.

Acceptance:

1. Babysitter CI can verify task summaries without local XDG state.
2. Gate review fails on forbidden path changes.
3. Wave review can list which agent changed which files and why.

### Phase 4: Claude Code adapter

Deliver:

1. PreToolUse and PostToolUse hook package.
2. Edit/Write/MultiEdit support.
3. Mutating Bash detection and warning.

Acceptance:

1. Claude Code edits produce the same kernel events as pi edits.
2. `verify --json` remains identical across harnesses.

### Phase 5: Post-MVP hardening

Deliver candidates:

1. Git-ref sync.
2. Cross-clone daemon.
3. Dashboard or TUI.
4. Secret scrubber.
5. Event signing.
6. Remote service.
7. Windows support.

## 33. Open decisions

1. How much tool interception can pi support directly today?
2. Should mutating Bash commands auto-record detected file changes or only warn and require explicit `record`? The Claude Code adapter starts with warnings for unpredictable commands.
3. What is the first real dogfood repo and workflow?
4. Should summary files remain under `tasks/agent-ledger/` after MVP, or should git-ref sync replace them once stable?

## 34. Recommended first implementation slice

Build the kernel without any harness integration first:

1. `agent-ledger init` creates the database and pointer.
2. `agent-ledger identify` creates agent sessions.
3. `agent-ledger assign` creates scope contracts.
4. `agent-ledger claim` creates intents and detects conflicts.
5. `agent-ledger record` stores privacy-safe change records.
6. `agent-ledger verify --json` reports scope and attribution failures.

Then add a thin pi adapter. Do not build dashboard, daemon, signing, or git-ref sync until the kernel and pi workflow are reliable.
