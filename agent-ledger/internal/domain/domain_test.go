package domain_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/conflicts"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/privacy"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
	sqlitedrv "modernc.org/sqlite"
)

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ledger")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Migrator().Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

const unsafeReason = "token ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA is embedded here"

// TestInsertAssignment_UnsafeReason verifies that InsertAssignment returns
// ErrUnsafeReason (detectable via errors.Is) when the reason string
// contains a pattern that fails the privacy safety check. RV2-005
// (wv1-rv-s03): programmatic callers that bypass the CLI guard must
// still receive the typed sentinel so they can map it to ExitConfigError.
func TestInsertAssignment_UnsafeReason(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	_, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:         "T1",
		OrchestratorID: "orch",
		AllowedPaths:   []string{"src/**"},
		ConflictPolicy: domain.PolicyWarn,
		// A GitHub fine-grained PAT triggers the privacy check.
		// The pattern is (?i)\bghp_[A-Za-z0-9]{30,}.
		Reason: unsafeReason,
	})
	if err == nil {
		t.Fatal("expected error for unsafe reason, got nil")
	}
	if !errors.Is(err, domain.ErrUnsafeReason) {
		t.Fatalf("expected errors.Is(err, domain.ErrUnsafeReason) to be true, got err=%v", err)
	}
}

// TestInsertAssignment_UnsafeReason_PreservesPrivacyType asserts that the
// underlying *privacy.SecretError is reachable via errors.As after the wrap.
// The previous fmt.Errorf("%w: %s", ...) formatted the privacy error as a
// plain string, stripping the typed error from the chain. errors.Join
// preserves both errors so callers can inspect the privacy error type.
// References: finding rv2-new-003, packet RV3-003.
func TestInsertAssignment_UnsafeReason_PreservesPrivacyType(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	_, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:         "T1",
		OrchestratorID: "orch",
		AllowedPaths:   []string{"src/**"},
		ConflictPolicy: domain.PolicyWarn,
		Reason:         unsafeReason,
	})
	assertUnsafeReasonError(t, err, "assignment.reason")
	if strings.Contains(err.Error(), "\n") {
		t.Fatalf("err.Error() should be single line, got %q", err.Error())
	}
}

// TestInsertIntent_UnsafeReason mirrors TestInsertAssignment_UnsafeReason
// for InsertIntent. RV2-005 (wv1-rv-s03).
func TestInsertIntent_UnsafeReason(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	_, err := d.InsertIntent(ctx, domain.Intent{
		TaskID:         "T1",
		AgentID:        "agent-1",
		AccessMode:     domain.AccessWrite,
		ConflictPolicy: domain.PolicyWarn,
		Reason:         unsafeReason,
	}, nil)
	if err == nil {
		t.Fatal("expected error for unsafe reason, got nil")
	}
	if !errors.Is(err, domain.ErrUnsafeReason) {
		t.Fatalf("expected errors.Is(err, domain.ErrUnsafeReason) to be true, got err=%v", err)
	}
}

// TestInsertIntent_UnsafeReason_PreservesPrivacyType mirrors
// TestInsertAssignment_UnsafeReason_PreservesPrivacyType for InsertIntent.
// References: finding rv2-new-003, packet RV3-003.
func TestInsertIntent_UnsafeReason_PreservesPrivacyType(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	_, err := d.InsertIntent(ctx, domain.Intent{
		TaskID:         "T1",
		AgentID:        "agent-1",
		AccessMode:     domain.AccessWrite,
		ConflictPolicy: domain.PolicyWarn,
		Reason:         unsafeReason,
	}, nil)
	assertUnsafeReasonError(t, err, "intent.reason")
}

func assertUnsafeReasonError(t *testing.T, err error, wantLabel string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, domain.ErrUnsafeReason) {
		t.Fatalf("errors.Is(ErrUnsafeReason)=false; err=%v", err)
	}
	var typed *privacy.SecretError
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As(*SecretError)=false; err=%v", err)
	}
	if typed.Label != wantLabel {
		t.Fatalf("SecretError.Label=%q want %q", typed.Label, wantLabel)
	}
}

