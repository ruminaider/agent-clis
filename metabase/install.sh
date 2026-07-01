#!/usr/bin/env bash
set -euo pipefail

# ─── Metabase CLI Installer ───────────────────────────────
# Installs @ruminaider/metabase-cli globally via npm.
# Full read/write access to a Metabase instance from the terminal.
# ──────────────────────────────────────────────────────────

info()  { echo "  → $*"; }
ok()    { echo "  ✓ $*"; }
fail()  { echo "  ✗ $*" >&2; exit 1; }

echo ""
echo "  Metabase CLI Installer"
echo "  ──────────────────────"
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
  npm install --silent 2>/dev/null || true
  npm link --silent 2>/dev/null && ok "Linked as 'metabase-cli'" || fail "npm link failed. Try: sudo npm link"
else
  info "Installing @ruminaider/metabase-cli from npm..."
  npm install -g @ruminaider/metabase-cli 2>&1 | tail -1
  ok "Installed from npm"
fi

if command -v metabase-cli &>/dev/null; then
  ok "metabase-cli is ready"
else
  info "metabase-cli installed but may need a shell restart"
fi

echo ""
echo "  ──────────────────────"
echo "  ✓ Installed!"
echo ""
echo "  Authenticate (API key recommended):"
echo "    metabase-cli auth login --url https://your-instance.metabaseapp.com --api-key <key>"
echo ""
echo "  Then use:"
echo "    metabase-cli database list"
echo "    metabase-cli query run <db-id> \"select 1\""
echo ""
