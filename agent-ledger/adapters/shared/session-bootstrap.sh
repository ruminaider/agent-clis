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
#   0. Pi subagent child       (PI_SUBAGENT_CHILD=1)
#                              => `<parent_task>/<child_agent>/<run_id>-<child_index>`
#                              and a deterministic child AGENT_ID of
#                              `agent:pi:subagent:<run_id>:<child_index>`.
#                              Runs before every other source. Hard-fails
#                              when any required pi-subagents env var is
#                              missing instead of falling back.
#   1. --task-id <id>          (orchestrator-supplied flag)
#   2. AGENT_LEDGER_TASK_ID    (orchestrator-supplied env var)
#   3. PR detection            (--detect-pr 1 or AGENT_LEDGER_DETECT_PR=1)
#                              => `pr-<number>` from the current branch
#   4. Git branch              => `<branch>` (any non-empty branch name)
#   5. Detached HEAD           => `detached/<short-sha>`
#   6. Pointer file default    => `<default_task_id>` from the local
#                                 .agent-ledger.toml when set
#   7. Auto fallback           => `auto/<agent>/<utc>` (last resort)
#
# Sources 1 and 2 are explicit; the bootstrap skips assigning when an
# active assignment already exists. If none exists, it fails early by
# default so orchestrators fix ordering before workers claim. Operators
# can opt into audited repair with AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT=1
# plus AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW.
#
# Sources 3-5 are harness-derived; the bootstrap creates a fresh
# assignment on first encounter with a [harness-derived ...] marker so
# reviewers can audit how the task id was sourced.
#
# Source 6 is operator-declared via the local pointer. It is the right
# answer for non-git, ambient multi-agent projects where the harness has
# no natural task signal. The bootstrap creates the same kind of
# harness-derived assignment as sources 3-5.
#
# Source 7 is the normal path for the legacy [auto-assigned ...]
# marker. Opt-in explicit repair assignments also use that marker, with
# structured metadata distinguishing the repair case. The pi extension
# reads AGENT_LEDGER_TASK_SOURCE and only surfaces a warning toast for
# source=auto. When source=auto, the bootstrap also exports
# AGENT_LEDGER_TASK_AUTO_REASON so the toast can name the cheapest fix
# (one of: not_in_git_repo, git_no_head, pointer_lacks_default).

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

# agent_ledger_derive_agent_id
#
# Single source of truth for AGENT_ID derivation. Branches on
# PI_SUBAGENT_CHILD=1 to produce one of two byte-stable shapes:
#
#   subagent mode (PI_SUBAGENT_CHILD=1):
#     agent:pi:subagent:<PI_SUBAGENT_RUN_ID>:<child_index>
#     where <child_index> is PI_SUBAGENT_CHILD_INDEX normalized as a
#     base-10 integer. Deterministic. No randomness, no sanitization.
#
#   legacy mode (otherwise):
#     agent:<harness>:<utc>:<nonce>             (default)
#     <user>@<host>:<harness>:<utc>             (when
#                                                AGENT_LEDGER_HUMAN_READABLE_AGENT_ID=1)
#     The result is sanitized through tr -c 'A-Za-z0-9.:@_-' '-'.
#
# The caller decides when to invoke this. The legacy branch only
# derives when the inherited AGENT_ID is empty. The subagent branch
# always derives (after preserving the inherited parent AGENT_ID in a
# separate variable for use as --orchestrator).
#
# Args:
#   $1  harness name (used by the legacy path; defaults to "unknown").
#
# Required env in subagent mode:
#   PI_SUBAGENT_RUN_ID, PI_SUBAGENT_CHILD_INDEX. The caller must have
#   already validated these before invoking the helper. The helper does
#   not enforce presence so the surrounding error messages stay where
#   they are.
#
# Prints the derived AGENT_ID to stdout. No side effects on caller env.
agent_ledger_derive_agent_id() {
  local harness="${1:-unknown}"
  local agent_id=""
  if [[ "${PI_SUBAGENT_CHILD:-}" == "1" ]]; then
    local child_index
    child_index="$((10#${PI_SUBAGENT_CHILD_INDEX}))"
    agent_id="agent:pi:subagent:${PI_SUBAGENT_RUN_ID}:${child_index}"
  else
    local ts nonce user host
    ts="$(date -u +%Y%m%dT%H%M%SZ)"
    if [[ "${AGENT_LEDGER_HUMAN_READABLE_AGENT_ID:-0}" == "1" ]]; then
      user="${USER:-${USERNAME:-anon}}"
      host="$(hostname -s 2>/dev/null || echo localhost)"
      agent_id="${user}@${host}:${harness}:${ts}"
    else
      nonce="${RANDOM}${RANDOM}$$"
      agent_id="agent:${harness}:${ts}:${nonce}"
    fi
    agent_id="$(printf '%s' "$agent_id" | tr -c 'A-Za-z0-9.:@_-' '-')"
  fi
  printf '%s' "$agent_id"
}

