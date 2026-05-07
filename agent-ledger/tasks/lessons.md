# Lessons

Working notes on patterns and pitfalls discovered while operating
agent-ledger and its surrounding tooling. Each entry names the failure
mode, then the rule to prevent it.

## Subagent dispatch

### 1. Read-only advisory and decomposition agents trip the "completed without making edits" heuristic

The pi-subagents harness flags any subagent that returns advisory or
decomposition output without applying file edits as `failed` with the
message "Subagent completed without making edits for an implementation
task."

This is the correct default for executor agents (`worker`, `delegate`).
It is a false positive for any agent whose system prompt explicitly
forbids file edits. Confirmed cases (this conversation alone tripped
the heuristic on three of these):

- Advisory: `oracle`, `super-reviewer`, `gate-reviewer`, the
  `*-reviewer` family, `intent-explainer`,
  `reviewing-document-consistency`.
- Readiness/plan review: `readiness-reviewer`, `plan-reviewer`.
- Decomposition: `task-generator`. Returns a JSON task packet, not
  file edits.

Rule: when an agent returns with `failed` status under that specific
message, treat the signal as informational. Open the agent definition
(`subagent action:get agent:<name>`) to confirm read-only intent, then
read the artifact's contents to judge success. Do not retry on the
false positive.

The broader pattern: any subagent whose system prompt contains "You
are read-only" or whose `Tools` list omits `write`/`edit` will trip
this heuristic by design.

### 2. Forked subagent context plus Claude extended thinking is fragile

Pi forwards the parent conversation as inherited context when a
subagent is dispatched with `defaultContext: fork`. If the parent's
last assistant message contains a Claude `thinking` or
`redacted_thinking` block, the harness's serialization can break the
block's signature, and Anthropic's API rejects the next call with:

```
400 messages.<n>.content.<m>: `thinking` or `redacted_thinking` blocks
in the latest assistant message cannot be modified. These blocks must
remain as they were in the original response.
```

The subagent fails on its first turn before any work is done.

Rule: when dispatching a forked-context subagent on a Claude model,
prefer `context: "fresh"` if the brief is self-contained. If the brief
genuinely needs the parent conversation, dispatch on a non-Claude model
like `openai-codex/gpt-5.4` to sidestep the thinking-block class.

### 3. Model allowlist for subagent dispatch is tighter than `enabledModels`

`~/.pi/agent/settings.json` `enabledModels` lists the models the user
has turned on. The subagent dispatch layer enforces a separate, smaller
allowlist. As of this session: `anthropic/claude-opus-4-7`,
`openai-codex/gpt-5.4`, `anthropic/claude-sonnet-4-6`. Other models
(including `claude-haiku-4-5`, `gpt-5.5`, locals, kimi) are rejected
at dispatch time with an error message that names the allowed set.

Rule: when overriding the model on a subagent dispatch, validate
against the allowlist by attempting a small dispatch first, or read
the allowlist source if known. Do not assume `enabledModels` is the
authoritative set.

## agent-ledger pi adapter

### 4. The single-flight env-injection guard does not release on every failure path

`adapters/pi/agent-ledger.ts` mutates the parent's `process.env` in the
`subagent` `tool_call` hook and restores it in `tool_result`. It
records the snapshot in `state.subagentEnvRestores` keyed by
`event.toolCallId`. The guard refuses any subsequent subagent dispatch
while that map is non-empty.

Several failure modes leave entries orphaned:
- API errors mid-execute (e.g., 400 thinking-block rejection) where
  `tool_result` may not fire cleanly for the parent.
- Async dispatch errors that report back later via a separate
  `subagent-result` notification rather than the original tool's
  `tool_result`.
- Model-allowlist rejections at dispatch time that may bypass the
  hook chain.

Once orphaned, every future subagent dispatch is blocked until the pi
session is reloaded.

This bug is explicitly resolved by the Option D redesign that this
session designed (children self-assign, parent extension is
observation-only, the guard and env mutation are deleted). Until that
ships, the recovery is `/reload`.

Rule: if a subagent dispatch is refused with
`agent-ledger refused overlapping subagent env injection`, reload pi
to clear the stuck lock. After Option D ships, this whole class
disappears.

## pi-subagents async runner

### 5. Stale-run reconciliation can fire on healthy-but-slow runners

pi-subagents' async runner uses a stale-run reconciliation timer that
marks a run failed when no events have been written within a deadline.
The runner uses `stdio: "ignore"` when spawning the child Node process
(in `src/runs/background/async-execution.ts:159`). If the child is
slow to register its first event but is otherwise running fine, the
parent can prematurely declare it failed while the child is still
working. The child eventually completes and writes its result file,
which the parent picks up later as a delayed completion notification.

Observed sequence in this session:
1. Worker dispatch fired. Runner spawned at PID 6450.
2. Within ~1 second, parent reported "Async runner process 6450
exited or disappeared before writing a result. Marked run failed by
stale-run reconciliation."
3. The runner was actually still running. It eventually completed the
task (a real `git commit` and `git push`) and wrote a result file.
4. The result file landed and the parent emitted a delayed
"subagent-result" notification reporting `success: true`.

Rule: when a runner reports the stale-reconciliation failure with no
other evidence (no `output-0.log`, no `events.jsonl` entries beyond
the stale marker), do not assume the work failed. Check
`<async-subagent-results>/<run-id>.json` and `git log` on the target
branch before retrying or rolling back. The work may have completed.

### 6. Stale async cfg files are a real footgun

Every async dispatch writes a cfg file to
`/var/folders/.../pi-subagents-uid-<uid>/async-cfg-<run-id>.json`.
The runner is supposed to delete the cfg after reading it. If the
runner crashes at startup, or in earlier pi-subagents versions if
cleanup was best-effort, the cfg can persist indefinitely.

A leftover cfg file is a complete worker dispatch waiting to be
reaped: target cwd, task brief, model, system prompt, intercom
targets, the lot. Manually invoking the runner with one of these cfgs
as an argument re-executes the original dispatch in full, including
file edits, commits, and pushes to remotes.

Observed in this session: a cfg from six days earlier referenced an
unrelated repo, an unrelated worker brief, and a write-capable agent.
The runner picked it up cleanly during a manual diagnostic
invocation, and the worker made a real commit and pushed to a fork
remote in a different repo before the operator realized what had
happened.

Rule: never invoke `node $JITI .../subagent-runner.ts <cfg>` against
a cfg you did not just write. Treat any leftover cfg in
`pi-subagents-uid-*/async-cfg-*.json` as live ammunition. Cleanup
(`rm pi-subagents-uid-*/async-cfg-*.json`) is safe if the
corresponding runs are confirmed dead and no in-flight dispatch is
expected to consume them.

Upstream pi-subagents could harden this by requiring the cfg to
contain a freshness sentinel (timestamp + nonce) the runner verifies
before executing, refusing to run cfgs older than some threshold.
Until that lands, treat manual runner invocation as a foot-gun and
periodically clean up the cfg directory.
