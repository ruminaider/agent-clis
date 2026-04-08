#!/usr/bin/env bash
set -euo pipefail

# ─── Notion CLI Installer ─────────────────────────────────
# Full workspace access via Notion's MCP OAuth.
# No admin setup, no integration creation, no page sharing.
# ──────────────────────────────────────────────────────────

INSTALL_DIR="${NOTION_CLI_DIR:-$HOME/.agent-clis/notion}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

info()  { echo "  → $*"; }
ok()    { echo "  ✓ $*"; }
fail()  { echo "  ✗ $*" >&2; exit 1; }

echo ""
echo "  Notion CLI Installer"
echo "  ────────────────────"
echo ""

# ─── Check Node.js ────────────────────────────────────────

if ! command -v node &>/dev/null; then
  fail "Node.js is required. Install from https://nodejs.org"
fi

NODE_VERSION=$(node -v | sed 's/v//' | cut -d. -f1)
if [ "$NODE_VERSION" -lt 18 ]; then
  fail "Node.js 18+ required (found $(node -v))"
fi
ok "Node.js $(node -v)"

# ─── Install ──────────────────────────────────────────────

if [ -d "$INSTALL_DIR" ]; then
  info "Updating existing installation"
else
  info "Installing to $INSTALL_DIR"
fi

mkdir -p "$INSTALL_DIR"

# If running from repo, copy source files
if [ -d "$SCRIPT_DIR/cli" ]; then
  cp -r "$SCRIPT_DIR/cli/"* "$INSTALL_DIR/"
  ok "Source files copied"
else
  fail "Cannot find cli/ directory. Run from the repo root or use the standalone installer."
fi

# Install dependencies
info "Installing dependencies..."
cd "$INSTALL_DIR"
npm install --silent 2>/dev/null
ok "Dependencies installed"

# Link globally
info "Linking notion-cli command..."
npm link --silent 2>/dev/null && ok "Linked as 'notion-cli'" || {
  SHELL_RC=""
  [ -f "$HOME/.zshrc" ] && SHELL_RC="$HOME/.zshrc"
  [ -z "$SHELL_RC" ] && [ -f "$HOME/.bashrc" ] && SHELL_RC="$HOME/.bashrc"

  if [ -n "$SHELL_RC" ]; then
    if ! grep -q "agent-clis/notion" "$SHELL_RC" 2>/dev/null; then
      echo "export PATH=\"$INSTALL_DIR/bin:\$PATH\"" >> "$SHELL_RC"
      info "Added to PATH in $SHELL_RC — restart shell or: source $SHELL_RC"
    fi
  fi
  ok "Installed (may need to restart shell)"
}

echo ""
echo "  ────────────────────"
echo "  ✓ Installed!"
echo ""
echo "  Authenticate:"
echo "    notion-cli auth login"
echo ""
echo "  Then use:"
echo "    notion-cli search \"my project\""
echo "    notion-cli fetch <page-url>"
echo ""
