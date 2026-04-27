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
  assign) exit 0 ;;
  *) exit 0 ;;
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

printf 'adapter tests passed\n'
