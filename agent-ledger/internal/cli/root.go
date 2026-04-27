package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/version"
)

// IOStreams bundles the standard input, output, and error streams a command
// should use. Tests inject buffers; main wires os.Stdin/Stdout/Stderr.
type IOStreams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// DefaultIOStreams returns streams bound to os.Stdin/Stdout/Stderr.
func DefaultIOStreams() IOStreams {
	return IOStreams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
}

// rootFlags holds global flags shared by every subcommand.
type rootFlags struct {
	JSON bool
}

// NewRootCommand builds the agent-ledger root command and registers every
// Phase 1 subcommand. Streams must be non-nil.
func NewRootCommand(streams IOStreams) *cobra.Command {
	flags := &rootFlags{}

	root := &cobra.Command{
		Use:           "agent-ledger",
		Short:         "Local coordination kernel for agentic coding workflows",
		Long:          "agent-ledger records assignments, intents, and changes so multiple coding agents and humans can coordinate edits without surprising each other.",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.SetIn(streams.In)
	root.SetOut(streams.Out)
	root.SetErr(streams.Err)

	root.PersistentFlags().BoolVar(&flags.JSON, "json", false, "Render output as JSON where supported")

	// Custom version template: just the version string plus newline.
	root.SetVersionTemplate("{{.Version}}\n")

	realCommands := map[string]func(IOStreams, *rootFlags) *cobra.Command{
		"init":   newInitCommand,
		"doctor": newDoctorCommand,
	}
	for _, name := range Phase1Commands() {
		if build, ok := realCommands[name]; ok {
			root.AddCommand(build(streams, flags))
			continue
		}
		root.AddCommand(newStubCommand(name, flags))
	}

	return root
}

// Phase1Commands returns the ordered list of Phase 1 kernel-slice subcommand
// names. The order matches SPEC §18.17.
func Phase1Commands() []string {
	return []string{
		"init",
		"identify",
		"assign",
		"claim",
		"heartbeat",
		"record",
		"close",
		"status",
		"verify",
		"conflicts",
		"adopt",
		"export-summary",
		"gc",
		"migrate",
		"doctor",
	}
}

// commandShortDescriptions maps each Phase 1 command to its --help one-liner.
func commandShortDescriptions() map[string]string {
	return map[string]string{
		"init":           "Initialize ledger storage and optional pointer file",
		"identify":       "Create or print an agent session identity",
		"assign":         "Record an orchestrator assignment for a task",
		"claim":          "Open a worker intent over one or more paths",
		"heartbeat":      "Renew an active intent and extend its expiration",
		"record":         "Record a change made under an open intent",
		"close":          "Close an intent with an outcome",
		"status":         "Show active claims, recent changes, and conflicts",
		"verify":         "Produce the agent-ledger.verify.v1 contract",
		"conflicts":      "List or acknowledge coordination conflicts",
		"adopt":          "Retroactively adopt an unclaimed change",
		"export-summary": "Export a privacy-safe task summary for CI",
		"gc":             "Mark stale active intents as orphaned",
		"migrate":        "Apply schema migrations",
		"doctor":         "Run environment and storage diagnostics",
	}
}

// newStubCommand builds a placeholder cobra command that returns
// ExitNotImplemented when run. Subsequent tasks replace these stubs.
func newStubCommand(name string, flags *rootFlags) *cobra.Command {
	short := commandShortDescriptions()[name]
	cmd := &cobra.Command{
		Use:   name,
		Short: short,
		Long:  fmt.Sprintf("%s\n\nThis command is part of the Phase 1 kernel slice and is not implemented yet.", short),
		RunE: func(cmd *cobra.Command, args []string) error {
			return NotImplemented(name)
		},
	}
	// Quiet usage on stub errors; the runner renders the envelope.
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	_ = flags // reserved for future global flag plumbing
	return cmd
}

// Execute runs the root command and translates any returned *Error into the
// process exit code. Non-Error failures map to ExitGeneric.
func Execute(streams IOStreams, args []string) int {
	root := NewRootCommand(streams)
	root.SetArgs(args)

	flags := struct{ json bool }{}
	// Parse the --json flag eagerly so error rendering can honor it. Cobra
	// will parse it again during normal execution; that is harmless.
	for _, a := range args {
		if a == "--json" {
			flags.json = true
			break
		}
	}

	if err := root.Execute(); err != nil {
		return renderError(streams.Err, err, flags.json)
	}
	return ExitOK
}

// renderError writes the error envelope and returns the exit code.
func renderError(w io.Writer, err error, asJSON bool) int {
	var cliErr *Error
	if errors.As(err, &cliErr) {
		if asJSON {
			_ = cliErr.WriteJSON(w)
		} else {
			_ = cliErr.WriteText(w)
		}
		return cliErr.ExitCode
	}
	// Unknown error: wrap as generic.
	wrapped := NewError(ExitGeneric, "unknown_error", err.Error())
	if asJSON {
		_ = wrapped.WriteJSON(w)
	} else {
		_ = wrapped.WriteText(w)
	}
	return wrapped.ExitCode
}
