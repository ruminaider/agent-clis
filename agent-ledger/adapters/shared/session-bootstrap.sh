#!/usr/bin/env bash
# session-bootstrap.sh
#
# Idempotent session bootstrap for any agent-ledger adapter. Resolves
# AGENT_ID and AGENT_LEDGER_TASK_ID, ensures an assignment exists for
# the task, and emits shell exports by default or a JSON payload when
# called with --json.

set -euo pipefail

HARNESS="unknown"
AGENT_KIND="worker"
TASK_ID_FLAG=""
PARENT_TASK_FLAG=""
ORCHESTRATOR_LABEL=""
JSON_OUTPUT=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --harness)         HARNESS="$2"; shift 2 ;;
    --agent-kind)      AGENT_KIND="$2"; shift 2 ;;
    --task-id)         TASK_ID_FLAG="$2"; shift 2 ;;
    --parent-task)     PARENT_TASK_FLAG="$2"; shift 2 ;;
    --orchestrator)    ORCHESTRATOR_LABEL="$2"; shift 2 ;;
    --json)            JSON_OUTPUT=1; shift ;;
    *) echo "session-bootstrap: unknown flag $1" >&2; exit 2 ;;
  esac
done

if ! command -v agent-ledger >/dev/null 2>&1; then
  echo "session-bootstrap: agent-ledger CLI not on PATH" >&2
  exit 3
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./marker.sh
source "$SCRIPT_DIR/marker.sh"

json_escape() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  s="${s//$'\r'/\\r}"
  s="${s//$'\t'/\\t}"
  printf '%s' "$s"
}

split_allow_args() {
  local value="$1"
  local -a globs=()
  local old_ifs="$IFS"
  IFS=':' read -r -a globs <<< "$value"
  IFS="$old_ifs"
  local g
  for g in "${globs[@]+"${globs[@]}"}"; do
    [[ -n "$g" ]] && printf '%s\0' "--allow" "$g"
  done
}

# 1. Resolve AGENT_ID.
if [[ -z "${AGENT_ID:-}" ]]; then
  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  if [[ "${AGENT_LEDGER_HUMAN_READABLE_AGENT_ID:-0}" == "1" ]]; then
    user="${USER:-${USERNAME:-anon}}"
    host="$(hostname -s 2>/dev/null || echo localhost)"
    AGENT_ID="${user}@${host}:${HARNESS}:${ts}"
  else
    nonce="${RANDOM}${RANDOM}$$"
    AGENT_ID="agent:${HARNESS}:${ts}:${nonce}"
  fi
  AGENT_ID="$(printf '%s' "$AGENT_ID" | tr -c 'A-Za-z0-9.:@_-' '-')"
  echo "session-bootstrap: derived AGENT_ID=$AGENT_ID" >&2
fi
export AGENT_ID

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
ledger_args=()
if [[ -n "${AGENT_LEDGER_DIR:-}" ]]; then
  ledger_args+=( --ledger-dir "$AGENT_LEDGER_DIR" )
fi

if [[ "$AUTO_DERIVED" == "1" ]]; then
  policy="${AGENT_LEDGER_AUTO_ASSIGN_POLICY:-warn}"
  allow="${AGENT_LEDGER_AUTO_ASSIGN_ALLOW:-**}"
  orch="${ORCHESTRATOR_LABEL:-${HARNESS}-adapter}"
  parent="${PARENT_TASK_FLAG:-${AGENT_LEDGER_PARENT_TASK_ID:-}}"
  marker_args=( --by "${HARNESS}-adapter" --task "$TASK_ID" --agent "$AGENT_ID" )
  if [[ -n "$parent" ]]; then
    marker_args+=( --parent "$parent" )
  fi
  marker="$(agent_ledger_auto_assigned_marker "${marker_args[@]}")"
  reason="${marker} session bootstrap (orchestrator did not pre-assign; see docs/adapters.md)"

  allow_args=()
  while IFS= read -r -d '' arg; do
    allow_args+=( "$arg" )
  done < <(split_allow_args "$allow")

  if ! agent-ledger assign \
      --task "$TASK_ID" \
      --orchestrator "$orch" \
      --agent "$AGENT_ID" \
      --policy "$policy" \
      "${allow_args[@]+"${allow_args[@]}"}" \
      --reason "$reason" \
      "${ledger_args[@]+"${ledger_args[@]}"}" >&2
  then
    echo "session-bootstrap: agent-ledger assign failed (task=$TASK_ID)" >&2
    exit 5
  fi
fi

# 4. Emit env data for the caller.
if [[ "$JSON_OUTPUT" == "1" ]]; then
  printf 'AGENT_LEDGER_BOOTSTRAP_JSON={"AGENT_ID":"%s","AGENT_LEDGER_TASK_ID":"%s","AGENT_LEDGER_AUTO_ASSIGNED":"%s"' \
    "$(json_escape "$AGENT_ID")" \
    "$(json_escape "$TASK_ID")" \
    "$AUTO_DERIVED"
  parent_export="${PARENT_TASK_FLAG:-${AGENT_LEDGER_PARENT_TASK_ID:-}}"
  if [[ -n "$parent_export" ]]; then
    printf ',"AGENT_LEDGER_PARENT_TASK_ID":"%s"' "$(json_escape "$parent_export")"
  fi
  printf '}\n'
else
  printf 'export AGENT_ID=%q\n' "$AGENT_ID"
  printf 'export AGENT_LEDGER_TASK_ID=%q\n' "$TASK_ID"
  if [[ "$AUTO_DERIVED" == "1" ]]; then
    printf 'export AGENT_LEDGER_AUTO_ASSIGNED=1\n'
  fi
  parent_export="${PARENT_TASK_FLAG:-${AGENT_LEDGER_PARENT_TASK_ID:-}}"
  if [[ -n "$parent_export" ]]; then
    printf 'export AGENT_LEDGER_PARENT_TASK_ID=%q\n' "$parent_export"
  fi
fi
