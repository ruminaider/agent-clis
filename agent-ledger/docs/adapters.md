# Agent Ledger adapters: cross-harness contract

Phase 1 ships only the kernel CLI. Phase 2 adapters (this document) wrap
each harness's tool-call lifecycle so claim, record, and verify happen
deterministically. AGENTS.md guidance for agents to "remember" to call
`agent-ledger` is still useful as a reference, but the adapters are the
load-bearing enforcement layer.

This file is the contract. Every adapter (pi extension, babysitter
wrapper, future Claude Code hooks) implements the same shape.

## Env var contract

| Variable | Producer | Consumer | Required | Purpose |
| -------- | -------- | -------- | -------- | ------- |
| `AGENT_ID` | harness or orchestrator | every claim/record | yes | Identity attributed to events. Set by the harness; falls back to a non-PII opaque value at session bootstrap. |
| `AGENT_LEDGER_TASK_ID` | orchestrator | adapters | strongly preferred | The task id this agent is working on. Orchestrators set it before dispatching a worker. If unset at first claim, the adapter auto-derives one. If set but no active assignment exists, the adapter fails before the first claim unless explicit repair is enabled. |
| `AGENT_LEDGER_PARENT_TASK_ID` | orchestrator | adapters | optional | Available for adapters and manual orchestration scenarios that need explicit parent-child task linkage. Linked pi subagent children derive a fresh task from inherited parent context and export this linkage. Parentless pi subagent orphans deliberately export no parent task id or parent metadata. |
| `AGENT_LEDGER_DIR` | operator | every CLI call | optional | Override the resolved ledger directory. Defaults to `${XDG_STATE_HOME:-$HOME/.local/state}/agent-ledger/repos/<slug>-<fingerprint>/`. |
| `AGENT_LEDGER_REASON` | orchestrator | adapters | optional | Default `--reason` text for claims and records when the adapter cannot derive one from tool input. |
| `AGENT_LEDGER_REQUIRE_TASK` | operator | adapters | optional | When `1`, the adapter fails closed on missing `AGENT_LEDGER_TASK_ID` instead of auto-deriving. Default `0`. |
| `AGENT_LEDGER_AUTO_ASSIGN_POLICY` | operator | adapters | optional | Default conflict policy for auto-derived assignments: `warn` (default) or `exclusive`. |
| `AGENT_LEDGER_AUTO_ASSIGN_ALLOW` | operator | adapters | optional | Glob list (colon-separated) for the auto-derived assignment's `--allow`. Defaults to `**` (permissive). |
| `AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT` | operator | adapters | optional | Emergency opt-in. When `1`, a bootstrap with an explicit task id but no active assignment creates a marked repair assignment instead of failing. Default `0`. |
| `AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW` | operator | adapters | required when repairing | Glob list (colon-separated) used for an opt-in explicit repair assignment. No default, to avoid silently widening scope. |

## Task id resolution

At session bootstrap the adapter first checks whether the process is a
pi subagent child. When `PI_SUBAGENT_CHILD=1` is set in the environment,
the bootstrap selects one of the dedicated child sources described below
and short-circuits the rest of the chain. Otherwise the adapter
resolves a task id through this chain (first match wins):

1. **`--task-id` flag** supplied by the orchestrator. Marked
   `TASK_SOURCE=flag`. The bootstrap first checks for an active
   assignment. If one exists, it writes nothing. If none exists, it
   fails before the worker reaches its first claim. Emergency repair is
   opt-in via `AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT=1` plus
   `AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW`.
2. **`AGENT_LEDGER_TASK_ID` env var**. Marked `TASK_SOURCE=env`. Same
   check-fail-or-opt-in-repair behavior as the flag.
3. **PR detection** (opt-in via `--detect-pr 1` or
   `AGENT_LEDGER_DETECT_PR=1`). If `gh pr view --json number` returns
   a PR for the current branch, task id becomes `pr-<number>`. Marked
   `TASK_SOURCE=pr`.
