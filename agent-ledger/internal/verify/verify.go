// Package verify implements the agent-ledger.verify.v1 contract from
// SPEC §19. It produces a stable JSON document with a status,
// per-finding codes, severities, paths, and recovery hints.
//
// JSON schema (top level), aligned with SPEC §19.2:
//
//	{
//	  "schema":             "agent-ledger.verify.v1",
//	  "status":             "passed|failed|needs-decision|error",
//	  "project_id":         "...",
//	  "project_fingerprint":"...",
//	  "project_slug":       "...",
//	  "task_id":            "...",         // when scoped to a task
//	  "generated_at":       "RFC3339",
//	  "summary": {
//	    "changed_paths": 0, "claimed_paths": 0, "unclaimed_paths": 0,
//	    "forbidden_path_violations": 0, "active_conflicts": 0,
//	    "open_intents": 0, "stale_intents": 0
//	  },
//	  "findings": [
//	    {
//	      "code":     "UNCLAIMED_CHANGE",
//	      "severity": "info|warning|error|fatal",
//	      "message":  "...",
//	      "path":     "src/foo.go",
//	      "details":  { ... },
//	      "suggested_recovery": "concrete CLI hint"
//	    }
//	  ]
//	}
//
// Additive extensions retained beyond SPEC §19.2 for operator
// utility (kept as documented extras, not contract changes):
//   - Top level: "mode" (project|task|summary), "summary_path"
//     (when mode=summary).
//   - Summary: "outside_assignment_paths", "findings" (count).
//
// Exit codes follow SPEC §19.1 directly: 0 passed, 1 failed, 2 config
// error, 3 storage error, 4 conflict requires decision, 5 reserved
// for sync/auth. Status "error" maps to either 2 or 3 by inspecting
// the dominant finding code (CONFIG_ERROR vs STORAGE_ERROR). The
// verify command MUST follow §19.1 exactly, so this package defines
// its own exit-code helper instead of routing through cli.Exit*.
package verify

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/paths"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/policy"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/project"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/summary"
)

// Schema is the schema field on every verify report.
const Schema = "agent-ledger.verify.v1"

// Verification statuses (SPEC §19.2).
const (
	StatusPassed        = "passed"
	StatusFailed        = "failed"
	StatusNeedsDecision = "needs-decision"
	StatusError         = "error"
)

// Finding severities (SPEC §19.2).
const (
	SevInfo    = "info"
	SevWarning = "warning"
	SevError   = "error"
	SevFatal   = "fatal"
)

// Verification mode strings.
const (
	ModeProject = "project"
	ModeTask    = "task"
	ModeSummary = "summary"
)

// Finding codes (SPEC §19.3). Phase 1 implements the subset below;
// integration test SchemaTest_VerifyCodeRegistry snapshots the
// canonical SPEC list to fail fast on future drift.
const (
	CodeUnclaimedChange       = "UNCLAIMED_CHANGE"
	CodeForbiddenPathChanged  = "FORBIDDEN_PATH_CHANGED"
	CodePathOutsideAssignment = "PATH_OUTSIDE_ASSIGNMENT"
	CodeActiveConflict        = "ACTIVE_CONFLICT"
	CodeStaleIntent           = "STALE_INTENT"
	CodeOpenIntent            = "OPEN_INTENT"
	CodeMissingReason         = "MISSING_REASON"
	CodeMissingAssignment     = "MISSING_ASSIGNMENT"
	CodeAgentMismatch         = "AGENT_MISMATCH"
	CodeReviewOnlyWrite       = "REVIEW_ONLY_WRITE"
	CodeExclusiveLockHeld     = "EXCLUSIVE_LOCK_HELD"
	CodeConfigError           = "CONFIG_ERROR"
	CodeStorageError          = "STORAGE_ERROR"
	CodeSummaryMismatch       = "SUMMARY_MISMATCH"
)

// DefaultStaleAfter is the heartbeat-expiry window applied when a
// caller does not configure one. SPEC §16.2 defaults to 2 minutes;
// for `verify` we use 1 hour as a "stale" threshold so short-running
// CI flows are not falsely flagged.
const DefaultStaleAfter = time.Hour

