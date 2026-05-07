# Implementation Plan

## Goal
Move pi subagent assignment creation into child bootstrap so every child self-assigns deterministically, uses its own agent identity, and preserves correct audit order and attribution across single, parallel, repeated, counted, async, retry, and cross-repo dispatches.

## Tasks
1. **Record the new child-bootstrap contract in the spec and adapter design docs**
   - File: `SPEC.md` (event ordering around lines 466-476, assign command section around lines 656-672, pi adapter requirements around lines 975-979)
   - File: `docs/adapters.md` (task-id resolution around lines 27-71, audit-trail metadata around lines 127-182, session bootstrap around lines 213-238, pi subagent behavior around lines 245-263)
   - Changes: Add the invariant from `context.md` verbatim, define `task_source=subagent`, document that pi subagent children bootstrap eagerly, define the child task-id inputs `(parent_task, child_agent, run_id, child_index)`, and document that `metadata.parent_task` is informational when a child resolves a different ledger than its parent.
   - Acceptance: The docs explicitly say a child cannot `claim`, `record`, `heartbeat`, or `close` before its own durable assignment exists, and they distinguish child `AGENT_ID` from assignment `orchestrator_id`.

2. **Refactor `session-bootstrap.sh` for subagent child mode before the old task-source chain runs**
   - File: `adapters/shared/session-bootstrap.sh` (header and source description around lines 1-33, flag parsing around lines 39-55, agent-id logic around lines 102-119, task-id resolution around lines 121-179, assignment creation around lines 194-347, emitted env around lines 348-372)
   - Changes: Add a `subagent` source branch that runs before the current `flag`, `env`, `pr`, `branch`, `detached`, and `auto` chain. When `PI_SUBAGENT_CHILD=1`, require `PI_SUBAGENT_RUN_ID`, `PI_SUBAGENT_CHILD_INDEX`, `PI_SUBAGENT_CHILD_AGENT`, and an inherited parent `AGENT_LEDGER_TASK_ID`. Fail loudly if any are missing. Preserve the inherited parent `AGENT_ID` separately as the value passed to `--orchestrator` on the child `assign`. Derive the child task id with the locked format `<parent_task>/<child_agent>/<run_id>-<child_index>` (see `context.md` decision 5), where `<child_index>` is the decimal integer rendering of `PI_SUBAGENT_CHILD_INDEX` with no padding. Set `TASK_SOURCE=subagent`, and call `agent-ledger assign --if-absent` with inherited allow and policy, the locked metadata schema (decision 7 in `context.md`), and the new `subagent` reason marker source.
   - Acceptance: In child mode, bootstrap never falls back to branch or auto sources, emits `AGENT_LEDGER_TASK_SOURCE=subagent`, emits a child task id matching the locked format byte-for-byte, exits non-zero with a clear diagnostic when required child context is missing, and never silently uses the inherited parent task id as the child task id.

3. **Extract and reuse agent-id derivation so child identity cannot inherit the parent by accident**
   - File: `adapters/shared/session-bootstrap.sh` (current inline agent-id generation around lines 102-116)
   - Changes: Replace the inline `AGENT_ID` block with a reusable helper that derives both: a normal session id (the existing `agent:pi:<TIMESTAMP>:<RANDOM>` shape, used by non-subagent task sources) and a child-specific id with the locked format `agent:pi:subagent:<run_id>:<child_index>` (see `context.md` decision 6). The child path must trigger only when `PI_SUBAGENT_CHILD=1`, must be fully deterministic from `PI_SUBAGENT_RUN_ID` and `PI_SUBAGENT_CHILD_INDEX`, and must run before `identify`, `claim`, `record`, `heartbeat`, or `close`. The inherited parent `AGENT_ID` is preserved in a separate shell variable for use on the child `assign --orchestrator` flag.
   - Acceptance: When `PI_SUBAGENT_CHILD=1`, the bootstrap always exports an `AGENT_ID` matching `agent:pi:subagent:<run_id>:<child_index>` byte-for-byte, that id is distinct from the inherited parent `AGENT_ID`, the parent id is captured separately and forwarded as `--orchestrator`, and a respawn of the same logical child (same `run_id` and same `child_index`) produces the same `AGENT_ID` (proved by Task 10 scenario 6).