4. **Git branch detection**. `git rev-parse --abbrev-ref HEAD` against
   the cwd; the branch name (sanitized) becomes the task id. Marked
   `TASK_SOURCE=branch`. Multiple sessions on the same branch share
   one task id, which is the correct behavior for ongoing work.
5. **Detached HEAD**. `detached/<short-sha>` from
   `git rev-parse --short HEAD`. Marked `TASK_SOURCE=detached`.
6. **Pointer file default**. The local `.agent-ledger.toml` may
   declare an optional `default_task_id`. When set, and steps 1-5
   produced no task id, the adapter uses it (sanitized) verbatim.
   Marked `TASK_SOURCE=pointer`. This is the right answer for non-git,
   ambient multi-agent projects where the harness has no natural task
   signal: declare it once in the pointer and every concurrent session
   in that directory shares the same task id without per-session env
   wiring.
7. **Pi session**. When pi supplies a session id through
   `--session-id` or `PI_SESSION_ID`, the adapter hashes it with SHA-256
   and uses `auto/pi-session/<first-24-hex-chars>`. Marked
   `TASK_SOURCE=pi-session`. The task id is deterministic for that pi
   session, but remains auto-assigned: it uses the `[auto-assigned ...]`
   marker, records `metadata.auto_assigned == true`, and verify emits
   `AUTO_ASSIGNED_TASK`. It does not trigger a routine warning toast.
8. **Auto fallback** (last resort, outside any git repo, with no
   pointer-declared default, and without a pi session id).
   `auto/<agent-slug>/<utc-timestamp>`. Marked `TASK_SOURCE=auto`.
   This is the only path that triggers the adapter's warning toast,
   because true context-less sessions are rare and worth flagging. The
   bootstrap also exports `AGENT_LEDGER_TASK_AUTO_REASON` so the toast
   can name the cheapest fix; current values are `not_in_git_repo`,
   `git_no_head`, and `pointer_lacks_default`.

Sources 1 and 2 are explicit. The bootstrap does not write an
assignment when the orchestrator already created one. If an explicit
task id has no active assignment, the bootstrap fails closed by
default and tells the operator to run `agent-ledger assign` first. If
emergency repair is explicitly enabled, the bootstrap writes a repair
assignment with `metadata.explicit_missing_assignment == true` and the
operator-supplied `AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW` scope.
Sources 3-8 are derived; the bootstrap writes an assignment with a
marker in the reason text so reviewers can audit how the task id was
sourced. The pointer source uses the same `[harness-derived ...]`
marker as sources 3-5. The pi-session and legacy auto sources use the
existing `[auto-assigned ...]` marker.

### Sources: `subagent` and `subagent-orphan` (pi subagent children)

Pi subagent children bootstrap eagerly at extension load, so an assignment exists before the child can issue `claim`, `record`, `heartbeat`, or `close`. The child branch preempts the normal task-id resolution chain and ranks inputs as follows:

1. A complete run tuple (`PI_SUBAGENT_RUN_ID`, `PI_SUBAGENT_CHILD_INDEX`, and `PI_SUBAGENT_CHILD_AGENT`) plus both inherited parent variables (`AGENT_LEDGER_TASK_ID` and `AGENT_ID`) retains `TASK_SOURCE=subagent`.
2. `AGENT_LEDGER_REQUIRE_TASK=1` fails closed when the linked path did not apply, including an otherwise valid orphan.
3. Both parent variables absent with a complete valid run tuple selects `TASK_SOURCE=subagent-orphan`.
4. Partial parent context, a missing run tuple value, or a non-decimal child index fails closed rather than falling back to `branch` or `auto`.

The linked source is unchanged. It derives:

```
<parent_task>/<child_agent>/<run_id>-<child_index>
agent:pi:subagent:<run_id>:<child_index>
```

It captures the inherited parent `AGENT_ID` before deriving the child identity, passes that value as `--orchestrator`, writes the `subagent` harness-derived marker, and exports `AGENT_LEDGER_PARENT_TASK_ID`.

