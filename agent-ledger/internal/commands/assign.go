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

type assignOpts struct {
	env          envOpener
	task         string
	orchestrator string
	agent        string
	allow        []string
	forbid       []string
	policy       string
	reason       string
	branch       string
	asJSON       bool
}

// NewAssignCommand implements SPEC §18.3.
func NewAssignCommand(streams Streams) *cobra.Command {
	o := &assignOpts{env: envOpener{streams: streams}, policy: domain.PolicyWarn}
	cmd := &cobra.Command{
		Use:           "assign",
		Short:         "Record an orchestrator assignment for a task",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runAssign(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.task, "task", "", "Task ID (required)")
	f.StringVar(&o.orchestrator, "orchestrator", "", "Orchestrator agent ID (required)")
	f.StringVar(&o.agent, "agent", "", "Assigned worker agent ID")
	f.StringArrayVar(&o.allow, "allow", nil, "Allowed path glob; repeatable")
	f.StringArrayVar(&o.forbid, "forbid", nil, "Forbidden path glob; repeatable")
	f.StringVar(&o.policy, "policy", domain.PolicyWarn, "Conflict policy (none|warn|exclusive)")
	f.StringVar(&o.reason, "reason", "", "Assignment reason (required)")
	f.StringVar(&o.branch, "branch", "", "Optional branch name")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runAssign(streams Streams, o *assignOpts) error {
	if strings.TrimSpace(o.task) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--task is required")
	}
	if strings.TrimSpace(o.orchestrator) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--orchestrator is required")
	}
	if strings.TrimSpace(o.reason) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--reason is required")
	}
	if err := privacy.AssertSafe("--reason", o.reason); err != nil {
		return cli.NewError(cli.ExitConfigError, "reason_unsafe", err.Error())
	}
	if !domain.ValidPolicy(o.policy) {
		return errf(cli.ExitUsage, "invalid_policy", "policy %q must be one of none|warn|exclusive", o.policy)
	}
	if len(o.allow) == 0 {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--allow must list at least one path")
	}

	ctx := ctxFor(streams)
	store, _, err := o.env.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	d := domain.New(store)

	// Best-effort upsert of orchestrator agent so the assignment can
	// reference it without requiring identify first.
	_ = d.UpsertAgent(ctx, domain.Agent{
		AgentID:   o.orchestrator,
		AgentKind: "orchestrator",
	})
	if o.agent != "" {
		_ = d.UpsertAgent(ctx, domain.Agent{
			AgentID:   o.agent,
			AgentKind: "worker",
		})
	}

	a, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          o.task,
		OrchestratorID:  o.orchestrator,
		AssignedAgentID: o.agent,
		AllowedPaths:    o.allow,
		ForbiddenPaths:  o.forbid,
		ConflictPolicy:  o.policy,
		Reason:          o.reason,
		Status:          "active",
		Metadata:        map[string]any{"branch": o.branch},
	})
	if err != nil {
		// The CLI guard above is canonical; the domain check is
		// defense-in-depth. Map the sentinel so programmatic callers
		// that bypass the CLI layer still get ExitConfigError.
		if errors.Is(err, domain.ErrUnsafeReason) {
			return cli.NewError(cli.ExitConfigError, "reason_unsafe", err.Error())
		}
		return cli.NewError(cli.ExitStorageIO, "assign_failed", err.Error())
	}
	if o.asJSON {
		return printJSON(streams.Out, map[string]any{
			"assignment_id":   a.AssignmentID,
			"event_id":        a.EventID,
			"task_id":         a.TaskID,
			"conflict_policy": a.ConflictPolicy,
		})
	}
	fmt.Fprintf(streams.Out, "assignment_id=%s task=%s policy=%s\n", a.AssignmentID, a.TaskID, a.ConflictPolicy)
	return nil
}