4. **Extend marker helpers and assignment metadata for the new subagent source**
   - File: `adapters/shared/marker.sh` (source switch around lines 10-47)
   - File: `adapters/shared/marker.js` (source classification around lines 1-33)
   - File: `adapters/pi/agent-ledger.ts` (inline marker helper around lines 199-209)
   - Changes: Add a `subagent` source value to the marker helpers (shell, JavaScript, inline TS) so the assignment reason marker carries `[harness-derived by ... source=subagent ...]`. Keep the shell and JavaScript helpers byte-identical (parity test in `adapters/tests/marker.test.mjs`). The assignment row's `--metadata` JSON payload must follow the locked schema in `context.md` decision 7:
     - `parent_task`: string.
     - `parent_agent_id`: string.
     - `subagent_run_id`: string.
     - `subagent_child_index`: number (decimal integer, JSON number type).
     - `subagent_child_agent`: string.
     - `dispatch_origin`: string literal `"pi-subagent-bootstrap"`.
   - Reason text remains an audit hint. Metadata is the authoritative surface for verify, audit, and cross-tool correlation.
   - Acceptance: Marker unit tests cover `source=subagent` with shell/JS parity. A bootstrap integration test asserts the metadata payload's exact JSON shape including the field types listed above (in particular, `subagent_child_index` must be a JSON number, not a string).

5. **Switch the pi extension from parent-side assignment to child-side self-assignment**
   - File: `adapters/pi/agent-ledger.ts` (task-source constants around lines 32-48, bootstrap state around lines 42-57, bootstrap loader around lines 100-147, marker helper around lines 199-209, subagent helpers around lines 278-319, lazy-bootstrap comment around lines 336-341, subagent hook around lines 354-413, subagent restore path around lines 447-454, exports around lines 492-507)
   - Changes:
     1. Add `subagent` to the `TaskSource` union and to `KNOWN_TASK_SOURCES`.
     2. Remove the parent-side child-task minting path: delete `generateChildTaskId`, the `randomBytes` import, `snapshotEnv`, `restoreEnv`, `subagentEnvRestores`, and the entire `tool_result` restore branch for subagent calls.
     3. Remove `collectSubagentCwds` and `resolveSubagentLedgerCwd` if no other code path uses them.
     4. **Keep the `subagent` `tool_call` hook present, but observation-only** (per `context.md` decision 4). The hook body is reduced to a single non-blocking record of the dispatch event (parent task id, child agent name, timestamp). It MUST NOT mutate `process.env`, MUST NOT call `agent-ledger assign`, MUST NOT return `{ block: true }`, and MUST NOT consult `subagentEnvRestores` or any equivalent guard. The exact recording mechanism is the executor's choice; the lightest acceptable option is a single `console.error` (stderr) line. Removing the hook entirely is NOT permitted.
     5. **Eager child bootstrap.** When `process.env.PI_SUBAGENT_CHILD === "1"` at extension load, the bootstrap runs immediately (not lazily on first tool call). The state machine must hold the result so subsequent tool calls see `state.bootstrapped === true` without a second bootstrap attempt.
   - Acceptance:
     - No code path in the parent extension mutates `process.env` for subagent dispatch (verifiable by static grep for `process.env[` writes within the subagent-tool branch).
     - No code path in the parent extension calls `runLedger(["assign", ...])` inside the subagent-tool branch (verifiable by static grep).
     - When `PI_SUBAGENT_CHILD=1` at extension load, bootstrap runs before any other `tool_call` hook can claim or record (verifiable by extension-level test that observes bootstrap completion before the first observed `tool_call` event in child mode).
     - The `subagent` `tool_call` hook is present in the file and contains exactly one observation path (verifiable by static grep that confirms the hook block exists AND does not contain forbidden tokens like `process.env[`, `runLedger`, `block: true`).

