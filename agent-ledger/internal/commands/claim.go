package commands

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/conflicts"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/locks"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/paths"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/privacy"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/project"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

type claimOpts struct {
	env       envOpener
	task      string
	reason    string
	access    string
	policy    string
	supersede string
	override  string
	agent     string
	asJSON    bool
}

// NewClaimCommand implements SPEC §18.4.
func NewClaimCommand(streams Streams) *cobra.Command {
	o := &claimOpts{env: envOpener{streams: streams}, access: domain.AccessWrite}
	cmd := &cobra.Command{
		Use:           "claim <path>...",
		Short:         "Open a worker intent over one or more paths",
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.asJSON = jsonFlag(cmd)
			return runClaim(streams, o, args)
		},
	}
	addStoreFlags(cmd, &o.env)
	f := cmd.Flags()
	f.StringVar(&o.task, "task", "", "Task ID (required)")
	f.StringVar(&o.reason, "reason", "", "Why the claim is being made (required)")
	f.StringVar(&o.access, "access-mode", domain.AccessWrite, "Access mode (observe|read|write|review-only)")
	f.StringVar(&o.policy, "policy", "", "Override conflict policy (none|warn|exclusive)")
	f.StringVar(&o.supersede, "supersede", "", "Existing intent ID to supersede")
	f.StringVar(&o.override, "override-conflict", "", "Acknowledged conflict ID to consume as override")
	f.StringVar(&o.agent, "agent", "", "Override AGENT_ID env for this claim")
	f.BoolVar(&o.asJSON, "json", false, "Render output as JSON")
	return cmd
}

