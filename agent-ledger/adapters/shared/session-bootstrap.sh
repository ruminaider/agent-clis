#!/usr/bin/env bash
# session-bootstrap.sh
#
# Idempotent session bootstrap for any agent-ledger adapter. Resolves
# AGENT_ID and AGENT_LEDGER_TASK_ID, ensures an assignment exists for
# the task, and emits export lines on stdout for the caller to eval.
#
# Usage:
#   eval "$(bash session-bootstrap.sh \
#     --harness pi \
#     --agent-kind worker \
#     [--task-id <id>] \
#     [--parent-task <id>])"
#
# Reads:
#   AGENT_ID, AGENT_LEDGER_TASK_ID, AGENT_LEDGER_PARENT_TASK_ID,
#   AGENT_LEDGER_DIR, AGENT_LEDGER_REQUIRE_TASK,
#   AGENT_LEDGER_AUTO_ASSIGN_POLICY, AGENT_LEDGER_AUTO_ASSIGN_ALLOW
#
# Writes (to stdout, intended for `eval`):
#   export AGENT_ID=...
#   export AGENT_LEDGER_TASK_ID=...
#   export AGENT_LEDGER_AUTO_ASSIGNED=1   (when auto-derived)
#
# Diagnostic output goes to stderr.
#
# Exit codes:
#   0   bootstrap succeeded (assignment exists or was created)
#   2   AGENT_LEDGER_REQUIRE_TASK=1 and no task id resolvable
#   3   agent-ledger CLI not on PATH
#   5   ledger I/O error during status/assign

set -euo pipefail

HARNESS="unknown"
AGENT_KIND="worker"
TASK_ID_FLAG=""
PARENT_TASK_FLAG=""
ORCHESTRATOR_LABEL=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --harness)         HARNESS="$2"; shift 2 ;;
    --agent-kind)      AGENT_KIND="$2"; shift 2 ;;
    --task-id)         TASK_ID_FLAG="$2"; shift 2 ;;
    --parent-task)     PARENT_TASK_FLAG="$2"; shift 2 ;;
    --orchestrator)    ORCHESTRATOR_LABEL="$2"; shift 2 ;;
    *) echo "session-bootstrap: unknown flag $1" >&2; exit 2 ;;
  esac
done

if ! command -v agent-ledger >/dev/null 2>&1; then
  echo "session-bootstrap: agent-ledger CLI not on PATH" >&2
  exit 3
fi

# 1. Resolve AGENT_ID.
if [[ -z "${AGENT_ID:-}" ]]; then
  user="${USER:-${USERNAME:-anon}}"
  host="$(hostname -s 2>/dev/null || echo localhost)"
  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  AGENT_ID="${user}@${host}:${HARNESS}:${ts}"
  # Sanitize: keep alnum, dot, underscore, dash, colon, at-sign.
  AGENT_ID="$(printf '%s' "$AGENT_ID" | tr -c 'A-Za-z0-9.:@_-' '-')"
  echo "session-bootstrap: derived AGENT_ID=$AGENT_ID" >&2
fi

# Pre-register identity. identify is idempotent for an existing AGENT_ID.
agent-ledger identify --agent-kind "$AGENT_KIND" --harness "$HARNESS" >/dev/null 2>&1 || true

# 2. Resolve task id.
TASK_ID="${TASK_ID_FLAG:-${AGENT_LEDGER_TASK_ID:-}}"
AUTO_DERIVED=0
if [[ -z "$TASK_ID" ]]; then
  if [[ "${AGENT_LEDGER_REQUIRE_TASK:-0}" == "1" ]]; then
    echo "session-bootstrap: AGENT_LEDGER_TASK_ID unset and AGENT_LEDGER_REQUIRE_TASK=1; refusing to auto-derive" >&2
    exit 2
  fi
  agent_slug="$(printf '%s' "$AGENT_ID" | tr -c 'A-Za-z0-9._-' '-')"
  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  TASK_ID="auto/${agent_slug}/${ts}"
  AUTO_DERIVED=1
  echo "session-bootstrap: auto-derived task id $TASK_ID" >&2
fi

# 3. Ensure assignment exists.
#
# The v0.1 kernel does not expose a query for "does this task have an
# assignment?". To stay correct without that query, we only create an
# assignment when this bootstrap call auto-derived the task id
# (AUTO_DERIVED=1). When the orchestrator supplied a task id via flag
# or env, we assume the orchestrator already wrote the assignment and
# we skip the assign step. v0.2 will gain a `status --assignments` (or
# similar) surface and this branch will become a true idempotent
# upsert.
ledger_args=()
if [[ -n "${AGENT_LEDGER_DIR:-}" ]]; then
  ledger_args+=( --ledger-dir "$AGENT_LEDGER_DIR" )
fi

if [[ "$AUTO_DERIVED" == "1" ]]; then
  policy="${AGENT_LEDGER_AUTO_ASSIGN_POLICY:-warn}"
  allow="${AGENT_LEDGER_AUTO_ASSIGN_ALLOW:-**}"
  orch="${ORCHESTRATOR_LABEL:-${HARNESS}-adapter}"
  parent="${PARENT_TASK_FLAG:-${AGENT_LEDGER_PARENT_TASK_ID:-}}"

  # Audit-trail signal goes in the reason text since the v0.1 kernel
  # does not have a --metadata flag on assign. Reviewers can
  # filter on the leading marker:
  #   agent-ledger status --json | jq '.assignments[] | select(.reason | startswith("[auto-assigned"))'
  # The kernel will gain a --metadata flag in v0.2 for structured
  # audit; the marker syntax stays compatible (kernel will surface
  # both reason text and metadata).
  marker="[auto-assigned by ${HARNESS}-adapter"
  if [[ "$AUTO_DERIVED" == "1" ]]; then
    marker="${marker} auto-derived"
  fi
  if [[ -n "$parent" ]]; then
    marker="${marker} parent=${parent}"
  fi
  marker="${marker}]"
  reason="${marker} session bootstrap (orchestrator did not pre-assign; see docs/adapters.md)"

  if ! agent-ledger assign \
      --task "$TASK_ID" \
      --orchestrator "$orch" \
      --agent "$AGENT_ID" \
      --policy "$policy" \
      --allow "$allow" \
      --reason "$reason" \
      "${ledger_args[@]}" >&2
  then
    echo "session-bootstrap: agent-ledger assign failed (task=$TASK_ID)" >&2
    exit 5
  fi
fi

# 4. Emit exports for the caller to eval.
printf 'export AGENT_ID=%q\n' "$AGENT_ID"
printf 'export AGENT_LEDGER_TASK_ID=%q\n' "$TASK_ID"
if [[ "$AUTO_DERIVED" == "1" ]]; then
  printf 'export AGENT_LEDGER_AUTO_ASSIGNED=1\n'
fi
if [[ -n "${AGENT_LEDGER_PARENT_TASK_ID:-}" ]]; then
  printf 'export AGENT_LEDGER_PARENT_TASK_ID=%q\n' "${AGENT_LEDGER_PARENT_TASK_ID}"
fi
