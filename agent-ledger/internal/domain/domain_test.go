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

	_, _, newIntent, err := d.ResolveAndInsertIntent(ctx, domain.Intent{
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
	if newIntent.IntentID == "" {
		t.Fatal("missing created intent")
	}
	if got, want := newIntent.Metadata["superseded_intent_id"], oldIntent.IntentID; got != want {
		t.Fatalf("new intent metadata superseded_intent_id=%v want %v", got, want)
	}

	decision, overlaps, _, err := d.ResolveAndInsertIntent(ctx, domain.Intent{
		TaskID:         "task-1",
		AgentID:        "agent-blocked",
		AccessMode:     domain.AccessWrite,
		ConflictPolicy: domain.PolicyExclusive,
		Reason:         "block path",
	}, []domain.IntentPath{path}, domain.PolicyExclusive, false, "")
	if err != nil {
		t.Fatalf("blocked probe: %v", err)
	}
	if decision != conflicts.Block || len(overlaps) == 0 {
		t.Fatalf("expected block on second active claim, got decision=%v overlaps=%d", decision, len(overlaps))
	}

	updatedOld, err := d.IntentByID(ctx, oldIntent.IntentID)
	if err != nil {
		t.Fatalf("load old intent: %v", err)
	}
	if updatedOld.Status != domain.IntentClosed || updatedOld.CloseOutcome != domain.OutcomeSuperseded {
		t.Fatalf("old intent not superseded: status=%s outcome=%s", updatedOld.Status, updatedOld.CloseOutcome)
	}
	if got := updatedOld.Metadata["superseded_by"]; got != newIntent.IntentID {
		t.Fatalf("old intent metadata superseded_by=%v want %s", got, newIntent.IntentID)
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
	if got["superseded_by"] != newIntent.IntentID {
		t.Fatalf("supersede payload superseded_by=%v want %s", got["superseded_by"], newIntent.IntentID)
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
