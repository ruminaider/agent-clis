package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
)

type conflictsOpts struct {
	env    envOpener
	task   string
	asJSON bool
}

// NewConflictsCommand implements SPEC §18.10. Returns the parent
// command with `acknowledge` as a subcommand.
func NewConflictsCommand(streams Streams) *cobra.Command {
	o := &conflictsOpts{env: envOpener{streams: streams}}
	cmd := &cobra.Command{
		Use:           "conflicts",
		Short:         "List or acknowledge coordination conflicts",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runConflictsList(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.task, "task", "", "Filter by task ID")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")

	cmd.AddCommand(newConflictsAcknowledgeCommand(streams))
	return cmd
}

func runConflictsList(streams Streams, o *conflictsOpts) error {
	ctx := ctxFor(streams)
	store, _, err := o.env.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	d := domain.New(store)
	confs, err := d.ListConflicts(ctx, o.task, "")
	if err != nil {
		return mapStorageReadError(err, "conflicts_list_failed")
	}
	if o.asJSON {
		out := make([]map[string]any, 0, len(confs))
		for _, c := range confs {
			out = append(out, conflictJSON(c))
		}
		return printJSON(streams.Out, map[string]any{"conflicts": out})
	}
	if len(confs) == 0 {
		fmt.Fprintln(streams.Out, "(no conflicts)")
		return nil
	}
	for _, c := range confs {
		fmt.Fprintf(streams.Out, "%s\t%s\t%s\tpolicy=%s\tpath=%s\n", c.ConflictID, c.Status, c.DetectedAt, c.Policy, c.Path)
	}
	return nil
}

type conflictsAckOpts struct {
	env        envOpener
	conflictID string
	reason     string
	asOverride bool
	agent      string
	asJSON     bool
}

func newConflictsAcknowledgeCommand(streams Streams) *cobra.Command {
	o := &conflictsAckOpts{env: envOpener{streams: streams}}
	cmd := &cobra.Command{
		Use:           "acknowledge [conflict-id]",
		Short:         "Acknowledge a conflict",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			if len(args) == 1 && o.conflictID == "" {
				o.conflictID = args[0]
			}
			return runConflictsAck(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.conflictID, "conflict", "", "Conflict ID (alternative to positional arg)")
	f.StringVar(&o.reason, "reason", "", "Acknowledgement reason")
	f.BoolVar(&o.asOverride, "as-override", false, "Mark resolution as orchestrator override")
	f.StringVar(&o.agent, "agent", "", "Override AGENT_ID env for the ack event")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runConflictsAck(streams Streams, o *conflictsAckOpts) error {
	if strings.TrimSpace(o.conflictID) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "conflict id is required")
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
	if err := d.AcknowledgeConflict(ctx, o.conflictID, agentID, o.reason, o.asOverride, store.Clock()()); err != nil {
		return cli.NewError(cli.ExitGeneric, "ack_failed", err.Error())
	}
	if o.asJSON {
		return printJSON(streams.Out, map[string]any{
			"conflict_id": o.conflictID,
			"status":      "acknowledged",
			"override":    o.asOverride,
		})
	}
	fmt.Fprintf(streams.Out, "conflict_id=%s status=acknowledged override=%t\n", o.conflictID, o.asOverride)
	return nil
}

func conflictJSON(c domain.Conflict) map[string]any {
	out := map[string]any{
		"conflict_id":        c.ConflictID,
		"status":             c.Status,
		"policy":             c.Policy,
		"path":               c.Path,
		"path_hash":          c.PathHash,
		"existing_intent_id": c.ExistingIntentID,
		"new_intent_id":      c.NewIntentID,
		"detected_at":        c.DetectedAt,
	}
	if c.AcknowledgedAt != "" {
		out["acknowledged_at"] = c.AcknowledgedAt
	}
	if c.Resolution != "" {
		out["resolution"] = c.Resolution
	}
	return out
}