func TestResolveAndInsertIntent_SupersedeBackReference(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "agent-old", AgentKind: "worker"}); err != nil {
		t.Fatalf("upsert old agent: %v", err)
	}
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "agent-new", AgentKind: "worker"}); err != nil {
		t.Fatalf("upsert new agent: %v", err)
	}
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "agent-blocked", AgentKind: "worker"}); err != nil {
		t.Fatalf("upsert blocked agent: %v", err)
	}

	path := domain.IntentPath{Path: "src/app.go", RealPath: "/tmp/src/app.go", PathHash: "hash-app", AccessMode: domain.AccessWrite}
	oldIntent, err := d.InsertIntent(ctx, domain.Intent{
		TaskID:         "task-1",
		AgentID:        "agent-old",
		AccessMode:     domain.AccessWrite,
		ConflictPolicy: domain.PolicyExclusive,
		Reason:         "old claim",
	}, []domain.IntentPath{path})
	if err != nil {
		t.Fatalf("insert old intent: %v", err)
	}

	res, err := d.ResolveAndInsertIntent(ctx, domain.Intent{
		TaskID:         "task-1",
		AgentID:        "agent-new",
		AccessMode:     domain.AccessWrite,
		ConflictPolicy: domain.PolicyExclusive,
		Reason:         "replace claim",
		Metadata: map[string]any{
			"superseded_intent_id": oldIntent.IntentID,
		},
	}, []domain.IntentPath{path}, domain.PolicyExclusive, false, oldIntent.IntentID)
	if err != nil {
		t.Fatalf("resolve+insert: %v", err)
	}
	if res.Intent.IntentID == "" {
		t.Fatal("missing created intent")
	}
	if got, want := res.Intent.Metadata["superseded_intent_id"], oldIntent.IntentID; got != want {
		t.Fatalf("new intent metadata superseded_intent_id=%v want %v", got, want)
	}

	blocked, err := d.ResolveAndInsertIntent(ctx, domain.Intent{
		TaskID:         "task-1",
		AgentID:        "agent-blocked",
		AccessMode:     domain.AccessWrite,
		ConflictPolicy: domain.PolicyExclusive,
		Reason:         "block path",
	}, []domain.IntentPath{path}, domain.PolicyExclusive, false, "")
	if err != nil {
		t.Fatalf("blocked probe: %v", err)
	}
	if blocked.Decision != conflicts.Block || len(blocked.Overlaps) == 0 {
		t.Fatalf("expected block on second active claim, got decision=%v overlaps=%d", blocked.Decision, len(blocked.Overlaps))
	}

	updatedOld, err := d.IntentByID(ctx, oldIntent.IntentID)
	if err != nil {
		t.Fatalf("load old intent: %v", err)
	}
	if updatedOld.Status != domain.IntentClosed || updatedOld.CloseOutcome != domain.OutcomeSuperseded {
		t.Fatalf("old intent not superseded: status=%s outcome=%s", updatedOld.Status, updatedOld.CloseOutcome)
	}
	if got := updatedOld.Metadata["superseded_by"]; got != res.Intent.IntentID {
		t.Fatalf("old intent metadata superseded_by=%v want %s", got, res.Intent.IntentID)
	}

	var count int
	if err := s.DB().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM events
		WHERE event_type = 'intent.superseded'
	`).Scan(&count); err != nil {
		t.Fatalf("count supersede events: %v", err)
	}
	if count != 1 {
		t.Fatalf("intent.superseded rows = %d, want 1", count)
	}

	var payload string
	if err := s.DB().QueryRowContext(ctx, `
		SELECT payload_json
		FROM events
		WHERE event_type = 'intent.superseded'
		ORDER BY created_at DESC
		LIMIT 1
	`).Scan(&payload); err != nil {
		t.Fatalf("load supersede event: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("decode supersede payload: %v", err)
	}
	if got["intent_id"] != oldIntent.IntentID {
		t.Fatalf("supersede payload intent_id=%v want %s", got["intent_id"], oldIntent.IntentID)
	}
	if got["superseded_by"] != res.Intent.IntentID {
		t.Fatalf("supersede payload superseded_by=%v want %s", got["superseded_by"], res.Intent.IntentID)
	}
}

func TestResolveAndInsertIntent_SupersedeStaleTarget_ReturnsErrSupersedeNotActive(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "agent-new", AgentKind: "worker"}); err != nil {
		t.Fatalf("upsert new agent: %v", err)
	}

	var before int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE event_type = 'intent.superseded'`).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	newIntentID := "int_stale_target_new"
	_, err := d.ResolveAndInsertIntent(ctx, domain.Intent{
		IntentID:       newIntentID,
		EventID:        "evt_stale_target_new",
		TaskID:         "task-1",
		AgentID:        "agent-new",
		AccessMode:     domain.AccessWrite,
		ConflictPolicy: domain.PolicyExclusive,
		Reason:         "replace missing claim",
	}, nil, domain.PolicyExclusive, false, "int_does_not_exist")
	if !errors.Is(err, domain.ErrSupersedeNotActive) {
		t.Fatalf("expected ErrSupersedeNotActive, got %v", err)
	}

	var after int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE event_type = 'intent.superseded'`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("intent.superseded rows changed: before=%d after=%d", before, after)
	}

	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM intents WHERE intent_id = ?`, newIntentID).Scan(&after); err != nil {
		t.Fatalf("count new intent: %v", err)
	}
	if after != 0 {
		t.Fatalf("new intent row written unexpectedly: %d", after)
	}
}