# 0. Pi subagent child source. Runs before the legacy task-source
# chain. When pi-subagents spawns this process with PI_SUBAGENT_CHILD=1,
# derive a deterministic child task id and child AGENT_ID from the
# pi-subagents run identifiers, preserve the inherited parent AGENT_ID
# for use as the assignment's orchestrator, write the assignment row
# with structured metadata, emit the env, and exit. The branch never
# falls back to flag, env, pr, branch, detached, or auto sources.
if [[ "${PI_SUBAGENT_CHILD:-}" == "1" ]]; then
  subagent_missing=()
  [[ -z "${PI_SUBAGENT_RUN_ID:-}" ]]      && subagent_missing+=( "PI_SUBAGENT_RUN_ID" )
  [[ -z "${PI_SUBAGENT_CHILD_INDEX:-}" ]] && subagent_missing+=( "PI_SUBAGENT_CHILD_INDEX" )
  [[ -z "${PI_SUBAGENT_CHILD_AGENT:-}" ]] && subagent_missing+=( "PI_SUBAGENT_CHILD_AGENT" )
  [[ -z "${AGENT_LEDGER_TASK_ID:-}" ]]    && subagent_missing+=( "AGENT_LEDGER_TASK_ID" )
  [[ -z "${AGENT_ID:-}" ]]                && subagent_missing+=( "AGENT_ID" )
  if [[ ${#subagent_missing[@]} -gt 0 ]]; then
    echo "session-bootstrap: PI_SUBAGENT_CHILD=1 but required env vars are unset or empty: ${subagent_missing[*]}" >&2
    echo "session-bootstrap: a pi subagent child cannot self-assign without an inherited parent task and run identifiers; refusing to fall back." >&2
    exit 4
  fi

  if [[ ! "${PI_SUBAGENT_CHILD_INDEX}" =~ ^[0-9]+$ ]]; then
    echo "session-bootstrap: PI_SUBAGENT_CHILD_INDEX must be a non-negative decimal integer (got '${PI_SUBAGENT_CHILD_INDEX}')" >&2
    exit 4
  fi

  subagent_parent_agent_id="$AGENT_ID"
  subagent_parent_task_id="$AGENT_LEDGER_TASK_ID"
  subagent_child_index="$((10#${PI_SUBAGENT_CHILD_INDEX}))"

  AGENT_ID="$(agent_ledger_derive_agent_id "$HARNESS")"
  export AGENT_ID

  TASK_ID="${subagent_parent_task_id}/${PI_SUBAGENT_CHILD_AGENT}/${PI_SUBAGENT_RUN_ID}-${subagent_child_index}"
  TASK_SOURCE="subagent"

  echo "session-bootstrap: harness-derived task id from subagent: $TASK_ID (parent_task=$subagent_parent_task_id parent_agent=$subagent_parent_agent_id child_agent=$AGENT_ID)" >&2

  agent-ledger identify --agent-kind "$AGENT_KIND" --harness "$HARNESS" >/dev/null 2>&1 || true

  ledger_args=()
  if [[ -n "${AGENT_LEDGER_DIR:-}" ]]; then
    ledger_args+=( --ledger-dir "$AGENT_LEDGER_DIR" )
  fi

  subagent_policy="${AGENT_LEDGER_AUTO_ASSIGN_POLICY:-warn}"
  subagent_allow="${AGENT_LEDGER_AUTO_ASSIGN_ALLOW:-**}"
  subagent_allow_args=()
  while IFS= read -r -d '' arg; do
    subagent_allow_args+=( "$arg" )
  done < <(split_allow_args "$subagent_allow")

  subagent_marker="$(agent_ledger_auto_assigned_marker \
    --by "${HARNESS}-adapter" \
    --source subagent \
    --parent "$subagent_parent_task_id" \
    --task "$TASK_ID" \
    --agent "$AGENT_ID")"
  subagent_reason="${subagent_marker} session bootstrap (pi subagent child self-assignment)"

  # Decision 7 metadata schema. subagent_child_index is a JSON number,
  # not a quoted string. All string fields go through json_escape so a
  # value containing a quote, backslash, or newline cannot break the
  # payload.
  subagent_metadata_json="{\"parent_task\":\"$(json_escape "$subagent_parent_task_id")\""
  subagent_metadata_json="${subagent_metadata_json},\"parent_agent_id\":\"$(json_escape "$subagent_parent_agent_id")\""
  subagent_metadata_json="${subagent_metadata_json},\"subagent_run_id\":\"$(json_escape "$PI_SUBAGENT_RUN_ID")\""
  subagent_metadata_json="${subagent_metadata_json},\"subagent_child_index\":${subagent_child_index}"
  subagent_metadata_json="${subagent_metadata_json},\"subagent_child_agent\":\"$(json_escape "$PI_SUBAGENT_CHILD_AGENT")\""
  subagent_metadata_json="${subagent_metadata_json},\"dispatch_origin\":\"pi-subagent-bootstrap\""
  subagent_metadata_json="${subagent_metadata_json}}"

  if subagent_assign_help="$(agent-ledger assign --help 2>&1)"; then
    if ! grep -q -- "--metadata" <<<"$subagent_assign_help"; then
      echo "session-bootstrap: agent-ledger assign --help does not advertise required --metadata capability (kernel v0.1.1+ required)" >&2
      exit 5
    fi
  else
    echo "session-bootstrap: agent-ledger assign --help failed, cannot verify required --metadata capability (kernel v0.1.1+ required)" >&2
    printf '%s\n' "$subagent_assign_help" >&2
    exit 5
  fi

  if ! agent-ledger assign \
      --task "$TASK_ID" \
      --orchestrator "$subagent_parent_agent_id" \
      --agent "$AGENT_ID" \
      --policy "$subagent_policy" \
      "${subagent_allow_args[@]+"${subagent_allow_args[@]}"}" \
      --if-absent \
      --reason "$subagent_reason" \
      --metadata "$subagent_metadata_json" \
      "${ledger_args[@]+"${ledger_args[@]}"}" >&2
  then
    echo "session-bootstrap: agent-ledger assign failed (task=$TASK_ID source=subagent)" >&2
    exit 5
  fi

  if [[ "$JSON_OUTPUT" == "1" ]]; then
    printf 'AGENT_LEDGER_BOOTSTRAP_JSON={"AGENT_ID":"%s","AGENT_LEDGER_TASK_ID":"%s","AGENT_LEDGER_TASK_SOURCE":"%s","AGENT_LEDGER_AUTO_ASSIGNED":"0","AGENT_LEDGER_PARENT_TASK_ID":"%s"}\n' \
      "$(json_escape "$AGENT_ID")" \
      "$(json_escape "$TASK_ID")" \
      "$(json_escape "$TASK_SOURCE")" \
      "$(json_escape "$subagent_parent_task_id")"
  else
    printf 'export AGENT_ID=%q\n' "$AGENT_ID"
    printf 'export AGENT_LEDGER_TASK_ID=%q\n' "$TASK_ID"
    printf 'export AGENT_LEDGER_TASK_SOURCE=%q\n' "$TASK_SOURCE"
    printf 'export AGENT_LEDGER_AUTO_ASSIGNED=0\n'
    printf 'export AGENT_LEDGER_PARENT_TASK_ID=%q\n' "$subagent_parent_task_id"
  fi

  exit 0
fi

# 1. Resolve AGENT_ID.
if [[ -z "${AGENT_ID:-}" ]]; then
  AGENT_ID="$(agent_ledger_derive_agent_id "$HARNESS")"
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
    pr_number="$( (cd "$DETECT_CWD" 2>/dev/null && gh pr view --json number --jq '.number' 2>/dev/null) || true )"
    if [[ -n "$pr_number" ]] && [[ "$pr_number" =~ ^[0-9]+$ ]]; then
      TASK_ID="pr-${pr_number}"
      TASK_SOURCE="pr"
    fi
  fi
fi

if [[ -z "$TASK_ID" ]]; then
  branch="$(git_in symbolic-ref --quiet --short HEAD || true)"
  if [[ -z "$branch" ]]; then
    branch="$(git_in branch --show-current || true)"
  fi
  if [[ -z "$branch" ]]; then
    branch="$(git_in rev-parse --abbrev-ref HEAD || true)"
  fi
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

# Pointer-file default. The local .agent-ledger.toml may declare a
# default_task_id for ambient multi-agent projects that have no natural
# harness signal (e.g. non-git scratch directories where two pi sessions
# need to share one task id). Adapter-level only: the kernel is the
# authoritative parser and is queried via `agent-ledger pointer show`.
POINTER_PRESENT=0
POINTER_HAD_DEFAULT=0
POINTER_UNREADABLE=0
POINTER_PARSER_UNAVAILABLE=0
if [[ -z "$TASK_ID" ]]; then
  # Capture stdout, stderr, and exit code separately. The kernel's
  # `pointer show --json` exits 0 when the pointer file is absent
  # (printing present=false) and non-zero only when the file exists
  # but cannot be parsed. Hiding that exit code with `|| true` would
  # silently demote a malformed pointer to a misleading
  # `not_in_git_repo` / `git_no_head` auto-fallback hint.
  pointer_stderr_file="$(mktemp "${TMPDIR:-/tmp}/agent-ledger-pointer.XXXXXX")"
  pointer_json=""
  pointer_exit=0
  if pointer_json="$( (cd "$DETECT_CWD" 2>/dev/null && agent-ledger pointer show --json) 2>"$pointer_stderr_file" )"; then
    pointer_exit=0
  else
    pointer_exit=$?
  fi
  if (( pointer_exit != 0 )); then
    POINTER_UNREADABLE=1
    pointer_err_msg="$(tr -d '\r' < "$pointer_stderr_file" | tr '\n' ' ' | sed 's/  */ /g; s/^ //; s/ $//')"
    if [[ -n "$pointer_err_msg" ]]; then
      echo "session-bootstrap: agent-ledger pointer show failed (exit $pointer_exit): $pointer_err_msg" >&2
    else
      echo "session-bootstrap: agent-ledger pointer show failed (exit $pointer_exit) with no stderr; pointer state unknown" >&2
    fi
    pointer_json=""
  fi
  rm -f "$pointer_stderr_file"
  if [[ -n "$pointer_json" ]]; then
    pointer_present_value=""
    pointer_default_value=""
    if command -v python3 >/dev/null 2>&1; then
      pointer_present_value="$(printf '%s\n' "$pointer_json" | python3 -c 'import json, sys; d=json.load(sys.stdin); print("1" if d.get("present") else "0")' 2>/dev/null)" || pointer_present_value=""
      pointer_default_value="$(printf '%s\n' "$pointer_json" | python3 -c 'import json, sys; d=json.load(sys.stdin); print(d.get("default_task_id",""))' 2>/dev/null)" || pointer_default_value=""
    elif command -v node >/dev/null 2>&1; then
      pointer_present_value="$(printf '%s\n' "$pointer_json" | node -e 'let i=""; process.stdin.setEncoding("utf8"); process.stdin.on("data",c=>i+=c); process.stdin.on("end",()=>{const d=JSON.parse(i); console.log(d.present?"1":"0");});' 2>/dev/null)" || pointer_present_value=""
      pointer_default_value="$(printf '%s\n' "$pointer_json" | node -e 'let i=""; process.stdin.setEncoding("utf8"); process.stdin.on("data",c=>i+=c); process.stdin.on("end",()=>{const d=JSON.parse(i); console.log(d.default_task_id||"");});' 2>/dev/null)" || pointer_default_value=""
    else
      # Neither python3 nor node is available. We have a non-empty
      # pointer_json (so a pointer file was reachable) but no way to
      # project its `default_task_id`. Without this branch, an operator
      # who declared `default_task_id` would silently land in the
      # auto-fallback path with a hint that blames git rather than the
      # missing parser dependency.
      POINTER_PARSER_UNAVAILABLE=1
      echo "session-bootstrap: cannot parse pointer JSON; install python3 or node to honor default_task_id" >&2
    fi
    if [[ "$pointer_present_value" == "1" ]]; then
      POINTER_PRESENT=1
      if [[ -n "$pointer_default_value" ]]; then
        POINTER_HAD_DEFAULT=1
        TASK_ID="$(sanitize_task_token "$pointer_default_value")"
        TASK_SOURCE="pointer"
      fi
    fi
  fi
fi

AUTO_REASON=""
if [[ -z "$TASK_ID" ]]; then
  if [[ "${AGENT_LEDGER_REQUIRE_TASK:-0}" == "1" ]]; then
    echo "session-bootstrap: no task id resolvable and AGENT_LEDGER_REQUIRE_TASK=1; refusing to fall back" >&2
    exit 2
  fi
  # Compute an actionable reason for the auto fallback. Priority,
  # most specific to least specific:
  #   1. pointer_unreadable: a pointer file exists but the kernel
  #      could not parse it; the operator should fix the file.
  #   2. pointer_parser_unavailable: a pointer file is reachable but
  #      the bootstrap has neither python3 nor node to project its
  #      default_task_id; the operator should install one.
  #   3. pointer_lacks_default: the pointer parsed but has no
  #      default_task_id field; the operator should add it.
  #   4. not_in_git_repo / git_no_head: no pointer signal at all,
  #      so surface the git state instead.
  # Keep this chain in sync with AUTO_REASON_HINTS in
  # adapters/shared/auto-fallback-toast.js.
  if [[ "$POINTER_UNREADABLE" == "1" ]]; then
    AUTO_REASON="pointer_unreadable"
  elif [[ "$POINTER_PARSER_UNAVAILABLE" == "1" ]]; then
    AUTO_REASON="pointer_parser_unavailable"
  elif [[ "$POINTER_PRESENT" == "1" ]]; then
    AUTO_REASON="pointer_lacks_default"
  elif ! git_in rev-parse --git-dir >/dev/null 2>&1; then
    AUTO_REASON="not_in_git_repo"
  else
    AUTO_REASON="git_no_head"
  fi
  agent_slug="$(printf '%s' "$AGENT_ID" | tr -c 'A-Za-z0-9._-' '-')"
  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  TASK_ID="auto/${agent_slug}/${ts}"
  TASK_SOURCE="auto"
fi

case "$TASK_SOURCE" in
  flag|env)
    echo "session-bootstrap: task id from $TASK_SOURCE: $TASK_ID" >&2
    ;;
  pr|branch|detached|pointer)
    echo "session-bootstrap: harness-derived task id from $TASK_SOURCE: $TASK_ID" >&2
    ;;
  auto)
    echo "session-bootstrap: auto-fallback task id (no harness context, reason=$AUTO_REASON): $TASK_ID" >&2
    ;;
