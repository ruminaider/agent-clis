# Agent Ledger adapters

Phase 2 deliverables that turn `agent-ledger` from a discipline an
agent must remember into a workflow-wrapped enforcement layer the
agent cannot bypass. Each adapter intercepts its harness's tool-call
lifecycle and calls `agent-ledger claim`, `record`, and `verify`
deterministically.

| Adapter | Status | Path |
| ------- | ------ | ---- |
| pi extension | scaffold | `pi/agent-ledger.ts` |
| babysitter wrapper | scaffold | `babysitter/define-ledger-task.js` |
| Claude Code hooks | not shipped | (planned for v0.3) |
| generic shell | not shipped | (planned post-MVP) |

The shared `shared/session-bootstrap.sh` helper implements the
idempotent identify + ensure-assignment dance every adapter runs
once per session.

## Cross-harness contract

Every adapter implements the same env var contract documented in
`agent-ledger/docs/adapters.md`. Briefly:

- `AGENT_ID`: identity, set by the harness or auto-derived.
- `AGENT_LEDGER_TASK_ID`: orchestrator-set; auto-derived if missing
  (with `metadata.auto_assigned = true` for audit).
- `AGENT_LEDGER_PARENT_TASK_ID`: chains a child task to its parent.
- `AGENT_LEDGER_DIR`: optional ledger directory override.
- `AGENT_LEDGER_REQUIRE_TASK=1`: opt into fail-closed enforcement
  when no task id is supplied.

## Auto-assignment with audit trail

The "orchestrator forgot to assign" failure mode is handled by
auto-derivation, not fail-closed-by-default. When a worker is
dispatched without `AGENT_LEDGER_TASK_ID`, the adapter generates
`auto/<agent-slug>/<utc-timestamp>` and writes an assignment marked
`metadata.auto_assigned = true`. The worker proceeds with full
attribution; reviewers find every auto-assigned task by filtering on
that metadata flag, and `verify` emits a `MISSING_ASSIGNMENT`
warning that surfaces in CI without blocking merges.

Operators who want strict enforcement set
`AGENT_LEDGER_REQUIRE_TASK=1` and accept the disruption.

See `agent-ledger/docs/adapters.md` for the complete design.

## Stability

The adapters are scaffolds, not v1 production code. They make
opinionated choices (auto-assignment defaults, bash post-scan,
subagent env injection) that may evolve. The env var contract is the
stable surface; pin against it.
