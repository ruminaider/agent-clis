# Agent Ledger pi extension

Deterministic, workflow-wrapped enforcement of `agent-ledger` claim
and record discipline for any pi session. Hooks pi's `tool_call` and
`tool_result` events to call the kernel CLI on every `Edit`, `Write`,
`MultiEdit`, and (with caveats) `Bash`. Every subagent spawned through
the `subagent` tool self-assigns its own task from its own session
bootstrap.

See `agent-ledger/docs/adapters.md` for the env var contract and
auto-assignment design.

## Install

```bash
# From the agent-ledger repo:
agent-ledger/adapters/pi/install.sh
```

This symlinks `agent-ledger.ts` and the shared bootstrap helpers into
`~/.pi/agent/extensions/`. Pi loads `.ts` extensions directly through
its TypeScript loader, so no build step is required on pi versions
that support extensions. Reload pi (`/reload` in the TUI) and the
extension is active.

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
  unset, the extension derives a task from the PR, branch, detached
  HEAD, or, as a last resort, `auto/<agent>/<utc-timestamp>`. That
  derivation runs on the first tool call that can change the project,
  so read-only tools and subagent management calls never create task
  context. When set, the extension verifies an active assignment
  exists and fails early if the orchestrator forgot one. Emergency
  repair requires
  `AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT=1` and
  `AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW`.
- `AGENT_LEDGER_REQUIRE_TASK=1`: opt into fail-closed mode. With
  this set, the extension blocks every edit if `AGENT_LEDGER_TASK_ID`
  is missing.

## What gets enforced

| Pi tool | What the extension does |
| ------- | ----------------------- |
| `write` / `edit` / `multi_edit` | Pre: `agent-ledger claim` for the path(s). Post: `agent-ledger record` with summary derived from input. Block on claim failure. |
| `bash` | Default: warn and let it run, snapshotting `git status --porcelain` before the call and claim/recording paths that become newly dirty after it returns. `AGENT_LEDGER_BASH_MODE=block` blocks all bash tool calls because shell mutation detection is not complete. |
| `subagent` execution | Pre (observation-only): record that a dispatch was initiated (parent task id, child agent name, dispatch timestamp) for correlation. The parent extension does not mutate `process.env` and does not call `agent-ledger assign`. Each spawned child detects `PI_SUBAGENT_CHILD=1` at extension load, derives a deterministic child task id (`<parent>/<agent>/<run_id>-<index>`) and a fresh child `AGENT_ID` (`agent:pi:subagent:<run_id>:<index>`), and self-assigns via `agent-ledger assign --if-absent`. Parallel fan-outs, async dispatch, and multiple `subagent()` calls in one turn all work correctly because every child is independent. |
| Read-only tools and `subagent` management | Nothing: no bootstrap, no claim, no record. These calls never create or require task context. |

## Subagent inheritance

When the parent pi extension intercepts a `subagent` execution call,
it records the dispatch event (parent task id, child agent name,
dispatch timestamp) for correlation and telemetry. It does not
mutate the child's environment and does not write a ledger assignment
on the child's behalf.

Each spawned child loads the same pi extension. Because pi-subagents
sets `PI_SUBAGENT_CHILD=1` in the child's environment, the extension
runs its bootstrap eagerly at extension load, before any tool call,
and selects `task_source=subagent`. The bootstrap derives a
deterministic child task id from four inherited inputs (parent task id,
child agent name, run id, and child index):

```
<parent_task>/<child_agent>/<run_id>-<child_index>
```

It also derives a fresh child `AGENT_ID`:

```
agent:pi:subagent:<run_id>:<child_index>
```

The child uses this id for every `claim`, `record`, `heartbeat`, and
`close` event. The inherited parent `AGENT_ID` is recorded as
`orchestrator_id` on the child's assignment row. Attribution stays
correct across parallel fan-outs: two children launched simultaneously
each get distinct task ids and agent ids with no coordination needed
between them.

A retry of the same logical child (same run id and child index)
produces the same task id and agent id, so `assign --if-absent` is a
no-op on retry. A new dispatch (different run id or child index)
produces a fresh task id and agent id.

For long-lived workers instructed later through intercom or prompt
text, the orchestrator still must run `agent-ledger assign --if-absent`
before it sends the worker a task brief or claim command. Session
bootstrap will not re-run for that later task id.

## Auto-assigned tasks: finding the gaps

Sessions where the adapter had to create a fallback or explicit
repair assignment carry either `reason_marker == "auto"` or
`metadata.explicit_missing_assignment == true`. To audit:

```bash
agent-ledger assignments --status active --json \
  | jq '.assignments[] | select(.reason_marker == "auto" or .metadata.explicit_missing_assignment == true)'
```

Verify (`agent-ledger verify --task <id> --json`) emits an
`AUTO_ASSIGNED_TASK` warning for adapter-derived tasks so CI can
surface them without blocking merges. A true missing assignment row
still reports `MISSING_ASSIGNMENT`.

## Caveats

- **Bash is fuzzy.** The extension cannot statically know which paths
  a `bash` invocation will mutate. The default warn-then-scan mode
  catches most cases but misses files written outside the project
  root or via `chmod`. SPEC §33 open decision #2 covers the design
  trade-off. For high-trust workflows, use `AGENT_LEDGER_BASH_MODE=block`
  to block bash entirely.
- **Parallel subagent dispatch is supported.** Each spawned child
  self-assigns from its own bootstrap, so two `subagent()` calls in
  the same assistant turn each land in their own assignment row with
  distinct task ids and agent ids. No env-mutation guard or
  serialization lock exists. Parallel `subagent({ tasks: [...] })`
  fan-outs, multiple independent `subagent()` calls, and `count: N`
  expansions all work correctly without any ordering constraint.
- **Async subagent dispatch works correctly.** `subagent({ async: true })`
  launches the child in a background runner. The child inherits
  `PI_SUBAGENT_CHILD=1` and its run context from pi-subagents, so the
  child's session bootstrap runs eagerly at extension load, reads the
  inherited parent task id and run context, and self-assigns a
  deterministic child task id. Ledger attribution is guaranteed rather
  than best-effort; the child does not fall back to branch or auto
  detection.
- **Concurrent exclusive claims have a known race** in v0.1.0
  (tracked for v0.1.x). Two parallel pi sessions both claiming an
  exclusive path may both win. Until the kernel fix lands, serialize
  exclusive claims through one orchestrator.
- **The extension is best-effort on record failure.** A failed
  `agent-ledger record` is logged to stderr but does not roll back
  the edit (the file is already on disk). `verify` will catch any
  edit that was claimed but not recorded.