func TestInsertAssignmentUniqueDetectionIsScopedToTaskAgentIndex(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	base := domain.Assignment{
		AssignmentID:    "asg-1",
		EventID:         "evt-1",
		TaskID:          "task-1",
		OrchestratorID:  "orch-1",
		AssignedAgentID: "agent-1",
		AllowedPaths:    []string{"src/**"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "ok",
	}
	if _, err := d.InsertAssignment(ctx, base); err != nil {
		t.Fatalf("insert base: %v", err)
	}

	do := base
	do.AssignmentID = "asg-2"
	do.EventID = "evt-2"
	if _, err := d.InsertAssignment(ctx, do); !errors.Is(err, domain.ErrAssignmentExists) {
		t.Fatalf("expected ErrAssignmentExists for duplicate active task/agent, got %v", err)
	}

	var se *sqlitedrv.Error
	if _, err := d.InsertAssignment(ctx, do); !errors.As(err, &se) || se.Code() != 2067 {
		t.Fatalf("expected sqlite unique error 2067 for duplicate active row, got %v", err)
	}

	pk := domain.Assignment{
		AssignmentID:    "asg-1",
		EventID:         "evt-3",
		TaskID:          "task-2",
		OrchestratorID:  "orch-2",
		AssignedAgentID: "agent-2",
		AllowedPaths:    []string{"src/**"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "ok",
	}
	if _, err := d.InsertAssignment(ctx, pk); err == nil {
		t.Fatal("expected primary-key collision")
	} else if errors.Is(err, domain.ErrAssignmentExists) {
		t.Fatalf("primary-key collision should not map to ErrAssignmentExists: %v", err)
	}
}

func TestSupersedeAndInsertAssignment_HappyPath(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	base, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "task-upd",
		OrchestratorID:  "orch-1",
		AssignedAgentID: "agent-1",
		AllowedPaths:    []string{"src/foo.py"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "initial scope",
	})
	if err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	res, err := d.SupersedeAndInsertAssignment(ctx, domain.AssignmentUpdateInput{
		TaskID:          "task-upd",
		AssignedAgentID: "agent-1",
		AddAllowedPaths: []string{"src/bar.py", "src/foo.py"},
		Reason:          "extend for continuation",
	})
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if res.Reused {
		t.Fatal("expected Reused=false on real change")
	}
	if res.Assignment.AssignmentID == base.AssignmentID {
		t.Fatal("expected new assignment_id to differ from prior")
	}
	if got, want := res.PriorAssignmentID, base.AssignmentID; got != want {
		t.Fatalf("PriorAssignmentID=%s want %s", got, want)
	}

	wantAllowed := []string{"src/foo.py", "src/bar.py"}
	if !stringSliceEqual(res.Assignment.AllowedPaths, wantAllowed) {
		t.Fatalf("allowed_paths=%v want %v", res.Assignment.AllowedPaths, wantAllowed)
	}
	if len(res.Assignment.ForbiddenPaths) != 0 {
		t.Fatalf("forbidden_paths=%v want empty (additive update only touches allowed_paths)", res.Assignment.ForbiddenPaths)
	}
	if got := res.Assignment.Metadata["superseded_assignment_id"]; got != base.AssignmentID {
		t.Fatalf("new metadata.superseded_assignment_id=%v want %s", got, base.AssignmentID)
	}

	// Prior row marked superseded with back-reference.
	historic, err := d.ListAssignments(ctx, domain.AssignmentFilter{TaskID: "task-upd", Status: "all", Limit: 10})
	if err != nil {
		t.Fatalf("list historic: %v", err)
	}
	var prior *domain.Assignment
	for i := range historic {
		if historic[i].AssignmentID == base.AssignmentID {
			prior = &historic[i]
			break
		}
	}
	if prior == nil {
		t.Fatal("prior assignment row not found in historic list")
	}
	if prior.Status != "superseded" {
		t.Fatalf("prior status=%q want superseded", prior.Status)
	}
	if got := prior.Metadata["superseded_by"]; got != res.Assignment.AssignmentID {
		t.Fatalf("prior metadata.superseded_by=%v want %s", got, res.Assignment.AssignmentID)
	}

	// Both events emitted with the right payloads. The events table
	// does not have an assignment_id column; carry the lineage in the
	// payload and assert it via JSON extraction.
	var supCount, assignedCount int
	if err := s.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events
		 WHERE event_type = 'assignment.superseded'
		   AND task_id = ?
		   AND json_extract(payload_json, '$.assignment_id') = ?
		   AND json_extract(payload_json, '$.superseded_by') = ?`,
		"task-upd", base.AssignmentID, res.Assignment.AssignmentID,
	).Scan(&supCount); err != nil {
		t.Fatalf("count assignment.superseded: %v", err)
	}
	if supCount != 1 {
		t.Fatalf("assignment.superseded count = %d, want 1", supCount)
	}
	if err := s.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events
		 WHERE event_type = 'task.assigned'
		   AND task_id = ?
		   AND json_extract(payload_json, '$.assignment_id') = ?
		   AND json_extract(payload_json, '$.superseded_assignment_id') = ?`,
		"task-upd", res.Assignment.AssignmentID, base.AssignmentID,
	).Scan(&assignedCount); err != nil {
		t.Fatalf("count task.assigned: %v", err)
	}
	if assignedCount != 1 {
		t.Fatalf("task.assigned (new row) count = %d, want 1", assignedCount)
	}

	// Latest active for (task, agent) is the new row.
	latest, err := d.LatestActiveAssignmentForTaskAndAgent(ctx, "task-upd", "agent-1")
	if err != nil {
		t.Fatalf("latest active: %v", err)
	}
	if latest.AssignmentID != res.Assignment.AssignmentID {
		t.Fatalf("latest active=%s want %s", latest.AssignmentID, res.Assignment.AssignmentID)
	}
}

