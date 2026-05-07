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
| `AGENT_LEDGER_PARENT_TASK_ID` | orchestrator | adapters | optional | Available for adapters and manual orchestration scenarios that need explicit parent-child task linkage. Pi subagent children do not rely on this env var: they inherit `AGENT_LEDGER_TASK_ID` as the parent task id, derive a fresh deterministic child task id from it, and record the linkage in `metadata.parent_task` on the child's assignment row. |
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
the bootstrap selects the dedicated `subagent` source described below
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
6. **Auto fallback** (last resort, outside any git repo).
   `auto/<agent-slug>/<utc-timestamp>`. Marked `TASK_SOURCE=auto`.
   This is the only path that triggers the adapter's warning toast,
   because true context-less sessions are rare and worth flagging.

Sources 1 and 2 are explicit. The bootstrap does not write an
assignment when the orchestrator already created one. If an explicit
task id has no active assignment, the bootstrap fails closed by
default and tells the operator to run `agent-ledger assign` first. If
emergency repair is explicitly enabled, the bootstrap writes a repair
assignment with `metadata.explicit_missing_assignment == true` and the
operator-supplied `AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW` scope.
Sources 3-6 are derived; the bootstrap writes an assignment with a
marker in the reason text so reviewers can audit how the task id was
sourced.

### Source: `subagent` (pi subagent children)

When the pi extension loads in a child process and
`process.env.PI_SUBAGENT_CHILD === "1"`, the bootstrap selects
`TASK_SOURCE=subagent` and skips the chain above. This source is
exclusive to pi subagents children and runs at extension load time, not
lazily on first tool call: the assignment row exists before the child
can issue any `claim`, `record`, `heartbeat`, or `close`. Children that
never invoke a tool still leave a row.

The bootstrap requires five inputs (any missing input is a hard fail
with a clear diagnostic, no fallback to `branch` or `auto`):

- `AGENT_LEDGER_TASK_ID`: the inherited parent task id.
- `PI_SUBAGENT_CHILD_AGENT`: the child agent name from pi-subagents.
- `PI_SUBAGENT_RUN_ID`: the run id from pi-subagents.
- `PI_SUBAGENT_CHILD_INDEX`: the child index from pi-subagents.
- `AGENT_ID`: the inherited parent agent id, used as
  `--orchestrator` when the child writes its assignment row.

The child task id is deterministic, with no random suffix and no
timestamp:

```
<parent_task>/<child_agent>/<run_id>-<child_index>
```

where `<child_index>` is `PI_SUBAGENT_CHILD_INDEX` rendered as a decimal
integer string with no padding.

The child `AGENT_ID` is also deterministic:

```
agent:pi:subagent:<run_id>:<child_index>
```

This child `AGENT_ID` is the value the child uses for its own `claim`,
`record`, `heartbeat`, and `close` events. It is distinct from the
inherited parent `AGENT_ID`. The bootstrap captures the parent
`AGENT_ID` separately and passes it to
`agent-ledger assign --orchestrator <parent-agent-id>`, so the
assignment row's `orchestrator_id` records the parent identity while
`assigned_agent_id` records the child identity. The same logical child
on a respawn (same `run_id`, same `child_index`) reuses both ids, so
`assign --if-absent` is a no-op and `verify` does not surface
`AGENT_MISMATCH` on the retry.

The bootstrap calls `agent-ledger assign --if-absent` with the
structured metadata schema (see the audit-trail section below) and the
`subagent` reason marker source. See SPEC.md section 21.1 for the full
invariant.

The pi extension passes `--cwd $(process.cwd())` and (when
`AGENT_LEDGER_DETECT_PR=1`) `--detect-pr 1` to the bootstrap, then
reads `AGENT_LEDGER_TASK_SOURCE` from the bootstrap output and only
shows a UI warning when source=auto.

