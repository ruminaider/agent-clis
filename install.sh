#!/usr/bin/env bash
set -euo pipefail

# ─── Agent Tool Shed — Master Installer ───────────────────
# Installs all available CLI tools.
# ──────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo ""
echo "  🏚️  Agent Tool Shed"
echo "  ─────────────────────"
echo "  CLI tools that replace MCP servers"
echo ""

TOOLS=()

# Discover available tools (directories with install.sh)
for tool_dir in "$SCRIPT_DIR"/*/; do
  tool_name=$(basename "$tool_dir")
  if [ -f "$tool_dir/install.sh" ] && [ "$tool_name" != "node_modules" ]; then
    TOOLS+=("$tool_name")
  fi
done

if [ ${#TOOLS[@]} -eq 0 ]; then
  echo "  No tools found to install."
  exit 1
fi

echo "  Tools to install: ${TOOLS[*]}"
echo ""

FAILED=()
INSTALLED=()

for tool in "${TOOLS[@]}"; do
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  if bash "$SCRIPT_DIR/$tool/install.sh"; then
    INSTALLED+=("$tool")
  else
    FAILED+=("$tool")
  fi
done

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "  Summary"
echo "  ───────"

if [ ${#INSTALLED[@]} -gt 0 ]; then
  echo "  ✓ Installed: ${INSTALLED[*]}"
fi
if [ ${#FAILED[@]} -gt 0 ]; then
  echo "  ✗ Failed: ${FAILED[*]}"
fi

echo ""
echo "  Next: authenticate each tool"
for tool in "${INSTALLED[@]}"; do
  echo "    ${tool}-cli auth login"
done
echo ""
