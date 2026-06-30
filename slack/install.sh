#!/usr/bin/env bash
set -euo pipefail

# ─── Slack CLI Installer ──────────────────────────────────
# Installs @ruminaider/slack-cli globally via npm.
# Authenticates as you using your existing Slack desktop session.
# No Slack app, no OAuth consent screen, no admin approval.
# ──────────────────────────────────────────────────────────

info()  { echo "  → $*"; }
ok()    { echo "  ✓ $*"; }
fail()  { echo "  ✗ $*" >&2; exit 1; }

echo ""
echo "  Slack CLI Installer"
echo "  ───────────────────"
echo ""

# ─── Check Node.js ────────────────────────────────────────

if ! command -v node &>/dev/null; then
  fail "Node.js 22+ is required. Install from https://nodejs.org"
fi

NODE_VERSION=$(node -v | sed 's/v//' | cut -d. -f1)
if [ "$NODE_VERSION" -lt 22 ]; then
  fail "Node.js 22+ required (found $(node -v)); uses the built-in node:sqlite"
fi
ok "Node.js $(node -v)"

if ! command -v npm &>/dev/null; then
  fail "npm is required"
fi
ok "npm $(npm -v)"

# ─── Install ──────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ -d "$SCRIPT_DIR/cli/bin" ] && [ -f "$SCRIPT_DIR/cli/package.json" ]; then
  info "Installing from local source..."
  cd "$SCRIPT_DIR/cli"
  npm install --silent 2>/dev/null || true
  npm link --silent 2>/dev/null && ok "Linked as 'slack-cli'" || {
    fail "npm link failed. Try: sudo npm link"
  }
else
  info "Installing @ruminaider/slack-cli from npm..."
  npm install -g @ruminaider/slack-cli 2>&1 | tail -1
  ok "Installed from npm"
fi

# ─── Verify ───────────────────────────────────────────────

if command -v slack-cli &>/dev/null; then
  ok "slack-cli is ready"
else
  info "slack-cli installed but may need a shell restart"
fi

echo ""
echo "  ───────────────────"
echo "  ✓ Installed!"
echo ""
echo "  Authenticate (uses your Slack desktop session):"
echo "    slack-cli auth login"
echo ""
echo "  Then use:"
echo "    slack-cli channel list"
echo "    slack-cli message send '#general' \"Hello from the terminal\""
echo ""
