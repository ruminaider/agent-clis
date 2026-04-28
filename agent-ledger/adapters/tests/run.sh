#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

node --test adapters/tests/*.test.mjs
bash -n adapters/shared/session-bootstrap.sh adapters/shared/marker.sh adapters/pi/install.sh
node --check adapters/shared/marker.js
node --check adapters/babysitter/define-ledger-task.js

# Static smoke checks for the TypeScript pi extension. Node cannot parse
# TypeScript without pi's loader, so keep these dependency-free.
! grep -n -- "--metadata" adapters/pi/agent-ledger.ts
! grep -n "AGENT_LEDGER_DIR = process.env.AGENT_LEDGER_DIR ?? \"\"" adapters/pi/agent-ledger.ts
grep -n "agent-ledger/session-bootstrap.sh" adapters/pi/agent-ledger.ts >/dev/null
grep -n "bootstrapPromise" adapters/pi/agent-ledger.ts >/dev/null
grep -n "file_path" adapters/pi/agent-ledger.ts >/dev/null
grep -n "filePath" adapters/pi/agent-ledger.ts >/dev/null

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"
cat > "$tmp/bin/agent-ledger" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$AGENT_LEDGER_STUB_LOG"
case "$1" in
  identify) exit 0 ;;
  assign)
    if [[ "${2:-}" == "--help" ]]; then
      case "${AGENT_LEDGER_STUB_ASSIGN_HELP_MODE:-ok}" in
        fail)
          printf 'assign help failed\n' >&2
          exit 1
          ;;
        no-metadata)
          printf 'usage: agent-ledger assign [--task] [--reason]\n'
          exit 0
          ;;
        *)
          printf 'usage: agent-ledger assign [--task] [--reason] [--metadata]\n'
          exit 0
          ;;
      esac
    fi
    if [[ -n "${AGENT_LEDGER_STUB_METADATA_LOG:-}" ]]; then
      while [[ $# -gt 0 ]]; do
        case "$1" in
          --metadata)
            printf '%s\n' "$2" > "$AGENT_LEDGER_STUB_METADATA_LOG"
            shift 2
            ;;
          *) shift ;;
        esac
      done
    fi
    if [[ " $* " == *" --if-absent "* ]]; then
      printf 'reused=true\n' >> "$AGENT_LEDGER_STUB_LOG"
    fi
    exit 0 ;;
esac
STUB
chmod +x "$tmp/bin/agent-ledger"

export PATH="$tmp/bin:$PATH"
export AGENT_LEDGER_STUB_LOG="$tmp/ledger.log"
export AGENT_LEDGER_AUTO_ASSIGN_ALLOW='src/**:tests/**'
unset AGENT_ID AGENT_LEDGER_TASK_ID AGENT_LEDGER_PARENT_TASK_ID AGENT_LEDGER_DIR || true

# Auto-fallback: outside any git repo, with no env or flag, the bootstrap
# must produce the timestamp-based id and emit the auto-assigned marker.
nogit="$tmp/nogit"
mkdir -p "$nogit"
json_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$nogit" --json)"
node -e '
const line = process.argv[1];
if (!line.startsWith("AGENT_LEDGER_BOOTSTRAP_JSON=")) throw new Error(line);
const env = JSON.parse(line.slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (!env.AGENT_ID || env.AGENT_ID.includes("@")) throw new Error(`bad AGENT_ID ${env.AGENT_ID}`);
if (!env.AGENT_LEDGER_TASK_ID?.startsWith("auto/")) throw new Error(`bad task ${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_TASK_SOURCE !== "auto") throw new Error(`expected source=auto, got ${env.AGENT_LEDGER_TASK_SOURCE}`);
if (env.AGENT_LEDGER_AUTO_ASSIGNED !== "1") throw new Error("missing auto-assigned flag");
' "$json_line"
grep -q -- "--allow src/\*\* --allow tests/\*\*" "$AGENT_LEDGER_STUB_LOG"
grep -q -- "\[auto-assigned by pi-adapter auto-derived" "$AGENT_LEDGER_STUB_LOG"

# Metadata JSON must survive a harness name containing a quote and the
# assign stub must receive valid JSON.
: > "$AGENT_LEDGER_STUB_LOG"
quoted_metadata="$tmp/quoted-metadata.json"
export AGENT_LEDGER_STUB_METADATA_LOG="$quoted_metadata"
quoted_harness='claude"code'
quoted_line="$(bash adapters/shared/session-bootstrap.sh --harness "$quoted_harness" --agent-kind worker --orchestrator test --cwd "$nogit" --json)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (!env.AGENT_LEDGER_TASK_ID?.startsWith("auto/")) throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_TASK_SOURCE !== "auto") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
' "$quoted_line"
python3 - "$quoted_metadata" <<'PY'
import json
import sys
with open(sys.argv[1], encoding='utf-8') as fh:
    meta = json.load(fh)
if meta.get('auto_assigned_by') != 'claude"code-adapter':
    raise SystemExit(meta.get('auto_assigned_by'))
if meta.get('task_source') != 'auto':
    raise SystemExit(meta.get('task_source'))
if meta.get('parent_task') is not None:
    raise SystemExit('unexpected parent_task')
PY
grep -q -- '^assign ' "$AGENT_LEDGER_STUB_LOG"
unset AGENT_LEDGER_STUB_METADATA_LOG

# Missing --metadata capability must fail loud when assign --help is
# unavailable or does not advertise the flag.
: > "$AGENT_LEDGER_STUB_LOG"
for mode in fail no-metadata; do
  export AGENT_LEDGER_STUB_ASSIGN_HELP_MODE="$mode"
  if bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$nogit" >/dev/null 2>"$tmp/missing-metadata-$mode.err"; then
    echo "expected bootstrap to fail when assign --help mode=$mode" >&2
    exit 1
  fi
  grep -q -- '--metadata capability' "$tmp/missing-metadata-$mode.err"
  grep -q -- 'kernel v0.1.1+ required' "$tmp/missing-metadata-$mode.err"
done
unset AGENT_LEDGER_STUB_ASSIGN_HELP_MODE

# Branch-derived: inside a git repo on a feature branch the bootstrap
# must use <branch> as the task id, mark TASK_SOURCE=branch, NOT set
# AUTO_ASSIGNED, and emit a [harness-derived ...] marker.
: > "$AGENT_LEDGER_STUB_LOG"
repo="$tmp/repo"
mkdir -p "$repo"
git -C "$repo" init -q
git -C "$repo" -c user.email=t@t -c user.name=t commit --allow-empty -qm init
git -C "$repo" checkout -q -b feature/branch-derived
branch_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo" --json)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_ID !== "feature/branch-derived") throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_TASK_SOURCE !== "branch") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
if (env.AGENT_LEDGER_AUTO_ASSIGNED !== "0") throw new Error(`AUTO_ASSIGNED=${env.AGENT_LEDGER_AUTO_ASSIGNED}`);
' "$branch_line"
grep -q -- "\[harness-derived by pi-adapter source=branch task=feature/branch-derived" "$AGENT_LEDGER_STUB_LOG"

# Detached HEAD: the bootstrap must use detached/<short-sha> and
# TASK_SOURCE=detached.
: > "$AGENT_LEDGER_STUB_LOG"
short_sha="$(git -C "$repo" rev-parse --short HEAD)"
git -C "$repo" checkout -q --detach
detached_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo" --json)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
const expectedShort = process.argv[2];
if (env.AGENT_LEDGER_TASK_ID !== `detached/${expectedShort}`) throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_TASK_SOURCE !== "detached") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
' "$detached_line" "$short_sha"
grep -q -- "\[harness-derived by pi-adapter source=detached" "$AGENT_LEDGER_STUB_LOG"

# Explicit env var beats branch detection. AGENT_LEDGER_TASK_ID set =>
# bootstrap must skip the assign step and emit TASK_SOURCE=env.
: > "$AGENT_LEDGER_STUB_LOG"
git -C "$repo" checkout -q feature/branch-derived
export AGENT_LEDGER_TASK_ID="explicit-task"
explicit_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo" --json)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_ID !== "explicit-task") throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_TASK_SOURCE !== "env") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
if (env.AGENT_LEDGER_AUTO_ASSIGNED !== "0") throw new Error("AUTO_ASSIGNED should be 0 for explicit task");
' "$explicit_line"
grep -q "^assign" "$AGENT_LEDGER_STUB_LOG" && { echo "explicit task should not trigger assign" >&2; exit 1; } || true
unset AGENT_LEDGER_TASK_ID

# --task-id flag beats env. Also explicit (TASK_SOURCE=flag).
: > "$AGENT_LEDGER_STUB_LOG"
export AGENT_LEDGER_TASK_ID="env-task"
flag_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo" --task-id flag-task --json)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_ID !== "flag-task") throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_TASK_SOURCE !== "flag") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
' "$flag_line"
unset AGENT_LEDGER_TASK_ID

# AGENT_LEDGER_REQUIRE_TASK=1 outside a git repo (no derivable context)
# must fail closed.
export AGENT_LEDGER_REQUIRE_TASK=1
if bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --cwd "$nogit" >/tmp/agent-ledger-bootstrap.out 2>/tmp/agent-ledger-bootstrap.err; then
  echo "expected AGENT_LEDGER_REQUIRE_TASK=1 outside a repo to fail" >&2
  exit 1
fi
unset AGENT_LEDGER_REQUIRE_TASK

# AGENT_LEDGER_REQUIRE_TASK=1 inside a git repo must NOT fail when
# branch detection succeeds.
export AGENT_LEDGER_REQUIRE_TASK=1
require_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --cwd "$repo" --json)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_SOURCE !== "branch") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
' "$require_line"
unset AGENT_LEDGER_REQUIRE_TASK

# PR detection success: gh resolves to a PR number, so the bootstrap must
# derive pr-42 and emit a harness-derived marker.
: > "$AGENT_LEDGER_STUB_LOG"
repo_pr="$tmp/repo-pr"
mkdir -p "$repo_pr"
git -C "$repo_pr" init -q
git -C "$repo_pr" -c user.email=t@t -c user.name=t commit --allow-empty -qm init
git -C "$repo_pr" checkout -q -b feature/pr-test
mkdir -p "$tmp/bin-gh-pr"
cat > "$tmp/bin-gh-pr/gh" <<'STUB'
#!/usr/bin/env bash
echo 42
STUB
chmod +x "$tmp/bin-gh-pr/gh"
pr_line="$(PATH="$tmp/bin-gh-pr:$tmp/bin:$PATH" bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo_pr" --detect-pr 1 --json)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_ID !== "pr-42") throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_TASK_SOURCE !== "pr") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
' "$pr_line"
grep -q -- "\[harness-derived by pi-adapter source=pr task=pr-42" "$AGENT_LEDGER_STUB_LOG"

# PR detection failure fallthrough: when gh exits non-zero, branch
# detection must still work.
: > "$AGENT_LEDGER_STUB_LOG"
repo_pr_fall="$tmp/repo-pr-fall"
mkdir -p "$repo_pr_fall"
git -C "$repo_pr_fall" init -q
git -C "$repo_pr_fall" -c user.email=t@t -c user.name=t commit --allow-empty -qm init
git -C "$repo_pr_fall" checkout -q -b feature/pr-fallthrough
mkdir -p "$tmp/bin-gh-fail"
cat > "$tmp/bin-gh-fail/gh" <<'STUB'
#!/usr/bin/env bash
exit 1
STUB
chmod +x "$tmp/bin-gh-fail/gh"
fall_line="$(PATH="$tmp/bin-gh-fail:$tmp/bin:$PATH" bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo_pr_fall" --detect-pr 1 --json)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_SOURCE !== "branch") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
if (env.AGENT_LEDGER_TASK_ID !== "feature/pr-fallthrough") throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
' "$fall_line"

# Unborn branch: branch detection must resolve without any commits.
: > "$AGENT_LEDGER_STUB_LOG"
repo_unborn="$tmp/repo-unborn"
mkdir -p "$repo_unborn"
git -C "$repo_unborn" init -q
git -C "$repo_unborn" checkout -q -b feature/no-commits
unborn_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo_unborn" --json)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_SOURCE !== "branch") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
if (env.AGENT_LEDGER_TASK_ID !== "feature/no-commits") throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
' "$unborn_line"

# Idempotency replay: the assign call site must include --if-absent on
# repeated bootstrap invocations.
: > "$AGENT_LEDGER_STUB_LOG"
repo_replay="$tmp/repo-replay"
mkdir -p "$repo_replay"
git -C "$repo_replay" init -q
git -C "$repo_replay" -c user.email=t@t -c user.name=t commit --allow-empty -qm init
git -C "$repo_replay" checkout -q -b feature/replay
export AGENT_ID="test-agent-replay"
bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo_replay" >/dev/null
grep -q -- '--if-absent' "$AGENT_LEDGER_STUB_LOG"
bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo_replay" >/dev/null
grep -q -- '--if-absent' "$AGENT_LEDGER_STUB_LOG"
unset AGENT_ID

# Shell export must surface AUTO_ASSIGNED=0 for branch-derived tasks.
: > "$AGENT_LEDGER_STUB_LOG"
repo_shell="$tmp/repo-shell"
mkdir -p "$repo_shell"
git -C "$repo_shell" init -q
git -C "$repo_shell" -c user.email=t@t -c user.name=t commit --allow-empty -qm init
git -C "$repo_shell" checkout -q -b feature/shell-export
shell_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo_shell")"
grep -q 'export AGENT_LEDGER_AUTO_ASSIGNED=0' <<<"$shell_line"

printf 'adapter tests passed\n'
