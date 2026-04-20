#!/usr/bin/env bash
set -euo pipefail

# ─── Linear CLI Installer ────────────────────────────────
# Installs @ruminaider/linear-cli globally via npm.
# Current MVP: auth, MCP discovery, project reads, and comment reads.
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
  fail "Installing from npm is not ready yet. Clone the repo and run this installer from local source."
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
echo "  Current MVP commands:"
echo "    linear-cli auth login"
echo "    linear-cli auth logout"
echo "    linear-cli auth status"
echo "    linear-cli mcp discover"
echo "    linear-cli project list"
echo "    linear-cli project get --project-id <project-id>"
echo "    linear-cli comment list --issue-id <issue-id>"
echo ""
echo "  Auth modes:"
echo "    linear-cli auth login"
echo "    linear-cli auth login --api-key <key>"
echo "    linear-cli auth status --api-key <key>"
echo ""
echo "  Persistence:"
echo "    Credentials and explicitly persisted API keys: ~/.config/linear-cli/credentials.json"
echo "    Team and workspace defaults: ~/.config/linear-cli/config.json"
echo ""
