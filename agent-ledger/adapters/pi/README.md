# Agent Ledger pi extension

Deterministic, workflow-wrapped enforcement of `agent-ledger` claim
and record discipline for any pi session. Hooks pi's `tool_call` and
`tool_result` events to call the kernel CLI on every `Edit`, `Write`,
`MultiEdit`, and (with caveats) `Bash`. Every subagent dispatched
through the `subagent` tool inherits the discipline by env-var
injection.

See `agent-ledger/docs/adapters.md` for the env var contract and
auto-assignment design.

## Install

```bash
# From the agent-ledger repo:
agent-ledger/adapters/pi/install.sh
```

This symlinks `agent-ledger.ts` and the shared session-bootstrap
helper into `~/.pi/agent/extensions/`. Reload pi (`/reload` in the
TUI) and the extension is active.

To install for one project only, copy or symlink the same files into
`<project>/.pi/extensions/` instead.

## Required setup per project

```bash
cd <project-root>
agent-ledger init --project-id <stable-id> --write-pointer
```

That creates the SQLite ledger and the project pointer.

## Env vars at a glance

The full contract is in `docs/adapters.md`. The two you most often
set explicitly:

- `AGENT_LEDGER_TASK_ID`: the task you are working on. When
  unset, the extension auto-derives `auto/<agent>/<utc-timestamp>`
  and writes an assignment with `metadata.auto_assigned = true` so
  reviewers can find sessions where the orchestrator forgot.
- `AGENT_LEDGER_REQUIRE_TASK=1`: opt into fail-closed mode. With
  this set, the extension blocks every edit if `AGENT_LEDGER_TASK_ID`
  is missing.

## What gets enforced

| Pi tool | What the extension does |
| ------- | ----------------------- |
| `write` / `edit` / `multi_edit` | Pre: `agent-ledger claim` for the path(s). Post: `agent-ledger record` with summary derived from input. Block on claim failure. |
| `bash` | Default: warn and let it run, then post-scan `git status --porcelain` and claim/record any newly-modified paths. `AGENT_LEDGER_BASH_MODE=block` switches to fail-closed for known mutating commands (`rm -rf`, `git checkout`, `sed -i`). |
| `subagent` | Pre: auto-assign a child task (`<parent>/<subagent>/<id>`), inject `AGENT_LEDGER_TASK_ID` and `AGENT_LEDGER_PARENT_TASK_ID` into the subagent's env so its own pi extension picks up the chain. |

## Subagent inheritance

When the parent pi extension intercepts a `subagent` tool call, it
auto-creates a child task id and injects it into the subagent
invocation's env. The child pi process loads the same extension from
`~/.pi/agent/extensions/`, picks up the env vars in its own
`tool_call` bootstrap, and proceeds with claim/record against the
child task. The parent's task id is recorded as the child task's
`metadata.parent_task` for audit.

This means the orchestrator does not have to manually call
`agent-ledger assign` before each subagent dispatch. The extension
does it on every `subagent` tool call.

## Auto-assigned tasks: finding the gaps

Sessions where the orchestrator forgot to set `AGENT_LEDGER_TASK_ID`
produce assignments with `metadata.auto_assigned = true`. To audit:

```bash
agent-ledger status --json | jq '.assignments[] | select(.metadata.auto_assigned)'
```

Verify (`agent-ledger verify --task <id> --json`) emits a
`MISSING_ASSIGNMENT` finding with severity `warning` for these tasks
so CI can surface them without blocking merges.

## Caveats

- **Bash is fuzzy.** The extension cannot statically know which paths
  a `bash` invocation will mutate. The default warn-then-scan mode
  catches most cases but misses files written outside the project
  root or via `chmod`. SPEC §33 open decision #2 covers the design
  trade-off. For high-trust workflows, use `AGENT_LEDGER_BASH_MODE=block`.
- **Concurrent exclusive claims have a known race** in v0.1.0
  (tracked for v0.1.x). Two parallel pi sessions both claiming an
  exclusive path may both win. Until the kernel fix lands, serialize
  exclusive claims through one orchestrator.
- **The extension is best-effort on record failure.** A failed
  `agent-ledger record` is logged to stderr but does not roll back
  the edit (the file is already on disk). `verify` will catch any
  edit that was claimed but not recorded.