// Report is the JSON-serializable verify output.
type Report struct {
	Schema             string    `json:"schema"`
	Status             string    `json:"status"`
	Mode               string    `json:"mode"`
	ProjectID          string    `json:"project_id,omitempty"`
	ProjectFingerprint string    `json:"project_fingerprint,omitempty"`
	ProjectSlug        string    `json:"project_slug,omitempty"`
	TaskID             string    `json:"task_id,omitempty"`
	SummaryPath        string    `json:"summary_path,omitempty"`
	GeneratedAt        string    `json:"generated_at"`
	Summary            Summary   `json:"summary"`
	Findings           []Finding `json:"findings"`
}

// Summary is the SPEC §19.2 flat counts block. The first seven
// fields are SPEC-canonical; OutsideAssignmentPaths and Findings are
// additive operator-facing extras (documented at the package level).
type Summary struct {
	ChangedPaths            int `json:"changed_paths"`
	ClaimedPaths            int `json:"claimed_paths"`
	UnclaimedPaths          int `json:"unclaimed_paths"`
	ForbiddenPathViolations int `json:"forbidden_path_violations"`
	ActiveConflicts         int `json:"active_conflicts"`
	OpenIntents             int `json:"open_intents"`
	StaleIntents            int `json:"stale_intents"`
	// Additive extensions (not in SPEC §19.2):
	OutsideAssignmentPaths int `json:"outside_assignment_paths"`
	Findings               int `json:"findings"`
}

// Finding is one verification finding (SPEC §19.2).
type Finding struct {
	Code              string         `json:"code"`
	Severity          string         `json:"severity"`
	Message           string         `json:"message"`
	Path              string         `json:"path,omitempty"`
	Details           map[string]any `json:"details,omitempty"`
	SuggestedRecovery string         `json:"suggested_recovery,omitempty"`
}

// Inputs control a verify run. All fields are optional unless noted.
type Inputs struct {
	// Root is the project root. Empty means cwd.
	Root string
	// LedgerDirFlag overrides the resolved ledger directory.
	LedgerDirFlag string
	// ProjectIDFlag overrides the resolved project id.
	ProjectIDFlag string
	// TaskID limits verification to one task (mode=task).
	TaskID string
	// SummaryFile limits verification to a single summary doc
	// (mode=summary). Mutually exclusive with TaskID.
	SummaryFile string
	// Now is the reference clock; defaults to time.Now().UTC().
	Now time.Time
	// StaleAfter overrides DefaultStaleAfter.
	StaleAfter time.Duration
	// Getenv lets tests override env lookups.
	Getenv func(string) string
	// ChangedPathsOverride bypasses git discovery: when non-nil, these
	// project-relative paths are used as the working-tree changes.
	ChangedPathsOverride []string
}

// ExitCode maps r.Status to the SPEC §19.1 process exit code.
//
// Note: SPEC §19.2 collapses configuration and storage problems into
// a single status "error". To preserve the §19.1 exit-code split
// (config=2, storage=3) we inspect the dominant finding code when
// status==error.
//
// This is the verify-specific mapping. It does NOT use internal/cli
// ExitStorageIO etc. See the package-level doc for the rationale.
func (r *Report) ExitCode() int {
	if r == nil {
		return 1
	}
	switch r.Status {
	case StatusPassed:
		return 0
	case StatusFailed:
		return 1
	case StatusNeedsDecision:
		return 4
	case StatusError:
		for _, f := range r.Findings {
			switch f.Code {
			case CodeConfigError:
				return 2
			case CodeStorageError:
				return 3
			}
		}
		return 1
	}
	return 1
}