esac

# 3. Ensure assignment exists. Derived sources always create an
#    assignment. Explicit sources are the orchestrator's responsibility:
#    if no active assignment exists, fail before the first claim unless
#    audited explicit repair is enabled.
ledger_args=()
if [[ -n "${AGENT_LEDGER_DIR:-}" ]]; then
  ledger_args+=( --ledger-dir "$AGENT_LEDGER_DIR" )
fi

assignment_count_for_task() {
  local out=""
  local count=""
  local err_file=""

  err_file="$(mktemp "${TMPDIR:-/tmp}/agent-ledger-assignments.XXXXXX")"
  if ! out="$(agent-ledger assignments --task "$TASK_ID" --status active --limit 1 --json "${ledger_args[@]+"${ledger_args[@]}"}" 2>"$err_file")"; then
    echo "session-bootstrap: agent-ledger assignments query failed (task=$TASK_ID)" >&2
    cat "$err_file" >&2
    rm -f "$err_file"
    return 2
  fi
  rm -f "$err_file"

  if command -v python3 >/dev/null 2>&1; then
    count="$(printf '%s\n' "$out" | python3 -c 'import json, sys; data = json.load(sys.stdin); print(int(data.get("count", len(data.get("assignments", [])))))' 2>/dev/null)" || count=""
  elif command -v node >/dev/null 2>&1; then
    count="$(printf '%s\n' "$out" | node -e 'let input=""; process.stdin.setEncoding("utf8"); process.stdin.on("data", chunk => input += chunk); process.stdin.on("end", () => { const data = JSON.parse(input); const fallback = Array.isArray(data.assignments) ? data.assignments.length : 0; const count = data.count ?? fallback; if (!Number.isInteger(count)) process.exit(1); console.log(count); });' 2>/dev/null)" || count=""
  else
    echo "session-bootstrap: python3 or node is required to parse agent-ledger assignments --json" >&2
    return 2
  fi
  if [[ -z "$count" ]]; then
    echo "session-bootstrap: could not parse agent-ledger assignments --json count (task=$TASK_ID)" >&2
    printf '%s\n' "$out" >&2
    return 2
  fi
  printf '%s\n' "$count"
}

