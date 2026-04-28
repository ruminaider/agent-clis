package commands

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
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
	metadata     string
	ifAbsent     bool
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
	f.StringVar(&o.metadata, "metadata", "", "Optional structured metadata as a JSON object (merged into assignment metadata_json)")
	f.BoolVar(&o.ifAbsent, "if-absent", false, "Reuse an identical active assignment if present")
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

	_ = d.UpsertAgent(ctx, domain.Agent{AgentID: o.orchestrator, AgentKind: "orchestrator"})
	if o.agent != "" {
		_ = d.UpsertAgent(ctx, domain.Agent{AgentID: o.agent, AgentKind: "worker"})
	}

	meta := map[string]any{}
	if o.branch != "" {
		meta["branch"] = o.branch
	}
	for k, v := range extraMetadata {
		meta[k] = v
	}
	assignment := domain.Assignment{
		TaskID:          o.task,
		OrchestratorID:  o.orchestrator,
		AssignedAgentID: o.agent,
		AllowedPaths:    o.allow,
		ForbiddenPaths:  o.forbid,
		ConflictPolicy:  o.policy,
		Reason:          o.reason,
		Status:          "active",
		Metadata:        meta,
	}
	reused := false
	if o.ifAbsent {
		prev, err := d.LatestActiveAssignmentForTaskAndAgent(ctx, o.task, o.agent)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return cli.NewError(cli.ExitStorageIO, "assign_lookup_failed", err.Error())
		}
		if err == nil && sameAssignmentReplay(prev, assignment) {
			assignment = prev
			reused = true
		} else {
			assignment, err = d.InsertAssignment(ctx, assignment)
			if err != nil {
				if errors.Is(err, domain.ErrUnsafeReason) {
					return cli.NewError(cli.ExitConfigError, "reason_unsafe", err.Error())
				}
				if errors.Is(err, domain.ErrAssignmentExists) {
					// The unique index caught a row that the SELECT
					// missed (concurrent winner committed between
					// our LatestActive lookup and our INSERT). Retry
					// the lookup; if it now matches, treat as reuse.
					updated, retryReused, lerr := recoverIfAbsentAssignment(ctx, d.LatestActiveAssignmentForTaskAndAgent, assignment, o.task, o.agent)
					if lerr != nil {
						return lerr
					}
					assignment = updated
					reused = retryReused
				} else {
					return cli.NewError(cli.ExitStorageIO, "assign_failed", err.Error())
				}
			}
		}
	} else {
		assignment, err = d.InsertAssignment(ctx, assignment)
		if err != nil {
			if errors.Is(err, domain.ErrUnsafeReason) {
				return cli.NewError(cli.ExitConfigError, "reason_unsafe", err.Error())
			}
			if errors.Is(err, domain.ErrAssignmentExists) {
				return cli.NewError(cli.ExitConflict, "assignment_exists",
					"an active assignment already exists for this (task, agent) pair; supply --if-absent to reuse identical assignments or close the prior one first")
			}
			return cli.NewError(cli.ExitStorageIO, "assign_failed", err.Error())
		}
	}

	if o.asJSON {
		return printJSON(streams.Out, map[string]any{
			"assignment_id":   assignment.AssignmentID,
			"event_id":        assignment.EventID,
			"task_id":         assignment.TaskID,
			"conflict_policy": assignment.ConflictPolicy,
			"reused":          reused,
		})
	}
	fmt.Fprintf(streams.Out, "assignment_id=%s task=%s policy=%s reused=%t\n", assignment.AssignmentID, assignment.TaskID, assignment.ConflictPolicy, reused)
	return nil
}

// sameAssignmentReplay reports whether b is byte-equivalent to a for
// the purpose of --if-absent reuse. The predicate intentionally
// compares every field a reviewer might care about: task, agent,
// orchestrator, policy, reason, allowed/forbidden paths, and
// metadata. This is tighter than the v0.2.0-rc2 predicate so that
// callers carrying audit metadata cannot have their assignment
// silently reused under a stale older row that lacked the metadata.
func sameAssignmentReplay(a, b domain.Assignment) bool {
	if a.TaskID != b.TaskID ||
		a.AssignedAgentID != b.AssignedAgentID ||
		a.OrchestratorID != b.OrchestratorID ||
		a.ConflictPolicy != b.ConflictPolicy ||
		a.Reason != b.Reason {
		return false
	}
	if strings.Join(a.AllowedPaths, "\x00") != strings.Join(b.AllowedPaths, "\x00") {
		return false
	}
	if strings.Join(a.ForbiddenPaths, "\x00") != strings.Join(b.ForbiddenPaths, "\x00") {
		return false
	}
	return metadataEqual(a.Metadata, b.Metadata)
}

// metadataEqual normalizes two metadata maps for replay comparison.
// Empty branch metadata is ignored so legacy rows that still carry
// {"branch":""} do not block reuse against newer rows that omit it.
func metadataEqual(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(canonicalizeMetaForCompare(a), canonicalizeMetaForCompare(b))
}

func canonicalizeMetaForCompare(m map[string]any) map[string]any {
	if len(m) == 0 {
		return map[string]any{}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return m
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return m
	}
	if out == nil {
		return map[string]any{}
	}
	if branch, ok := out["branch"]; ok && branch == "" {
		delete(out, "branch")
	}
	return out
}

// parseMetadataFlag validates --metadata as a JSON object and returns
// it as a map. Empty input returns nil. Top-level arrays, scalars,
// and malformed JSON are rejected with ExitUsage so the caller fails
// fast rather than writing a garbage metadata blob.
func parseMetadataFlag(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, errf(cli.ExitUsage, "invalid_metadata", "--metadata must be valid JSON: %s", err.Error())
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = fmt.Errorf("unexpected trailing data")
		}
		return nil, errf(cli.ExitUsage, "invalid_metadata", "--metadata must contain exactly one JSON value: %s", err.Error())
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, cli.NewError(cli.ExitUsage, "invalid_metadata",
			"--metadata must be a JSON object (got non-object top-level value)")
	}
	return obj, nil
}

func recoverIfAbsentAssignment(ctx context.Context, lookup func(context.Context, string, string) (domain.Assignment, error), wanted domain.Assignment, taskID, agentID string) (domain.Assignment, bool, error) {
	prev, lerr := lookup(ctx, taskID, agentID)
	if errors.Is(lerr, sql.ErrNoRows) {
		return wanted, false, cli.NewError(cli.ExitConflict, "assignment_exists",
			"an active assignment already exists for this (task, agent) pair; supply --if-absent to reuse identical assignments or close the prior one first")
	}
	if lerr != nil {
		return wanted, false, cli.NewError(cli.ExitStorageIO, "assign_lookup_failed", lerr.Error())
	}
	if sameAssignmentReplay(prev, wanted) {
		return prev, true, nil
	}
	return wanted, false, cli.NewError(cli.ExitConflict, "assignment_exists",
		"an active assignment already exists for this (task, agent) pair; supply --if-absent to reuse identical assignments or close the prior one first")
}
