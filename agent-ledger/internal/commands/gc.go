package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/gc"
)

type gcOpts struct {
	env        envOpener
	staleAfter string
	dryRun     bool
	asJSON     bool
}

// NewGCCommand implements SPEC §18.13 (gc --stale-after <duration>).
//
// The command finds active intents whose last heartbeat (or opened_at
// when no heartbeat has fired) is older than now - stale-after and
// marks them orphaned, emitting one intent.orphaned event per change.
// It never deletes audit history.
func NewGCCommand(streams Streams) *cobra.Command {
	o := &gcOpts{env: envOpener{streams: streams}}
	cmd := &cobra.Command{
		Use:           "gc",
		Short:         "Mark stale active intents as orphaned",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runGC(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.staleAfter, "stale-after", "", "Inactivity window past which intents are orphaned (e.g. 24h, 30m)")
	f.BoolVar(&o.dryRun, "dry-run", false, "Report stale candidates without marking them orphaned")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runGC(streams Streams, o *gcOpts) error {
	if strings.TrimSpace(o.staleAfter) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--stale-after is required")
	}
	dur, err := gc.ParseStaleAfter(o.staleAfter)
	if err != nil {
		return cli.NewError(cli.ExitUsage, "invalid_duration", err.Error())
	}

	ctx := ctxFor(streams)
	store, _, err := o.env.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()

	if o.dryRun {
		cutoff := store.Clock()().Add(-dur)
		stale, err := store.ListStaleActiveIntents(ctx, cutoff)
		if err != nil {
			return cli.NewError(cli.ExitStorageIO, "list_stale_failed", err.Error())
		}
		ids := make([]string, 0, len(stale))
		for _, si := range stale {
			ids = append(ids, si.IntentID)
		}
		if o.asJSON {
			return printJSON(streams.Out, map[string]any{
				"dry_run":    true,
				"cutoff":     cutoff.UTC(),
				"candidates": ids,
				"count":      len(ids),
			})
		}
		fmt.Fprintf(streams.Out, "dry-run: %d stale intent(s) older than %s\n", len(ids), dur)
		for _, idStr := range ids {
			fmt.Fprintf(streams.Out, "  %s\n", idStr)
		}
		return nil
	}

	res, err := gc.Run(ctx, store, gc.Options{
		StaleAfter: dur,
		Now:        store.Clock(),
		AgentID:    "agent-ledger.gc",
	})
	if err != nil {
		if errors.Is(err, gc.ErrInvalidStaleAfter) {
			return cli.NewError(cli.ExitUsage, "invalid_duration", err.Error())
		}
		return cli.NewError(cli.ExitStorageIO, "gc_failed", err.Error())
	}

	if o.asJSON {
		return printJSON(streams.Out, res)
	}
	fmt.Fprintf(streams.Out, "orphaned %d/%d stale intent(s) (cutoff %s)\n",
		len(res.Orphaned), res.Candidates, res.Cutoff.Format("2006-01-02T15:04:05Z"))
	for _, idStr := range res.Orphaned {
		fmt.Fprintf(streams.Out, "  orphaned %s\n", idStr)
	}
	for _, idStr := range res.Skipped {
		fmt.Fprintf(streams.Out, "  skipped  %s (no longer active)\n", idStr)
	}
	return nil
}
