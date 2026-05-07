# Planning Context: Self-Assigning Subagent Children (Option D, tightened)

## Decisions Locked In (user-approved)

These four decisions are baked into the plan and are not open for executor
debate. The plan and any reviewer must honor them:

1. **`AUTO_ASSIGNED_TASK` policy on subagent children: SUPPRESS.** When
   `verify` encounters an assignment with
   `metadata.dispatch_origin = pi-subagent-bootstrap`, it must NOT emit
   the `AUTO_ASSIGNED_TASK` warning. The warning continues to fire for
   true adapter-derived self-bootstrap (`branch`, `detached`, `auto`,
   explicit-repair). This is a behavior change in `verify` output;
   downstream CI or dashboards that count the warning will see fewer
   warnings on subagent-heavy projects. Documented in Task 1 (spec) and
   implemented in Task 7 (verify).

2. **Bootstrap timing for subagent children: EAGER.** When the bootstrap
   detects `PI_SUBAGENT_CHILD=1`, it runs at extension load (before any
   tool call), creating the assignment row immediately. Trade-off
   accepted: every child pays one `agent-ledger assign --if-absent`
   round-trip even if it never edits anything. Benefit: audit
   chronology stays clean (`task.assigned` precedes any later
   `intent.opened`), and zero-tool children leave a row.

3. **Missing parent task in child mode: HARD-FAIL.** If `PI_SUBAGENT_CHILD=1`
   is set but `AGENT_LEDGER_TASK_ID` (the inherited parent task id) is
   not present, bootstrap exits non-zero with a clear diagnostic. It
   does NOT fall back to `branch`, `auto`, or any other task source.
   Trade-off accepted: orchestrator flows that currently dispatch
   subagents without first bootstrapping the parent will surface
   visibly. That is the desired regression.

4. **Parent-side `subagent` `tool_call` hook: OBSERVATION-ONLY** (not
   removed). The hook stays in `adapters/pi/agent-ledger.ts`, but its
   body is reduced to recording a single "dispatch initiated" entry
   (parent task id, child agent name, dispatch timestamp) for
   correlation, telemetry, and future cross-cutting concerns. It does
   NOT mutate `process.env`, NOT call `agent-ledger assign`, NOT block.
   The exact recording mechanism (stderr line, ledger event, file
   append) is the executor's choice during Task 5; the lightest option
   that preserves the hook point is acceptable.

5. **Child task-id format: deterministic.**
   `<parent_task>/<child_agent>/<run_id>-<child_index>`. Inputs are:
   - `parent_task`: the inherited `AGENT_LEDGER_TASK_ID`.
   - `child_agent`: `PI_SUBAGENT_CHILD_AGENT`.
   - `run_id`: `PI_SUBAGENT_RUN_ID`.
   - `child_index`: `PI_SUBAGENT_CHILD_INDEX` rendered as a decimal
     integer string (no padding).

   The format is fully deterministic from these four inputs. No
   random suffix, no timestamp. Same logical child across retries
   produces the same task id, which lets `agent-ledger assign
   --if-absent` reuse the existing assignment row instead of
   creating a duplicate. This matches oracle's directive: "do not
   add a random suffix unless you can prove retries must fork a new
   logical task."

6. **Child `AGENT_ID` format: deterministic.**
   `agent:pi:subagent:<run_id>:<child_index>`. Same inputs as the
   task-id derivation. Two consequences:

   - On a pi-subagents internal retry of the same logical child
     (same `run_id`, same `child_index`), the child gets the same
     `AGENT_ID` and the same task id, so `agent-ledger assign
     --if-absent` is a no-op and `verify` does NOT raise
     `AGENT_MISMATCH` for the second spawn's claims and records.
   - On a genuinely new dispatch (different `run_id` or different
     `child_index`), the child gets a fresh `AGENT_ID` and a fresh
     task id, so it is a new agent with its own assignment row.

   The inherited parent `AGENT_ID` is preserved separately for use
   as `--orchestrator` on the child's `assign` call. The child uses
   only the fresh derived id for its own `claim`, `record`,
   `heartbeat`, and `close` events.