func TestSupersedeAndInsertAssignment_Idempotent(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	base, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "task-noop",
		OrchestratorID:  "orch",
		AssignedAgentID: "agent",
		AllowedPaths:    []string{"src/foo.py", "src/bar.py"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "initial",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	var supBefore, assignedBefore int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE event_type='assignment.superseded'`).Scan(&supBefore); err != nil {
		t.Fatalf("count assignment.superseded (before): %v", err)
	}
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE event_type='task.assigned'`).Scan(&assignedBefore); err != nil {
		t.Fatalf("count task.assigned (before): %v", err)
	}

	res, err := d.SupersedeAndInsertAssignment(ctx, domain.AssignmentUpdateInput{
		TaskID:          "task-noop",
		AssignedAgentID: "agent",
		AddAllowedPaths: []string{"src/foo.py"}, // already present
		Reason:          "ensure-shape script",
	})
	if err != nil {
		t.Fatalf("idempotent call: %v", err)
	}
	if !res.Reused {
		t.Fatal("expected Reused=true on no-op")
	}
	if res.Assignment.AssignmentID != base.AssignmentID {
		t.Fatalf("Reused returned new id %s want prior %s", res.Assignment.AssignmentID, base.AssignmentID)
	}
	if res.PriorAssignmentID != base.AssignmentID {
		t.Fatalf("PriorAssignmentID=%s want %s", res.PriorAssignmentID, base.AssignmentID)
	}

	var supAfter, assignedAfter int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE event_type='assignment.superseded'`).Scan(&supAfter); err != nil {
		t.Fatalf("count assignment.superseded (after): %v", err)
	}
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE event_type='task.assigned'`).Scan(&assignedAfter); err != nil {
		t.Fatalf("count task.assigned (after): %v", err)
	}
	if supAfter != supBefore {
		t.Fatalf("assignment.superseded events changed on no-op: before=%d after=%d", supBefore, supAfter)
	}
	if assignedAfter != assignedBefore {
		t.Fatalf("task.assigned events changed on no-op: before=%d after=%d", assignedBefore, assignedAfter)
	}
}

