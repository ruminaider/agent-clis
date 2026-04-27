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

## Auto-derived task ids: solving "the orchestrator forgot"

The Phase 1 kernel only enforces what the operator tells it. When a
worker is dispatched without `AGENT_LEDGER_TASK_ID` (orchestrator
missed the assign step), the adapter must choose between:

- **Fail closed**: block every edit until the orchestrator assigns.
  Safe but disruptive; punishes the worker for the orchestrator's
  miss.
- **Fail open**: let the worker edit without attribution. Defeats the
  point.
- **Auto-derive with audit trail (default)**: at session bootstrap,
  the adapter generates a task id of the shape
  `auto/<agent-slug>/<utc-timestamp>` and writes an assignment reason
  that starts with `[auto-assigned`. The worker proceeds. Reviewers
  filtering assignment reasons with that prefix see every session
  where the orchestrator forgot.

Auto-derivation is the default because it solves the "subagent is
useful even when the orchestrator forgot" requirement. Operators who
want strict enforcement set `AGENT_LEDGER_REQUIRE_TASK=1` and accept
the disruption.

### Audit trail in v0.1

The v0.1 kernel does not yet have a `--metadata` flag on `assign`,
so adapters encode the audit signal in the assignment's `reason`
text as a leading bracketed marker:

```
[auto-assigned by <source> auto-derived task=<task-id> parent=<parent-id> agent=<agent-id> effect=<effect-id>] <human reason>
```

Reviewers find every auto-derived assignment with:

```bash
agent-ledger status --json \
  | jq '.assignments[] | select(.reason | startswith("[auto-assigned"))'
```

Verify emits a `MISSING_ASSIGNMENT` finding with severity `warning`
(not error) for these tasks so CI can surface them without blocking
the merge.

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
2. Resolve `AGENT_LEDGER_TASK_ID`. If unset:
   - If `AGENT_LEDGER_REQUIRE_TASK=1`, error and exit non-zero.
   - Else derive `auto/<agent-slug>/<session-start-utc>` and proceed.
3. Resolve assignment for the task id. If
   `agent-ledger status --task <id> --json` returns no assignment, run
   `agent-ledger assign --task <id> --orchestrator "<adapter>" --agent
   "$AGENT_ID" --policy "$AGENT_LEDGER_AUTO_ASSIGN_POLICY"` with one
   `--allow` per colon-separated glob in
   `$AGENT_LEDGER_AUTO_ASSIGN_ALLOW` and a reason that starts with the
   v0.1 marker prefix above.
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

## Kernel dependencies and v0.2 work

The adapters work end to end against the v0.1 kernel for the core
claim / record / verify cycle. Two audit-trail features need v0.2
kernel work:

1. **`assign --metadata <json>`**: today's adapters encode the audit
   signal in `--reason` text with a leading `[auto-assigned by ...]`
   marker. v0.2 should add a `--metadata` flag (and likely matching
   flags on `claim`, `record`, `adopt`) so adapters write structured
   `metadata.auto_assigned = true`. The marker prefix stays as a
   forward-compatible fallback.
2. **Assignment query surface**: `agent-ledger status --task <id>
   --json` does not include assignments today. Reviewers cannot
   programmatically discover "every auto-assigned task in this
   ledger" without reading SQLite directly. v0.2 should add either
   `assignments` to the status output or a dedicated
   `agent-ledger assignments [--task <id>] [--orchestrator <id>] --json`
   command.

Until those land, the bootstrap script avoids the missing query by
only creating an assignment when it auto-derived the task id. When
the orchestrator supplied a task id, the bootstrap trusts that the
orchestrator already wrote the assignment and skips the assign step.
Reviewers who need the audit trail today can query SQLite directly
until the kernel surface lands.

## Stability

The env var contract above is the public surface. Adapter
implementations may evolve; the contract is what users pin against.
Renaming an env var is a breaking change and bumps the adapter
package's major version. Adding new optional vars is a minor.
