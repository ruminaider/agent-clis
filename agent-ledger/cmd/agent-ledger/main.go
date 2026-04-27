// Command agent-ledger is the local coordination kernel CLI.
//
// See SPEC.md for the full design. cmd/agent-ledger wires the
// Wave-1 stubs, Wave-2 real handlers, and the Wave-3 verify command
// into a single root cobra command and runs it.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/commands"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/verify"
)

func main() {
	os.Exit(run(cli.DefaultIOStreams(), os.Args[1:]))
}

func run(streams cli.IOStreams, args []string) int {
	root := cli.NewRootCommand(streams)
	commands.Register(root, streams)

	// Replace the Wave-1 verify stub with the Wave-3 implementation.
	for _, child := range root.Commands() {
		if child.Name() == "verify" {
			root.RemoveCommand(child)
			break
		}
	}
	root.AddCommand(verify.NewVerifyCommand(streams))

	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
			break
		}
	}
	root.SetArgs(args)

	if err := root.Execute(); err != nil {
		var cliErr *cli.Error
		if errors.As(err, &cliErr) {
			// Verify writes its own report on stdout; when it returns
			// a *cli.Error with an empty Message, suppress the
			// envelope and only honor the exit code.
			if cliErr.Message != "" {
				if asJSON {
					_ = cliErr.WriteJSON(streams.Err)
				} else {
					_ = cliErr.WriteText(streams.Err)
				}
			}
			return cliErr.ExitCode
		}
		fmt.Fprintln(streams.Err, err.Error())
		return cli.ExitGeneric
	}
	return cli.ExitOK
}
