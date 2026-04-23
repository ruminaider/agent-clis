#!/usr/bin/env bash
set -euo pipefail

info()  { echo "  → $*"; }
ok()    { echo "  ✓ $*"; }
fail()  { echo "  ✗ $*" >&2; exit 1; }

echo ""
echo "  CircleCI CLI Installer"
echo "  ──────────────────────"
echo ""

if ! command -v python3 &>/dev/null; then
  fail "python3 is required"
fi
ok "python3 $(python3 --version 2>&1)"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

if [ ! -d .venv ]; then
  info "Creating virtual environment..."
  python3 -m venv .venv
fi

info "Installing package in editable mode..."
.venv/bin/pip install -e .[dev] >/dev/null
ok "Package installed"

mkdir -p "$HOME/.local/bin"
ln -sf "$SCRIPT_DIR/.venv/bin/circleci-cli" "$HOME/.local/bin/circleci-cli"
ok "Linked circleci-cli into ~/.local/bin"

echo ""
echo "  Current scaffold commands:"
echo "    circleci-cli doctor"
echo "    circleci-cli status --target tonic"
echo "    circleci-cli logs --target tonic"
echo "    circleci-cli flaky-tests --target tonic"
echo ""
echo "  Required environment:"
echo "    export CIRCLECI_TOKEN=your-token"
echo "    export PI_CIRCLECI_PROJECT_SLUG=gh/Recora-Health/recora-health-back-end"
echo ""
