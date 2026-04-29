package domain_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
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

func TestMetadataDecodeError_StrictObjectValidation(t *testing.T) {
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
		TaskID:         "T-metadata-strict",
		OrchestratorID: "orch",
		AllowedPaths:   []string{"**"},
		ConflictPolicy: "warn",
		Reason:         "metadata strictness",
		Status:         "active",
		Metadata:       map[string]any{"ok": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		raw       string
		wantErr   bool
		wantCause string
		checkRaw  bool
	}{
		{name: "trailing-junk", raw: `{"ok":true} trailing-junk`, wantErr: true},
		{name: "concatenated-values", raw: `{"ok":true}{"other":false}`, wantErr: true},
		{name: "null", raw: `null`, wantErr: true, wantCause: "expected JSON object, got null"},
		{name: "array", raw: `[1,2]`, wantErr: true, wantCause: "expected JSON object, got array"},
		{name: "scalar", raw: `42`, wantErr: true, wantCause: "expected JSON object, got number"},
		{name: "empty", raw: ``, wantErr: false},
		{name: "long-trailing-junk", raw: `{"ok":true}` + strings.Repeat("x", 240), wantErr: true, checkRaw: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.DB().ExecContext(ctx, `UPDATE assignments SET metadata_json = ? WHERE assignment_id = ?`, tc.raw, a.AssignmentID); err != nil {
				t.Fatal(err)
			}
			got, err := d.LatestActiveAssignmentForTask(ctx, "T-metadata-strict")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected MetadataDecodeError, got nil")
				}
				var mde *domain.MetadataDecodeError
				if !errors.As(err, &mde) {
					t.Fatalf("expected *MetadataDecodeError, got %T: %v", err, err)
				}
				if mde.Field != "assignments.metadata_json" {
					t.Fatalf("Field = %q, want assignments.metadata_json", mde.Field)
				}
				if mde.RowID != a.AssignmentID {
					t.Fatalf("RowID = %q, want %q", mde.RowID, a.AssignmentID)
				}
				if mde.Raw == "" {
					t.Fatal("Raw should carry the offending payload")
				}
				if tc.checkRaw {
					if len(mde.Raw) != 203 {
						t.Fatalf("Raw length = %d, want 203", len(mde.Raw))
					}
					if !strings.HasSuffix(mde.Raw, "...") {
						t.Fatalf("Raw should be truncated with ellipsis, got %q", mde.Raw)
					}
				}
				if tc.wantCause != "" && !strings.Contains(mde.Error(), tc.wantCause) {
					t.Fatalf("error = %q, want cause %q", mde.Error(), tc.wantCause)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for empty metadata: %v", err)
			}
			if len(got.Metadata) != 0 {
				t.Fatalf("expected empty metadata map, got %v", got.Metadata)
			}
		})
	}
}

func TestPathsDecodeError_PropagatesFromAssignmentReaders(t *testing.T) {
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
		TaskID:         "T-paths-strict",
		OrchestratorID: "orch",
		AllowedPaths:   []string{"**"},
		ForbiddenPaths: []string{"secret.md"},
		ConflictPolicy: "warn",
		Reason:         "paths strictness",
		Status:         "active",
		Metadata:       map[string]any{"ok": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.DB().ExecContext(ctx, `UPDATE assignments SET allowed_paths_json = ? WHERE assignment_id = ?`, `{"bad":true}`, a.AssignmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.LatestActiveAssignmentForTask(ctx, "T-paths-strict"); err == nil {
		t.Fatal("expected PathsDecodeError for allowed_paths_json, got nil")
	} else {
		var pde *domain.PathsDecodeError
		if !errors.As(err, &pde) {
			t.Fatalf("expected *PathsDecodeError, got %T: %v", err, err)
		}
		if pde.Field != "assignments.allowed_paths_json" {
			t.Fatalf("Field = %q, want assignments.allowed_paths_json", pde.Field)
		}
		if pde.RowID != a.AssignmentID {
			t.Fatalf("RowID = %q, want %q", pde.RowID, a.AssignmentID)
		}
		if pde.Raw == "" {
			t.Fatal("Raw should carry the offending payload")
		}
	}

	if _, err := store.DB().ExecContext(ctx, `UPDATE assignments SET allowed_paths_json = ?, forbidden_paths_json = ? WHERE assignment_id = ?`, `["**"]`, `null`, a.AssignmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.LatestActiveAssignmentForTask(ctx, "T-paths-strict"); err == nil {
		t.Fatal("expected PathsDecodeError for forbidden_paths_json, got nil")
	} else {
		var pde *domain.PathsDecodeError
		if !errors.As(err, &pde) {
			t.Fatalf("expected *PathsDecodeError, got %T: %v", err, err)
		}
		if pde.Field != "assignments.forbidden_paths_json" {
			t.Fatalf("Field = %q, want assignments.forbidden_paths_json", pde.Field)
		}
		if pde.RowID != a.AssignmentID {
			t.Fatalf("RowID = %q, want %q", pde.RowID, a.AssignmentID)
		}
		if pde.Raw == "" {
			t.Fatal("Raw should carry the offending payload")
		}
	}
}
