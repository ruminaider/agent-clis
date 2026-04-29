# Agent Ledger babysitter wrapper

> **Status (as of v0.2.0): experimental, opt-in.** The wrapper ships
> in the repo for users who want to invoke it from a Babysitter
> process file, but it is not part of the v0.2.x supported contract
> and has not been dogfooded at scale. The CLI surface, env-var
> convention, and chain-of-tasks shape may change in v0.3+. The
> v0.2.0 stable surface is the pi extension only; see
> `agent-ledger/adapters/pi/`.

`defineLedgerTask` is a higher-order replacement for the Babysitter
SDK's `defineTask`. It brackets every wrapped agent task with an
`agent-ledger assign` shell pre-step and a `agent-ledger verify` shell
post-step. The agent runs with `AGENT_LEDGER_TASK_ID`,
`AGENT_LEDGER_PARENT_TASK_ID`, and `AGENT_ID` injected into
`execution.env`, so the pi extension (or any other adapter) inside the
worker subagent picks up the task id automatically.

See `agent-ledger/docs/adapters.md` for the env var contract.

## Install

The wrapper is a single ESM JavaScript file with one dependency
(`@a5c-ai/babysitter-sdk`). No build step.

In a process file:

```javascript
// Replace this:
//   import { defineTask } from "@a5c-ai/babysitter-sdk";
//
// with:
import { defineLedgerTask as defineTask } from
  "../../path/to/agent-clis/agent-ledger/adapters/babysitter/define-ledger-task.js";
```

Every `defineTask(...)` call in the file now goes through the
wrapper. No other changes required.

## Usage

```javascript
export const codingTask = defineTask("coding-task", (args, taskCtx) => ({
  kind: "agent",
  title: "Implement feature X",
  agent: {
    name: "worker",
    prompt: { /* ... */ },
    outputSchema: { /* ... */ },
  },
  io: {
    inputJsonPath: `tasks/${taskCtx.effectId}/input.json`,
    outputJsonPath: `tasks/${taskCtx.effectId}/output.json`,
  },
}));
```

Caller invokes the task with the usual SDK arguments plus optional
ledger fields:

```javascript
{
  taskId: "feature-x-impl",      // required for non-auto assignment
  parentTaskId: "feature-x",      // optional; links child to parent
  agentId: "worker.opus",         // optional; defaults to "babysitter/<name>"
  orchestrator: "babysitter",     // optional; defaults to "babysitter"
  allow: ["src/**", "tests/**"],  // optional; defaults to ["**"]
  policy: "warn",                 // optional; "warn" or "exclusive"
  reason: "implement feature X",  // optional; defaults to "babysitter task <name>"
}
```

If `taskId` is omitted, the wrapper auto-derives
`auto/<task-name>/<effect-id>` and prefixes the assignment reason with
`[auto-assigned by babysitter-wrapper ...]`. Explicit `taskId` values
leave the reason unmodified.

## Wrapped task structure

Every wrapped task expands into a three-step chain:

```
chain[0]  shell: agent-ledger assign --task <id> --agent <agentId> ...
chain[1]  agent: original task definition (with execution.env injected)
chain[2]  shell: agent-ledger verify --task <id> --json   (failOnError)
```

The verify step is the gate: if it returns a non-zero exit the
wrapper task fails and the orchestrator can route the failure into
the cycle's normal remediation flow.

## Per-call options

| Option | Default | Effect |
| ------ | ------- | ------ |
| `taskIdField` | `"taskId"` | name of the field on `args` to read the task id from |
| `parentTaskField` | `"parentTaskId"` | name of the field for the parent task id |
| `allow` | `["**"]` | default --allow globs when `args.allow` is unset |
| `policy` | `"warn"` | default --policy when `args.policy` is unset |
| `failOnVerify` | `true` | when false, verify findings log but do not fail the wrapper |

## Caveats

- **SDK chain support.** The wrapper returns a `kind: "chain"` task.
  The SDK runner must support inline chain definitions. If your
  version does not, file an issue or fall back to the manual pattern:
  emit the assign and verify as separate `defineTask` shell tasks in
  your process file and dispatch them around the agent task by
  step ordering.
- **Worker must have agent-ledger on PATH.** The injected env vars
  are useless if the worker subagent cannot run `agent-ledger claim`
  in the spawned environment. Make sure the worker context inherits
  the operator's PATH or pin a known location with `AGENT_LEDGER_BIN`.
- **Failures inside the wrapper still fail the task.** A failed
  assign or verify halts the chain and surfaces as a wrapper-task
  failure in the SDK journal, which is what you want.
