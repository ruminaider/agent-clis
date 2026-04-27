package summary

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/paths"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/project"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

func setupSummaryTestStore(t *testing.T) (*sqlite.Store, func()) {
	t.Helper()
	ledger := t.TempDir()
	store, err := sqlite.Open(context.Background(), ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrator().Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store, func() { _ = store.Close() }
}

func seedSummaryChange(t *testing.T, store *sqlite.Store, root, taskID, path string) {
	t.Helper()
	ctx := context.Background()
	d := domain.New(store)
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "pi.worker.test", AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		AssignmentID:    "A1",
		TaskID:          taskID,
		OrchestratorID:  "pi.main.test",
		AssignedAgentID: "pi.worker.test",
		AllowedPaths:    []string{},
		ForbiddenPaths:  []string{},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "seed",
	}); err != nil {
		t.Fatal(err)
	}
	n, err := paths.Normalize(root, path)
	if err != nil {
		t.Fatal(err)
	}
	in, err := d.InsertIntent(ctx, domain.Intent{TaskID: taskID, AgentID: "pi.worker.test", AccessMode: domain.AccessWrite, ConflictPolicy: domain.PolicyWarn, Reason: "edit"}, []domain.IntentPath{{Path: n.Display, RealPath: n.RealPath, PathHash: n.PathHash, AccessMode: domain.AccessWrite}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertChange(ctx, domain.RecordChangeInput{Change: domain.Change{IntentID: in.IntentID, TaskID: taskID, AgentID: "pi.worker.test", Summary: "seed change"}, Paths: []domain.ChangePath{{Path: n.Display, RealPath: n.RealPath, PathHash: n.PathHash, Status: domain.PathStatusModified}}, EventType: "change.recorded"}); err != nil {
		t.Fatal(err)
	}
}

func TestBuild_PathHash_AppliesToSlashAtCallSite(t *testing.T) {
	t.Logf("runtime.GOOS=%s; FromSlash(\"a/b/c.txt\")=%q", runtime.GOOS, filepath.FromSlash("a/b/c.txt"))
	// On POSIX filepath.FromSlash is a no-op (a/b/c.txt == a/b/c.txt), so this
	// test is tautological there. On Windows it produces a\\b\\c.txt and catches
	// removal of filepath.ToSlash at internal/summary/summary.go:187.
	store, cleanup := setupSummaryTestStore(t)
	defer cleanup()
	seedSummaryChange(t, store, t.TempDir(), "T1", filepath.FromSlash("a/b/c.txt"))

	doc, err := Build(context.Background(), Inputs{Store: domain.New(store), TaskID: "T1", GeneratedAt: "2026-04-27T00:00:00Z", Identity: project.Identity{ProjectID: "P1", Slug: "demo", Fingerprint: "abc"}})
	if err != nil {
		t.Fatal(err)
	}
	expected := paths.PortableHash("a/b/c.txt")
	if len(doc.ChangedPaths) != 1 {
		t.Fatalf("expected 1 changed path, got %+v", doc.ChangedPaths)
	}
	if doc.ChangedPaths[0].PathHash != expected {
		t.Fatalf("expected path hash %q, got %q", expected, doc.ChangedPaths[0].PathHash)
	}
}

func TestMarshalDeterministic(t *testing.T) {
	d := Document{
		Schema:      "agent-ledger-summary.v1",
		GeneratedAt: "2026-04-27T00:00:00Z",
		Project:     ProjectInfo{Slug: "demo", Fingerprint: "abc"},
		Task:        TaskInfo{ID: "T-1"},
		AssignmentSnapshot: AssignmentSnapshot{
			TaskID:         "T-1",
			AllowedPaths:   []string{"a", "b"},
			ForbiddenPaths: []string{},
			ConflictPolicy: "warn",
		},
		ChangedPaths: []PathRef{},
		Changes:      []ChangeRef{},
		Validations:  []ValidationRef{},
	}
	a, err := Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("non-deterministic marshal: %s vs %s", a, b)
	}
	var v map[string]any
	if err := json.Unmarshal(a, &v); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if !strings.Contains(string(a), `"schema": "agent-ledger-summary.v1"`) {
		t.Fatalf("missing schema: %s", a)
	}
}
