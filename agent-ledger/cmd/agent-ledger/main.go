// Command agent-ledger is the local coordination kernel CLI.
//
// See SPEC.md for the full design. This binary is the Phase 1 kernel-slice
// scaffold: every subcommand is registered, but most still return exit code
// 3 (not implemented).
package main

import (
	"os"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
)

func main() {
	os.Exit(cli.Execute(cli.DefaultIOStreams(), os.Args[1:]))
}
