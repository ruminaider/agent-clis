package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/privacy"
)

type assignUpdateOpts struct {
	env          envOpener
	task         string
	agent        string
	orchestrator string
	addAllow     []string
	reason       string
	metadata     string
	asJSON       bool
}

// NewAssignUpdateCommand implements `agent-ledger assign update`.
//
// The MVP is allow-list extension only: it accepts --add-allow globs
// and merges them into the active assignment for the requested
// (task, agent) pair. Adding forbid globs, removing globs, replacing
// the full path lists, or changing the conflict policy is intentionally
// out of scope. See SPEC §18.3 for the surface contract and the
// rationale: any change that narrows what an in-flight intent may
// write (including a new forbid that overlaps an already-claimed path)
// can leave `record` accepting writes that `verify` later rejects,
// because record validates against intent path hashes, not current
// assignment scope. Close and re-`assign` for those cases.
//
// On success with at least one new glob: the prior active row is
// superseded (status='superseded', closed_at set, metadata.superseded_by
// pointing at the new row), a fresh active row is inserted with the
// merged paths and metadata.superseded_assignment_id pointing back,
// and two events are emitted (assignment.superseded, task.assigned).
// On a no-op (every supplied glob already present): exit 0 with
// changed=false, reused=true; no row is inserted, no event is written.
func NewAssignUpdateCommand(streams Streams) *cobra.Command {
	o := &assignUpdateOpts{env: envOpener{streams: streams}}
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Extend an active assignment's allow-list (allow-only, additive)",
		Long: `Extend an active assignment's allow-list with one or more globs.

The MVP is allow-list extension only: --add-allow merges new globs
into the active assignment for the requested (task, agent) pair.
Adding forbid globs, removing globs, replacing the full path lists,
or changing the conflict policy is out of scope; close and re-assign
for those cases. The restriction is deliberate: any change that
narrows what an in-flight intent may write (including a new forbid
that overlaps an already-claimed path) can let record accept writes
that verify later rejects.

Idempotent: rerunning with the same --add-allow values after a
successful update returns changed=false, reused=true. Globs are merged
as raw strings; near-equivalents like "src/*" and "src/**" are treated
as distinct.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runAssignUpdate(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.task, "task", "", "Task ID (required)")
	f.StringVar(&o.agent, "agent", "", "Assigned worker agent ID (required, may be empty string for unassigned)")
	f.StringVar(&o.orchestrator, "orchestrator", "", "Override orchestrator agent ID for the new assignment row (defaults to the prior row's orchestrator)")
	f.StringArrayVar(&o.addAllow, "add-allow", nil, "Glob to append to allowed_paths; repeatable; at least one is required")
	f.StringVar(&o.reason, "reason", "", "Reason for extending the assignment (required)")
	f.StringVar(&o.metadata, "metadata", "", "Optional structured metadata as a JSON object (merged into the new assignment's metadata_json)")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runAssignUpdate(streams Streams, o *assignUpdateOpts) error {
	if strings.TrimSpace(o.task) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--task is required")
	}
	if strings.TrimSpace(o.reason) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--reason is required")
	}
	if len(o.addAllow) == 0 {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--add-allow is required (at least one glob)")
	}
	if err := privacy.AssertSafe("--reason", o.reason); err != nil {
		return cli.NewError(cli.ExitConfigError, "reason_unsafe", err.Error())
	}
	extraMetadata, err := parseMetadataFlag(o.metadata)
	if err != nil {
		return err
	}

	ctx := ctxFor(streams)
	store, _, err := o.env.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	d := domain.New(store)

	res, err := d.SupersedeAndInsertAssignment(ctx, domain.AssignmentUpdateInput{
		TaskID:          o.task,
		AssignedAgentID: o.agent,
		OrchestratorID:  o.orchestrator,
		AddAllowedPaths: o.addAllow,
		Reason:          o.reason,
		ExtraMetadata:   extraMetadata,
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrUnsafeReason):
			return cli.NewError(cli.ExitConfigError, "reason_unsafe", err.Error())
		case errors.Is(err, domain.ErrNoActiveAssignment):
			return cli.NewError(cli.ExitConflict, "no_active_assignment",
				fmt.Sprintf("no active assignment for task %q and agent %q; run `agent-ledger assign` first", o.task, o.agent))
		case errors.Is(err, domain.ErrStaleUpdate), errors.Is(err, domain.ErrAssignmentExists):
			// Both sentinels reduce to the same user-facing contract:
			// a concurrent writer rotated the active row, rerun the
			// command to merge against the new row. ErrStaleUpdate
			// fires when the SELECT-then-UPDATE inside the immediate
			// transaction sees zero affected rows; ErrAssignmentExists
			// fires when the partial unique index catches a duplicate
			// at INSERT time. The implementation distinction is not a
			// public contract; SPEC §18.3.1 documents one code.
			return cli.NewError(cli.ExitConflict, "assignment_stale_update",
				"the active assignment was superseded by a concurrent writer; rerun the command to merge against the new row")
		}
		return cli.NewError(cli.ExitStorageIO, "assign_update_failed", err.Error())
	}

	if o.asJSON {
		return printJSON(streams.Out, map[string]any{
			"task_id":             res.Assignment.TaskID,
			"assignment_id":       res.Assignment.AssignmentID,
			"prior_assignment_id": res.PriorAssignmentID,
			"changed":             !res.Reused,
			"reused":              res.Reused,
			"allowed_paths":       res.Assignment.AllowedPaths,
			"forbidden_paths":     res.Assignment.ForbiddenPaths,
		})
	}
	if res.Reused {
		fmt.Fprintf(streams.Out, "assignment_id=%s task=%s changed=false reused=true\n",
			res.Assignment.AssignmentID, res.Assignment.TaskID)
		return nil
	}
	fmt.Fprintf(streams.Out, "assignment_id=%s task=%s changed=true reused=false superseded=%s\n",
		res.Assignment.AssignmentID, res.Assignment.TaskID, res.PriorAssignmentID)
	return nil
}