The orphan source handles scheduled or revived pi-subagent runs whose spawning process never had the parent ledger environment. It derives:

```
auto/pi-subagent/<run_id>-<child_index>
agent:pi:subagent:<run_id>:<child_index>
```

`<child_index>` is a normalized decimal integer. The orphan assignment passes the adapter actor supplied through `--orchestrator` (for pi, `pi-extension`), not a fabricated parent agent id. It uses the existing `[auto-assigned ...]` marker, exports `AGENT_LEDGER_AUTO_ASSIGNED=1`, and does not export a parent task id. One stderr warning names `AGENT_LEDGER_TASK_ID`, `AGENT_ID`, and the orphan task id. The extension intentionally shows no UI toast for this source.

Both sources use `agent-ledger assign --if-absent`. Replaying the same run id and child index therefore reuses the same task and agent identities.

### Source: `pointer` (non-git ambient projects)

When the harness has no natural task signal (no PR, no branch, no
detached HEAD), the adapter consults the local pointer file at the
cwd. If `.agent-ledger.toml` exists and declares `default_task_id`,
that value (sanitized) becomes the task id and `TASK_SOURCE=pointer`.
The bootstrap calls `agent-ledger pointer show --json` for this query;
adapters never parse the pointer TOML themselves.

The canonical use case is a non-git scratch directory shared by two or
three concurrent pi sessions that should all attribute to one task.
Declare it once:

```toml
# .agent-ledger.toml at the project root
version = 1
project_id = "scratch/agent-coordination-experiment"
ledger_dir = "/Users/you/.local/state/agent-ledger/repos/..."
default_task_id = "exploration-2026-05"
```

`agent-ledger init --write-pointer --default-task-id exploration-2026-05`
writes this file. Reruns of `init --write-pointer` carry forward an
existing `default_task_id` when `--default-task-id` is not supplied,
so operators can refresh the pointer without erasing the value.

Pointer detection looks at the cwd only. It does not walk the
directory tree upward today. If your project's pointer lives at the
project root and pi is launched from a subdirectory, the pointer is
not found. Workarounds: launch pi from the project root, or set
`AGENT_LEDGER_TASK_ID` explicitly. Upward search is a candidate
follow-up.

## Orchestrator ordering for long-lived workers

Adapters only bootstrap a process once. A long-lived worker that is
later instructed over intercom or prompt text to run
`agent-ledger claim --task <task>` will not re-run session bootstrap
for that task id. The orchestrator must create the assignment before
sending the worker any claim command or task brief that names that
id.

Required order for manual or long-lived-worker dispatch:

```bash
agent-ledger assign \
  --task "$TASK_ID" \
  --orchestrator "$ORCHESTRATOR_AGENT_ID" \
  --agent "$WORKER_AGENT_ID_OR_LABEL" \
  --allow 'path/or/glob/**' \
  --policy warn \
  --reason "<why this worker owns this scope>" \
  --if-absent

agent-ledger assignments --task "$TASK_ID" --status active --json
# Only after count > 0: send the worker its task brief or claim command.
```

Do not rely on explicit-task bootstrap repair for this flow. Repair is
an emergency fallback for process launch mistakes, not an orchestration
protocol. The pi `subagent` tool is different: each subagent child
creates its own assignment row from its own session bootstrap, eagerly,
before the child issues any tool call.

## Auto-derived task ids: solving "the orchestrator forgot"

The Phase 1 kernel only enforces what the operator tells it. When a
worker is dispatched without an explicit task id, the adapter must
choose between:

- **Fail closed**: block every edit until the orchestrator assigns.
  Safe but disruptive; punishes the worker for the orchestrator's
  miss.
- **Fail open**: let the worker edit without attribution. Defeats the
  point.
- **Derive from harness context (default)**: pull the task id from
  the current PR or branch. Almost always meaningful, since the
  branch name and PR number ARE the task in practice.
