#!/usr/bin/env bash
# Shared auto-assignment marker helper.

agent_ledger_sanitize_marker_token() {
  printf '%s' "$1" | tr -c 'A-Za-z0-9._:@/-' '-'
}

agent_ledger_auto_assigned_marker() {
  local by=""
  local parent=""
  local task=""
  local agent=""
  local effect=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --by) by="$2"; shift 2 ;;
      --parent) parent="$2"; shift 2 ;;
      --task) task="$2"; shift 2 ;;
      --agent) agent="$2"; shift 2 ;;
      --effect) effect="$2"; shift 2 ;;
      *) echo "marker.sh: unknown flag $1" >&2; return 2 ;;
    esac
  done

  if [[ -z "$by" ]]; then
    echo "marker.sh: --by is required" >&2
    return 2
  fi

  local marker="[auto-assigned by $(agent_ledger_sanitize_marker_token "$by") auto-derived"
  if [[ -n "$parent" ]]; then
    marker="${marker} parent=$(agent_ledger_sanitize_marker_token "$parent")"
  fi
  if [[ -n "$task" ]]; then
    marker="${marker} task=$(agent_ledger_sanitize_marker_token "$task")"
  fi
  if [[ -n "$agent" ]]; then
    marker="${marker} agent=$(agent_ledger_sanitize_marker_token "$agent")"
  fi
  if [[ -n "$effect" ]]; then
    marker="${marker} effect=$(agent_ledger_sanitize_marker_token "$effect")"
  fi
  printf '%s]' "$marker"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  agent_ledger_auto_assigned_marker "$@"
fi
