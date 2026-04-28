package domain_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

// TestMetadataDecodeError_PropagatesFromAssignmentReaders verifies
// that a corrupted metadata_json column on an assignment row surfaces
// as a typed *domain.MetadataDecodeError through the assignment-reader
// API surface, instead of being silently swallowed and replaced with
// an empty map.
//
// Pre-v0.1.3 the kernel returned an empty map on decode failure,
// hiding ledger corruption from reviewers. v0.1.3 makes the failure
// observable with detection via errors.As.
func TestMetadataDecodeError_PropagatesFromAssignmentReaders(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlite.Open(context.Background(), filepath.Join(dir, "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Migrator().Up(ctx); err != nil {
		t.Fatal(err)
	}
	d := domain.New(store)

	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "orch", AgentKind: "orchestrator"}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "worker", AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	a, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "T-corrupt",
		OrchestratorID:  "orch",
		AssignedAgentID: "worker",
		AllowedPaths:    []string{"**"},
		ConflictPolicy:  "warn",
		Reason:          "corruption test",
		Status:          "active",
		Metadata:        map[string]any{"valid": "for now"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the metadata_json column directly via the underlying DB.
	// This simulates a ledger that suffered partial-write corruption,
	// a manual SQL edit gone wrong, or a future schema mismatch.
	if _, err := store.DB().ExecContext(ctx,
		`UPDATE assignments SET metadata_json = '{not json' WHERE assignment_id = ?`,
		a.AssignmentID,
	); err != nil {
		t.Fatal(err)
	}

	// LatestActiveAssignmentForTask should now surface the typed error.
	_, err = d.LatestActiveAssignmentForTask(ctx, "T-corrupt")
	if err == nil {
		t.Fatal("expected MetadataDecodeError from LatestActiveAssignmentForTask, got nil")
	}
	var mde *domain.MetadataDecodeError
	if !errors.As(err, &mde) {
		t.Fatalf("expected *MetadataDecodeError, got %T: %v", err, err)
	}
	if mde.Field != "assignments.metadata_json" {
		t.Errorf("Field = %q, want assignments.metadata_json", mde.Field)
	}
	if mde.RowID != a.AssignmentID {
		t.Errorf("RowID = %q, want %q", mde.RowID, a.AssignmentID)
	}
	if mde.Raw == "" {
		t.Errorf("Raw should carry the truncated payload, got empty")
	}

	// LatestActiveAssignmentForTaskAndAgent should also surface it.
	_, err = d.LatestActiveAssignmentForTaskAndAgent(ctx, "T-corrupt", "worker")
	if err == nil || !errors.As(err, &mde) {
		t.Fatalf("expected MetadataDecodeError from LatestActiveAssignmentForTaskAndAgent, got %v", err)
	}

	// ListAssignments should fail loudly (one corrupt row halts the
	// iteration so the caller sees ledger corruption rather than a
	// silently-truncated result set).
	_, err = d.ListAssignments(ctx, domain.AssignmentFilter{TaskID: "T-corrupt"})
	if err == nil || !errors.As(err, &mde) {
		t.Fatalf("expected MetadataDecodeError from ListAssignments, got %v", err)
	}
}

// TestMetadataDecodeError_EmptyMetadataIsNotAnError confirms the
// pre-v0.1.3 happy path still works: missing or empty metadata_json
// returns an empty map without raising MetadataDecodeError.
func TestMetadataDecodeError_EmptyMetadataIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlite.Open(context.Background(), filepath.Join(dir, "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Migrator().Up(ctx); err != nil {
		t.Fatal(err)
	}
	d := domain.New(store)
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "orch", AgentKind: "orchestrator"}); err != nil {
		t.Fatal(err)
	}
	a, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:         "T-empty",
		OrchestratorID: "orch",
		AllowedPaths:   []string{"**"},
		ConflictPolicy: "warn",
		Reason:         "empty metadata",
		Status:         "active",
		Metadata:       nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.LatestActiveAssignmentForTask(ctx, "T-empty")
	if err != nil {
		t.Fatalf("unexpected error reading empty-metadata assignment: %v", err)
	}
	if got.AssignmentID != a.AssignmentID {
		t.Errorf("got %q, want %q", got.AssignmentID, a.AssignmentID)
	}
}
