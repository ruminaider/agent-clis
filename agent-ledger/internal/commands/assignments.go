package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
)

type assignmentsOpts struct {
	env          envOpener
	task         string
	orchestrator string
	agent        string
	status       string
	limit        int
	asJSON       bool
}

// NewAssignmentsCommand lists assignment rows matching the supplied
// filters. Companion to `status` (which shows live claims, conflicts,
// and changes); this command exposes the historical contract surface
// so reviewers can find auto-assigned, harness-derived, or explicit
// assignments without reading SQLite directly.
//
// The default --status filter is "active" so the common reviewer
// query (find every live assignment) is the simplest invocation.
// Use --status all to include superseded and closed rows.
func NewAssignmentsCommand(streams Streams) *cobra.Command {
	o := &assignmentsOpts{env: envOpener{streams: streams}, status: "active", limit: 50}
	cmd := &cobra.Command{
		Use:   "assignments",
		Short: "List task assignments matching the supplied filters",
		Long: `List task assignments matching the supplied filters.

Status defaults to "active". Use --status all to include superseded
and closed rows. In JSON output, inspect reason_marker for auto,
harness-derived, or explicit classification. For structured queries,
prefer metadata.task_source over parsing reason text.
`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runAssignments(streams, o)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.task, "task", "", "Filter by task ID")
	f.StringVar(&o.orchestrator, "orchestrator", "", "Filter by orchestrator agent ID")
	f.StringVar(&o.agent, "agent", "", "Filter by assigned worker agent ID")
	f.StringVar(&o.status, "status", "active", "Filter by status (active|superseded|closed|all)")
	f.IntVar(&o.limit, "limit", 50, "Maximum rows to return, 1-1000 (most-recent first)")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runAssignments(streams Streams, o *assignmentsOpts) error {
	switch o.status {
	case "active", "superseded", "closed", "all":
	default:
		return errf(cli.ExitUsage, "invalid_status",
			"--status %q must be one of active|superseded|closed|all", o.status)
	}
	if o.limit <= 0 || o.limit > 1000 {
		return cli.NewError(cli.ExitUsage, "invalid_limit", "--limit must be between 1 and 1000")
	}

	ctx := ctxFor(streams)
	store, _, err := o.env.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	d := domain.New(store)

	rows, err := d.ListAssignments(ctx, domain.AssignmentFilter{
		TaskID:         o.task,
		OrchestratorID: o.orchestrator,
		AgentID:        o.agent,
		Status:         o.status,
		Limit:          o.limit,
	})
	if err != nil {
		return mapAssignmentReadError(err, "assignments_query_failed")
	}

	if o.asJSON {
		out := make([]map[string]any, 0, len(rows))
		for _, a := range rows {
			out = append(out, assignmentToMap(a))
		}
		return printJSON(streams.Out, map[string]any{
			"schema":      "agent-ledger.assignments.v1",
			"count":       len(rows),
			"assignments": out,
		})
	}

	if len(rows) == 0 {
		fmt.Fprintln(streams.Out, "no assignments match the supplied filters")
		return nil
	}
	for _, a := range rows {
		marker := assignmentMarkerKind(a.Reason)
		fmt.Fprintf(streams.Out, "%s\t%s\t%s\tagent=%s\torch=%s\tpolicy=%s\tmarker=%s\n",
			a.AssignmentID,
			a.Status,
			a.TaskID,
			defaultIfEmpty(a.AssignedAgentID, "(none)"),
			a.OrchestratorID,
			a.ConflictPolicy,
			marker,
		)
	}
	return nil
}

func assignmentToMap(a domain.Assignment) map[string]any {
	return map[string]any{
		"assignment_id":   a.AssignmentID,
		"event_id":        a.EventID,
		"task_id":         a.TaskID,
		"orchestrator_id": a.OrchestratorID,
		"assigned_agent":  a.AssignedAgentID,
		"allowed_paths":   a.AllowedPaths,
		"forbidden_paths": a.ForbiddenPaths,
		"conflict_policy": a.ConflictPolicy,
		"reason":          a.Reason,
		"reason_marker":   assignmentMarkerKind(a.Reason),
		"status":          a.Status,
		"created_at":      a.CreatedAt,
		"metadata":        a.Metadata,
	}
}

// assignmentMarkerKind classifies the reason text by the leading
// audit-trail marker convention from docs/adapters.md.
//
//	"auto"             -> [auto-assigned by ...] (no harness context found)
//	"harness-derived"  -> [harness-derived by ...] (branch/PR/detached)
//	"explicit"         -> no marker (orchestrator supplied --task explicitly)
func assignmentMarkerKind(reason string) string {
	switch {
	case strings.HasPrefix(reason, "[auto-assigned"):
		return "auto"
	case strings.HasPrefix(reason, "[harness-derived"):
		return "harness-derived"
	default:
		return "explicit"
	}
}

func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