active_assignment_exists_for_task() {
  local count=""
  if count="$(assignment_count_for_task)"; then
    [[ "$count" -gt 0 ]]
    return
  fi
  return $?
}

write_bootstrap_assignment() {
  local assignment_source="$1"
  local marker_source="$2"
  local auto_assigned="$3"
  local reason_detail="$4"
  local explicit_repair="${5:-0}"
  local allow_override="${6:-}"
  local policy="${AGENT_LEDGER_AUTO_ASSIGN_POLICY:-warn}"
  local allow="${allow_override:-${AGENT_LEDGER_AUTO_ASSIGN_ALLOW:-**}}"
  local orch="${ORCHESTRATOR_LABEL:-${HARNESS}-adapter}"
  local parent="${PARENT_TASK_FLAG:-${AGENT_LEDGER_PARENT_TASK_ID:-}}"
  local marker=""
  local reason=""
  local metadata_json=""
  local assign_help=""
  local -a marker_args
  local -a allow_args
  local -a metadata_args

  marker_args=( --by "${HARNESS}-adapter" --source "$marker_source" --task "$TASK_ID" --agent "$AGENT_ID" )
  if [[ -n "$parent" ]]; then
    marker_args+=( --parent "$parent" )
  fi
  marker="$(agent_ledger_auto_assigned_marker "${marker_args[@]}")"
  reason="${marker} ${reason_detail}"

  allow_args=()
  while IFS= read -r -d '' arg; do
    allow_args+=( "$arg" )
  done < <(split_allow_args "$allow")

  # Build structured metadata for v0.1.1+ kernels. The reason marker
  # stays as a forward-compatible audit signal; the metadata JSON is
  # the canonical surface for programmatic queries via the
  # agent-ledger assignments command.
  metadata_json="{\"auto_assigned\":${auto_assigned}"
  metadata_json="${metadata_json},\"auto_assigned_by\":\"$(json_escape "${HARNESS}-adapter")\""
  metadata_json="${metadata_json},\"task_source\":\"$(json_escape "$assignment_source")\""
  if [[ "$explicit_repair" == "1" ]]; then
    metadata_json="${metadata_json},\"explicit_missing_assignment\":true"
  fi
  if [[ -n "$parent" ]]; then
    metadata_json="${metadata_json},\"parent_task\":\"$(json_escape "$parent")\""
  fi
  metadata_json="${metadata_json}}"

  # v0.1.1+ kernels require --metadata. Treat missing support as a hard
  # compatibility error rather than silently dropping structured data.
  metadata_args=( --metadata "$metadata_json" )
  if assign_help="$(agent-ledger assign --help 2>&1)"; then
    if ! grep -q -- "--metadata" <<<"$assign_help"; then
      echo "session-bootstrap: agent-ledger assign --help does not advertise required --metadata capability (kernel v0.1.1+ required)" >&2
      exit 5
    fi
  else
    echo "session-bootstrap: agent-ledger assign --help failed, cannot verify required --metadata capability (kernel v0.1.1+ required)" >&2
    printf '%s\n' "$assign_help" >&2
    exit 5
  fi

  if ! agent-ledger assign \
      --task "$TASK_ID" \
      --orchestrator "$orch" \
      --agent "$AGENT_ID" \
      --policy "$policy" \
      "${allow_args[@]+"${allow_args[@]}"}" \
      --if-absent \
      --reason "$reason" \
      "${metadata_args[@]+"${metadata_args[@]}"}" \
      "${ledger_args[@]+"${ledger_args[@]}"}" >&2
  then
    echo "session-bootstrap: agent-ledger assign failed (task=$TASK_ID)" >&2
    exit 5
  fi
}

