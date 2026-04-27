// Package version exposes the agent-ledger build version.
package version

// Version is the semantic version of the agent-ledger binary.
//
// It is overridable at link time:
//
//	go build -ldflags "-X github.com/ruminaider/agent-clis/agent-ledger/internal/version.Version=v0.1.0"
var Version = "0.0.0-dev"
