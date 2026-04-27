#!/usr/bin/env bash
# session-bootstrap.sh
#
# Idempotent session bootstrap for any agent-ledger adapter. Resolves
# AGENT_ID and AGENT_LEDGER_TASK_ID, ensures an assignment exists for
# the task, and emits shell exports by default or a JSON payload when
# called with --json.
#
# Task id resolution chain (first match wins):
#
#   1. --task-id <id>          (orchestrator-supplied flag)
#   2. AGENT_LEDGER_TASK_ID    (orchestrator-supplied env var)
#   3. PR detection            (--detect-pr 1 or AGENT_LEDGER_DETECT_PR=1)
#                              => `pr-<number>` from the current branch
#   4. Git branch              => `<branch>` (any non-empty branch name)
#   5. Detached HEAD           => `detached/<short-sha>`
#   6. Auto fallback           => `auto/<agent>/<utc>` (last resort)
#
# Sources 1 and 2 are explicit; the bootstrap skips assigning since the
# orchestrator is expected to have already done so.
#
# Sources 3-5 are harness-derived; the bootstrap creates a fresh
# assignment on first encounter with a [harness-derived ...] marker so
# reviewers can audit how the task id was sourced.
#
# Source 6 is the only path that triggers the legacy
# [auto-assigned ...] marker. The pi extension reads AGENT_LEDGER_TASK_SOURCE
# and only surfaces a warning toast for source=auto.

set -euo pipefail

HARNESS="unknown"
AGENT_KIND="worker"
TASK_ID_FLAG=""
PARENT_TASK_FLAG=""
ORCHESTRATOR_LABEL=""
JSON_OUTPUT=0
CWD_FLAG=""
DETECT_PR_FLAG=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --harness)         HARNESS="$2"; shift 2 ;;
    --agent-kind)      AGENT_KIND="$2"; shift 2 ;;
    --task-id)         TASK_ID_FLAG="$2"; shift 2 ;;
    --parent-task)     PARENT_TASK_FLAG="$2"; shift 2 ;;
    --orchestrator)    ORCHESTRATOR_LABEL="$2"; shift 2 ;;
    --cwd)             CWD_FLAG="$2"; shift 2 ;;
    --detect-pr)       DETECT_PR_FLAG="$2"; shift 2 ;;
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

DETECT_PR="${DETECT_PR_FLAG:-${AGENT_LEDGER_DETECT_PR:-0}}"
DETECT_CWD="${CWD_FLAG:-$PWD}"

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

sanitize_task_token() {
  printf '%s' "$1" | tr -c 'A-Za-z0-9._:@/-' '-'
}

git_in() {
  command git -C "$DETECT_CWD" "$@" 2>/dev/null
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
TASK_ID=""
TASK_SOURCE=""
EXPLICIT=0

if [[ -n "$TASK_ID_FLAG" ]]; then
  TASK_ID="$TASK_ID_FLAG"
  TASK_SOURCE="flag"
  EXPLICIT=1
elif [[ -n "${AGENT_LEDGER_TASK_ID:-}" ]]; then
  TASK_ID="$AGENT_LEDGER_TASK_ID"
  TASK_SOURCE="env"
  EXPLICIT=1
fi

if [[ -z "$TASK_ID" ]]; then
  # PR detection (opt-in). Requires gh CLI and a git repo with a
  # branch that has an open PR. Errors and missing tools are silent
  # so the chain falls through to branch detection.
  if [[ "$DETECT_PR" == "1" ]] && command -v gh >/dev/null 2>&1; then
    pr_number="$(gh -R "$DETECT_CWD" pr view --json number --jq '.number' 2>/dev/null || true)"
    if [[ -n "$pr_number" ]] && [[ "$pr_number" =~ ^[0-9]+$ ]]; then
      TASK_ID="pr-${pr_number}"
      TASK_SOURCE="pr"
    fi
  fi
fi

if [[ -z "$TASK_ID" ]]; then
  branch="$(git_in rev-parse --abbrev-ref HEAD || true)"
  if [[ -n "$branch" ]] && [[ "$branch" != "HEAD" ]]; then
    TASK_ID="$(sanitize_task_token "$branch")"
    TASK_SOURCE="branch"
  fi
fi

if [[ -z "$TASK_ID" ]]; then
  short_sha="$(git_in rev-parse --short HEAD || true)"
  if [[ -n "$short_sha" ]]; then
    TASK_ID="detached/$(sanitize_task_token "$short_sha")"
    TASK_SOURCE="detached"
  fi
fi

if [[ -z "$TASK_ID" ]]; then
  if [[ "${AGENT_LEDGER_REQUIRE_TASK:-0}" == "1" ]]; then
    echo "session-bootstrap: no task id resolvable and AGENT_LEDGER_REQUIRE_TASK=1; refusing to fall back" >&2
    exit 2
  fi
  agent_slug="$(printf '%s' "$AGENT_ID" | tr -c 'A-Za-z0-9._-' '-')"
  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  TASK_ID="auto/${agent_slug}/${ts}"
  TASK_SOURCE="auto"
fi

case "$TASK_SOURCE" in
  flag|env)
    : "session-bootstrap: task id explicitly supplied via $TASK_SOURCE: $TASK_ID"
    echo "session-bootstrap: task id from $TASK_SOURCE: $TASK_ID" >&2
    ;;
  pr|branch|detached)
    echo "session-bootstrap: harness-derived task id from $TASK_SOURCE: $TASK_ID" >&2
    ;;
  auto)
    echo "session-bootstrap: auto-fallback task id (no harness context): $TASK_ID" >&2
    ;;