Operators who want strict enforcement set `AGENT_LEDGER_REQUIRE_TASK=1`; the bootstrap then blocks only the auto fallback. PR, branch, and detached harness-derived sources still satisfy the requirement.

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
- **Auto-fallback (last resort)**: synthetic
  `auto/<agent-slug>/<utc-timestamp>` only when no harness context is
  available (outside any git repo). This is the only path that
  surfaces a UI warning, since true context-less sessions are rare
  and worth flagging.

The derive-from-harness path is the default because the harness
almost always knows what the human is working on. Operators who want
strict enforcement set `AGENT_LEDGER_REQUIRE_TASK=1`; the bootstrap then blocks only the auto fallback. PR, branch, and detached harness-derived sources still satisfy the requirement.

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

- **Harness-derived** (task id sourced from PR, branch, detached HEAD, or pi subagent child bootstrap):

  ```
  [harness-derived by <by> source=<branch|pr|detached|subagent> task=<id> agent=<id>] <human reason>
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
case. The `subagent` source is treated separately: subagent children
are orchestrator-initiated even though the assignment row is written
from the child's bootstrap, so verify suppresses `AUTO_ASSIGNED_TASK`
for any assignment whose `metadata.dispatch_origin` is
`"pi-subagent-bootstrap"`.

### Subagent child metadata schema

A pi subagent child writes its own assignment with a fixed metadata
shape so verify, audit, and cross-tool correlation can read it without
regex-matching reason text. The `agent-ledger assign --metadata` JSON
payload must include exactly these fields:

- `parent_task`: string. Inherited parent task id. When the child
  resolves a different ledger than its parent (different `cwd` per
  task), this field is informational cross-ledger linkage. It is not a
  relational foreign key, and `verify` running inside the child's
  ledger cannot follow it back to the parent's assignment in the
  parent's ledger.
- `parent_agent_id`: string. Inherited parent `AGENT_ID`. This is the
  same value the bootstrap passes to `assign --orchestrator`.
- `subagent_run_id`: string. `PI_SUBAGENT_RUN_ID` verbatim.
- `subagent_child_index`: number (decimal integer, JSON number type).
  `PI_SUBAGENT_CHILD_INDEX` parsed as int.
- `subagent_child_agent`: string. `PI_SUBAGENT_CHILD_AGENT` verbatim.
- `dispatch_origin`: string literal `"pi-subagent-bootstrap"`. This
  is the discriminator verify reads to classify the row as a subagent
  child and suppress `AUTO_ASSIGNED_TASK`.

Reason text remains an audit hint. Metadata is the authoritative
surface for programmatic readers.

### Replay idempotency

Bootstrap calls `agent-ledger assign --if-absent` for non-explicit sources (pr, branch, detached, auto, and subagent), so repeated pi launches on the same branch do not create duplicate `task.assigned` events. For the `subagent` source, both the child task id and the child `AGENT_ID` are deterministic functions of `(parent_task, child_agent, run_id, child_index)`, so a respawn of the same logical child is a true no-op on `assign --if-absent`. Dedupe is scoped to `(task_id, assigned_agent_id)`: a genuinely new `AGENT_ID` for the same branch still creates a new assignment, which is correct because the agent is new. Changes to `--allow`, `--forbid`, or `--policy` always create a new assignment, so policy drift remains visible to reviewers. Explicit `--task-id` and `AGENT_LEDGER_TASK_ID` paths first query for an active assignment. They skip assignment when one exists, fail when none exists, and create a repair assignment only when `AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT=1` and `AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW` are set.

### Future audit work

The v0.1.1 kernel ships `--metadata`, the v0.1.2 kernel closes the
concurrent claim race, and the v0.1.3 kernel surfaces metadata
decode failures as a typed `MetadataDecodeError`. v0.1.5 adds the
`AUTO_ASSIGNED_TASK` finding for adapter-derived rows. A future
release can refine that finding to distinguish branch-derived,
auto-fallback, and explicit-repair assignments with separate policy
knobs.

## Session bootstrap

Every adapter runs the same bootstrap once per session, idempotent:

1. Resolve `AGENT_ID`. If unset, derive a non-PII opaque value,
   sanitize it, export it, then run `agent-ledger identify --agent-kind <kind>
   --harness <harness>` to register the identity. Operators who want a
   human-readable local identity can opt in with
   `AGENT_LEDGER_HUMAN_READABLE_AGENT_ID=1`.
2. Resolve `AGENT_LEDGER_TASK_ID` per the chain in "Task id resolution"
   above. The bootstrap exposes the chosen source via
   `AGENT_LEDGER_TASK_SOURCE`.
3. For sources `pr`, `branch`, `detached`, and `auto`, write a fresh
   assignment with `agent-ledger assign --task <id> --orchestrator
   "<adapter>" --agent "$AGENT_ID" --policy
   "$AGENT_LEDGER_AUTO_ASSIGN_POLICY"`, one `--allow` per
   colon-separated glob in `$AGENT_LEDGER_AUTO_ASSIGN_ALLOW`, and a
   reason that starts with the appropriate marker prefix from the
   audit-trail section above. For sources `flag` and `env`, query
   active assignments for the task. If none exists, fail before the
   first claim. If emergency repair is explicitly enabled, write a
   repair assignment with an `[auto-assigned ...]` reason marker,
   `metadata.explicit_missing_assignment == true`, and the
   operator-supplied `AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW` scope. For
   source `subagent`, write a fresh assignment with `agent-ledger
   assign --if-absent --task <child-task-id> --orchestrator
   <parent-agent-id> --agent <child-agent-id>`, the structured metadata
   schema from the audit-trail section above, and a reason that starts
   with the `subagent` harness-derived marker. The bootstrap derives
   the child task id and the child `AGENT_ID` deterministically from
   `(parent_task, child_agent, run_id, child_index)` before issuing the
   assign call.
4. Export the resolved env vars for child processes. Shell callers use
   export lines; pi uses the helper's `--json` mode.

For subagent children, step 1 is constrained: the child `AGENT_ID` is
not derived from the generic non-PII opaque path. It is the
deterministic format `agent:pi:subagent:<run_id>:<child_index>`,
which preserves identity across retries of the same logical child. The
inherited parent `AGENT_ID` is captured into a separate variable and
passed only on `assign --orchestrator`. Step 2 is also constrained:
the `subagent` source short-circuits the chain in "Task id resolution"
above. Step 3 must complete before any child-side `claim`, `record`,
`heartbeat`, or `close`.

The shared shell helper `adapters/shared/session-bootstrap.sh`
implements this and is sourced by both the pi extension launcher and
the babysitter wrapper.

## Per-adapter behaviour

### pi extension (`adapters/pi/agent-ledger.ts`)

- Hooks `tool_call` for `write`, `edit`, `multi_edit`. Calls
  `agent-ledger claim` for the target path. On non-zero exit, returns
  `{ block: true, reason }` to stop the edit.
- Hooks `tool_result` for the same call id. Calls
  `agent-ledger record` with the captured intent id and a summary
  derived from the tool input.
- Hooks `tool_call` for `bash`. Default mode is `warn`: snapshot
  `git status --porcelain`, let the command run, then record paths
  that became newly dirty against an "auto-bash" intent.
  `AGENT_LEDGER_BASH_MODE=block` blocks all bash tool calls because
  shell mutation detection is not complete.
- Hooks `tool_call` for `subagent`. The hook is observation-only: it
  records that a dispatch was initiated (parent task id, child agent
  name, dispatch timestamp) for correlation and telemetry. It does not
  mutate `process.env`, does not call `agent-ledger assign`, and does
  not block the dispatch. Each spawned child runs its own session
  bootstrap, detects `PI_SUBAGENT_CHILD=1`, and self-assigns its own
  task with a deterministic child task id and a deterministic child
  `AGENT_ID`. The child writes the assignment row in the ledger its
  own `cwd` resolves, so cross-repo subagents land in the correct
  ledger; in that case `metadata.parent_task` is informational
  cross-ledger linkage rather than a relational guarantee. See SPEC.md
  section 21.1 and the `subagent` source above for the full child
  bootstrap contract.
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