7. **Assignment metadata schema** for subagent-created child rows.
   The `assign --metadata` JSON payload must include:

   - `parent_task`: string. Inherited parent task id.
   - `parent_agent_id`: string. Inherited parent `AGENT_ID`.
   - `subagent_run_id`: string. `PI_SUBAGENT_RUN_ID` verbatim.
   - `subagent_child_index`: number (decimal integer).
     `PI_SUBAGENT_CHILD_INDEX` parsed as int.
   - `subagent_child_agent`: string. `PI_SUBAGENT_CHILD_AGENT`
     verbatim.
   - `dispatch_origin`: string literal `"pi-subagent-bootstrap"`.
     This is the discriminator `verify` reads to suppress
     `AUTO_ASSIGNED_TASK` per decision 1.

   Reason text remains an audit hint. Metadata is the authoritative
   surface for programmatic readers (verify, audit, cross-tool
   correlation).

Reviewers and the executor must treat these four decisions as fixed
constraints. If a reviewer surfaces a counter-argument that materially
affects correctness, surface it as a P0/P1 finding and escalate before
executing; do not silently override.

## Decision

Implement **Option D: self-assigning children**. Move `agent-ledger assign`
from the parent's `tool_call` hook in `adapters/pi/agent-ledger.ts` to the
child's bootstrap in `adapters/shared/session-bootstrap.sh`. The parent
extension stops mutating `process.env`, drops the single-flight env-injection
guard, and stops calling `assign` on behalf of the child. Each spawned child
generates its own task id, derives a fresh agent identity, calls
`agent-ledger assign` itself, and proceeds.

This is the only option among the seven considered that is correct across
**all** dispatch modes: single child, `tasks:[...]` parallel fan-out, chains
with parallel sub-blocks, `count: N` expansion, async/background dispatch,
and multiple separate `subagent()` tool calls in one assistant turn.

## Binding Constraints (from oracle adversarial review)

These are not optional. They are preconditions for the design being correct.

1. **Child task ids are derived in the child, not the parent.** The parent
   never mints a child task id.

2. **Child task ids are deterministic** from
   `(parent_task, child_agent, run_id, child_index)`. **No random suffix**
   unless retries must fork a new logical task. Determinism gives idempotent
   retries and a cleaner audit trail.

   Suggested format: `<parent_task>/<child_agent>/<PI_SUBAGENT_RUN_ID>-<PI_SUBAGENT_CHILD_INDEX>`.
   The planner should confirm this format and document any rationale for
   alternatives.