func runClaim(streams Streams, o *claimOpts, args []string) error {
	if strings.TrimSpace(o.task) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--task is required")
	}
	if strings.TrimSpace(o.reason) == "" {
		return cli.NewError(cli.ExitUsage, "missing_flag", "--reason is required")
	}
	if err := privacy.AssertSafe("--reason", o.reason); err != nil {
		return cli.NewError(cli.ExitConfigError, "reason_unsafe", err.Error())
	}
	if !domain.ValidAccessMode(o.access) {
		return errf(cli.ExitUsage, "invalid_access_mode", "--access-mode must be observe|read|write|review-only")
	}
	if o.policy != "" && !domain.ValidPolicy(o.policy) {
		return errf(cli.ExitUsage, "invalid_policy", "--policy must be none|warn|exclusive")
	}

	agentID := pickAgentID(o.agent)
	if agentID == "" {
		return cli.NewError(cli.ExitUsage, "missing_agent", "AGENT_ID not set; pass --agent or run identify first")
	}

	ctx := ctxFor(streams)
	store, res, err := o.env.openStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	d := domain.New(store)
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: agentID, AgentKind: "worker"}); err != nil {
		return cli.NewError(cli.ExitStorageIO, "agent_upsert_failed", err.Error())
	}

	// Resolve assignment.
	assignment, err := d.LatestActiveAssignmentForTask(ctx, o.task)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return cli.NewError(cli.ExitConflict, "missing_assignment", fmt.Sprintf("no active assignment for task %s", o.task)).
				WithDetails(map[string]any{"task_id": o.task, "finding": "MISSING_ASSIGNMENT"})
		}
		return mapAssignmentReadError(err, "assignment_lookup_failed")
	}

	policy := assignment.ConflictPolicy
	if o.policy != "" {
		policy = o.policy
	}
	if policy == "" {
		policy = domain.PolicyWarn
	}

	// Normalize requested paths.
	abspaths, err := expandPaths(res.Root, args)
	if err != nil {
		return cli.NewError(cli.ExitGeneric, "path_expand_failed", err.Error())
	}
	type req struct {
		display string
		hash    string
		real    string
	}
	requested := make([]req, 0, len(abspaths))
	for _, p := range abspaths {
		n, err := paths.Normalize(res.Root, p)
		if err != nil {
			if paths.IsOutsideProject(err) {
				return cli.NewError(cli.ExitConflict, "path_outside_project", err.Error()).
					WithDetails(map[string]any{"path": p, "finding": "PATH_OUTSIDE_ASSIGNMENT"})
			}
			return cli.NewError(cli.ExitGeneric, "path_normalize_failed", err.Error())
		}
		requested = append(requested, req{display: n.Display, hash: n.PathHash, real: n.RealPath})
	}

	// Scope check against assignment allow/forbid.
	for _, p := range requested {
		if matchGlob(assignment.ForbiddenPaths, p.display) {
			return cli.NewError(cli.ExitConflict, "forbidden_path", fmt.Sprintf("path %q is forbidden by task %s", p.display, assignment.TaskID)).
				WithDetails(map[string]any{"path": p.display, "finding": "FORBIDDEN_PATH_CHANGED"})
		}
		if !matchGlob(assignment.AllowedPaths, p.display) {
			return cli.NewError(cli.ExitConflict, "path_outside_assignment", fmt.Sprintf("path %q is not in task %s allowed paths", p.display, assignment.TaskID)).
				WithDetails(map[string]any{"path": p.display, "finding": "PATH_OUTSIDE_ASSIGNMENT"})
		}
	}

	// If override was supplied, validate it is acknowledged with override
	// resolution and that the caller is the orchestrator.
	hasOverride := false
	if o.override != "" {
		c, cerr := d.ConflictByID(ctx, o.override)
		if cerr != nil {
			return cli.NewError(cli.ExitConflict, "override_conflict_invalid", cerr.Error())
		}
		if c.Status != domain.ConflictAcknowledged || c.Resolution != "override" {
			return cli.NewError(cli.ExitConflict, "override_not_authorized",
				"--override-conflict requires the conflict to be acknowledged with --as-override")
		}
		if agentID != assignment.OrchestratorID {
			return cli.NewError(cli.ExitConflict, "override_requires_orchestrator",
				"only the assignment orchestrator may consume --override-conflict")
		}
		hasOverride = true
	}

	// Build IntentPaths and path hashes.
	ipaths := make([]domain.IntentPath, 0, len(requested))
	hashes := make([]string, 0, len(requested))
	for _, r := range requested {
		ipaths = append(ipaths, domain.IntentPath{
			Path:       r.display,
			RealPath:   r.real,
			PathHash:   r.hash,
			AccessMode: o.access,
		})
		hashes = append(hashes, r.hash)
	}

	intent := domain.Intent{
		AssignmentID:   assignment.AssignmentID,
		TaskID:         assignment.TaskID,
		AgentID:        agentID,
		AccessMode:     o.access,
		ConflictPolicy: policy,
		Reason:         o.reason,
		Metadata:       map[string]any{},
	}
	if o.supersede != "" {
		intent.Metadata["superseded_intent_id"] = o.supersede
	}
	if hasOverride {
		intent.Metadata["override_conflict_id"] = o.override
	}

	claimRes, err := d.ResolveAndInsertIntent(ctx, intent, ipaths, policy, hasOverride, o.supersede)
	if err != nil {
		// The CLI guard above is canonical; the domain check is
		// defense-in-depth. Map the sentinel so programmatic callers
		// that bypass the CLI layer still get ExitConfigError.
		if errors.Is(err, domain.ErrUnsafeReason) {
			return cli.NewError(cli.ExitConfigError, "reason_unsafe", err.Error())
		}
		if errors.Is(err, domain.ErrSupersedeNotActive) {
			return cli.NewError(cli.ExitConflict, "supersede_target_not_active", err.Error())
		}
		return cli.NewError(cli.ExitStorageIO, "intent_insert_failed", err.Error())
	}
	if claimRes.Blocked() {
		// SPEC §16.1 #7: write conflict.detected, exit 4, no intent.
		// We synthesize a placeholder new_intent_id (empty) since the
		// claim is rejected.
		var firstID string
		details := map[string]any{
			"finding":  "ACTIVE_CONFLICT",
			"policy":   policy,
			"task_id":  assignment.TaskID,
			"overlaps": flattenOverlaps(claimRes.Overlaps),
		}
		for _, ov := range claimRes.Overlaps {
			c, cerr := d.InsertConflict(ctx, domain.Conflict{
				Path:             ov.NewPath,
				PathHash:         ov.NewPathHash,
				ExistingIntentID: ov.ExistingIntent,
				Policy:           policy,
				Status:           domain.ConflictDetected,
			})
			if cerr != nil {
				return cli.NewError(cli.ExitStorageIO, "conflict_write_failed", cerr.Error())
			}
			if firstID == "" {
				firstID = c.ConflictID
			}
		}
		details["conflict_id"] = firstID
		return cli.NewError(cli.ExitConflict, "exclusive_conflict",
			fmt.Sprintf("exclusive policy blocks claim on %d overlapping path(s)", len(claimRes.Overlaps))).
			WithDetails(details)
	}

	// Warn-policy overlaps create conflict rows but the intent still
	// opens. We record one conflict per overlap.
	conflictIDs := []string{}
	if claimRes.Decision == conflicts.Warn {
		for _, ov := range claimRes.Overlaps {
			c, cerr := d.InsertConflict(ctx, domain.Conflict{
				Path:             ov.NewPath,
				PathHash:         ov.NewPathHash,
				ExistingIntentID: ov.ExistingIntent,
				NewIntentID:      claimRes.Intent.IntentID,
				Policy:           policy,
				Status:           domain.ConflictDetected,
			})
			if cerr != nil {
				return cli.NewError(cli.ExitStorageIO, "conflict_write_failed", cerr.Error())
			}
			conflictIDs = append(conflictIDs, c.ConflictID)
		}
	}

	// Best-effort flock for exclusive policy. Failure is informational
	// only per SPEC §28; verify reports EXCLUSIVE_LOCK_HELD.
	if policy == domain.PolicyExclusive {
		acquireLocks(store, hashes)
	}

	if o.asJSON {
		out := map[string]any{
			"intent_id":     claimRes.Intent.IntentID,
			"event_id":      claimRes.Intent.EventID,
			"assignment_id": claimRes.Intent.AssignmentID,
			"task_id":       claimRes.Intent.TaskID,
			"agent_id":      agentID,
			"access_mode":   o.access,
			"policy":        policy,
			"paths":         displayPaths(ipaths),
		}
		if len(conflictIDs) > 0 {
			out["conflicts"] = conflictIDs
		}
		if claimRes.Decision == conflicts.Warn {
			out["status"] = "warn"
		} else {
			out["status"] = "ok"
		}
		return printJSON(streams.Out, out)
	}
	fmt.Fprintf(streams.Out, "intent_id=%s task=%s policy=%s\n", claimRes.Intent.IntentID, claimRes.Intent.TaskID, policy)
	if claimRes.Decision == conflicts.Warn {
		fmt.Fprintf(streams.Err, "warning: %d overlapping intent(s); conflict(s) recorded: %s\n", len(claimRes.Overlaps), strings.Join(conflictIDs, ","))
	}
	return nil
}

