#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

# Build the real agent-ledger binary for adapter E2E tests.
# The runner always rebuilds and relies on Go's build cache, so
# incremental rebuilds stay cheap when sources have not changed.
echo "adapters/tests/run.sh: building agent-ledger binary..." >&2
make build >&2
export AGENT_LEDGER_BIN="$ROOT/bin/agent-ledger"

node --test adapters/tests/*.test.mjs
bash -n adapters/shared/session-bootstrap.sh adapters/shared/marker.sh adapters/pi/install.sh
node --check adapters/shared/marker.js
node --check adapters/shared/auto-fallback-toast.js
node --check adapters/babysitter/define-ledger-task.js

# Parity check: the inline AUTO_REASON_HINTS map in the TS extension and
# the shared JS module must agree byte-for-byte on every reason key and
# its hint text. The shared JS file is what the node-based test suite
# exercises; the TS file is what pi loads at runtime. Drift between them
# would mean the toast tests pass while the actual operator UI shows a
# stale or missing hint. We diff a normalized projection of both files
# rather than parse JS, so neither file needs a build step.
python3 - <<'PYHINTS'
import re, sys
from pathlib import Path

def extract(path):
    text = Path(path).read_text()
    m = re.search(r'AUTO_REASON_HINTS\b[^=]*=\s*(?:Object\.freeze\(\s*)?\{(.*?)\}\s*\)?\s*[;,]', text, re.S)
    if not m:
        sys.exit(f"AUTO_REASON_HINTS not found in {path}")
    body = m.group(1)
    # Split by `,` followed by newline at top level (no nested braces in this map).
    entries = re.findall(r'(\w+)\s*:\s*"((?:[^"\\]|\\.)*)"', body)
    if not entries:
        sys.exit(f"AUTO_REASON_HINTS entries not found in {path}")
    return dict(entries)

ts = extract('adapters/pi/agent-ledger.ts')
js = extract('adapters/shared/auto-fallback-toast.js')
if ts != js:
    only_ts = {k: ts[k] for k in ts if ts.get(k) != js.get(k)}
    only_js = {k: js[k] for k in js if js.get(k) != ts.get(k)}
    sys.exit(f"AUTO_REASON_HINTS drift between TS and JS:\n  TS-only/diff: {only_ts}\n  JS-only/diff: {only_js}")
required = {'not_in_git_repo','git_no_head','pointer_lacks_default','pointer_unreadable','pointer_parser_unavailable'}
missing = required - set(ts)
if missing:
    sys.exit(f"AUTO_REASON_HINTS missing required reasons: {sorted(missing)}")
PYHINTS

# Static smoke checks for the TypeScript pi extension. Node cannot parse
# TypeScript without pi's loader, so keep these dependency-free.
! grep -n -- "--metadata" adapters/pi/agent-ledger.ts
! grep -n "AGENT_LEDGER_DIR = process.env.AGENT_LEDGER_DIR ?? \"\"" adapters/pi/agent-ledger.ts
grep -n "agent-ledger/session-bootstrap.sh" adapters/pi/agent-ledger.ts >/dev/null
grep -n "bootstrapPromise" adapters/pi/agent-ledger.ts >/dev/null
grep -n "isExecutionSubagentCall" adapters/pi/agent-ledger.ts >/dev/null
grep -n "shouldBlockBootstrapFailure" adapters/pi/agent-ledger.ts >/dev/null
grep -n "cwd," adapters/pi/agent-ledger.ts >/dev/null
grep -n "file_path" adapters/pi/agent-ledger.ts >/dev/null
grep -n "filePath" adapters/pi/agent-ledger.ts >/dev/null
# Old format (timestamp only) must not reappear inline.
! grep -n 'childTask = `\${state.resolvedTaskId}/\${childAgent}/\${Date.now().toString(36)}`' adapters/pi/agent-ledger.ts

# Child self-assignment contract checks (Option D, task-007).
#
# These prove the parent extension does not mint child task ids,
# does not call agent-ledger assign on behalf of the child, and does
# not mutate process.env for any subagent dispatch. The child is
# responsible for its own bootstrap.
#
# 1. The TaskSource union and KNOWN_TASK_SOURCES include `subagent`
#    so parseTaskSource("subagent") returns "subagent".
python3 - <<'PYEXT'
import re
import sys
from pathlib import Path

src = Path('adapters/pi/agent-ledger.ts').read_text()
m = re.search(r'KNOWN_TASK_SOURCES\s*=\s*new Set<TaskSource>\(\[(.*?)\]\)', src, re.S)
if not m or '"subagent"' not in m.group(1):
    sys.exit('KNOWN_TASK_SOURCES must include "subagent"')
m = re.search(r'type\s+TaskSource\s*=\s*([^;]+);', src)
if not m or '"subagent"' not in m.group(1):
    sys.exit('TaskSource union must include "subagent"')
PYEXT

# 2. Eager child bootstrap fires at extension load when
#    PI_SUBAGENT_CHILD === "1".
grep -n 'process.env.PI_SUBAGENT_CHILD === "1"' adapters/pi/agent-ledger.ts >/dev/null

# 2a. shouldBlockBootstrapFailure() must hard-fail in child mode so a
#     misconfigured subagent child cannot soft-fail past bootstrap and
#     write files without a durable assignment row. The check appears
#     inside the function body alongside the existing
#     AGENT_LEDGER_REQUIRE_TASK and AGENT_LEDGER_TASK_ID checks. See
#     `tasks/option-d-context.md` decision 3.
python3 - <<'PYEXT'
from pathlib import Path
import re
import sys

src = Path('adapters/pi/agent-ledger.ts').read_text()
m = re.search(r'function\s+shouldBlockBootstrapFailure\s*\([^)]*\)\s*:\s*boolean\s*\{(.*?)\n\}', src, re.S)
if not m:
    sys.exit('shouldBlockBootstrapFailure() not found')
body = m.group(1)
if 'PI_SUBAGENT_CHILD' not in body or '"1"' not in body:
    sys.exit('shouldBlockBootstrapFailure must hard-fail when PI_SUBAGENT_CHILD === "1"')
if 'AGENT_LEDGER_REQUIRE_TASK' not in body or 'AGENT_LEDGER_TASK_ID' not in body:
    sys.exit('shouldBlockBootstrapFailure must keep its existing AGENT_LEDGER_REQUIRE_TASK / AGENT_LEDGER_TASK_ID checks')
PYEXT

# 2b. Eager child bootstrap rejection must persist on the BootstrapState
#     so the first `tool_call` hook can observe it and block. Without
#     this the rejection only logs to stderr and subsequent tool calls
#     proceed as if bootstrap had not been attempted, which violates
#     the locked decision 3 contract.
python3 - <<'PYEXT'
from pathlib import Path
import re
import sys

src = Path('adapters/pi/agent-ledger.ts').read_text()
if 'eagerBootstrapError' not in src:
    sys.exit('BootstrapState must track an eager bootstrap failure (e.g. eagerBootstrapError)')
# The state field must be declared on the interface.
m = re.search(r'interface\s+BootstrapState\s*\{(.*?)\n\}', src, re.S)
if not m or 'eagerBootstrapError' not in m.group(1):
    sys.exit('BootstrapState interface must declare eagerBootstrapError')
# The eager bootstrap rejection path must assign to it.
m = re.search(r'PI_SUBAGENT_CHILD === "1"\s*\)\s*\{(.*?)\n  \}', src, re.S)
if not m or 'state.eagerBootstrapError' not in m.group(1):
    sys.exit('eager bootstrap rejection must persist state.eagerBootstrapError')
# The tool_call hook must read it and return block: true under
# shouldBlockBootstrapFailure() semantics.
hook_start = src.index('pi.on("tool_call"')
hook = src[hook_start:hook_start + 4000]
if 'state.eagerBootstrapError' not in hook:
    sys.exit('tool_call hook must read state.eagerBootstrapError')
if 'shouldBlockBootstrapFailure()' not in hook:
    sys.exit('tool_call hook must gate the eager-failure block on shouldBlockBootstrapFailure()')
if 'block: true' not in hook:
    sys.exit('tool_call hook must return block: true on eager bootstrap failure')
# The block reason should connect the failure back to the bootstrap path.
block_marker = 'block: true'
block_idx = hook.index(block_marker)
window = hook[max(0, block_idx - 200):block_idx + 200]
if 'agent-ledger bootstrap failed' not in window and 'PI_SUBAGENT_CHILD' not in window and 'subagent' not in window.lower():
    sys.exit('eager-failure block reason must mention agent-ledger bootstrap, subagent, or PI_SUBAGENT_CHILD')
PYEXT

# 3. The subagent `tool_call` block exists, but its body is
#    observation-only: no env mutation, no parent-side assign call,
#    no overlap guard, and no `block: true` return.
python3 - <<'PYEXT'
from pathlib import Path
import re
import sys

src = Path('adapters/pi/agent-ledger.ts').read_text()
start = src.index('if (SUBAGENT_TOOLS.has(toolName)) {')
# Walk braces from the `{` after the `if (...)` to find the matching close.
body_start = src.index('{', start)
depth = 0
end = None
for i in range(body_start, len(src)):
    ch = src[i]
    if ch == '{':
        depth += 1
    elif ch == '}':
        depth -= 1
        if depth == 0:
            end = i + 1
            break
if end is None:
    sys.exit('could not find end of subagent tool_call block')
block = src[body_start:end]
forbidden = [
    ('process.env[', 'subagent block must not mutate process.env'),
    ('runLedger(["assign"', 'subagent block must not call agent-ledger assign'),
    ('subagentEnvRestores', 'subagent block must not use a single-flight env guard'),
    ('block: true', 'subagent block must not return block: true'),
    ('snapshotEnv', 'subagent block must not snapshot env'),
    ('restoreEnv', 'subagent block must not restore env'),
]
for token, message in forbidden:
    if token in block:
        sys.exit(message)
PYEXT

# 4. The deleted helpers must not reappear.
! grep -n 'function generateChildTaskId' adapters/pi/agent-ledger.ts
! grep -n 'from "node:crypto"' adapters/pi/agent-ledger.ts
! grep -n 'function snapshotEnv' adapters/pi/agent-ledger.ts
! grep -n 'function restoreEnv' adapters/pi/agent-ledger.ts
! grep -n 'function collectSubagentCwds' adapters/pi/agent-ledger.ts
! grep -n 'function resolveSubagentLedgerCwd' adapters/pi/agent-ledger.ts
! grep -n 'subagentEnvRestores' adapters/pi/agent-ledger.ts

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"
cat > "$tmp/bin/agent-ledger" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$AGENT_LEDGER_STUB_LOG"
case "$1" in
  identify) exit 0 ;;
  assignments)
    count="${AGENT_LEDGER_STUB_ASSIGNMENTS_COUNT:-0}"
    printf '{"assignments":[],"count":%s,"schema":"agent-ledger.assignments.v1"}\n' "$count"
    exit 0 ;;
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
  pointer)
    # `pointer show` returns the JSON in AGENT_LEDGER_STUB_POINTER_JSON
    # if set; otherwise an absent pointer. The bootstrap script invokes
    # this for source=pointer detection, so this default lets every
    # other test continue to fall through to auto.
    #
    # AGENT_LEDGER_STUB_POINTER_FAIL=1 simulates a malformed pointer
    # file: stderr is written and the stub exits non-zero. The bootstrap
    # must surface AUTO_REASON=pointer_unreadable rather than silently
    # treating this as 'no pointer'.
    case "${2:-}" in
      show)
        if [[ "${AGENT_LEDGER_STUB_POINTER_FAIL:-0}" == "1" ]]; then
          printf 'pointer parse error: invalid TOML at line 1\n' >&2
          exit 1
        fi
        if [[ -n "${AGENT_LEDGER_STUB_POINTER_JSON:-}" ]]; then
          printf '%s\n' "$AGENT_LEDGER_STUB_POINTER_JSON"
        else
          printf '{"present":false,"path":"%s/.agent-ledger.toml"}\n' "$PWD"
        fi
        exit 0 ;;
    esac
    ;;
esac
STUB
chmod +x "$tmp/bin/agent-ledger"

export PATH="$tmp/bin:$PATH"
export AGENT_LEDGER_STUB_LOG="$tmp/ledger.log"
export AGENT_LEDGER_AUTO_ASSIGN_ALLOW='src/**:tests/**'
unset AGENT_ID AGENT_LEDGER_TASK_ID AGENT_LEDGER_PARENT_TASK_ID AGENT_LEDGER_DIR || true
# Pi subagent child env may be inherited from a parent session that
# itself runs as a subagent. Clear it so the legacy task-source chain
# tests below behave as if the script were invoked from a normal
# (non-subagent) shell.
unset PI_SUBAGENT_CHILD PI_SUBAGENT_RUN_ID PI_SUBAGENT_CHILD_INDEX PI_SUBAGENT_CHILD_AGENT || true

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

# Pointer-derived: outside any git repo, with a pointer file declaring
# default_task_id, the bootstrap must use that task id, mark
# TASK_SOURCE=pointer, and NOT set AUTO_ASSIGNED.
: > "$AGENT_LEDGER_STUB_LOG"
export AGENT_LEDGER_STUB_POINTER_JSON='{"present":true,"path":"/tmp/x/.agent-ledger.toml","version":1,"default_task_id":"ambient-2026-05"}'
pointer_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$nogit" --json)"
unset AGENT_LEDGER_STUB_POINTER_JSON
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_ID !== "ambient-2026-05") throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_TASK_SOURCE !== "pointer") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
if (env.AGENT_LEDGER_AUTO_ASSIGNED !== "0") throw new Error(`AUTO_ASSIGNED=${env.AGENT_LEDGER_AUTO_ASSIGNED}`);
if ("AGENT_LEDGER_TASK_AUTO_REASON" in env) throw new Error("non-auto source must not set AUTO_REASON");
' "$pointer_line"
grep -q -- "\[harness-derived by pi-adapter source=pointer task=ambient-2026-05" "$AGENT_LEDGER_STUB_LOG"

# Pointer file exists but the kernel could not parse it. The bootstrap
# must surface AUTO_REASON=pointer_unreadable rather than silently
# falling through to a misleading not_in_git_repo / git_no_head hint.
# Regression guard for PR #23 finding F1 (the original `|| true`
# silently swallowed the kernel exit code).
: > "$AGENT_LEDGER_STUB_LOG"
export AGENT_LEDGER_STUB_POINTER_FAIL=1
ptr_unreadable_err="$tmp/ptr-unreadable.err"
ptr_unreadable_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$nogit" --json 2>"$ptr_unreadable_err")"
unset AGENT_LEDGER_STUB_POINTER_FAIL
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_SOURCE !== "auto") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
if (env.AGENT_LEDGER_TASK_AUTO_REASON !== "pointer_unreadable") throw new Error(`AUTO_REASON=${env.AGENT_LEDGER_TASK_AUTO_REASON}`);
' "$ptr_unreadable_line"
grep -q "agent-ledger pointer show failed" "$ptr_unreadable_err"
grep -q "pointer parse error: invalid TOML" "$ptr_unreadable_err"

# Pointer present but missing default_task_id: bootstrap must fall
# through to auto and surface AUTO_REASON=pointer_lacks_default so the
# UI toast can name the cheapest fix.
: > "$AGENT_LEDGER_STUB_LOG"
export AGENT_LEDGER_STUB_POINTER_JSON='{"present":true,"path":"/tmp/x/.agent-ledger.toml","version":1}'
ptr_empty_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$nogit" --json)"
unset AGENT_LEDGER_STUB_POINTER_JSON
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (!env.AGENT_LEDGER_TASK_ID?.startsWith("auto/")) throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_TASK_SOURCE !== "auto") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
if (env.AGENT_LEDGER_AUTO_ASSIGNED !== "1") throw new Error("AUTO_ASSIGNED missing");
if (env.AGENT_LEDGER_TASK_AUTO_REASON !== "pointer_lacks_default") throw new Error(`AUTO_REASON=${env.AGENT_LEDGER_TASK_AUTO_REASON}`);
' "$ptr_empty_line"

# Auto-fallback in a non-git directory must surface
# AUTO_REASON=not_in_git_repo so the toast can suggest the cheapest
# fix. This test is paired with the auto-fallback case earlier in the
# file; that earlier case asserts behavior, this one asserts the new
# AUTO_REASON contract.
: > "$AGENT_LEDGER_STUB_LOG"
auto_reason_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$nogit" --json)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_SOURCE !== "auto") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
if (env.AGENT_LEDGER_TASK_AUTO_REASON !== "not_in_git_repo") throw new Error(`AUTO_REASON=${env.AGENT_LEDGER_TASK_AUTO_REASON}`);
' "$auto_reason_line"

# Explicit env var beats branch detection. AGENT_LEDGER_TASK_ID set =>
# bootstrap verifies the orchestrator already created an active
# assignment, skips the repair assign, and emits TASK_SOURCE=env.
: > "$AGENT_LEDGER_STUB_LOG"
git -C "$repo" checkout -q feature/branch-derived
export AGENT_LEDGER_STUB_ASSIGNMENTS_COUNT=1
export AGENT_LEDGER_TASK_ID="explicit-task"
explicit_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo" --json)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_ID !== "explicit-task") throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_TASK_SOURCE !== "env") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
if (env.AGENT_LEDGER_AUTO_ASSIGNED !== "0") throw new Error("AUTO_ASSIGNED should be 0 for explicit task");
' "$explicit_line"
grep -q "^assign " "$AGENT_LEDGER_STUB_LOG" && { echo "explicit task with existing assignment should not trigger assign" >&2; exit 1; } || true
unset AGENT_LEDGER_TASK_ID AGENT_LEDGER_STUB_ASSIGNMENTS_COUNT

# Explicit env var with no active assignment fails closed by default.
: > "$AGENT_LEDGER_STUB_LOG"
export AGENT_LEDGER_STUB_ASSIGNMENTS_COUNT=0
export AGENT_LEDGER_TASK_ID="missing-explicit-task"
if bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo" --json >/dev/null 2>"$tmp/missing-explicit-default.err"; then
  echo "expected explicit task without assignment to fail by default" >&2
  exit 1
fi
grep -q -- 'run agent-ledger assign before launching or instructing this worker' "$tmp/missing-explicit-default.err"
grep -q "^assign " "$AGENT_LEDGER_STUB_LOG" && { echo "default explicit missing assignment should not repair" >&2; exit 1; } || true
unset AGENT_LEDGER_TASK_ID AGENT_LEDGER_STUB_ASSIGNMENTS_COUNT

# Opt-in explicit repair requires a non-empty explicit repair allow-list,
# so the bootstrap cannot silently widen scope to **.
: > "$AGENT_LEDGER_STUB_LOG"
export AGENT_LEDGER_STUB_ASSIGNMENTS_COUNT=0
export AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT=1
export AGENT_LEDGER_TASK_ID="missing-explicit-task"
if bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo" --json >/dev/null 2>"$tmp/missing-explicit-no-allow.err"; then
  echo "expected explicit repair without allow-list to fail" >&2
  exit 1
fi
grep -q -- 'AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW is required' "$tmp/missing-explicit-no-allow.err"
unset AGENT_LEDGER_TASK_ID AGENT_LEDGER_STUB_ASSIGNMENTS_COUNT AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT

# Explicit repair is available only with an operator-supplied allow-list.
# The task id remains explicit, but the assignment is marked for audit.
: > "$AGENT_LEDGER_STUB_LOG"
repair_metadata="$tmp/explicit-repair-metadata.json"
export AGENT_LEDGER_STUB_ASSIGNMENTS_COUNT=0
export AGENT_LEDGER_STUB_METADATA_LOG="$repair_metadata"
export AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT=1
export AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW='src/explicit/**:tests/explicit/**'
export AGENT_LEDGER_TASK_ID="missing-explicit-task"
repair_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo" --json)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_ID !== "missing-explicit-task") throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_TASK_SOURCE !== "env") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
if (env.AGENT_LEDGER_AUTO_ASSIGNED !== "0") throw new Error(`AUTO_ASSIGNED=${env.AGENT_LEDGER_AUTO_ASSIGNED}`);
' "$repair_line"
grep -q -- '^assign ' "$AGENT_LEDGER_STUB_LOG"
grep -q -- '--allow src/explicit/\*\* --allow tests/explicit/\*\*' "$AGENT_LEDGER_STUB_LOG"
grep -q -- 'opt-in repair assignment' "$AGENT_LEDGER_STUB_LOG"
python3 - "$repair_metadata" <<'PYREPAIR'
import json
import sys
with open(sys.argv[1], encoding='utf-8') as fh:
    meta = json.load(fh)
if meta.get('auto_assigned') is not True:
    raise SystemExit(meta)
if meta.get('task_source') != 'env':
    raise SystemExit(meta)
if meta.get('explicit_missing_assignment') is not True:
    raise SystemExit(meta)
PYREPAIR
unset AGENT_LEDGER_TASK_ID AGENT_LEDGER_STUB_ASSIGNMENTS_COUNT AGENT_LEDGER_STUB_METADATA_LOG AGENT_LEDGER_REPAIR_EXPLICIT_ASSIGNMENT AGENT_LEDGER_EXPLICIT_REPAIR_ALLOW

# --task-id flag beats env. Also explicit (TASK_SOURCE=flag).
: > "$AGENT_LEDGER_STUB_LOG"
export AGENT_LEDGER_STUB_ASSIGNMENTS_COUNT=1
export AGENT_LEDGER_TASK_ID="env-task"
flag_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --cwd "$repo" --task-id flag-task --json)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_ID !== "flag-task") throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_TASK_SOURCE !== "flag") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
' "$flag_line"
grep -q "^assign " "$AGENT_LEDGER_STUB_LOG" && { echo "explicit flag with existing assignment should not trigger assign" >&2; exit 1; } || true
unset AGENT_LEDGER_TASK_ID AGENT_LEDGER_STUB_ASSIGNMENTS_COUNT

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

# Subagent source: PI_SUBAGENT_CHILD=1 with the four required env vars
# must derive a deterministic child task id, a deterministic child
# AGENT_ID, preserve the inherited parent AGENT_ID as --orchestrator,
# emit TASK_SOURCE=subagent, and write a metadata payload matching the
# locked decision 7 schema (subagent_child_index is a JSON number).
: > "$AGENT_LEDGER_STUB_LOG"
subagent_metadata="$tmp/subagent-metadata.json"
export AGENT_LEDGER_STUB_METADATA_LOG="$subagent_metadata"
subagent_line="$(
  PI_SUBAGENT_CHILD=1 \
  PI_SUBAGENT_RUN_ID=run-abc \
  PI_SUBAGENT_CHILD_INDEX=0 \
  PI_SUBAGENT_CHILD_AGENT=worker \
  AGENT_LEDGER_TASK_ID=parent/task \
  AGENT_ID=agent:pi:parent:42 \
  bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --cwd "$nogit" --json
)"
node -e '
const env = JSON.parse(process.argv[1].slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (env.AGENT_LEDGER_TASK_ID !== "parent/task/worker/run-abc-0") throw new Error(`task=${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_TASK_SOURCE !== "subagent") throw new Error(`source=${env.AGENT_LEDGER_TASK_SOURCE}`);
if (env.AGENT_LEDGER_AUTO_ASSIGNED !== "0") throw new Error(`AUTO_ASSIGNED=${env.AGENT_LEDGER_AUTO_ASSIGNED}`);
if (env.AGENT_ID !== "agent:pi:subagent:run-abc:0") throw new Error(`AGENT_ID=${env.AGENT_ID}`);
if (env.AGENT_LEDGER_PARENT_TASK_ID !== "parent/task") throw new Error(`PARENT_TASK_ID=${env.AGENT_LEDGER_PARENT_TASK_ID}`);
' "$subagent_line"
grep -q -- "--orchestrator agent:pi:parent:42" "$AGENT_LEDGER_STUB_LOG"
grep -q -- "--agent agent:pi:subagent:run-abc:0" "$AGENT_LEDGER_STUB_LOG"
grep -q -- "--task parent/task/worker/run-abc-0" "$AGENT_LEDGER_STUB_LOG"
grep -q -- "--if-absent" "$AGENT_LEDGER_STUB_LOG"
grep -q -- "\[harness-derived by pi-adapter source=subagent" "$AGENT_LEDGER_STUB_LOG"
python3 - "$subagent_metadata" <<'PYSUB'
import json
import sys
with open(sys.argv[1], encoding='utf-8') as fh:
    raw = fh.read()
    meta = json.loads(raw)
if meta.get("parent_task") != "parent/task":
    raise SystemExit(meta)
if meta.get("parent_agent_id") != "agent:pi:parent:42":
    raise SystemExit(meta)
if meta.get("subagent_run_id") != "run-abc":
    raise SystemExit(meta)
if meta.get("subagent_child_index") != 0:
    raise SystemExit(meta)
if not isinstance(meta.get("subagent_child_index"), int):
    raise SystemExit("subagent_child_index must be a JSON number")
if meta.get("subagent_child_agent") != "worker":
    raise SystemExit(meta)
if meta.get("dispatch_origin") != "pi-subagent-bootstrap":
    raise SystemExit(meta)
PYSUB
unset AGENT_LEDGER_STUB_METADATA_LOG

# Subagent source: a missing required env var must hard-fail with a
# clear diagnostic and must NOT fall back to branch or auto.
: > "$AGENT_LEDGER_STUB_LOG"
if PI_SUBAGENT_CHILD=1 \
   PI_SUBAGENT_RUN_ID=run-abc \
   PI_SUBAGENT_CHILD_INDEX=0 \
   PI_SUBAGENT_CHILD_AGENT=worker \
   AGENT_ID=agent:pi:parent:42 \
   bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --cwd "$repo" --json \
     >/dev/null 2>"$tmp/subagent-missing-parent-task.err"; then
  echo "expected subagent bootstrap to fail when AGENT_LEDGER_TASK_ID is unset" >&2
  exit 1
fi
grep -q -- 'AGENT_LEDGER_TASK_ID' "$tmp/subagent-missing-parent-task.err"
grep -q -- 'refusing to fall back' "$tmp/subagent-missing-parent-task.err"
grep -q '^assign ' "$AGENT_LEDGER_STUB_LOG" && { echo "missing-env subagent bootstrap should not call assign" >&2; exit 1; } || true

printf 'adapter tests passed\n'