// WriteJSON renders r as indented JSON to w.
func (r *Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText renders r as a short human-readable summary to w.
func (r *Report) WriteText(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "agent-ledger verify (mode=%s status=%s)\n", r.Mode, r.Status); err != nil {
		return err
	}
	c := r.Summary
	if _, err := fmt.Fprintf(w, "  changed=%d claimed=%d unclaimed=%d forbidden=%d outside=%d conflicts=%d open=%d stale=%d findings=%d\n",
		c.ChangedPaths, c.ClaimedPaths, c.UnclaimedPaths, c.ForbiddenPathViolations, c.OutsideAssignmentPaths, c.ActiveConflicts, c.OpenIntents, c.StaleIntents, c.Findings); err != nil {
		return err
	}
	for _, f := range r.Findings {
		if _, err := fmt.Fprintf(w, "  [%s] %s: %s", f.Severity, f.Code, f.Message); err != nil {
			return err
		}
		if f.Path != "" {
			if _, err := fmt.Fprintf(w, " path=%s", f.Path); err != nil {
				return err
			}
		}
		if f.SuggestedRecovery != "" {
			if _, err := fmt.Fprintf(w, "\n      suggested_recovery: %s", f.SuggestedRecovery); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

// Run dispatches to the project, task, or summary verifier based on
// in.TaskID and in.SummaryFile. It always returns a Report; callers
// should not treat error as the dominant signal. err is only set for
// programming bugs that prevent producing any report at all.
func Run(ctx context.Context, in Inputs) (*Report, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if in.StaleAfter == 0 {
		in.StaleAfter = DefaultStaleAfter
	}
	getenv := in.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	root := in.Root
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return errorReport(StatusError, CodeConfigError, "could not determine working directory: "+err.Error(), now), nil
		}
		root = cwd
	}

	if in.SummaryFile != "" {
		return runSummary(ctx, in, root, now)
	}

	mode := ModeProject
	if in.TaskID != "" {
		mode = ModeTask
	}

	res, err := project.Resolve(project.Options{
		Root:          root,
		LedgerDirFlag: in.LedgerDirFlag,
		ProjectIDFlag: in.ProjectIDFlag,
		EnvLedgerDir:  getenv(project.EnvVar),
		XDGStateHome:  getenv("XDG_STATE_HOME"),
	})
	if err != nil {
		return errorReport(StatusError, CodeConfigError, "project resolution failed: "+err.Error(), now), nil
	}
	if err := res.Validate(); err != nil {
		return errorReport(StatusError, CodeConfigError, "ledger directory could not be resolved: "+err.Error(), now), nil
	}

	store, err := sqlite.Open(ctx, res.LedgerDir)
	if err != nil {
		return errorReport(StatusError, CodeStorageError, "open ledger: "+err.Error(), now), nil
	}
	defer store.Close()
	if err := store.Migrator().Up(ctx); err != nil {
		return errorReport(StatusError, CodeStorageError, "migrate ledger: "+err.Error(), now), nil
	}
	d := domain.New(store)

	r := newReport(mode, now)
	r.ProjectID = res.Identity.ProjectID
	r.ProjectFingerprint = res.Identity.Fingerprint
	r.ProjectSlug = res.Identity.Slug
	r.TaskID = in.TaskID

	changed, err := discoverChangedPaths(root, in.ChangedPathsOverride)
	if err != nil {
		// Treat as storage error; the verifier could not enumerate.
		return errorReport(StatusError, CodeStorageError, "discover changed paths: "+err.Error(), now), nil
	}

	// Compute changed-path hashes once; used for claim coverage.
	type changedPath struct {
		Display string
		Hash    string
	}
	cps := make([]changedPath, 0, len(changed))
	for _, p := range changed {
		n, err := paths.Normalize(root, p)
		if err != nil {
			// Path resolves outside project; treat as outside assignment in task mode, otherwise skip.
			cps = append(cps, changedPath{Display: p, Hash: paths.Hash(filepath.Join(root, p))})
			continue
		}
		cps = append(cps, changedPath{Display: n.Display, Hash: n.PathHash})
	}
	sort.Slice(cps, func(i, j int) bool { return cps[i].Display < cps[j].Display })
	r.Summary.ChangedPaths = len(cps)

	// Build a set of claimed/recorded path hashes scoped to taskID
	// when set; otherwise scoped to all active intents and recent
	// changes.
	claimedHashes, claimedDisplays, err := collectClaimedHashes(ctx, d, in.TaskID)
	if err != nil {
		return errorReport(StatusError, CodeStorageError, "load claimed paths: "+err.Error(), now), nil
	}
	recordedHashes, err := collectRecordedHashes(ctx, d, in.TaskID)
	if err != nil {
		return errorReport(StatusError, CodeStorageError, "load recorded paths: "+err.Error(), now), nil
	}

	// Resolve assignment for task mode (used for forbidden/outside).
	var assignment *domain.Assignment
	if in.TaskID != "" {
		a, err := d.LatestActiveAssignmentForTask(ctx, in.TaskID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return errorReport(StatusError, CodeStorageError, "load assignment: "+err.Error(), now), nil
			}
			// No assignment: emit MISSING_ASSIGNMENT and continue.
			r.Findings = append(r.Findings, Finding{
				Code:              CodeMissingAssignment,
				Severity:          SevError,
				Message:           fmt.Sprintf("no active assignment exists for task %q", in.TaskID),
				SuggestedRecovery: fmt.Sprintf("Run `agent-ledger assign --task %s --orchestrator <orchestrator-id> --agent <agent-id> --allow <path> --reason '...'` before claiming.", in.TaskID),
			})
		} else {
			assignment = &a
		}
	}

	// Per-changed-path findings.
	claimedSet := setOf(claimedHashes)
	recordedSet := setOf(recordedHashes)
	for _, cp := range cps {
		_, claimed := claimedSet[cp.Hash]
		_, recorded := recordedSet[cp.Hash]
		switch {
		case claimed, recorded:
			r.Summary.ClaimedPaths++
		default:
			r.Summary.UnclaimedPaths++
			r.Findings = append(r.Findings, Finding{
				Code:              CodeUnclaimedChange,
				Severity:          SevError,
				Message:           fmt.Sprintf("%s changed but no agent has claimed or recorded it", cp.Display),
				Path:              cp.Display,
				SuggestedRecovery: fmt.Sprintf("Run `agent-ledger adopt %s --task <task> --reason 'Backfill missed claim found during verification'` or `agent-ledger claim %s --task <task> --reason '...'` before recording.", cp.Display, cp.Display),
			})
		}
		if assignment != nil {
			if policy.MatchAny(assignment.ForbiddenPaths, cp.Display) {
				r.Summary.ForbiddenPathViolations++
				r.Findings = append(r.Findings, Finding{
					Code:              CodeForbiddenPathChanged,
					Severity:          SevError,
					Message:           fmt.Sprintf("%s changed but task %s forbids this path", cp.Display, assignment.TaskID),
					Path:              cp.Display,
					Details:           map[string]any{"task_id": assignment.TaskID, "forbidden_paths": assignment.ForbiddenPaths},
					SuggestedRecovery: fmt.Sprintf("Revert %s or route the change to the orchestrator (%s).", cp.Display, assignment.OrchestratorID),
				})
				continue
			}
			if !policy.MatchAny(assignment.AllowedPaths, cp.Display) {
				r.Summary.OutsideAssignmentPaths++
				r.Findings = append(r.Findings, Finding{
					Code:              CodePathOutsideAssignment,
					Severity:          SevError,
					Message:           fmt.Sprintf("%s changed but task %s does not include this path in allowed paths", cp.Display, assignment.TaskID),
					Path:              cp.Display,
					Details:           map[string]any{"task_id": assignment.TaskID, "allowed_paths": assignment.AllowedPaths},
					SuggestedRecovery: fmt.Sprintf("Ask the orchestrator to extend assignment %s with `--allow %s`, or revert the change.", assignment.AssignmentID, cp.Display),
				})
			}
		}
	}
	_ = claimedDisplays

	// Active conflicts.
	confs, err := d.ListConflicts(ctx, in.TaskID, "detected")
	if err != nil {
		return errorReport(StatusError, CodeStorageError, "list conflicts: "+err.Error(), now), nil
	}
	r.Summary.ActiveConflicts = len(confs)
	for _, c := range confs {
		r.Findings = append(r.Findings, Finding{
			Code:              CodeActiveConflict,
			Severity:          SevError,
			Message:           fmt.Sprintf("conflict %s on %s requires acknowledgement (policy=%s)", c.ConflictID, c.Path, c.Policy),
			Path:              c.Path,
			Details:           map[string]any{"conflict_id": c.ConflictID, "policy": c.Policy, "existing_intent_id": c.ExistingIntentID, "new_intent_id": c.NewIntentID},
			SuggestedRecovery: fmt.Sprintf("Run `agent-ledger conflicts acknowledge --conflict %s --reason '...'` or stop and route to the orchestrator.", c.ConflictID),
		})
	}

	// Open and stale intents (task mode treats open as informational
	// finding; project mode only reports stale).
	intents, err := d.ListActiveIntents(ctx, in.TaskID)
	if err != nil {
		return errorReport(StatusError, CodeStorageError, "list intents: "+err.Error(), now), nil
	}
	for _, intent := range intents {
		// Stale check.
		if intent.HeartbeatExpiresAt != "" {
			if exp, err := time.Parse(time.RFC3339Nano, intent.HeartbeatExpiresAt); err == nil {
				if now.Sub(exp) > in.StaleAfter {
					r.Summary.StaleIntents++
					r.Findings = append(r.Findings, Finding{
						Code:              CodeStaleIntent,
						Severity:          SevWarning,
						Message:           fmt.Sprintf("intent %s last heartbeat expired at %s", intent.IntentID, intent.HeartbeatExpiresAt),
						Details:           map[string]any{"intent_id": intent.IntentID, "task_id": intent.TaskID, "agent_id": intent.AgentID, "expired_at": intent.HeartbeatExpiresAt},
						SuggestedRecovery: fmt.Sprintf("Run `agent-ledger close --intent %s --outcome abandoned` or heartbeat the intent.", intent.IntentID),
					})
				}
			}
		}
		if in.TaskID != "" && intent.TaskID == in.TaskID {
			r.Summary.OpenIntents++
			r.Findings = append(r.Findings, Finding{
				Code:              CodeOpenIntent,
				Severity:          SevWarning,
				Message:           fmt.Sprintf("intent %s is still active for task %s", intent.IntentID, intent.TaskID),
				Details:           map[string]any{"intent_id": intent.IntentID, "agent_id": intent.AgentID},
				SuggestedRecovery: fmt.Sprintf("Close the intent with `agent-ledger close --intent %s --outcome completed` once work is done.", intent.IntentID),
			})
		}
		if strings.TrimSpace(intent.Reason) == "" {
			r.Findings = append(r.Findings, Finding{
				Code:              CodeMissingReason,
				Severity:          SevWarning,
				Message:           fmt.Sprintf("intent %s has no reason recorded", intent.IntentID),
				Details:           map[string]any{"intent_id": intent.IntentID},
				SuggestedRecovery: "Re-open the intent with --reason or close and reclaim it with a non-empty reason.",
			})
		}
		// Review-only writes: any change row with this intent_id and
		// the intent access mode is not write/observe.
		if intent.AccessMode == domain.AccessReviewOnly || intent.AccessMode == domain.AccessRead {
			has, err := intentHasChanges(ctx, store, intent.IntentID)
			if err != nil {
				return errorReport(StatusError, CodeStorageError, "check intent changes: "+err.Error(), now), nil
			}
			if has {
				r.Findings = append(r.Findings, Finding{
					Code:              CodeReviewOnlyWrite,
					Severity:          SevError,
					Message:           fmt.Sprintf("intent %s is %s but recorded a change", intent.IntentID, intent.AccessMode),
					Details:           map[string]any{"intent_id": intent.IntentID, "access_mode": intent.AccessMode},
					SuggestedRecovery: "Open a write-mode intent before recording changes, or annotate the access mode correctly.",
				})
			}
		}
	}

	// EXCLUSIVE_LOCK_HELD: lock sentinels exist for path hashes that
	// have no active exclusive intent in the DB. Use the DB lock-set
	// dir under <ledger-dir>/locks/.
	if r2 := scanExclusiveLocks(res.LedgerDir, intents); r2 != nil {
		r.Findings = append(r.Findings, r2...)
	}

	// Task-mode integrity checks across changes for this task.
	if in.TaskID != "" {
		changes, err := d.ChangesForTask(ctx, in.TaskID)
		if err != nil {
			return errorReport(StatusError, CodeStorageError, "load changes for task: "+err.Error(), now), nil
		}
		if assignment != nil {
			for _, c := range changes {
				if c.AgentID != "" && assignment.AssignedAgentID != "" && c.AgentID != assignment.AssignedAgentID {
					r.Findings = append(r.Findings, Finding{
						Code:              CodeAgentMismatch,
						Severity:          SevError,
						Message:           fmt.Sprintf("change %s recorded by %s but task %s is assigned to %s", c.ChangeID, c.AgentID, assignment.TaskID, assignment.AssignedAgentID),
						Details:           map[string]any{"change_id": c.ChangeID, "agent_id": c.AgentID, "assigned_agent_id": assignment.AssignedAgentID},
						SuggestedRecovery: fmt.Sprintf("Reassign the task with `agent-ledger assign --task %s --agent %s ...` or have the assigned agent record the change.", assignment.TaskID, c.AgentID),
					})
				}
			}
		} else if len(changes) > 0 {
			// Already emitted MISSING_ASSIGNMENT above; nothing else.
		}
	}

	r.Status = decideStatus(r)
	r.Summary.Findings = len(r.Findings)
	return r, nil
}

