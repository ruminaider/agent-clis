package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
)

type heartbeatOpts struct {
	env       envOpener
	intent    string
	agent     string
	expiresIn time.Duration
	asJSON    bool
}

// NewHeartbeatCommand implements SPEC §18.5.
func NewHeartbeatCommand(streams Streams) *cobra.Command {
	o := &heartbeatOpts{env: envOpener{streams: streams}, expiresIn: 2 * time.Minute}
	cmd := &cobra.Command{
		Use:           "heartbeat",
		Short:         "Renew an active intent and extend its expiration",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runHeartbeat(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.intent, "intent", "", "Intent ID (required)")
	f.StringVar(&o.agent, "agent", "", "Override AGENT_ID env for the heartbeat")
	f.DurationVar(&o.expiresIn, "expires-in", 2*time.Minute, "Heartbeat expiration window")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runHeartbeat(streams Streams, o *heartbeatOpts) error {
	if strings.TrimSpace(o.intent) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--intent is required")
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
	now := store.Clock()()
	expires := now.Add(o.expiresIn)
	if err := d.Heartbeat(ctx, o.intent, agentID, now, expires); err != nil {
		return cli.NewError(cli.ExitStale, "heartbeat_failed", err.Error())
	}
	if o.asJSON {
		return printJSON(streams.Out, map[string]any{
			"intent_id":            o.intent,
			"heartbeat_expires_at": expires.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		})
	}
	fmt.Fprintf(streams.Out, "intent_id=%s expires_at=%s\n", o.intent, expires.UTC().Format(time.RFC3339))
	return nil
}
