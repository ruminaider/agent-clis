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

type assignCloseOpts struct {
	env        envOpener
	assignment string
	outcome    string
	reason     string
	agent      string
	asJSON     bool
}

// NewAssignCloseCommand implements `agent-ledger assign close`.
//
// Terminal closure of an assignment without inserting a replacement
// row. SPEC §11.3 reserves this transition for the case where the
// orchestrator wants to record scope-contract closure (completed or
// abandoned) directly, rather than relying on natural pruning by
// agent-ledger gc once the underlying intents have completed.
//
// The command emits `assignment.closed` and transitions the row's
// status to the supplied outcome. Active intents that reference the
// closed assignment are left untouched: worker-side intent lifecycle
// is independent of orchestrator-side assignment lifecycle. Closing
// an assignment frees its slot in the partial unique index on
// (task_id, assigned_agent_id) WHERE status='active', so the next
// `assign` for the same pair succeeds without an intervening
// `assign update`.
//
// The outcome `superseded` is intentionally rejected here. Supersede
// transitions are owned by `assign update`, which inserts the
// replacement row in the same transaction; emitting `assignment.closed`
// with outcome=superseded would break the SPEC §11.3.1 chain walk
// (consumers locate the replacement via metadata.superseded_by) and
// would let an orchestrator strand a task without a forwarding pointer.
func NewAssignCloseCommand(streams Streams) *cobra.Command {
	o := &assignCloseOpts{env: envOpener{streams: streams}, outcome: domain.OutcomeCompleted}
	cmd := &cobra.Command{
		Use:   "close",
		Short: "Close an active assignment with outcome completed|abandoned",
		Long: `Close an active assignment without inserting a replacement row.

Emits an assignment.closed event and transitions the assignment row
to the supplied outcome (completed or abandoned). The superseded
outcome is reserved for assign update and is rejected here.

Active intents that reference the closed assignment are left untouched.
Worker-side intent lifecycle is independent of orchestrator-side
assignment lifecycle; close intents through agent-ledger close.

A successful close frees the (task, agent) slot in the active-row
unique index, so a fresh assign for the same pair can run without
an intervening assign update.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runAssignClose(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.assignment, "assignment", "", "Assignment ID (required)")
	f.StringVar(&o.outcome, "outcome", domain.OutcomeCompleted, "Close outcome (completed|abandoned)")
	f.StringVar(&o.reason, "reason", "", "Optional reason; stored as sha256 hash only")
	f.StringVar(&o.agent, "agent", "", "Override AGENT_ID env for the close event")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runAssignClose(streams Streams, o *assignCloseOpts) error {
	if strings.TrimSpace(o.assignment) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--assignment is required")
	}
	if !domain.ValidAssignmentCloseOutcome(o.outcome) {
		return errf(cli.ExitUsage, "invalid_outcome", "--outcome must be completed|abandoned (superseded is reserved for `assign update`)")
	}
	// Pre-validate at the CLI boundary so an unsafe reason returns
	// ExitConfigError with a clear code rather than reaching the
	// domain layer and being wrapped under ErrUnsafeReason.
	if strings.TrimSpace(o.reason) != "" {
		if err := privacy.AssertSafe("--reason", o.reason); err != nil {
			return cli.NewError(cli.ExitConfigError, "reason_unsafe", err.Error())
		}
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
	if err := d.CloseAssignment(ctx, o.assignment, agentID, o.outcome, o.reason, store.Clock()()); err != nil {
		switch {
		case errors.Is(err, domain.ErrAssignmentNotFound):
			return cli.NewError(cli.ExitNotFound, "assignment_not_found",
				fmt.Sprintf("no assignment with id %q", o.assignment))
		case errors.Is(err, domain.ErrAssignmentNotActive):
			return cli.NewError(cli.ExitConflict, "assignment_not_active",
				fmt.Sprintf("assignment %q is not active; another writer may have closed or superseded it", o.assignment))
		case errors.Is(err, domain.ErrInvalidAssignmentCloseOutcome):
			return cli.NewError(cli.ExitUsage, "invalid_outcome", err.Error())
		case errors.Is(err, domain.ErrUnsafeReason):
			return cli.NewError(cli.ExitConfigError, "reason_unsafe", err.Error())
		}
		return cli.NewError(cli.ExitStorageIO, "assign_close_failed", err.Error())
	}
	if o.asJSON {
		return printJSON(streams.Out, map[string]any{
			"assignment_id": o.assignment,
			"close_outcome": o.outcome,
		})
	}
	fmt.Fprintf(streams.Out, "assignment_id=%s outcome=%s\n", o.assignment, o.outcome)
	return nil
}