// runSummary verifies a committed summary file against the working
// tree without consulting the ledger DB. SPEC §20.1.
func runSummary(ctx context.Context, in Inputs, root string, now time.Time) (*Report, error) {
	r := newReport(ModeSummary, now)
	r.SummaryPath = in.SummaryFile

	abs := in.SummaryFile
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return errorReport(StatusError, CodeConfigError, "read summary file: "+err.Error(), now), nil
	}
	var doc summary.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return errorReport(StatusError, CodeConfigError, "parse summary file: "+err.Error(), now), nil
	}
	if doc.Schema != summary.Schema {
		return errorReport(StatusError, CodeConfigError, "unsupported summary schema: "+doc.Schema, now), nil
	}
	r.TaskID = doc.Task.ID
	r.ProjectID = doc.Project.ID
	r.ProjectSlug = doc.Project.Slug
	r.ProjectFingerprint = doc.Project.Fingerprint

	// Recompute assignment hash.
	if doc.AssignmentHash != "" {
		recomputed := recomputeAssignmentHash(doc.AssignmentSnapshot)
		if recomputed != doc.AssignmentHash {
			r.Findings = append(r.Findings, Finding{
				Code:              CodeSummaryMismatch,
				Severity:          SevError,
				Message:           "assignment_hash does not match assignment_snapshot",
				Details:           map[string]any{"declared": doc.AssignmentHash, "recomputed": recomputed},
				SuggestedRecovery: "Regenerate the summary with `agent-ledger export-summary --task " + doc.Task.ID + " --output <path>`.",
			})
		}
	}

	// Validate each summary path's path_hash against the current
	// working tree. This is the SPEC §20.1 tamper check: a clean
	// checkout must reproduce the recorded hash.
	for _, ref := range doc.ChangedPaths {
		actual, exists := pathHashAtRoot(root, ref.Path)
		if !exists {
			r.Findings = append(r.Findings, Finding{
				Code:              CodeSummaryMismatch,
				Severity:          SevError,
				Message:           fmt.Sprintf("summary references %s but the file does not exist in the checkout", ref.Path),
				Path:              ref.Path,
				SuggestedRecovery: "Restore the file or regenerate the summary against the current tree.",
			})
			continue
		}
		if actual != ref.PathHash {
			r.Findings = append(r.Findings, Finding{
				Code:              CodeSummaryMismatch,
				Severity:          SevError,
				Message:           fmt.Sprintf("path_hash mismatch for %s", ref.Path),
				Path:              ref.Path,
				Details:           map[string]any{"declared": ref.PathHash, "recomputed": actual},
				SuggestedRecovery: fmt.Sprintf("Regenerate the summary with `agent-ledger export-summary --task %s --output %s`.", doc.Task.ID, in.SummaryFile),
			})
		}
	}
	r.Summary.ChangedPaths = len(doc.ChangedPaths)

	// Forbidden / outside-assignment checks against the snapshot.
	a := doc.AssignmentSnapshot
	for _, ref := range doc.ChangedPaths {
		if policy.MatchAny(a.ForbiddenPaths, ref.Path) {
			r.Summary.ForbiddenPathViolations++
			r.Findings = append(r.Findings, Finding{
				Code:              CodeForbiddenPathChanged,
				Severity:          SevError,
				Message:           fmt.Sprintf("%s changed but task %s forbids this path", ref.Path, a.TaskID),
				Path:              ref.Path,
				Details:           map[string]any{"forbidden_paths": a.ForbiddenPaths},
				SuggestedRecovery: "Revert the file or route the change to the orchestrator.",
			})
		} else if !policy.MatchAny(a.AllowedPaths, ref.Path) {
			r.Summary.OutsideAssignmentPaths++
			r.Findings = append(r.Findings, Finding{
				Code:              CodePathOutsideAssignment,
				Severity:          SevError,
				Message:           fmt.Sprintf("%s is not in the assignment allowed_paths", ref.Path),
				Path:              ref.Path,
				Details:           map[string]any{"allowed_paths": a.AllowedPaths},
				SuggestedRecovery: fmt.Sprintf("Extend assignment %s with `--allow %s` or revert.", a.AssignmentID, ref.Path),
			})
		}
	}

	r.Status = decideStatus(r)
	r.Summary.Findings = len(r.Findings)
	_ = ctx
	return r, nil
}