- **Pointer file default**: for non-git projects, declare
  `default_task_id` in `.agent-ledger.toml` once. Multiple sessions in
  the same directory share the task id without per-session env
  wiring. Marked `TASK_SOURCE=pointer`.
- **Pi-session fallback**: deterministic
  `auto/pi-session/<first-24-hex-chars-of-sha256(session-id)>` when pi
  supplies a session id and no higher-priority source resolves. It is
  auto-assigned and verify warns, but it stays silent in the UI because
  the session retains stable attribution.
- **Auto-fallback (last resort)**: synthetic
  `auto/<agent-slug>/<utc-timestamp>` only when no harness context,
  pointer default, or pi session id is available. This is the only path
  that surfaces a UI warning, since true context-less sessions are rare
  and worth flagging.

The derive-from-harness path is the default because the harness
almost always knows what the human is working on. Operators who want
strict enforcement set `AGENT_LEDGER_REQUIRE_TASK=1`; the bootstrap then blocks both auto-assigned fallbacks. PR, branch, detached, and pointer-derived sources still satisfy the requirement.

### Audit trail

Adapters write the audit signal in two complementary forms:

1. The assignment's `metadata_json` column carries structured fields
   (`auto_assigned`, `auto_assigned_by`, `task_source`,
   `parent_task`). This is the canonical surface for programmatic
   queries via `agent-ledger assignments --json` (kernel v0.1.1+).
2. The assignment's `reason` text carries a leading bracketed marker
   that the v0.1.0 kernel surfaces verbatim and that the v0.1.1+
   kernel keeps for forward compatibility with legacy queries. Two
   formats:

- **Auto-fallback** (no harness context found):

  ```
  [auto-assigned by <by> auto-derived task=<id> agent=<id>] <human reason>
  ```

- **Harness-derived** (task id sourced from PR, branch, detached HEAD, a linked pi subagent child, or a pointer-file default):

  ```
  [harness-derived by <by> source=<branch|pr|detached|subagent|pointer> task=<id> agent=<id>] <human reason>
  ```

- **Pi-session and subagent-orphan fallbacks** use the existing auto-fallback marker, not a harness-derived marker:

  ```
  [auto-assigned by <by> auto-derived task=<id> agent=<id>] <human reason>
  ```

Assignments without either marker prefix were supplied explicitly
by an orchestrator (`--task-id` flag or `AGENT_LEDGER_TASK_ID` env
var) and already existed when the adapter bootstrapped. Explicit task
ids that lacked an active assignment fail by default. If emergency
repair is enabled, they are repaired with the `[auto-assigned ...]`
prefix plus `metadata.explicit_missing_assignment == true`.

Reviewers query the audit trail through the
`agent-ledger assignments` command on v0.1.1+ kernels:

```bash
# True orchestrator-forgot cases (auto-fallback, no harness context):
agent-ledger assignments --status all --limit 200 --json \
  | jq '.assignments[] | select(.reason_marker == "auto")'

# Harness-derived sessions, grouped by source:
agent-ledger assignments --status all --limit 200 --json \
  | jq '.assignments[] | select(.reason_marker == "harness-derived")
        | {task_id, source: .metadata.task_source, agent: .assigned_agent}'

# Live (active) auto-assigned and explicit-repair tasks:
agent-ledger assignments --status active --json \
  | jq '.assignments[] | select(.reason_marker == "auto" or .metadata.explicit_missing_assignment == true)'
```

The v0.1.1 kernel additionally exposes structured metadata:
`metadata.auto_assigned`, `metadata.auto_assigned_by`,
`metadata.task_source`, and `metadata.parent_task` round-trip
through the assignments query. Querying on `metadata.task_source`
or `metadata.auto_assigned` is preferred over regex-matching the
reason text since metadata is the canonical structured surface.

Legacy ledgers from v0.1.0 (no metadata) and v0.2.0-rc2 (no
structured metadata; reason marker only) remain queryable via the
reason marker classifier (`reason_marker` field on each row) which
classifies any reason starting with `[auto-assigned` or
`[harness-derived` accordingly.