3. **Derive a fresh child `AGENT_ID` when `PI_SUBAGENT_CHILD=1`.** The biggest
   hidden requirement. Today the child inherits the parent's `AGENT_ID`
   through its environment, which would misattribute claims and records
   under the new model. The bootstrap must:

   - Detect `PI_SUBAGENT_CHILD=1`.
   - Preserve the inherited parent `AGENT_ID` separately (it becomes the
     child assignment's `orchestrator_id`).
   - Generate a fresh child `AGENT_ID` (e.g.
     `agent:pi:subagent:<timestamp>:<short-hash>` or similar; reuse the
     existing `bootstrap_derive_agent_id` helper if compatible).
   - Use the fresh child id for all child-side claim/record/heartbeat/close
     events.

4. **Bootstrap eagerly for subagent children**, OR guarantee that the
   assignment is durable before the child can issue its first claim.
   Today bootstrap is lazy (runs on first tool call). For subagent children,
   the planner must pick one of:

   - Eager bootstrap on extension load when `PI_SUBAGENT_CHILD=1` is set.
     Pro: assignment row exists immediately, audit chronology preserved,
     no-tool children still leave a row.
   - Lazy but ordered: first tool call triggers bootstrap; bootstrap is
     synchronous and completes before the tool action proceeds.
     Pro: avoids work for never-acting children. Con: no-tool children
     leave no assignment row.

   The plan must pick one and document the chronology contract explicitly.

5. **Explicit metadata for child assignment origin and parent linkage.**
   Do not rely on the reason-text marker alone. The assignment metadata
   must include enough structured fields that a verifier or reviewer can
   tell, programmatically, that this is a subagent-created child and what
   its parent is.

## Hidden Risks Oracle Flagged

The plan must address each:

- **Inherited `AGENT_ID` trap.** Without (3), the redesign still
  misattributes work even after parallelism is fixed.
- **Audit chronology shifts** from dispatch-time to child-bootstrap-time
  (or first-action-time, depending on (4)). Tools or workflows that read
  `task.assigned` immediately after dispatch must be updated, or the plan
  must commit to eager bootstrap.
- **Zero-tool children** (lazy bootstrap path only): a child that never
  invokes a tool leaves no assignment row. Decide explicitly whether
  acceptable.
- **Cross-repo subagents.** When a child resolves a different ledger than
  its parent (different `cwd` per task), `parent_task` is informational
  cross-ledger linkage, not a relational guarantee. Document the rule and
  whether `parent_task` metadata is set in this case.
- **Hard dependency on `PI_SUBAGENT_RUN_ID` and `PI_SUBAGENT_CHILD_INDEX`.**
  These are pi-subagents internals. The bootstrap must fail loudly (error
  exit, clear diagnostic) if either is missing in `PI_SUBAGENT_CHILD=1`
  mode, rather than silently falling back to branch/auto.
- **Verify finding semantics.** Today subagent-created children would
  surface as `AUTO_ASSIGNED_TASK` warnings in `verify`. Decide upfront
  whether subagent children keep that surfacing, or whether they need
  distinct metadata that means "adapter-created, but orchestrator-initiated"
  so reviewers do not get noisy warnings that blur real
  orchestrator-forgot cases.

## The Invariant to Write Down

Before implementation begins, the plan must commit this invariant in
`SPEC.md` (or an adapter spec doc):

> For a pi subagent child, no `claim`, `record`, `heartbeat`, or `close`
> may execute until a durable assignment exists for that child task in
> the same ledger, and every child-side event must use a child `AGENT_ID`
> while the assignment's `orchestrator_id` remains the parent agent
> identity. Define explicitly whether assignment visibility is required
> at dispatch time or only before first child action.

## Minimum Verification (executor cannot declare done without these)

1. **E2E single child**: assignment row exists, `parent_task` metadata set,
   child claims and records under a child `AGENT_ID`, `verify --task <child>`
   passes with no `AGENT_MISMATCH`.
2. **Parallel `tasks:[...]`** with two children sharing the same agent name
   and same task text. Distinct task ids. Correct attribution.
3. **Two separate `subagent()` calls in one assistant turn**, both child
   index 0, identical task text. Both succeed in parallel. No
   cross-attribution. This is the user's primary complaint case.
4. **`count: N` test**. Each expanded child gets a unique deterministic
   task id.
5. **Async/background dispatch** (`subagent({ async: true })`). Child
   self-assigns the intended task, not branch-derived or auto-fallback.
6. **Retry/respawn**. If the same child is restarted within the same run,
   assignment reuse behavior matches the chosen deterministic-id policy.
7. **Audit ordering**: `task.assigned` precedes the first `intent.opened`
   for that child task.
8. **Cross-repo `cwd`**. Child writes to the ledger its own cwd resolves,
   not silently to the parent's ledger.

## Existing Code to Read

The plan must be grounded in the following files. Read them before writing
the plan; do not guess at their shapes.

### Adapter (changes here)

- `adapters/pi/agent-ledger.ts` — current pi extension. The subagent hook
  is around line 354-410 (`if (SUBAGENT_TOOLS.has(toolName))`). The
  single-flight guard at line 360, env mutation at line 410, restore in
  `tool_result` at line 442-446.
- `adapters/shared/session-bootstrap.sh` — current bootstrap. Read
  end-to-end. Existing task sources: `flag`, `env`, `pr`, `branch`,
  `detached`, `auto`. The plan adds a new source: `subagent`.
- `adapters/shared/marker.sh` and `adapters/shared/marker.js` — assignment
  reason marker helpers. The plan likely needs a new marker source value
  (e.g., `subagent`) and possibly new metadata fields.
- `adapters/pi/README.md` — current dispatch caveats (lines ~107-130).
  Will need rewriting to describe the new model.
- `adapters/tests/run.sh` — adapter test suite. The plan adds new test
  scenarios covering the eight verification cases above.

### Kernel CLI (potentially changes here)

- `internal/commands/assign.go` — `agent-ledger assign` command. May need
  new flags or metadata fields:
  - explicit `--orchestrator` (already exists, but its semantics may
    change; today it identifies the calling agent, in the new model it
    identifies the parent agent which is NOT the calling child).
  - explicit `--parent-task` flag for parent linkage in metadata.
  - explicit subagent-origin metadata flag so verify can distinguish
    subagent-created children from `auto`-fallback children.
- `internal/verify/` — verify finding catalog. Decide whether
  `AUTO_ASSIGNED_TASK` continues to fire on subagent children, or whether
  a distinct warning (or no warning) is appropriate.
- `SPEC.md` — invariant section above must be written here.

### pi-subagents (read-only reference, do NOT plan changes here)

These files are the upstream contract the plan rides on. The plan must
cite them as load-bearing:

- `~/.local/share/mise/installs/node/25.9.0/lib/node_modules/pi-subagents/src/runs/shared/pi-args.ts`
  — defines `PI_SUBAGENT_CHILD`, `PI_SUBAGENT_CHILD_AGENT`,
  `PI_SUBAGENT_CHILD_INDEX`, `PI_SUBAGENT_RUN_ID`.
- `~/.local/share/mise/installs/node/25.9.0/lib/node_modules/pi-subagents/src/runs/foreground/execution.ts:201`
  — foreground spawn site, `spawnEnv = { ...process.env, ...sharedEnv, ... }`.
- `~/.local/share/mise/installs/node/25.9.0/lib/node_modules/pi-subagents/src/runs/background/subagent-runner.ts:221`
  — background spawn site, same env shape.

## Out of Scope

- Changes to pi-subagents itself. pi-subagents 0.24.0 is treated as a fixed
  dependency.
- Ledger daemon. That is a future architectural concern, not part of this
  plan.
- Function-level monkey-patching of pi-subagents (Option F). Rejected by
  oracle as fragile.
- Kernel changes that allow `claim` to imply `assign` (Option G). Rejected
  by oracle as a semantics downgrade.

## In-Flight Work

PR https://github.com/ruminaider/agent-clis/pull/21 (branch
`fix/pi-child-task-id-collision`) is unmerged. It adds a random hex suffix
to child task ids minted by the parent. It is independently valuable for
documentation cleanup and fixes a latent bug in the current parent-mints
scheme. Once Option D ships, the parent-mints path is removed entirely
and the hex suffix becomes unused; the second PR (Option D) explicitly
notes the supersession.

The plan should:
- Treat PR #21 as already merged conceptually (assume the new branch
  starts from main with PR #21 applied).
- Note that the parent-side `generateChildTaskId` helper introduced by
  PR #21 will be deleted as part of Option D.

## Goal of the Plan

Produce a concrete, ordered, executable implementation plan that another
agent (`task-generator`, then `worker` agents) can execute without
guessing. Every binding constraint above must be addressed. Every hidden
risk must have a mitigation step. The eight verification cases must each
have a corresponding test task in the plan.

The plan should be small enough that a single PR can land it cleanly,
or large enough that it explicitly proposes splitting into multiple PRs
(in which case, name the split, the order, and the merge dependency).