6. **Confirm `assign` can express parent and child identities without overloading fields incorrectly**
   - File: `internal/commands/assign.go` (flag surface around lines 18-54, row construction and `--if-absent` reuse around lines 95-170, replay equality around lines 177-216)
   - File: `internal/commands/assign_internal_test.go`
   - Changes: Audit current semantics and keep the existing shape if it already supports `--orchestrator=<parent-agent-id>` and `--agent=<child-agent-id>`. Add regression tests for the new metadata payload and the chosen retry policy so `--if-absent` behaves correctly for subagent rows. If the audit finds downstream assumptions that caller identity equals orchestrator identity, add the smallest compatible command or metadata adjustment before wiring bootstrap to it.
   - Acceptance: A child bootstrap can write a valid assignment row that names the parent as orchestrator and the child as assignee, and replay tests pin the intended reuse behavior.

7. **Adjust verify so orchestrator-initiated subagent children do not look like orchestrator-forgot sessions**
   - File: `internal/verify/verify.go` (finding description around lines 107-116, task-level finding emission around lines 401-417, `assignmentIsAutoDerived` around lines 1080-1098)
   - File: `internal/verify/verify_test.go` (auto-assigned coverage around lines 485-588)
   - Changes: Teach verify to treat `dispatch_origin=pi-subagent-bootstrap` separately from true adapter self-bootstrap paths such as branch, detached, auto, and explicit repair. Recommended behavior: suppress `AUTO_ASSIGNED_TASK` for valid subagent child assignments, while preserving current warnings for sessions where the adapter had to invent or derive the task without an orchestrator dispatch.
   - Acceptance: `verify --task <child-task>` passes without `AUTO_ASSIGNED_TASK` or `AGENT_MISMATCH` for a healthy subagent child, and existing branch and auto bootstrap warnings still fire.

8. **Rewrite pi adapter docs and release notes around the new behavior**
   - File: `adapters/pi/README.md` (tool summary around lines 58-76, subagent inheritance around lines 62-77, caveats around lines 99-129)
   - File: `docs/adapters.md` (same sections listed in Task 1, if that file remains the canonical bootstrap table)
   - File: `CHANGELOG.md` (Unreleased section around lines 11-20)
   - Changes: Replace the old env-injection story with child self-assignment, document `task_source=subagent`, document the eager child chronology choice, remove the serialized-dispatch caveat, remove the background inheritance caveat, and note that the parent-minted helper added by PR #21 is superseded by Option D.
   - Acceptance: No shipped docs describe subagent dispatch as serialized or describe background dispatch as losing ledger inheritance.

9. **Replace old static implementation-detail checks with bootstrap-focused adapter tests AND add extension-contract static checks**
   - File: `adapters/tests/run.sh` (static TypeScript checks around lines 14-45)
   - File: `adapters/tests/marker.test.mjs`
   - New File: `adapters/tests/pi-subagent-bootstrap.test.mjs`
   - Changes:
     1. Delete the obsolete static checks that require `subagentEnvRestores`, `process.env[key] = value` inside the subagent branch, the restore-order Python snippet, `generateChildTaskId`, `randomBytes`, and the old timestamp-only child-id guard.
     2. **Add new extension-contract static checks** in `adapters/tests/run.sh` that prove the three Task 5 contracts directly against `adapters/pi/agent-ledger.ts`:
        - The subagent `tool_call` hook block exists in the file (grep for the existing entry comment / `if (SUBAGENT_TOOLS.has(toolName))`).
        - The subagent hook block does NOT contain `runLedger(["assign"` (no parent-side assign call). Use a python or grep snippet that locates the hook block by its surrounding comment markers and asserts the forbidden token is absent within that block only.
        - The subagent hook block does NOT contain `process.env[` writes (no env mutation). Same scoping technique.
        - The extension load path checks `process.env.PI_SUBAGENT_CHILD === "1"` and triggers eager bootstrap (grep for both the env check and the eager-bootstrap call site).
     3. Replace the deleted shell-level checks with tests in the new `pi-subagent-bootstrap.test.mjs` that execute `session-bootstrap.sh` under synthetic subagent env and assert: hard failure for missing `PI_SUBAGENT_RUN_ID`, child index, child agent, or parent task; `TASK_SOURCE=subagent`; child task id matches the locked format byte-for-byte; child `AGENT_ID` matches the locked format byte-for-byte; parent and child `AGENT_ID` differ; the inherited parent `AGENT_ID` is preserved as the value passed to `--orchestrator`; metadata payload has the locked schema with the locked types; and shell/JavaScript marker parity for `source=subagent`.
   - Acceptance:
     - The adapter test suite no longer asserts the parent env-injection implementation details that Option D deletes.
     - The new extension-contract static checks prove the three Task 5 contracts hold in the source code.
     - The new shell-level tests cover deterministic id formats, missing-env failure modes, metadata schema, and marker parity.