Verify emits an `AUTO_ASSIGNED_TASK` warning for adapter-derived or
repaired tasks so CI can surface them without blocking the merge.
`MISSING_ASSIGNMENT` remains reserved for the true no-assignment-row
case. Linked `subagent` rows are orchestrator-initiated even though the
child writes the row, so verify suppresses the warning only when
`metadata.dispatch_origin` is exactly `"pi-subagent-bootstrap"`.
`subagent-orphan` rows intentionally retain the warning because their
spawning process lacked parent ledger context.

### Subagent child metadata schema

Linked and orphan pi subagent rows use distinct metadata contracts so audit tooling can distinguish a normal dispatch from a missing parent context without parsing reason text.

Linked `subagent` rows include:

- `parent_task`: string. The inherited parent task id.
- `parent_agent_id`: string. The inherited parent `AGENT_ID`, also passed to `assign --orchestrator`.
- `subagent_run_id`: string. `PI_SUBAGENT_RUN_ID` verbatim.
- `subagent_child_index`: number. `PI_SUBAGENT_CHILD_INDEX` parsed as a decimal integer.
- `subagent_child_agent`: string. `PI_SUBAGENT_CHILD_AGENT` verbatim.
- `dispatch_origin`: the literal `"pi-subagent-bootstrap"`.

Orphan `subagent-orphan` rows include:

- `auto_assigned`: boolean `true`.
- `task_source`: the literal `"subagent-orphan"`.
- `dispatch_origin`: the literal `"pi-subagent-orphan-bootstrap"`.
- `parent_context_missing`: boolean `true`.
- `missing_parent_env`: the absent names, `AGENT_LEDGER_TASK_ID` and `AGENT_ID`.
- `subagent_run_id`, numeric `subagent_child_index`, and `subagent_child_agent`.
- `pi_session_id` only when a session id is available.

Orphan rows never include fabricated `parent_task` or `parent_agent_id`. Verify suppresses `AUTO_ASSIGNED_TASK` only for the linked discriminator, so orphan rows are intentionally flagged for review.

### Replay idempotency

Bootstrap calls `agent-ledger assign --if-absent` for non-explicit sources (pr, branch, detached, pointer, pi-session, auto, subagent, and subagent-orphan), so repeated launches do not create duplicate `task.assigned` events. Linked child identities are deterministic functions of `(parent_task, child_agent, run_id, child_index)`. Orphan child identities are deterministic functions of `(run_id, child_index)`. A respawn of either logical child is therefore a true no-op on `assign --if-absent`. Dedupe is scoped to `(task_id, assigned_agent_id)`: a genuinely new `AGENT_ID` for the same branch still creates a new assignment, which is correct because the agent is new. Changes to `--allow`, `--forbid`, or `--policy` always create a new assignment, so policy drift remains visible to reviewers. Explicit `--task-id` and `AGENT_LEDGER_TASK_ID` paths first query for an active assignment. They skip assignment when one exists, fail when none exists, and create a repair assignment only when `AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT=1` and `AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW` are set.

### Future audit work

The v0.1.1 kernel ships `--metadata`, the v0.1.2 kernel closes the
concurrent claim race, and the v0.1.3 kernel surfaces metadata
decode failures as a typed `MetadataDecodeError`. v0.1.5 adds the
`AUTO_ASSIGNED_TASK` finding for adapter-derived rows. A future
release can refine that finding to distinguish branch-derived,
auto-fallback, and explicit-repair assignments with separate policy
knobs.

## Session bootstrap

Every adapter runs the same bootstrap once per session, idempotent. Adapters choose when to run it: the pi extension waits for the first tool call that can change the project, and pi subagent children run it eagerly at extension load (see "Per-adapter behaviour" below). The steps are:

