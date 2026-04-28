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
| `AGENT_LEDGER_TASK_ID` | orchestrator | adapters | strongly preferred | The task id this agent is working on. Orchestrators set it before dispatching a worker. If unset at first claim, the adapter auto-derives one. |
| `AGENT_LEDGER_PARENT_TASK_ID` | orchestrator | adapters | optional | Set by the orchestrator when dispatching a subagent for a sub-task. Auto-derived child tasks include this in the v0.1 reason marker as `parent=<id>` for audit. |
| `AGENT_LEDGER_DIR` | operator | every CLI call | optional | Override the resolved ledger directory. Defaults to `${XDG_STATE_HOME:-$HOME/.local/state}/agent-ledger/repos/<slug>-<fingerprint>/`. |
| `AGENT_LEDGER_REASON` | orchestrator | adapters | optional | Default `--reason` text for claims and records when the adapter cannot derive one from tool input. |
| `AGENT_LEDGER_REQUIRE_TASK` | operator | adapters | optional | When `1`, the adapter fails closed on missing `AGENT_LEDGER_TASK_ID` instead of auto-deriving. Default `0`. |
| `AGENT_LEDGER_AUTO_ASSIGN_POLICY` | operator | adapters | optional | Default conflict policy for auto-derived assignments: `warn` (default) or `exclusive`. |
| `AGENT_LEDGER_AUTO_ASSIGN_ALLOW` | operator | adapters | optional | Glob list (colon-separated) for the auto-derived assignment's `--allow`. Defaults to `**` (permissive). |

## Task id resolution

At session bootstrap the adapter resolves a task id through this
chain (first match wins):

1. **`--task-id` flag** supplied by the orchestrator. Marked
   `TASK_SOURCE=flag`. The bootstrap trusts the orchestrator already
   wrote an assignment and skips the assign step.
2. **`AGENT_LEDGER_TASK_ID` env var**. Marked `TASK_SOURCE=env`. Same
   skip behavior as the flag.
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

Sources 1 and 2 are explicit; the bootstrap does not write an
assignment. Sources 3-6 are derived; the bootstrap writes an
assignment with a marker in the reason text so reviewers can audit
how the task id was sourced.

The pi extension passes `--cwd $(process.cwd())` and (when
`AGENT_LEDGER_DETECT_PR=1`) `--detect-pr 1` to the bootstrap, then
reads `AGENT_LEDGER_TASK_SOURCE` from the bootstrap output and only
shows a UI warning when source=auto.

Operators who want strict enforcement set `AGENT_LEDGER_REQUIRE_TASK=1`; the bootstrap then blocks only the auto fallback. PR, branch, and detached harness-derived sources still satisfy the requirement.

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

### Audit trail in v0.1

The v0.1 kernel does not yet have a `--metadata` flag on `assign`,
so adapters encode the audit signal in the assignment's `reason`
text as a leading bracketed marker. Two formats:

- **Auto-fallback** (no harness context found):

  ```
  [auto-assigned by <by> auto-derived task=<id> agent=<id>] <human reason>
  ```

- **Harness-derived** (task id sourced from PR, branch, or detached HEAD):

  ```
  [harness-derived by <by> source=<branch|pr|detached> task=<id> agent=<id>] <human reason>
  ```

Assignments without either prefix were supplied explicitly by an
orchestrator (`--task-id` flag or `AGENT_LEDGER_TASK_ID` env var).

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

# Live (active) auto-assigned tasks, the most common review query:
agent-ledger assignments --status active --json \
  | jq '.assignments[] | select(.reason_marker == "auto")'
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

Verify emits a `MISSING_ASSIGNMENT` finding with severity `warning`
(not error) for auto-fallback tasks so CI can surface them without
blocking the merge.

### Replay idempotency

Bootstrap calls `agent-ledger assign --if-absent` for non-explicit sources (pr, branch, detached, and auto), so repeated pi launches on the same branch do not create duplicate `task.assigned` events. Dedupe is scoped to `(task_id, assigned_agent_id)`: a genuinely new `AGENT_ID` for the same branch still creates a new assignment, which is correct because the agent is new. Changes to `--allow`, `--forbid`, or `--policy` always create a new assignment, so policy drift remains visible to reviewers. Explicit `--task-id` and `AGENT_LEDGER_TASK_ID` paths still skip bootstrap assignment entirely, because the orchestrator owns it.

### Audit trail in v0.2

v0.2 of the kernel will add `--metadata` to `assign` (and likely
`claim`, `record`, `adopt`). Adapters will then write structured
`metadata.auto_assigned = true` while keeping the reason marker for
backwards compatibility. The `MISSING_ASSIGNMENT` finding will key
on the structured metadata flag.

This is a small kernel patch tracked under issue "agent-ledger v0.2:
--metadata on assign and friends". Until then the marker prefix is
the public surface for adapter audit.

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
   audit-trail section above. For sources `flag` and `env` the
   bootstrap trusts the orchestrator already wrote the assignment.
4. Export the resolved env vars for child processes. Shell callers use
   export lines; pi uses the helper's `--json` mode.

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
- Hooks `tool_call` for `subagent`. Auto-creates a child task id and
  passes it to the subagent via env so the child's pi extension picks
  up where the parent left off. This is how subagent inheritance is
  enforced deterministically.
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

### v0.1.1: structured audit and concurrency hardening

v0.1.1 closed the audit-trail gap the rc1/rc2 adapters carried:

1. **`assign --metadata <json>`** is now a kernel flag. The bootstrap
   probes `agent-ledger assign --help` for `--metadata` support; if
   present, it writes structured `metadata.auto_assigned`,
   `metadata.auto_assigned_by`, `metadata.task_source`, and
   `metadata.parent_task`. The reason marker is still emitted for
   forward-compatibility with older queries.
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

### Future kernel work

- Concurrent claim race in `claim` (warn and exclusive policies).
  Two integration tests are skipped pending the v0.1.2 kernel patch
  that moves `conflicts.Resolve` inside the `InsertIntent` BEGIN
  IMMEDIATE transaction.

## Stability

The env var contract above is the public surface. Adapter
implementations may evolve; the contract is what users pin against.
Renaming an env var is a breaking change and bumps the adapter
package's major version. Adding new optional vars is a minor.