10. **Add end-to-end adapter scenarios for all eight required verification cases, plus the zero-tool child check that justifies eager bootstrap**
   - File: `adapters/tests/run.sh`
   - New File: `adapters/tests/pi-subagent-e2e.test.mjs`
   - Changes: Build a real `agent-ledger` test binary once from the repo, then simulate parent and child sessions by calling `session-bootstrap.sh`, `agent-ledger claim`, `agent-ledger record`, `agent-ledger assignments`, and `agent-ledger verify` in temporary repos and ledgers. Add one discrete named test for each scenario from `context.md`:
     1. Single child, with assignment metadata and clean verify.
     2. Parallel `tasks:[...]` siblings with the same child agent name and same task text.
     3. Two separate `subagent()` calls in one turn, both `child_index=0`, differentiated by run id.
     4. `count: N` expansion with unique deterministic child task ids.
     5. Async or background dispatch, with child self-assignment instead of branch or auto fallback.
     6. Retry or respawn, pinned to the chosen deterministic-id and assignment-reuse policy.
     7. Audit ordering, proving `task.assigned` lands before the first `intent.opened` for the child task.
     8. Cross-repo `cwd`, proving the child writes to its own resolved ledger and treats `parent_task` as informational linkage.
     9. A no-tool child, proving eager bootstrap still leaves an assignment row.
   - Acceptance: Every scenario is a separate test with explicit assertions on task ids, metadata, agent attribution, verify findings, and ledger placement.

11. **Do a final sweep for superseded parent-minted child-id code and comments**
   - File: `adapters/pi/agent-ledger.ts`
   - File: `adapters/pi/README.md`
   - File: `CHANGELOG.md`
   - Changes: Remove any remaining references to parent-minted child ids, the random hex suffix helper, or env restoration behavior. Treat PR #21 as already present in the base branch and fully superseded by the child-bootstrap design.
   - Acceptance: No repository file still claims that the parent extension is the source of child task ids or child assignment rows.

## Files to Modify
- `SPEC.md` - add the invariant, chronology rule, and cross-ledger parent-task note.
- `docs/adapters.md` - update the canonical bootstrap source table, metadata schema, and pi subagent behavior.
- `adapters/shared/session-bootstrap.sh` - add subagent source handling, child identity derivation, metadata, and eager child assignment behavior.
- `adapters/shared/marker.sh` - add `source=subagent` support.
- `adapters/shared/marker.js` - add `source=subagent` support and keep parity with the shell helper.
- `adapters/pi/agent-ledger.ts` - remove parent-side env injection and assignment, add eager child bootstrap handling, and update task-source parsing.
- `internal/commands/assign.go` - confirm or extend flag semantics for parent-orchestrator and child-assignee rows.
- `internal/commands/assign_internal_test.go` - add replay and metadata regression tests for subagent assignments.
- `internal/verify/verify.go` - change verify classification for subagent-created child assignments.
- `internal/verify/verify_test.go` - cover the new verify classification.
- `adapters/pi/README.md` - rewrite subagent behavior and caveats.
- `adapters/tests/run.sh` - remove obsolete static greps, set up real binary test runs, and wire in new subagent tests.
- `adapters/tests/marker.test.mjs` - add subagent marker parity coverage.
- `CHANGELOG.md` - add an Unreleased note for child self-assignment and correct attribution.

## New Files
- `adapters/tests/pi-subagent-bootstrap.test.mjs` - unit-style bootstrap and diagnostic coverage for child mode.
- `adapters/tests/pi-subagent-e2e.test.mjs` - end-to-end child bootstrap, claim, record, verify, audit-order, retry, async, and cross-repo scenarios.

