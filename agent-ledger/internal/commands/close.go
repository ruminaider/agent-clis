package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/locks"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
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
	// Capture intent metadata before close so we can clean up lock
	// sentinels for exclusive intents after the DB transition succeeds.
	// Failure to load the intent here is non-fatal: close() below will
	// surface the real error if the intent is missing or already closed.
	var (
		priorPolicy string
		priorPaths  []domain.IntentPath
	)
	if intent, ierr := d.IntentByID(ctx, o.intent); ierr == nil {
		priorPolicy = intent.ConflictPolicy
		if priorPolicy == domain.PolicyExclusive {
			if ps, perr := d.IntentPaths(ctx, intent.IntentID); perr == nil {
				priorPaths = ps
			}
		}
	}
	if err := d.Close(ctx, o.intent, agentID, o.outcome, o.summary, store.Clock()()); err != nil {
		return cli.NewError(cli.ExitGeneric, "close_failed", err.Error())
	}
	// Best-effort sentinel cleanup. Errors are swallowed; the verify
	// EXCLUSIVE_LOCK_HELD scan remains the authoritative reporter of
	// any sentinel that survives.
	cleanupExclusiveSentinels(ctx, store, priorPolicy, priorPaths)
	if o.asJSON {
		return printJSON(streams.Out, map[string]any{
			"intent_id":     o.intent,
			"close_outcome": o.outcome,
		})
	}
	fmt.Fprintf(streams.Out, "intent_id=%s outcome=%s\n", o.intent, o.outcome)
	return nil
}

// cleanupExclusiveSentinels removes <ledger-dir>/locks/<hash>.lock for
// every path of an exclusive intent that has just transitioned out of
// active. The call is best-effort: any error is dropped on the floor
// to honor SPEC §28's "DB row is authoritative" rule.
//
// ctx is reserved for future telemetry. The signature accepts it so
// callers can wire structured logs without churning this surface.
func cleanupExclusiveSentinels(ctx context.Context, store *sqlite.Store, policy string, paths []domain.IntentPath) {
	_ = ctx
	if policy != domain.PolicyExclusive || len(paths) == 0 {
		return
	}
	dir := storage.Layout{Dir: store.LedgerDir()}.LocksDir()
	for _, p := range paths {
		_ = locks.RemoveSentinel(dir, p.PathHash)
	}
}
