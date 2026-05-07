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
