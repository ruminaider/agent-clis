package commands

import (
	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
)

// Register replaces the Wave-1 stub commands on root with the real
// Wave-2 implementations from this package. Stubs that are still
// owned by later tasks (record, verify, adopt, export-summary, gc)
// remain unchanged.
func Register(root *cobra.Command, streams Streams) {
	replacements := map[string]func(Streams) *cobra.Command{
		"identify":  NewIdentifyCommand,
		"assign":    NewAssignCommand,
		"claim":     NewClaimCommand,
		"heartbeat": NewHeartbeatCommand,
		"close":     NewCloseCommand,
		"status":    NewStatusCommand,
		"conflicts": NewConflictsCommand,
	}
	for _, child := range root.Commands() {
		if build, ok := replacements[child.Name()]; ok {
			root.RemoveCommand(child)
			root.AddCommand(build(streams))
		}
	}
}

// Execute is the same entrypoint as cli.Execute but registers Wave-2
// commands first. It mirrors cli.Execute's error-rendering logic so
// command behavior is identical.
func Execute(streams Streams, args []string) int {
	root := cli.NewRootCommand(streams)
	Register(root, streams)
	root.SetArgs(args)

	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
			break
		}
	}
	if err := root.Execute(); err != nil {
		var cliErr *cli.Error
		if asError(err, &cliErr) {
			if asJSON {
				_ = cliErr.WriteJSON(streams.Err)
			} else {
				_ = cliErr.WriteText(streams.Err)
			}
			return cliErr.ExitCode
		}
		wrapped := cli.NewError(cli.ExitGeneric, "unknown_error", err.Error())
		if asJSON {
			_ = wrapped.WriteJSON(streams.Err)
		} else {
			_ = wrapped.WriteText(streams.Err)
		}
		return wrapped.ExitCode
	}
	return cli.ExitOK
}

func asError(err error, target **cli.Error) bool {
	if err == nil {
		return false
	}
	for {
		if e, ok := err.(*cli.Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
		if err == nil {
			return false
		}
	}
}