1. Resolve `AGENT_ID`. If unset, derive a non-PII opaque value,
   sanitize it, export it, then run `agent-ledger identify --agent-kind <kind>
   --harness <harness>` to register the identity. Operators who want a
   human-readable local identity can opt in with
   `AGENT_LEDGER_HUMAN_READABLE_AGENT_ID=1`.
2. Resolve `AGENT_LEDGER_TASK_ID` per the chain in "Task id resolution"
   above. The bootstrap exposes the chosen source via
   `AGENT_LEDGER_TASK_SOURCE`.
3. For sources `pr`, `branch`, `detached`, `pointer`, `pi-session`, and `auto`, write a fresh assignment with `agent-ledger assign --task <id> --orchestrator "<adapter>" --agent "$AGENT_ID" --policy "$AGENT_LEDGER_AUTO_ASSIGN_POLICY"`, one `--allow` per colon-separated glob in `$AGENT_LEDGER_AUTO_ASSIGN_ALLOW`, and the appropriate audit marker. For sources `flag` and `env`, query active assignments for the task. If none exists, fail before the first claim. If emergency repair is enabled, write a repair assignment with an `[auto-assigned ...]` reason marker, `metadata.explicit_missing_assignment == true`, and the operator-supplied `AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW` scope.
4. For a linked `subagent` source, write `assign --if-absent --task <child-task-id> --orchestrator <parent-agent-id> --agent <child-agent-id>` with the linked metadata and a `subagent` harness-derived marker. For `subagent-orphan`, write `assign --if-absent --task <orphan-task-id> --orchestrator <adapter-actor> --agent <child-agent-id>` with orphan metadata and an `[auto-assigned ...]` marker. Both paths derive the child `AGENT_ID` from the normalized run tuple before assigning.
5. Export the resolved env vars for child processes. Shell callers use export lines; pi uses the helper's `--json` mode.

For pi subagent children, generic agent-id derivation does not apply. Every valid child run tuple derives `agent:pi:subagent:<run_id>:<child_index>`, which preserves identity across retries. A linked child captures the inherited parent `AGENT_ID` only for `assign --orchestrator`. An orphan child has no parent context, uses the adapter actor as orchestrator, and exports no parent task id. Both child sources short-circuit the normal task-id resolution chain, and their assignments complete before any child-side `claim`, `record`, `heartbeat`, or `close`.

## Per-adapter behaviour

### pi extension (`adapters/pi/agent-ledger.ts`)

- Defers session bootstrap until a `write`, `edit`, `multi_edit`, `bash`, or executing `subagent` call. Read-only tools and subagent management calls return without creating or requiring task context, so browsing a project never opens a task. A pi subagent child still bootstraps eagerly at extension load so its assignment exists even when it invokes no tools.
- Hooks `tool_call` for `write`, `edit`, `multi_edit`. Calls
  `agent-ledger claim` for the target path. On non-zero exit, returns
  `{ block: true, reason }` to stop the edit.
- Hooks `tool_result` for the same call id. Calls
  `agent-ledger record` with the captured intent id and a summary
  derived from the tool input.
- Hooks `tool_call` for `bash`. Default mode is `warn`: inside a Git repository, snapshot `git status --porcelain`, let the command run, then record paths that became newly dirty against an "auto-bash" intent. Outside Git, Bash continues without change attribution and the extension reports one session-level notice. `AGENT_LEDGER_BASH_MODE=block` blocks all bash tool calls because shell mutation detection is not complete.
- Hooks `tool_call` for `subagent`. The hook is observation-only: it
  records that a dispatch was initiated (parent task id, child agent
  name, dispatch timestamp) for correlation and telemetry. It does not
  mutate `process.env`, does not call `agent-ledger assign`, and does
  not block the dispatch. Each spawned child detects `PI_SUBAGENT_CHILD=1`
  and bootstraps a deterministic linked `subagent` task when parent
  context is complete, or an auto-assigned `subagent-orphan` task when
  both parent variables are absent. Partial context and failed assignment
  creation remain fail-closed for bash, edits, and executing subagents.
  The child writes the assignment row in the ledger its own `cwd` resolves;
  linked `metadata.parent_task` is informational cross-ledger linkage rather
  than a relational guarantee. See SPEC.md section 21.1 for the contract.