## Dependencies
- Task 1 should land first, because it pins the invariant, chronology rule, and metadata contract that code and tests must implement.
- Tasks 2, 3, and 4 are the core contract wave. Task 3 depends on Task 2's task-source structure. Task 4 depends on Task 1's invariant and Task 2's bootstrap surface.
- Task 6 depends on Task 4 because the kernel `assign` audit must validate the metadata schema Task 4 specifies.
- Task 5 depends on Task 2 because the extension consumes the new bootstrap outputs and removes the old parent-side path.
- Task 7 depends on Tasks 1, 2, 4, and 6, because the verify classification reads the metadata schema from Task 4 and the kernel field semantics confirmed in Task 6.
- Task 8 (docs and release notes) depends on Tasks 1, 2, 5, and 7, because docs must describe the final spec invariant, bootstrap behavior, extension behavior, and verify behavior.
- Task 9 depends on Tasks 2 through 5, because the obsolete static checks should not be removed until the new bootstrap and extension behavior exists.
- Task 10 depends on Tasks 2 through 7, because the end-to-end scenarios need the final task-id, metadata, verify, and chronology behavior.
- Task 11 is last, after the implementation and docs no longer rely on any PR #21 parent-minting leftovers.

## Risks
- **Child `AGENT_ID` format and retry semantics are coupled.** There is no existing `bootstrap_derive_agent_id` helper in the repo today, and current `assign --if-absent` replay is keyed by `assigned_agent_id`. RESOLVED: `context.md` decisions 5 and 6 lock both the child task-id format (`<parent_task>/<child_agent>/<run_id>-<child_index>`) and the child `AGENT_ID` format (`agent:pi:subagent:<run_id>:<child_index>`) as deterministic functions of the same inputs. A respawn of the same logical child reuses both, so `assign --if-absent` is idempotent and `verify` does not surface `AGENT_MISMATCH` on retry. Task 3 implements the helper; Task 10 scenario 6 asserts the contract.
- **`assign --orchestrator` looks compatible, but the meaning must be audited end to end.** The command already stores separate `orchestrator_id` and `assigned_agent_id`, which suggests the child bootstrap can reuse it. The risk is hidden downstream assumptions that the calling process and orchestrator are the same actor. Mitigation: Task 6 audits command semantics before bootstrap wiring is finalized.
- **`AUTO_ASSIGNED_TASK` policy for subagent children is a product decision.** Suppressing the warning for orchestrator-initiated child rows is the cleanest fit, but it changes current verify behavior. Mitigation: record the rule in Task 1, implement it in Task 7, and keep branch and auto warnings covered so real orchestrator-forgot sessions stay visible.
- **Eager child bootstrap may expose a pi lifecycle assumption that current lazy bootstrap hides.** The plan intentionally picks eager bootstrap so zero-tool children get assignment rows and `task.assigned` precedes `intent.opened`, but extension-load timing still needs validation. Mitigation: implement eager child bootstrap in Task 5 and cover both audit ordering and no-tool children in Task 10.
- **Missing parent task in child mode must fail closed.** If a parent session never bootstrapped, a child cannot derive the deterministic child task id safely. That will block some currently sloppy flows. Mitigation: Task 2 adds a specific diagnostic instead of falling back to branch or auto, and Task 9 tests that failure path directly.
- **Cross-repo child ledgers only have informational parent linkage.** A child running in a different repo can resolve a different ledger, so `metadata.parent_task` cannot be treated as a relational foreign key. Mitigation: document the rule in Task 1 and prove it in Task 10 scenario 8.
- **The design depends on upstream pi-subagents env names and spawn behavior.** `PI_SUBAGENT_CHILD`, `PI_SUBAGENT_RUN_ID`, `PI_SUBAGENT_CHILD_AGENT`, and `PI_SUBAGENT_CHILD_INDEX` come from pi-subagents 0.24.0 internals, and the child inherits them through the spawner's env merge. Mitigation: cite the upstream files in docs during Task 1 and add hard-missing-env tests in Task 9.
- **The bootstrap source table currently lives in `docs/adapters.md`, not `adapters/pi/README.md`.** The task request calls for README updates, but the canonical per-source explanation is elsewhere today. Mitigation: update both files together so the README stays accurate and the canonical source table gains `subagent`.
