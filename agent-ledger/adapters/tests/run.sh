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
json_line="$(bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker --orchestrator test --json)"
node -e '
const line = process.argv[1];
if (!line.startsWith("AGENT_LEDGER_BOOTSTRAP_JSON=")) throw new Error(line);
const env = JSON.parse(line.slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
if (!env.AGENT_ID || env.AGENT_ID.includes("@")) throw new Error(`bad AGENT_ID ${env.AGENT_ID}`);
if (!env.AGENT_LEDGER_TASK_ID?.startsWith("auto/")) throw new Error(`bad task ${env.AGENT_LEDGER_TASK_ID}`);
if (env.AGENT_LEDGER_AUTO_ASSIGNED !== "1") throw new Error("missing auto-assigned flag");
' "$json_line"
grep -q -- "--allow src/\*\* --allow tests/\*\*" "$AGENT_LEDGER_STUB_LOG"
grep -q -- "\[auto-assigned by pi-adapter auto-derived" "$AGENT_LEDGER_STUB_LOG"

export AGENT_LEDGER_REQUIRE_TASK=1
if bash adapters/shared/session-bootstrap.sh --harness pi --agent-kind worker >/tmp/agent-ledger-bootstrap.out 2>/tmp/agent-ledger-bootstrap.err; then
  echo "expected AGENT_LEDGER_REQUIRE_TASK=1 without task id to fail" >&2
  exit 1
fi

printf 'adapter tests passed\n'