esac

# 3. Ensure assignment exists for non-explicit sources. Explicit sources
#    are the orchestrator's responsibility and the bootstrap trusts that
#    an assignment already exists.
ledger_args=()
if [[ -n "${AGENT_LEDGER_DIR:-}" ]]; then
  ledger_args+=( --ledger-dir "$AGENT_LEDGER_DIR" )
fi

if [[ "$EXPLICIT" == "0" ]]; then
  policy="${AGENT_LEDGER_AUTO_ASSIGN_POLICY:-warn}"
  allow="${AGENT_LEDGER_AUTO_ASSIGN_ALLOW:-**}"
  orch="${ORCHESTRATOR_LABEL:-${HARNESS}-adapter}"
  parent="${PARENT_TASK_FLAG:-${AGENT_LEDGER_PARENT_TASK_ID:-}}"
  marker_args=( --by "${HARNESS}-adapter" --source "$TASK_SOURCE" --task "$TASK_ID" --agent "$AGENT_ID" )
  if [[ -n "$parent" ]]; then
    marker_args+=( --parent "$parent" )
  fi
  marker="$(agent_ledger_auto_assigned_marker "${marker_args[@]}")"
  case "$TASK_SOURCE" in
    auto) reason="${marker} session bootstrap (no harness context found; see docs/adapters.md)" ;;
    pr) reason="${marker} session bootstrap (task id derived from current PR)" ;;
    branch) reason="${marker} session bootstrap (task id derived from current branch)" ;;
    detached) reason="${marker} session bootstrap (task id derived from detached HEAD short sha)" ;;
    *) reason="${marker} session bootstrap" ;;
  esac

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
  printf 'AGENT_LEDGER_BOOTSTRAP_JSON={"AGENT_ID":"%s","AGENT_LEDGER_TASK_ID":"%s","AGENT_LEDGER_TASK_SOURCE":"%s","AGENT_LEDGER_AUTO_ASSIGNED":"%s"' \
    "$(json_escape "$AGENT_ID")" \
    "$(json_escape "$TASK_ID")" \
    "$(json_escape "$TASK_SOURCE")" \
    "$([[ "$TASK_SOURCE" == "auto" ]] && printf '1' || printf '0')"
  parent_export="${PARENT_TASK_FLAG:-${AGENT_LEDGER_PARENT_TASK_ID:-}}"
  if [[ -n "$parent_export" ]]; then
    printf ',"AGENT_LEDGER_PARENT_TASK_ID":"%s"' "$(json_escape "$parent_export")"
  fi
  printf '}\n'
else
  printf 'export AGENT_ID=%q\n' "$AGENT_ID"
  printf 'export AGENT_LEDGER_TASK_ID=%q\n' "$TASK_ID"
  printf 'export AGENT_LEDGER_TASK_SOURCE=%q\n' "$TASK_SOURCE"
  if [[ "$TASK_SOURCE" == "auto" ]]; then
    printf 'export AGENT_LEDGER_AUTO_ASSIGNED=1\n'
  fi
  parent_export="${PARENT_TASK_FLAG:-${AGENT_LEDGER_PARENT_TASK_ID:-}}"
  if [[ -n "$parent_export" ]]; then
    printf 'export AGENT_LEDGER_PARENT_TASK_ID=%q\n' "$parent_export"
  fi
fi
