#!/usr/bin/env bash
# Install the Agent Ledger pi extension into ~/.pi/agent/extensions/.
#
# Symlinks the extension and the shared bootstrap helper so updates in
# the source tree are picked up by `pi /reload`.
#
# Usage: ./install.sh [--prefix <dir>]   (default prefix: ~/.pi/agent/extensions)

set -euo pipefail

PREFIX="${HOME}/.pi/agent/extensions"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix) PREFIX="$2"; shift 2 ;;
    *) echo "install.sh: unknown flag $1" >&2; exit 2 ;;
  esac
done

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SHARED_DIR="$HERE/../shared"

mkdir -p "$PREFIX"

# Symlink the extension itself.
ln -sf "$HERE/agent-ledger.ts" "$PREFIX/agent-ledger.ts"

# Symlink the bootstrap helper into a co-located dir the extension can find.
mkdir -p "$PREFIX/agent-ledger"
ln -sf "$SHARED_DIR/session-bootstrap.sh" "$PREFIX/agent-ledger/session-bootstrap.sh"

echo "Installed:"
echo "  $PREFIX/agent-ledger.ts -> $HERE/agent-ledger.ts"
echo "  $PREFIX/agent-ledger/session-bootstrap.sh -> $SHARED_DIR/session-bootstrap.sh"
echo
echo "Next: open pi in any project that has run 'agent-ledger init',"
echo "then run /reload to load the extension. Set AGENT_LEDGER_TASK_ID"
echo "before dispatching subagents to keep auto-assignment quiet."
