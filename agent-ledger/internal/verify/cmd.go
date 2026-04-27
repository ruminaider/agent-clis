package verify

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
)

type verifyOpts struct {
	asJSON        bool
	taskID        string
	summaryFile   string
	ledgerDirFlag string
	projectIDFlag string
}

// NewVerifyCommand builds the `verify` cobra command. It is registered
// from cmd/agent-ledger/main.go (Phase 1 wave 3) replacing the Wave-1
// stub.
func NewVerifyCommand(streams cli.IOStreams) *cobra.Command {
	o := &verifyOpts{}
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Produce the agent-ledger.verify.v1 contract",
		Long: `verify produces the agent-ledger.verify.v1 JSON contract.

With --task it scopes verification to one task, validating assignment,
claims, recorded changes, conflicts, and intent state.

With --summary it validates a committed task summary file against the
current working tree (path hashes and assignment hash). This mode does
NOT require local XDG ledger state and is suitable for CI on a clean
checkout.

With neither flag it inspects the project working tree against active
intents and recently recorded changes.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if v, err := cmd.Root().PersistentFlags().GetBool("json"); err == nil && v {
				o.asJSON = true
			}
			return runVerify(cmd.Context(), streams, o)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.asJSON, "json", false, "Render the verify report as JSON")
	f.StringVar(&o.taskID, "task", "", "Verify only the given task ID")
	f.StringVar(&o.summaryFile, "summary", "", "Verify a committed summary file (clean checkout, no XDG state required)")
	f.StringVar(&o.ledgerDirFlag, "ledger-dir", "", "Override ledger directory")
	f.StringVar(&o.projectIDFlag, "project-id", "", "Override project ID")
	return cmd
}

func runVerify(ctx context.Context, streams cli.IOStreams, o *verifyOpts) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if o.taskID != "" && o.summaryFile != "" {
		return cli.NewError(2, "verify_usage", "--task and --summary are mutually exclusive")
	}
	rep, err := Run(ctx, Inputs{
		LedgerDirFlag: o.ledgerDirFlag,
		ProjectIDFlag: o.projectIDFlag,
		TaskID:        strings.TrimSpace(o.taskID),
		SummaryFile:   strings.TrimSpace(o.summaryFile),
	})
	if err != nil {
		return cli.NewError(1, "verify_failed", err.Error())
	}
	// Render. Errors writing the report cannot be remediated here; the
	// process still exits with the verify-specific code.
	if o.asJSON {
		_ = rep.WriteJSON(streams.Out)
	} else {
		_ = rep.WriteText(streams.Out)
	}

	code := rep.ExitCode()
	if code == 0 {
		return nil
	}
	// Surface a sentinel cli.Error so the orchestrator translates to
	// the matching process exit code. The Error envelope is suppressed
	// (we already wrote our own report); we only need the exit code.
	return cli.NewError(code, statusCode(rep.Status), "")
}

func statusCode(status string) string {
	switch status {
	case StatusPassed:
		return "verify_passed"
	case StatusFailed:
		return "verify_failed"
	case StatusError:
		return "verify_error"
	case StatusNeedsDecision:
		return "verify_needs_decision"
	}
	return "verify_failed"
}