func TestSupersedeAndInsertAssignment_NoActiveAssignment(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	_, err := d.SupersedeAndInsertAssignment(ctx, domain.AssignmentUpdateInput{
		TaskID:          "missing-task",
		AssignedAgentID: "missing-agent",
		AddAllowedPaths: []string{"src/foo.py"},
		Reason:          "no prior row",
	})
	if !errors.Is(err, domain.ErrNoActiveAssignment) {
		t.Fatalf("expected ErrNoActiveAssignment, got %v", err)
	}
}

func TestSupersedeAndInsertAssignment_UnsafeReason(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "task-unsafe",
		OrchestratorID:  "orch",
		AssignedAgentID: "agent",
		AllowedPaths:    []string{"src/foo.py"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "ok",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := d.SupersedeAndInsertAssignment(ctx, domain.AssignmentUpdateInput{
		TaskID:          "task-unsafe",
		AssignedAgentID: "agent",
		AddAllowedPaths: []string{"src/bar.py"},
		Reason:          unsafeReason,
	})
	if !errors.Is(err, domain.ErrUnsafeReason) {
		t.Fatalf("expected ErrUnsafeReason, got %v", err)
	}
	var se *privacy.SecretError
	if !errors.As(err, &se) {
		t.Fatalf("expected wrapped *SecretError, got %v", err)
	}
}

func TestSupersedeAndInsertAssignment_PreservesIntentLineage(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "agent-w", AgentKind: "worker"}); err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	base, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "task-lineage",
		OrchestratorID:  "orch",
		AssignedAgentID: "agent-w",
		AllowedPaths:    []string{"src/foo.py"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "initial",
	})
	if err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	intent, err := d.InsertIntent(ctx, domain.Intent{
		AssignmentID:   base.AssignmentID,
		TaskID:         "task-lineage",
		AgentID:        "agent-w",
		AccessMode:     domain.AccessWrite,
		ConflictPolicy: domain.PolicyWarn,
		Reason:         "claim foo",
	}, []domain.IntentPath{{Path: "src/foo.py", RealPath: "/tmp/src/foo.py", PathHash: "h-foo", AccessMode: domain.AccessWrite}})
	if err != nil {
		t.Fatalf("insert intent: %v", err)
	}

	res, err := d.SupersedeAndInsertAssignment(ctx, domain.AssignmentUpdateInput{
		TaskID:          "task-lineage",
		AssignedAgentID: "agent-w",
		AddAllowedPaths: []string{"src/bar.py"},
		Reason:          "extend allow",
	})
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}

	got, err := d.IntentByID(ctx, intent.IntentID)
	if err != nil {
		t.Fatalf("reload intent: %v", err)
	}
	if got.AssignmentID != base.AssignmentID {
		t.Fatalf("intent.assignment_id=%s want prior %s (must NOT auto-rebind to new %s)",
			got.AssignmentID, base.AssignmentID, res.Assignment.AssignmentID)
	}
	if got.Status != domain.IntentActive {
		t.Fatalf("intent.status=%q want active (supersede must not close intents)", got.Status)
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSupersedeAndInsertAssignment_RotatesAfterOutOfBandSupersede
// covers the case where the active row pointer rotates between
// successful calls: an out-of-band writer marks the prior active row
// superseded and inserts a replacement, and the helper then correctly
// operates on the replacement (not the original) row.
//
// The post-lookup race that ErrStaleUpdate guards against requires an
// in-callback interleaving that this external test cannot inject;
// supersedeAssignmentTx's zero-row branch is covered directly in the
// internal test (TestSupersedeAssignmentTxZeroRowsReturnsErrStaleUpdate).
func TestSupersedeAndInsertAssignment_RotatesAfterOutOfBandSupersede(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	base, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "task-stale",
		OrchestratorID:  "orch",
		AssignedAgentID: "agent",
		AllowedPaths:    []string{"src/foo.py"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "initial",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Out-of-band: mark the original active row superseded and insert
	// a fresh active replacement (row B). The helper's lookup then
	// sees row B, its UPDATE fires against row B, and it produces the
	// new active row with PriorAssignmentID = replacement.AssignmentID.
	// This proves the helper operates on the current active row and
	// does not regress when the pointer rotates between calls.
	if _, err := s.DB().ExecContext(ctx, `
		UPDATE assignments SET status = 'superseded', closed_at = ?
		WHERE assignment_id = ?
	`, "2026-05-12T02:00:00Z", base.AssignmentID); err != nil {
		t.Fatalf("manual supersede: %v", err)
	}
	replacement := domain.Assignment{
		TaskID:          "task-stale",
		OrchestratorID:  "orch",
		AssignedAgentID: "agent",
		AllowedPaths:    []string{"src/foo.py"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "manual replacement",
	}
	repl, err := d.InsertAssignment(ctx, replacement)
	if err != nil {
		t.Fatalf("manual replacement insert: %v", err)
	}

	res, err := d.SupersedeAndInsertAssignment(ctx, domain.AssignmentUpdateInput{
		TaskID:          "task-stale",
		AssignedAgentID: "agent",
		AddAllowedPaths: []string{"src/bar.py"},
		Reason:          "extend after rotate",
	})
	if err != nil {
		t.Fatalf("supersede after rotation: %v", err)
	}
	if res.PriorAssignmentID != repl.AssignmentID {
		t.Fatalf("PriorAssignmentID=%s want replacement %s (helper must operate on the current active row)",
			res.PriorAssignmentID, repl.AssignmentID)
	}
}

// TestSupersedeAndInsertAssignment_RejectsReservedMetadataKeys verifies
// that a caller cannot inject reserved lineage keys via ExtraMetadata.
// Without this guard a caller could set superseded_by="fake" on the
// new active row, breaking the SPEC §11.3.1 chain convention.
func TestSupersedeAndInsertAssignment_RejectsReservedMetadataKeys(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "task-meta",
		OrchestratorID:  "orch",
		AssignedAgentID: "agent",
		AllowedPaths:    []string{"src/foo.py"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "initial",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := d.SupersedeAndInsertAssignment(ctx, domain.AssignmentUpdateInput{
		TaskID:          "task-meta",
		AssignedAgentID: "agent",
		AddAllowedPaths: []string{"src/bar.py"},
		Reason:          "with hostile metadata",
		ExtraMetadata: map[string]any{
			"superseded_by":            "fake-id",
			"superseded_assignment_id": "another-fake",
			"updated_from":             "yet-another",
			"continuation":             true,
		},
	})
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if got := res.Assignment.Metadata["superseded_by"]; got != nil {
		t.Errorf("new row metadata.superseded_by=%v; reserved key must not be settable by callers", got)
	}
	if got := res.Assignment.Metadata["superseded_assignment_id"]; got != res.PriorAssignmentID {
		t.Errorf("new row metadata.superseded_assignment_id=%v want %s; helper must overwrite caller value",
			got, res.PriorAssignmentID)
	}
	if got := res.Assignment.Metadata["continuation"]; got != true {
		t.Errorf("benign caller metadata dropped: continuation=%v want true", got)
	}
}

// TestSupersedeAndInsertAssignment_OrchestratorChangeTriggersNewRow
// verifies that supplying a different --orchestrator alongside an
// already-present --add-allow value still produces a real update (a
// new active row, both events, changed=true). Without this guarantee
// the documented --orchestrator flag would silently drop on the
// idempotent path.
func TestSupersedeAndInsertAssignment_OrchestratorChangeTriggersNewRow(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	base, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "task-orch",
		OrchestratorID:  "orch-a",
		AssignedAgentID: "agent",
		AllowedPaths:    []string{"src/foo.py"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "initial",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := d.SupersedeAndInsertAssignment(ctx, domain.AssignmentUpdateInput{
		TaskID:          "task-orch",
		AssignedAgentID: "agent",
		OrchestratorID:  "orch-b",
		AddAllowedPaths: []string{"src/foo.py"}, // already present
		Reason:          "change orchestrator only",
	})
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if res.Reused {
		t.Fatal("expected Reused=false when --orchestrator changes; got Reused=true")
	}
	if res.Assignment.AssignmentID == base.AssignmentID {
		t.Fatalf("expected new assignment id, got prior %s", base.AssignmentID)
	}
	if res.Assignment.OrchestratorID != "orch-b" {
		t.Fatalf("new row orchestrator=%s want orch-b", res.Assignment.OrchestratorID)
	}
}

// TestSupersedeAndInsertAssignment_NonReservedMetadataChangeTriggersNewRow
// verifies that supplying a non-reserved metadata key whose value
// differs from the prior row produces a real update even when every
// --add-allow value is already present.
func TestSupersedeAndInsertAssignment_NonReservedMetadataChangeTriggersNewRow(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "task-meta-change",
		OrchestratorID:  "orch",
		AssignedAgentID: "agent",
		AllowedPaths:    []string{"src/foo.py"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "initial",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := d.SupersedeAndInsertAssignment(ctx, domain.AssignmentUpdateInput{
		TaskID:          "task-meta-change",
		AssignedAgentID: "agent",
		AddAllowedPaths: []string{"src/foo.py"}, // already present
		Reason:          "annotate continuation",
		ExtraMetadata:   map[string]any{"continuation": true},
	})
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if res.Reused {
		t.Fatal("expected Reused=false when non-reserved metadata changes")
	}
	if got := res.Assignment.Metadata["continuation"]; got != true {
		t.Errorf("new row metadata.continuation=%v want true", got)
	}
}

// TestSupersedeAndInsertAssignment_EmptyAddAllowRejected verifies the
// domain-level guard against an empty effective addition set. A direct
// domain caller that passes nil or all-whitespace AddAllowedPaths
// must get ErrEmptyAddAllowedPaths, not a Reused=true no-op.
func TestSupersedeAndInsertAssignment_EmptyAddAllowRejected(t *testing.T) {
	s := openStore(t)
	d := domain.New(s)
	ctx := context.Background()

	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "task-empty",
		OrchestratorID:  "orch",
		AssignedAgentID: "agent",
		AllowedPaths:    []string{"src/foo.py"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "initial",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for name, in := range map[string]domain.AssignmentUpdateInput{
		"nil_slice": {
			TaskID:          "task-empty",
			AssignedAgentID: "agent",
			Reason:          "no paths",
		},
		"whitespace_only": {
			TaskID:          "task-empty",
			AssignedAgentID: "agent",
			AddAllowedPaths: []string{"   ", "\t"},
			Reason:          "whitespace paths",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := d.SupersedeAndInsertAssignment(ctx, in)
			if !errors.Is(err, domain.ErrEmptyAddAllowedPaths) {
				t.Fatalf("expected ErrEmptyAddAllowedPaths, got %v", err)
			}
		})
	}
}
