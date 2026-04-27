// Command agent-ledger is the local coordination kernel CLI.
//
// See SPEC.md for the full design. cmd/agent-ledger wires Wave-1 and
// Wave-2 commands together; later waves will continue to layer in
// real handlers in place of internal/cli stubs.
package main

import (
	"os"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/commands"
)

func main() {
	streams := cli.DefaultIOStreams()
	os.Exit(commands.Execute(streams, os.Args[1:]))
}
