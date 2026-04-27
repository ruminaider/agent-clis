package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
)

type closeOpts struct {
	env     envOpener
	intent  string
	outcome string
	summary string
	agent   string
	asJSON  bool
}

// NewCloseCommand implements SPEC §18.7.
func NewCloseCommand(streams Streams) *cobra.Command {
	o := &closeOpts{env: envOpener{streams: streams}, outcome: domain.OutcomeCompleted}
	cmd := &cobra.Command{
		Use:           "close",
		Short:         "Close an intent with an outcome",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runClose(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.intent, "intent", "", "Intent ID (required)")
	f.StringVar(&o.outcome, "outcome", domain.OutcomeCompleted, "Close outcome (completed|abandoned|superseded)")
	f.StringVar(&o.summary, "summary", "", "Optional summary text (stored as hash only)")
	f.StringVar(&o.agent, "agent", "", "Override AGENT_ID env for the close event")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runClose(streams Streams, o *closeOpts) error {
	if strings.TrimSpace(o.intent) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--intent is required")
	}
	if !domain.ValidOutcome(o.outcome) {
		return errf(cli.ExitUsage, "invalid_outcome", "--outcome must be completed|abandoned|superseded")
	}
	agentID := pickAgentID(o.agent)
	if agentID == "" {
		agentID = "unknown"
	}
	ctx := ctxFor(streams)
	store, _, err := o.env.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	d := domain.New(store)
	if err := d.Close(ctx, o.intent, agentID, o.outcome, o.summary, store.Clock()()); err != nil {
		return cli.NewError(cli.ExitGeneric, "close_failed", err.Error())
	}
	if o.asJSON {
		return printJSON(streams.Out, map[string]any{
			"intent_id":     o.intent,
			"close_outcome": o.outcome,
		})
	}
	fmt.Fprintf(streams.Out, "intent_id=%s outcome=%s\n", o.intent, o.outcome)
	return nil
}