- Hooks `agent_end` and `session_end` to close any open intents.

### babysitter wrapper (`adapters/babysitter/define-ledger-task.js`)

- Higher-order `defineLedgerTask(name, factory)`. Returns a chain of
  three effects: pre-shell `agent-ledger assign`, the original agent
  task (with `execution.env` injecting the task vars), post-shell
  `agent-ledger verify --task <id> --json`. If `verify` exits non-zero
  the wrapper task fails so the SDK halts iteration.
- Process-level adoption: replace `import { defineTask } from
  '@a5c-ai/babysitter-sdk'` with `import { defineLedgerTask as
  defineTask } from '<adapter>'` in any process file to opt the entire
  process into ledger discipline.

## Installation

See `agent-ledger/adapters/pi/README.md` and
`agent-ledger/adapters/babysitter/README.md` for harness-specific
install steps. Both adapters depend on the `agent-ledger` binary being
on `PATH` and the project ledger having been initialized with
`agent-ledger init --write-pointer`.

## Kernel dependencies

### v0.1.1: structured audit and assignment invariant

1. **`assign --metadata <json>`** is a kernel flag. The bootstrap
   passes structured `metadata.auto_assigned`,
   `metadata.auto_assigned_by`, `metadata.task_source`, and
   `metadata.parent_task` directly. The reason marker is still
   emitted for forward-compatibility with v0.1.0 ledgers and
   pre-v0.1.1 queries.
2. **`agent-ledger assignments` query command** lists assignments by
   `--task`, `--orchestrator`, `--agent`, `--status`, and `--limit`.
   The classifier `reason_marker` distinguishes auto, harness-derived,
   and explicit assignments without the operator regex-matching
   reason text.
3. **Partial unique index** on `(task_id, assigned_agent_id)` WHERE
   `status='active'` closes the F9 race surfaced in v0.2.0-rc2.
   Concurrent bootstraps cannot produce duplicate active rows; the
   loser sees `assignment_exists` (ExitConflict) and the bootstrap's
   `--if-absent` retry path catches it. Plain `assign` without
   `--if-absent` is now strict: a duplicate fails fast with
   `assignment_exists` instead of silently inserting a competing row.

### v0.1.2: concurrent claim race fix

The claim flow now runs overlap detection, conflict resolution,
and intent insert inside a single SQLite `BEGIN IMMEDIATE`
transaction. Concurrent claims under both `warn` and `exclusive`
policies are now race-free; `tests/integration/concurrent_test`
exercises this end-to-end.

### v0.1.3: typed metadata decode error

Assignment, intent, conflict, change, and validation readers now
return a typed `*domain.MetadataDecodeError` when a row's
`metadata_json` column does not parse, instead of silently
replacing it with an empty map. CLI handlers map this to
`ExitStorageIO` with code `metadata_decode_failed` and details
pointing at the corrupted row. Programmatic callers can detect
with `errors.As`.

### Older kernels

Legacy ledgers from v0.1.0 (no metadata flag, no assignments query,
no unique index) and v0.2.0-rc2 (no structured metadata; reason
marker only) remain queryable via the `reason_marker` classifier
emitted by `agent-ledger assignments` once the kernel is upgraded
to v0.1.1+. The reason marker prefix is forward-compatible across
all versions.

### Future kernel work

Nothing kernel-blocking remains for the v0.2.0 adapter promotion.
The `verify` command's `AUTO_ASSIGNED_TASK` finding can be tightened
with separate policy knobs for branch-derived, auto-fallback, and
explicit-repair assignments; tracked as future polish.

## Stability

The env var contract above is the public surface. Adapter
implementations may evolve; the contract is what users pin against.
Renaming an env var is a breaking change and bumps the adapter
package's major version. Adding new optional vars is a minor.