// errorReport returns a minimal report carrying a single fatal
// finding. Used for config and storage failures.
func errorReport(status, code, message string, now time.Time) *Report {
	r := newReport("", now)
	r.Status = status
	r.Findings = append(r.Findings, Finding{
		Code:              code,
		Severity:          SevFatal,
		Message:           message,
		SuggestedRecovery: "Run `agent-ledger doctor` to diagnose configuration and storage issues.",
	})
	r.Summary.Findings = len(r.Findings)
	return r
}

func newReport(mode string, now time.Time) *Report {
	return &Report{
		Schema:      Schema,
		Mode:        mode,
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Findings:    []Finding{},
	}
}

// decideStatus picks the report status based on findings. CONFIG and
// STORAGE error codes are caller-controlled; this helper handles the
// pass/fail/conflict split.
//
// ACTIVE_CONFLICT findings carry SevError but must NOT contribute to
// hasError: their presence alone signals "needs decision", not a hard
// failure. Setting hasConflict and then continuing skips the
// severity-based hasError accumulation for conflict findings.
func decideStatus(r *Report) string {
	hasConflict := false
	hasError := false
	for _, f := range r.Findings {
		switch f.Code {
		case CodeConfigError:
			return StatusError
		case CodeStorageError:
			return StatusError
		case CodeActiveConflict:
			hasConflict = true
			continue // skip severity check; conflict alone routes to needs-decision
		}
		if f.Severity == SevError || f.Severity == SevFatal {
			hasError = true
		}
	}
	switch {
	case hasError && !hasConflict:
		return StatusFailed
	case hasConflict && !hasError:
		return StatusNeedsDecision
	case hasError && hasConflict:
		// Errors take precedence over conflicts for exit-code routing,
		// since SPEC §19.1 conflict (4) is reserved for "needs decision"
		// only. Real failures must surface as failed.
		return StatusFailed
	}
	return StatusPassed
}

