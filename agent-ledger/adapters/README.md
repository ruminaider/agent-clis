# Agent Ledger adapters

Phase 2 deliverables that turn `agent-ledger` from a discipline an
agent must remember into a workflow-wrapped enforcement layer the
agent cannot bypass. Each adapter intercepts its harness's tool-call
lifecycle and calls `agent-ledger claim`, `record`, and `verify`
deterministically.

| Adapter | Status | Path |
| ------- | ------ | ---- |
| pi extension | **stable** (v0.2.0) | `pi/agent-ledger.ts` |
| babysitter wrapper | experimental, opt-in | `babysitter/define-ledger-task.js` |
| Claude Code hooks | not shipped | (planned for v0.3) |
| generic shell | not shipped | (planned post-MVP) |

The pi extension is the v0.2.0 supported surface and has been
continuously dogfooded against shima-enaga's real ledger. The
babysitter wrapper ships in the repo for users who want to opt in
but is not part of the v0.2.x contract: its CLI surface, env-var
convention, and chain-of-tasks shape may evolve in v0.3+ once it
has comparable production exposure.

The shared `shared/session-bootstrap.sh` helper implements the
idempotent identify + ensure-assignment dance every adapter runs
once per session.

## Cross-harness contract

Every adapter implements the same env var contract documented in
`agent-ledger/docs/adapters.md`. Briefly:

- `AGENT_ID`: identity, set by the harness or auto-derived.
- `AGENT_LEDGER_TASK_ID`: orchestrator-set; auto-derived if missing
  (with a leading `[auto-assigned ...]` reason marker for v0.1 audit).
  If explicit but unassigned, bootstrap fails unless emergency repair
  env vars are set.
- `AGENT_LEDGER_PARENT_TASK_ID`: chains a child task to its parent.
- `AGENT_LEDGER_DIR`: optional ledger directory override.
- `AGENT_LEDGER_REQUIRE_TASK=1`: opt into fail-closed enforcement
  when no task id is supplied.

## Auto-assignment with audit trail

The "orchestrator forgot to assign" failure mode is handled by
auto-derivation, not fail-closed-by-default. When a pi session has no
higher-priority task source, the adapter first derives deterministic
`auto/pi-session/<sha256-prefix>` from the pi session id. It remains
auto-assigned and uses the existing `[auto-assigned by <source>
auto-derived ...]` marker, so verify emits `AUTO_ASSIGNED_TASK` without
showing a routine toast. If no pi session id is available, the adapter
uses the legacy `auto/<agent-slug>/<utc-timestamp>` fallback. The worker
proceeds with full attribution;
reviewers find every auto-assigned task by filtering assignment
reasons that start with `[auto-assigned`, and `verify` emits an
`AUTO_ASSIGNED_TASK` warning that surfaces in CI without blocking
merges.

Operators who want strict enforcement set
`AGENT_LEDGER_REQUIRE_TASK=1` and accept the disruption.

For long-lived workers, auto-derivation is not an orchestrator-ordering
substitute. The orchestrator must run `agent-ledger assign --if-absent`
and confirm an active assignment before it sends a worker a task brief
or claim command for that task id. See `agent-ledger/docs/adapters.md`
for the complete design.

## Stability

The adapters are scaffolds, not v1 production code. They make
opinionated choices (auto-assignment defaults, bash post-scan,
subagent env injection) that may evolve. The env var contract is the
stable surface; pin against it.