if [[ "$EXPLICIT" == "0" ]]; then
  case "$TASK_SOURCE" in
    auto) write_bootstrap_assignment "$TASK_SOURCE" "$TASK_SOURCE" true "session bootstrap (no harness context found; see docs/adapters.md)" ;;
    pr) write_bootstrap_assignment "$TASK_SOURCE" "$TASK_SOURCE" false "session bootstrap (task id derived from current PR)" ;;
    branch) write_bootstrap_assignment "$TASK_SOURCE" "$TASK_SOURCE" false "session bootstrap (task id derived from current branch)" ;;
    detached) write_bootstrap_assignment "$TASK_SOURCE" "$TASK_SOURCE" false "session bootstrap (task id derived from detached HEAD short sha)" ;;
    pointer) write_bootstrap_assignment "$TASK_SOURCE" "$TASK_SOURCE" false "session bootstrap (task id from .agent-ledger.toml default_task_id)" ;;
    *) write_bootstrap_assignment "$TASK_SOURCE" auto true "session bootstrap" ;;
  esac
elif active_assignment_exists_for_task; then
  :
else
  assignment_rc=$?
  if [[ "$assignment_rc" == "1" ]]; then
    if [[ "${AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT:-0}" != "1" ]]; then
      echo "session-bootstrap: explicit task id has no active assignment (task=$TASK_ID source=$TASK_SOURCE)" >&2
      echo "session-bootstrap: run agent-ledger assign before launching or instructing this worker" >&2
      echo "session-bootstrap: optional emergency repair requires AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT=1 and AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW" >&2
      exit 5
    fi
    if [[ -z "${AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW:-}" ]]; then
      echo "session-bootstrap: AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW is required when AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT=1" >&2
      exit 5
    fi
    echo "session-bootstrap: WARNING: explicit task id has no active assignment; creating audited repair assignment (task=$TASK_ID source=$TASK_SOURCE allow=$AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW)" >&2
    write_bootstrap_assignment "$TASK_SOURCE" auto true "session bootstrap (explicit task id from $TASK_SOURCE lacked an active assignment; adapter created an opt-in repair assignment)" 1 "$AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW"
  else
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
  if [[ "$TASK_SOURCE" == "auto" ]] && [[ -n "$AUTO_REASON" ]]; then
    printf ',"AGENT_LEDGER_TASK_AUTO_REASON":"%s"' "$(json_escape "$AUTO_REASON")"
  fi
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
    if [[ -n "$AUTO_REASON" ]]; then
      printf 'export AGENT_LEDGER_TASK_AUTO_REASON=%q\n' "$AUTO_REASON"
    fi
  else
    printf 'export AGENT_LEDGER_AUTO_ASSIGNED=0\n'
  fi
  parent_export="${PARENT_TASK_FLAG:-${AGENT_LEDGER_PARENT_TASK_ID:-}}"
  if [[ -n "$parent_export" ]]; then
    printf 'export AGENT_LEDGER_PARENT_TASK_ID=%q\n' "$parent_export"
  fi
fi
