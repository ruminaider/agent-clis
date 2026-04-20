#!/usr/bin/env bash
set -euo pipefail

# ─── Linear CLI Installer ────────────────────────────────
# Installs @ruminaider/linear-cli globally via npm.
# Scaffold only. Core behavior lands in later waves.
# ──────────────────────────────────────────────────────────

info()  { echo "  → $*"; }
ok()    { echo "  ✓ $*"; }
fail()  { echo "  ✗ $*" >&2; exit 1; }

echo ""
echo "  Linear CLI Installer"
echo "  ────────────────────"
echo ""

if ! command -v node &>/dev/null; then
  fail "Node.js 18+ is required. Install from https://nodejs.org"
fi

NODE_VERSION=$(node -v | sed 's/v//' | cut -d. -f1)
if [ "$NODE_VERSION" -lt 18 ]; then
  fail "Node.js 18+ required (found $(node -v))"
fi
ok "Node.js $(node -v)"

if ! command -v npm &>/dev/null; then
  fail "npm is required"
fi
ok "npm $(npm -v)"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ -d "$SCRIPT_DIR/cli/bin" ] && [ -f "$SCRIPT_DIR/cli/package.json" ]; then
  info "Installing from local source..."
  cd "$SCRIPT_DIR/cli"
  npm install --silent 2>/dev/null
  npm link --silent 2>/dev/null && ok "Linked as 'linear-cli'" || {
    fail "npm link failed. Try: sudo npm link"
  }
else
  info "Installing @ruminaider/linear-cli from npm..."
  npm install -g @ruminaider/linear-cli 2>&1 | tail -1
  ok "Installed from npm"
fi

if command -v linear-cli &>/dev/null; then
  ok "linear-cli is ready"
else
  info "linear-cli installed but may need a shell restart"
fi

echo ""
echo "  ────────────────────"
echo "  ✓ Installed!"
echo ""
echo "  Authenticate:"
echo "    linear-cli auth"
echo ""
echo "  Then use:"
echo "    linear-cli search \"my project\""
echo "    linear-cli issue get <issue-id>"
echo ""
