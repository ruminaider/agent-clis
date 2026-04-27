#!/usr/bin/env bash
# release-snapshot.sh runs a local snapshot release for agent-ledger.
#
# It produces tar.gz archives and a SHA256 checksums file under dist/
# for every supported target (darwin/arm64, darwin/amd64, linux/arm64,
# linux/amd64). Snapshot mode requires no secrets and does not publish.
#
# Install goreleaser if it is not on $PATH:
#
#   go install github.com/goreleaser/goreleaser/v2@latest
#
# or via Homebrew:
#
#   brew install goreleaser
#
# Then run:
#
#   make release-snapshot
#
# or invoke this script directly from the repo root.

set -euo pipefail

cd "$(dirname "$0")/.."

if ! command -v goreleaser >/dev/null 2>&1; then
  if [ -x "$(go env GOPATH)/bin/goreleaser" ]; then
    GORELEASER="$(go env GOPATH)/bin/goreleaser"
  else
    echo "goreleaser not found on PATH." >&2
    echo "Install with: go install github.com/goreleaser/goreleaser/v2@latest" >&2
    echo "or: brew install goreleaser" >&2
    exit 1
  fi
else
  GORELEASER="goreleaser"
fi

echo "Using $GORELEASER ($("$GORELEASER" --version | head -1 || true))"

# --snapshot avoids requiring a clean tag. --clean wipes any prior dist/.
# --skip publish guarantees no network publishing even if the config grew
# a release target. This script is intentionally redundant on safety.
exec "$GORELEASER" release --snapshot --clean --skip=publish
