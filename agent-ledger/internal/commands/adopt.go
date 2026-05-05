package commands

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/paths"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/privacy"
)

type adoptOpts struct {
	env    envOpener
	task   string
	agent  string
	reason string
	asJSON bool
}

// NewAdoptCommand implements SPEC §18.11.
func NewAdoptCommand(streams Streams) *cobra.Command {
	o := &adoptOpts{env: envOpener{streams: streams}}
	cmd := &cobra.Command{
		Use:           "adopt <path>...",
		Short:         "Retroactively adopt an unclaimed change",
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runAdopt(streams, o, args)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.task, "task", "", "Task ID to adopt under (required)")
	f.StringVar(&o.agent, "agent", "", "Agent that made the change (required)")
	f.StringVar(&o.reason, "reason", "", "Why the adoption is needed (required)")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runAdopt(streams Streams, o *adoptOpts, args []string) error {
	if strings.TrimSpace(o.task) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--task is required")
	}
	if strings.TrimSpace(o.agent) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--agent is required")
	}
	if strings.TrimSpace(o.reason) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--reason is required")
	}
	if err := privacy.AssertSafe("reason", o.reason); err != nil {
		return cli.NewError(cli.ExitValidation, "reason_unsafe", err.Error())
	}

	ctx := ctxFor(streams)
	store, res, err := o.env.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	d := domain.New(store)

	assignment, err := d.LatestActiveAssignmentForTask(ctx, o.task)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return mapStorageReadError(err, "assignment_lookup_failed")
	}
	// Adoption is allowed without an active assignment so backfill on
	// completed tasks works. We still record assignment metadata when
	// available.
	assignmentID := assignment.AssignmentID

	// Normalize each path. Paths must be inside the project root but
	// need not match any allow/forbid list: that is the whole point of
	// retroactive adoption.
	abspaths, err := expandPaths(res.Root, args)
	if err != nil {
		return cli.NewError(cli.ExitGeneric, "path_expand_failed", err.Error())
	}
	chPaths := make([]domain.ChangePath, 0, len(abspaths))
	for _, p := range abspaths {
		n, err := paths.NormalizeAt(res.Roots, p)
		if err != nil {
			if paths.IsOutsideProject(err) {
				return cli.NewError(cli.ExitGeneric, "path_outside_project", err.Error())
			}
			return cli.NewError(cli.ExitGeneric, "path_normalize_failed", err.Error())
		}
		chPaths = append(chPaths, domain.ChangePath{
			Path:          n.Display,
			RealPath:      n.RealPath,
			PathHash:      n.PathHash,
			CanonicalHash: n.CanonicalHash,
			Status:        domain.PathStatusUnknown,
		})
	}

	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: o.agent, AgentKind: "worker"}); err != nil {
		return cli.NewError(cli.ExitStorageIO, "agent_upsert_failed", err.Error())
	}

	summary := "Retroactive adoption: " + o.reason
	change, err := d.InsertChange(ctx, domain.RecordChangeInput{
		Change: domain.Change{
			AssignmentID: assignmentID,
			TaskID:       o.task,
			AgentID:      o.agent,
			ActorKind:    domain.ActorAgent,
			Summary:      summary,
			Metadata: map[string]any{
				"retroactive":   true,
				"reason_sha256": shortHashTag(o.reason),
			},
		},
		Paths:     chPaths,
		EventType: "change.adopted",
	})
	if err != nil {
		return cli.NewError(cli.ExitStorageIO, "change_insert_failed", err.Error())
	}

	if o.asJSON {
		return printJSON(streams.Out, map[string]any{
			"change_id":   change.ChangeID,
			"event_id":    change.EventID,
			"task_id":     change.TaskID,
			"agent_id":    o.agent,
			"retroactive": true,
			"paths":       displayChangePaths(chPaths),
		})
	}
	fmt.Fprintf(streams.Out, "change_id=%s task=%s adopted_paths=%d\n", change.ChangeID, change.TaskID, len(chPaths))
	return nil
}

// shortHashTag returns "sha256:<hex>" of s; used for reason metadata
// where we want a binding hash but never the cleartext.
func shortHashTag(s string) string {
	if s == "" {
		return ""
	}
	return "sha256:" + sha256HexShort(s)
}
