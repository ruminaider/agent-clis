// Package version exposes the agent-ledger build version metadata.
//
// All three vars are overridable at link time. The Makefile and
// .goreleaser.yaml inject them from `git describe`, `git rev-parse`,
// and an ISO 8601 UTC build date.
//
//	go build -ldflags "\
//	  -X github.com/ruminaider/agent-clis/agent-ledger/internal/version.Version=v0.1.0 \
//	  -X github.com/ruminaider/agent-clis/agent-ledger/internal/version.Commit=abc1234 \
//	  -X github.com/ruminaider/agent-clis/agent-ledger/internal/version.BuildDate=2026-04-27T00:00:00Z"
package version

// Version is the semantic version of the agent-ledger binary.
// Falls back to "0.0.0-dev" when not injected.
var Version = "0.0.0-dev"

// Commit is the short git commit SHA the binary was built from.
// Falls back to "unknown" when not injected.
var Commit = "unknown"

// BuildDate is the ISO 8601 UTC timestamp of the build.
// Falls back to "unknown" when not injected.
var BuildDate = "unknown"

// String returns the canonical --version output line, without trailing newline.
//
// Format: "agent-ledger version <Version> commit <Commit> built <BuildDate>".
func String() string {
	return "agent-ledger version " + Version + " commit " + Commit + " built " + BuildDate
}
