// Agent Ledger babysitter wrapper.
//
// Higher-order replacement for @a5c-ai/babysitter-sdk's defineTask
// that injects agent-ledger discipline as deterministic shell
// pre/post effects. Every wrapped agent task is bracketed by:
//
//   1. shell `agent-ledger assign ...` (idempotent, auto-assigned
//      metadata applied if no orchestrator-supplied task id).
//   2. the original agent task, with execution.env carrying
//      AGENT_LEDGER_TASK_ID, AGENT_LEDGER_PARENT_TASK_ID, AGENT_ID.
//   3. shell `agent-ledger verify --task <id> --json`. Non-zero exit
//      fails the wrapper task so the SDK halts.
//
// Drop-in usage in a process file:
//
//   import { defineLedgerTask as defineTask } from
//     '<path>/agent-ledger/adapters/babysitter/define-ledger-task.js';
//
// See agent-ledger/docs/adapters.md for the env var contract.

import { defineTask } from "@a5c-ai/babysitter-sdk";
import { buildAssignmentMarker } from "../shared/marker.js";

/**
 * defineLedgerTask wraps a defineTask factory with assign/verify
 * shell pre/post effects.
 *
 * @param {string} name - task name (same as defineTask).
 * @param {(args: any, taskCtx: any) => any} factory - the original
 *   task factory function. Receives `(args, taskCtx)` and returns a
 *   task definition.
 * @param {object} [options]
 * @param {string} [options.taskIdField='taskId'] - field on `args` to
 *   read the agent-ledger task id from. Defaults to `taskId`.
 * @param {string} [options.parentTaskField='parentTaskId'] - field
 *   on `args` for the parent task id (links child to parent).
 * @param {string[]} [options.allow=['**']] - default --allow globs
 *   for assign when args.allow is not provided.
 * @param {'warn'|'exclusive'} [options.policy='warn'] - default
 *   --policy for assign.
 * @param {boolean} [options.failOnVerify=true] - if false, verify
 *   findings are logged but do not fail the wrapper task.
 */
export function defineLedgerTask(name, factory, options = {}) {
  const opts = {
    taskIdField: "taskId",
    parentTaskField: "parentTaskId",
    allow: ["**"],
    policy: "warn",
    failOnVerify: true,
    ...options,
  };

  return defineTask(name, (args, taskCtx) => {
    const inner = factory(args, taskCtx);
    if (!inner || typeof inner !== "object") {
      throw new Error(`defineLedgerTask(${name}): factory must return a task definition`);
    }

    const taskId = String(args?.[opts.taskIdField] ?? `auto/${name}/${taskCtx.effectId}`);
    const parentTaskId = args?.[opts.parentTaskField] ? String(args[opts.parentTaskField]) : "";
    const allow = Array.isArray(args?.allow) && args.allow.length ? args.allow : opts.allow;
    const policy = args?.policy ?? opts.policy;
    const orchestrator = args?.orchestrator ?? "babysitter";
    const agentId = args?.agentId ?? `babysitter/${name}`;
    const reason = args?.reason ?? `babysitter task ${name}`;

    const ledgerEnv = {
      AGENT_LEDGER_TASK_ID: taskId,
      AGENT_LEDGER_PARENT_TASK_ID: parentTaskId,
      AGENT_ID: agentId,
    };

    // Inject env into the inner agent task so the agent runtime sees
    // it. SDK schema: execution.env is the documented surface.
    const innerExecution = inner.execution ?? {};
    const innerEnv = innerExecution.env ?? {};
    inner.execution = {
      ...innerExecution,
      env: { ...innerEnv, ...ledgerEnv },
    };

    // Pre-shell: assign. The v0.1 kernel does not have --metadata on
    // assign, so audit-trail markers are encoded in the reason text
    // with a leading bracketed token. Reviewers filter via:
    //   jq '.assignments[] | select(.reason | startswith("[auto-assigned"))'
    // The kernel will gain --metadata in v0.2; the marker syntax is
    // forward-compatible.
    const allowArgs = allow.flatMap((g) => ["--allow", g]);
    const isAutoDerived = !args?.[opts.taskIdField];
    const wrappedReason = isAutoDerived
      ? `${buildAssignmentMarker({ by: "babysitter-wrapper", parent: parentTaskId, task: name, agent: agentId, effect: taskCtx.effectId })} ${reason}`
      : reason;

    const assignCommand = [
      "agent-ledger",
      "assign",
      "--task", shellQuote(taskId),
      "--orchestrator", shellQuote(orchestrator),
      "--agent", shellQuote(agentId),
      "--policy", shellQuote(policy),
      ...allowArgs.map(shellQuote),
      "--reason", shellQuote(wrappedReason),
    ].join(" ");

    // Post-shell: verify. Non-zero exit fails the wrapper task when
    // failOnVerify is true (default).
    const verifyCommand = [
      "agent-ledger",
      "verify",
      "--task", shellQuote(taskId),
      "--json",
    ].join(" ");

    // Build a chain. Babysitter SDK supports multi-step compositions
    // via taskCtx.chain in some patterns; for portability we return
    // the agent task directly with execution.env set, and emit the
    // assign and verify as side-channel hints in metadata so the
    // process runner can prepend / append them as separate effects.
    //
    // The simple-and-portable approach below: the wrapper returns a
    // chain object the SDK can consume. If the SDK at this version
    // does not support an inline chain, the runner falls back to
    // emitting just the inner task plus a side-effect log; users
    // then run `make agent-ledger-assign` / `make agent-ledger-verify`
    // out of band. See README for the alternative low-tech pattern.
    return {
      kind: "chain",
      title: `${inner.title ?? name} (ledger-wrapped)`,
      steps: [
        {
          kind: "shell",
          title: `agent-ledger assign (${taskId})`,
          shell: { command: assignCommand },
          io: {
            inputJsonPath: `tasks/${taskCtx.effectId}/ledger-assign-input.json`,
            outputJsonPath: `tasks/${taskCtx.effectId}/ledger-assign-output.json`,
          },
        },
        inner,
        {
          kind: "shell",
          title: `agent-ledger verify (${taskId})`,
          shell: { command: verifyCommand },
          failOnError: opts.failOnVerify,
          io: {
            inputJsonPath: `tasks/${taskCtx.effectId}/ledger-verify-input.json`,
            outputJsonPath: `tasks/${taskCtx.effectId}/ledger-verify-output.json`,
          },
        },
      ],
    };
  });
}

function shellQuote(s) {
  return `'${String(s).replace(/'/g, "'\\''")}'`;
}

export { shellQuote };
export default defineLedgerTask;