func flattenOverlaps(o []conflicts.Overlap) []map[string]any {
	out := make([]map[string]any, 0, len(o))
	for _, x := range o {
		out = append(out, map[string]any{
			"path":            x.NewPath,
			"path_hash":       x.NewPathHash,
			"existing_intent": x.ExistingIntent,
		})
	}
	return out
}

func displayPaths(p []domain.IntentPath) []map[string]any {
	out := make([]map[string]any, 0, len(p))
	for _, x := range p {
		out = append(out, map[string]any{
			"path":      x.Path,
			"path_hash": x.PathHash,
		})
	}
	return out
}

// acquireLocks attempts non-blocking flock on each hash. Failures are
// silently ignored; the verifier surfaces lock state.
func acquireLocks(store *sqlite.Store, hashes []string) {
	dir := storage.Layout{Dir: store.LedgerDir()}.LocksDir()
	ls := locks.NewLockSet(dir)
	for _, h := range hashes {
		_, _ = ls.TryLock(h)
	}
	// The lock is intentionally retained: when the process exits, the
	// OS releases the flock automatically. We do not call ReleaseAll
	// here so a subsequent claim within the same process correctly
	// observes contention.
	_ = ls
}

// silence unused import detector when project is referenced only via
// transitive types.
var _ = project.EnvVar
var _ = context.Background