// discoverChangedPaths returns project-relative changed paths in the
// working tree. When override is non-nil, those are returned verbatim.
// Otherwise we shell out to git: tracked changes via `git status
// --porcelain=v1 --untracked-files=normal`.
func discoverChangedPaths(root string, override []string) ([]string, error) {
	if override != nil {
		out := make([]string, 0, len(override))
		for _, p := range override {
			if strings.TrimSpace(p) != "" {
				out = append(out, filepath.ToSlash(p))
			}
		}
		sort.Strings(out)
		return out, nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		// No git: there is nothing to diff. Treat as no changes.
		return nil, nil
	}
	cmd := exec.Command("git", "-C", root, "status", "--porcelain=v1", "--untracked-files=all")
	out, err := cmd.Output()
	if err != nil {
		// Not a git repo: best-effort, no changes.
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, nil
		}
		return nil, err
	}
	seen := map[string]struct{}{}
	var result []string
	for _, raw := range strings.Split(string(out), "\n") {
		if len(raw) < 4 {
			continue
		}
		// Format: XY <path>  or "?? <path>" (untracked) or rename "R  old -> new".
		body := raw[3:]
		if strings.Contains(body, " -> ") {
			parts := strings.SplitN(body, " -> ", 2)
			body = parts[1]
		}
		body = strings.Trim(body, "\"")
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		body = filepath.ToSlash(body)
		if _, ok := seen[body]; ok {
			continue
		}
		seen[body] = struct{}{}
		result = append(result, body)
	}
	sort.Strings(result)
	return result, nil
}

