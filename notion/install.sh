#!/usr/bin/env bash
set -euo pipefail

# ─── Notion CLI Installer ─────────────────────────────────
# Installs @ruminaider/notion-cli globally via npm.
# Full workspace access via Notion's MCP OAuth.
# No admin setup, no integration creation, no page sharing.
# ──────────────────────────────────────────────────────────

info()  { echo "  → $*"; }
ok()    { echo "  ✓ $*"; }
fail()  { echo "  ✗ $*" >&2; exit 1; }

echo ""
echo "  Notion CLI Installer"
echo "  ────────────────────"
echo ""

# ─── Check Node.js ────────────────────────────────────────

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

# ─── Install via npm ──────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ -d "$SCRIPT_DIR/cli/bin" ] && [ -f "$SCRIPT_DIR/cli/package.json" ]; then
  # Installing from local repo clone
  info "Installing from local source..."
  cd "$SCRIPT_DIR/cli"
  npm install --silent 2>/dev/null
  npm link --silent 2>/dev/null && ok "Linked as 'notion-cli'" || {
    fail "npm link failed. Try: sudo npm link"
  }
else
  # Installing from npm registry
  info "Installing @ruminaider/notion-cli from npm..."
  npm install -g @ruminaider/notion-cli 2>&1 | tail -1
  ok "Installed from npm"
fi

# ─── Verify ───────────────────────────────────────────────

if command -v notion-cli &>/dev/null; then
  ok "notion-cli is ready"
else
  info "notion-cli installed but may need a shell restart"
fi

echo ""
echo "  ────────────────────"
echo "  ✓ Installed!"
echo ""
echo "  Authenticate:"
echo "    notion-cli auth"
echo ""
echo "  Then use:"
echo "    notion-cli search \"my project\""
echo "    notion-cli fetch <page-url>"
echo ""