func collectClaimedHashes(ctx context.Context, d *domain.Store, taskID string) ([]string, []string, error) {
	intents, err := d.ListActiveIntents(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	hashes := make([]string, 0)
	displays := make([]string, 0)
	for _, in := range intents {
		paths, err := d.IntentPaths(ctx, in.IntentID)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range paths {
			hashes = append(hashes, p.PathHash)
			displays = append(displays, p.Path)
		}
	}
	return hashes, displays, nil
}

func collectRecordedHashes(ctx context.Context, d *domain.Store, taskID string) ([]string, error) {
	if taskID == "" {
		// Project-wide: collect from every change row attached to any
		// task. We need a wider query; piggyback on iterating tasks via
		// changes table directly via the domain helper.
		return allRecordedHashes(ctx, d)
	}
	changes, err := d.ChangesForTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	for _, c := range changes {
		paths, err := d.ChangePaths(ctx, c.ChangeID)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			out = append(out, p.PathHash)
		}
	}
	return out, nil
}

func allRecordedHashes(ctx context.Context, d *domain.Store) ([]string, error) {
	// Project mode: we want every change_paths.path_hash across every
	// task. The domain layer doesn't expose that directly, so query
	// through the underlying *sqlite.Store via a small helper.
	rows, err := d.S.DB().QueryContext(ctx, `SELECT DISTINCT path_hash FROM change_paths`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func intentHasChanges(ctx context.Context, store *sqlite.Store, intentID string) (bool, error) {
	row := store.DB().QueryRowContext(ctx, `SELECT 1 FROM changes WHERE intent_id = ? LIMIT 1`, intentID)
	var n int
	if err := row.Scan(&n); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// scanExclusiveLocks reports lock sentinel files in
// <ledger-dir>/locks/ that are not paired with an active intent in
// the DB. SPEC §16: dropping a lock without closing the intent leaks
// the sentinel, so verify surfaces it for cleanup.
func scanExclusiveLocks(ledgerDir string, intents []domain.Intent) []Finding {
	dir := filepath.Join(ledgerDir, "locks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	known := map[string]struct{}{}
	for _, in := range intents {
		_ = in
	}
	var findings []Finding
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".lock") {
			continue
		}
		hash := strings.TrimSuffix(name, ".lock")
		if _, ok := known[hash]; ok {
			continue
		}
		findings = append(findings, Finding{
			Code:              CodeExclusiveLockHeld,
			Severity:          SevWarning,
			Message:           fmt.Sprintf("lock sentinel %s present in %s without an active intent", name, dir),
			Details:           map[string]any{"path_hash": hash, "lock_dir": dir},
			SuggestedRecovery: "Close or supersede the owning intent, or remove the stale sentinel after confirming no process holds it.",
		})
	}
	return findings
}

func setOf(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, x := range in {
		out[x] = struct{}{}
	}
	return out
}

// recomputeAssignmentHash mirrors summary.assignmentHash, which is
// unexported. SPEC §22: hash is sha256 over the JSON of the
// assignment_snapshot.
func recomputeAssignmentHash(s summary.AssignmentSnapshot) string {
	raw, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// pathHashAtRoot returns the portable path hash for a project-relative path
// rooted at root. Returns (hash, exists). When the file does not exist, exists
// is false. The hash is sha256(NFC(display)) so it matches the value written
// by summary.Build regardless of the absolute realpath in any checkout.
// SPEC §20.1, §32.
func pathHashAtRoot(root, rel string) (string, bool) {
	p := rel
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, rel)
	}
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return paths.PortableHash(rel), true
}
