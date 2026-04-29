package verify_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/paths"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/summary"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/verify"
)

// setupProject creates a temp project root + writable ledger dir,
// chdirs into the project, and registers a closer.
func setupProject(t *testing.T) (root, ledger string) {
	t.Helper()
	root = t.TempDir()
	ledger = filepath.Join(t.TempDir(), "ledger")
	if err := os.MkdirAll(ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root, ledger
}

// openTestStore opens a ledger store and runs migrations.
func openTestStore(t *testing.T, ledger string) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(context.Background(), ledger)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Migrator().Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedAssignmentClaimRecord creates an assignment, intent, change row,
// then closes the intent. Returns the closed intent id.
func seedAssignmentClaimRecord(t *testing.T, ledger, root, task, agentID string, allow []string, forbid []string, changedPath string, closeIntent bool) string {
	t.Helper()
	store := openTestStore(t, ledger)
	t.Cleanup(func() { store.Close() })
	d := domain.New(store)
	ctx := context.Background()

	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: agentID, AgentKind: "worker"}); err != nil {
		t.Fatalf("agent: %v", err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          task,
		OrchestratorID:  "pi.main.test",
		AssignedAgentID: agentID,
		AllowedPaths:    allow,
		ForbiddenPaths:  forbid,
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "seed",
	}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: agentID, AgentKind: "worker"}); err != nil {
		t.Fatalf("agent: %v", err)
	}

	n, err := paths.Normalize(root, changedPath)
	if err != nil {
		t.Fatal(err)
	}
	in, err := d.InsertIntent(ctx, domain.Intent{
		TaskID:         task,
		AgentID:        agentID,
		AccessMode:     domain.AccessWrite,
		ConflictPolicy: domain.PolicyWarn,
		Reason:         "edit",
	}, []domain.IntentPath{{
		Path:       n.Display,
		RealPath:   n.RealPath,
		PathHash:   n.PathHash,
		AccessMode: domain.AccessWrite,
	}})
	if err != nil {
		t.Fatalf("intent: %v", err)
	}
	if _, err := d.InsertChange(ctx, domain.RecordChangeInput{
		Change: domain.Change{
			IntentID: in.IntentID,
			TaskID:   task,
			AgentID:  agentID,
			Summary:  "seed change",
		},
		Paths: []domain.ChangePath{{
			Path:     n.Display,
			RealPath: n.RealPath,
			PathHash: n.PathHash,
			Status:   domain.PathStatusModified,
		}},
		EventType: "change.recorded",
	}); err != nil {
		t.Fatalf("change: %v", err)
	}
	if closeIntent {
		if err := d.Close(ctx, in.IntentID, agentID, domain.OutcomeCompleted, "done", time.Now().UTC()); err != nil {
			t.Fatalf("close: %v", err)
		}
	}
	return in.IntentID
}

func runVerify(t *testing.T, in verify.Inputs) *verify.Report {
	t.Helper()
	rep, err := verify.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("verify run: %v", err)
	}
	return rep
}

func findingsByCode(rep *verify.Report) map[string][]verify.Finding {
	m := map[string][]verify.Finding{}
	for _, f := range rep.Findings {
		m[f.Code] = append(m[f.Code], f)
	}
	return m
}

func TestVerify_PathHashAtRoot_AppliesToSlashAtCallSite(t *testing.T) {
	t.Logf("runtime.GOOS=%s; FromSlash(\"a/b/c.txt\")=%q", runtime.GOOS, filepath.FromSlash("a/b/c.txt"))
	// On POSIX filepath.FromSlash is a no-op (a/b/c.txt == a/b/c.txt), so this
	// test is tautological there. On Windows it produces a\\b\\c.txt and catches
	// removal of filepath.ToSlash at internal/verify/verify.go:911.
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "a/b/c.txt"), "x")
	seedAssignmentClaimRecord(t, ledger, root, "T1", "pi.worker.test", []string{"a/**"}, nil, "a/b/c.txt", true)
	rep := runVerify(t, verify.Inputs{Root: root, LedgerDirFlag: ledger, TaskID: "T1", ChangedPathsOverride: []string{filepath.FromSlash("a/b/c.txt")}})
	if rep.Status != verify.StatusPassed {
		t.Fatalf("expected passed, got %s", rep.Status)
	}
	if got := rep.Summary.ClaimedPaths; got != 1 {
		t.Fatalf("expected 1 claimed path, got %d", got)
	}
}

func TestVerify_HappyPath(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "src/foo.go"), "package foo\n")

	seedAssignmentClaimRecord(t, ledger, root, "T1", "pi.worker.test", []string{"src/**"}, nil, "src/foo.go", true)

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "T1",
		ChangedPathsOverride: []string{"src/foo.go"},
	})

	if rep.Status != verify.StatusPassed {
		raw, _ := json.MarshalIndent(rep, "", "  ")
		t.Fatalf("expected passed, got %s\n%s", rep.Status, raw)
	}
	if rep.ExitCode() != 0 {
		t.Fatalf("expected exit 0, got %d", rep.ExitCode())
	}
	if rep.Summary.ClaimedPaths != 1 {
		t.Fatalf("expected 1 claimed, got %+v", rep.Summary)
	}
}

func TestVerify_UnclaimedChange(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "src/foo.go"), "x")

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "TX",
		ChangedPathsOverride: []string{"src/foo.go"},
	})
	if rep.Status != verify.StatusFailed {
		t.Fatalf("expected failed, got %s", rep.Status)
	}
	if rep.ExitCode() != 1 {
		t.Fatalf("expected exit 1, got %d", rep.ExitCode())
	}
	codes := findingsByCode(rep)
	if len(codes[verify.CodeUnclaimedChange]) == 0 {
		t.Fatalf("expected UNCLAIMED_CHANGE, got %+v", rep.Findings)
	}
	if len(codes[verify.CodeMissingAssignment]) == 0 {
		t.Fatalf("expected MISSING_ASSIGNMENT, got %+v", rep.Findings)
	}
}

func TestVerify_ForbiddenPath(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "uv.lock"), "x")

	seedAssignmentClaimRecord(t, ledger, root, "T1", "pi.worker.test", []string{"uv.lock"}, []string{"uv.lock"}, "uv.lock", true)

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "T1",
		ChangedPathsOverride: []string{"uv.lock"},
	})
	if rep.Status != verify.StatusFailed {
		t.Fatalf("expected failed, got %s", rep.Status)
	}
	codes := findingsByCode(rep)
	if len(codes[verify.CodeForbiddenPathChanged]) == 0 {
		t.Fatalf("expected FORBIDDEN_PATH_CHANGED, findings=%+v", rep.Findings)
	}
}

func TestVerify_OutsideAssignment(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "elsewhere.txt"), "x")

	seedAssignmentClaimRecord(t, ledger, root, "T1", "pi.worker.test", []string{"src/**"}, nil, "elsewhere.txt", true)

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "T1",
		ChangedPathsOverride: []string{"elsewhere.txt"},
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodePathOutsideAssignment]) == 0 {
		t.Fatalf("expected PATH_OUTSIDE_ASSIGNMENT, findings=%+v", rep.Findings)
	}
}

func TestVerify_OpenIntentTaskMode(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "src/foo.go"), "x")
	seedAssignmentClaimRecord(t, ledger, root, "T1", "pi.worker.test", []string{"src/**"}, nil, "src/foo.go", false)

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "T1",
		ChangedPathsOverride: []string{"src/foo.go"},
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeOpenIntent]) == 0 {
		t.Fatalf("expected OPEN_INTENT, findings=%+v", rep.Findings)
	}
}

func TestVerify_StaleIntent(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "src/foo.go"), "x")
	store := openTestStore(t, ledger)
	d := domain.New(store)
	ctx := context.Background()
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "pi.worker.test", AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "T1",
		OrchestratorID:  "pi.main.test",
		AssignedAgentID: "pi.worker.test",
		AllowedPaths:    []string{"src/**"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "x",
	}); err != nil {
		t.Fatal(err)
	}
	n, _ := paths.Normalize(root, "src/foo.go")
	expired := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	if _, err := d.InsertIntent(ctx, domain.Intent{
		TaskID:             "T1",
		AgentID:            "pi.worker.test",
		AccessMode:         domain.AccessWrite,
		ConflictPolicy:     domain.PolicyWarn,
		Reason:             "edit",
		HeartbeatExpiresAt: expired,
	}, []domain.IntentPath{{
		Path: n.Display, RealPath: n.RealPath, PathHash: n.PathHash, AccessMode: domain.AccessWrite,
	}}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "T1",
		StaleAfter:           time.Hour,
		Now:                  time.Now().UTC(),
		ChangedPathsOverride: []string{"src/foo.go"},
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeStaleIntent]) == 0 {
		t.Fatalf("expected STALE_INTENT, findings=%+v", rep.Findings)
	}
}

func TestVerify_ActiveConflict(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "shared.md"), "x")
	store := openTestStore(t, ledger)
	d := domain.New(store)
	ctx := context.Background()
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "pi.worker.a", AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID: "TC", OrchestratorID: "pi.main.test", AssignedAgentID: "pi.worker.a",
		AllowedPaths: []string{"shared.md"}, ConflictPolicy: domain.PolicyWarn, Reason: "x",
	}); err != nil {
		t.Fatal(err)
	}
	n, _ := paths.Normalize(root, "shared.md")
	first, err := d.InsertIntent(ctx, domain.Intent{TaskID: "TC", AgentID: "pi.worker.a", AccessMode: domain.AccessWrite, ConflictPolicy: domain.PolicyWarn, Reason: "first"}, []domain.IntentPath{{Path: n.Display, RealPath: n.RealPath, PathHash: n.PathHash, AccessMode: domain.AccessWrite}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertConflict(ctx, domain.Conflict{
		Path: n.Display, PathHash: n.PathHash, ExistingIntentID: first.IntentID, NewIntentID: first.IntentID, Policy: domain.PolicyWarn,
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "TC",
		ChangedPathsOverride: []string{"shared.md"},
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeActiveConflict]) == 0 {
		t.Fatalf("expected ACTIVE_CONFLICT, got %+v", rep.Findings)
	}
}

func TestVerify_ReviewOnlyWrite(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "src/foo.go"), "x")
	store := openTestStore(t, ledger)
	d := domain.New(store)
	ctx := context.Background()
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "pi.worker.test", AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{TaskID: "TR", OrchestratorID: "pi.main.test", AssignedAgentID: "pi.worker.test", AllowedPaths: []string{"src/**"}, ConflictPolicy: domain.PolicyWarn, Reason: "x"}); err != nil {
		t.Fatal(err)
	}
	n, _ := paths.Normalize(root, "src/foo.go")
	in, err := d.InsertIntent(ctx, domain.Intent{TaskID: "TR", AgentID: "pi.worker.test", AccessMode: domain.AccessReviewOnly, ConflictPolicy: domain.PolicyWarn, Reason: "review"}, []domain.IntentPath{{Path: n.Display, RealPath: n.RealPath, PathHash: n.PathHash, AccessMode: domain.AccessReviewOnly}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertChange(ctx, domain.RecordChangeInput{
		Change:    domain.Change{IntentID: in.IntentID, TaskID: "TR", AgentID: "pi.worker.test", Summary: "wrote"},
		Paths:     []domain.ChangePath{{Path: n.Display, RealPath: n.RealPath, PathHash: n.PathHash, Status: domain.PathStatusModified}},
		EventType: "change.recorded",
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "TR",
		ChangedPathsOverride: []string{"src/foo.go"},
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeReviewOnlyWrite]) == 0 {
		t.Fatalf("expected REVIEW_ONLY_WRITE, got %+v", rep.Findings)
	}
}

func TestVerify_AgentMismatch(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "src/foo.go"), "x")
	seedAssignmentClaimRecord(t, ledger, root, "TM", "pi.worker.expected", []string{"src/**"}, nil, "src/foo.go", true)
	// Insert a second change row attributed to a different agent.
	store := openTestStore(t, ledger)
	d := domain.New(store)
	ctx := context.Background()
	n, _ := paths.Normalize(root, "src/foo.go")
	if _, err := d.InsertChange(ctx, domain.RecordChangeInput{
		Change:    domain.Change{TaskID: "TM", AgentID: "pi.worker.other", Summary: "rogue"},
		Paths:     []domain.ChangePath{{Path: n.Display, RealPath: n.RealPath, PathHash: n.PathHash, Status: domain.PathStatusModified}},
		EventType: "change.recorded",
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "TM",
		ChangedPathsOverride: []string{"src/foo.go"},
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeAgentMismatch]) == 0 {
		t.Fatalf("expected AGENT_MISMATCH, got %+v", rep.Findings)
	}
}

func TestVerify_MissingAssignment(t *testing.T) {
	root, ledger := setupProject(t)
	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "ZZ",
		ChangedPathsOverride: nil,
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeMissingAssignment]) == 0 {
		t.Fatalf("expected MISSING_ASSIGNMENT, got %+v", rep.Findings)
	}
}

// TestVerify_AutoAssignedTask_Metadata verifies that an assignment
// carrying metadata.auto_assigned == true (the v0.1.1+ structured
// signal written by the adapter bootstrap) surfaces as an
// AUTO_ASSIGNED_TASK finding with severity warning.
func TestVerify_AutoAssignedTask_Metadata(t *testing.T) {
	root, ledger := setupProject(t)
	store := openTestStore(t, ledger)
	t.Cleanup(func() { store.Close() })
	d := domain.New(store)
	ctx := context.Background()

	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "pi.worker.test", AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "AUTO",
		OrchestratorID:  "pi-adapter",
		AssignedAgentID: "pi.worker.test",
		AllowedPaths:    []string{"**"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "adapter session bootstrap",
		Metadata: map[string]any{
			"auto_assigned":    true,
			"auto_assigned_by": "pi-adapter",
			"task_source":      "branch",
		},
	}); err != nil {
		t.Fatal(err)
	}
	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "AUTO",
		ChangedPathsOverride: nil,
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeAutoAssignedTask]) == 0 {
		t.Fatalf("expected AUTO_ASSIGNED_TASK, got %+v", rep.Findings)
	}
	if codes[verify.CodeAutoAssignedTask][0].Severity != verify.SevWarning {
		t.Errorf("AUTO_ASSIGNED_TASK severity = %q, want warning", codes[verify.CodeAutoAssignedTask][0].Severity)
	}
	// MISSING_ASSIGNMENT must NOT fire because the assignment exists.
	if len(codes[verify.CodeMissingAssignment]) != 0 {
		t.Errorf("MISSING_ASSIGNMENT should not fire when an assignment row exists; got %+v", codes[verify.CodeMissingAssignment])
	}
}

// TestVerify_AutoAssignedTask_ReasonMarkerFallback verifies the
// pre-v0.1.1 fallback path: an assignment WITHOUT structured metadata
// but with a leading [auto-assigned by ...] reason marker is still
// recognized as adapter-derived. Catches sessions that wrote against
// older kernel binaries before the structured surface existed.
func TestVerify_AutoAssignedTask_ReasonMarkerFallback(t *testing.T) {
	root, ledger := setupProject(t)
	store := openTestStore(t, ledger)
	t.Cleanup(func() { store.Close() })
	d := domain.New(store)
	ctx := context.Background()

	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "pi.worker.test", AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "LEGACY",
		OrchestratorID:  "pi-adapter",
		AssignedAgentID: "pi.worker.test",
		AllowedPaths:    []string{"**"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "[auto-assigned by pi-adapter auto-derived task=auto/x] legacy session",
		// No structured metadata; v0.2.0-rc1 ledgers look like this.
	}); err != nil {
		t.Fatal(err)
	}
	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "LEGACY",
		ChangedPathsOverride: nil,
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeAutoAssignedTask]) == 0 {
		t.Fatalf("expected AUTO_ASSIGNED_TASK from reason-marker fallback, got %+v", rep.Findings)
	}
}

// TestVerify_ExplicitAssignment_NoAutoFinding confirms that an
// orchestrator-supplied assignment (no metadata.auto_assigned, no
// reason marker) does NOT trigger AUTO_ASSIGNED_TASK. Regression
// guard against false positives.
func TestVerify_ExplicitAssignment_NoAutoFinding(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "x.md"), "x")
	seedAssignmentClaimRecord(t, ledger, root, "EXPLICIT", "pi.worker.test", []string{"x.md"}, nil, "x.md", true)
	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "EXPLICIT",
		ChangedPathsOverride: []string{"x.md"},
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeAutoAssignedTask]) != 0 {
		t.Errorf("AUTO_ASSIGNED_TASK should not fire for an explicit orchestrator assignment; got %+v", codes[verify.CodeAutoAssignedTask])
	}
}

func TestVerify_ExclusiveLockHeld(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(ledger, "locks/abc.lock"), "")
	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		ChangedPathsOverride: []string{},
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeExclusiveLockHeld]) == 0 {
		t.Fatalf("expected EXCLUSIVE_LOCK_HELD, got %+v", rep.Findings)
	}
}

func TestVerify_Summary_HappyPath(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "src/foo.go"), "x")
	seedAssignmentClaimRecord(t, ledger, root, "T1", "pi.worker.test", []string{"src/**"}, nil, "src/foo.go", true)

	// Build a real summary via summary.Build then write it.
	store := openTestStore(t, ledger)
	d := domain.New(store)
	doc, err := summary.Build(context.Background(), summary.Inputs{
		Store:       d,
		TaskID:      "T1",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	raw, _ := summary.Marshal(doc)
	sumFile := filepath.Join(root, "summary.json")
	if err := os.WriteFile(sumFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	// Summary mode does not need a ledger directory. Verify against
	// the original root (path hashes derived from realpath are stable
	// when the project root is the same).
	rep := runVerify(t, verify.Inputs{
		Root:        root,
		SummaryFile: "summary.json",
	})
	if rep.Status != verify.StatusPassed {
		out, _ := json.MarshalIndent(rep, "", "  ")
		t.Fatalf("expected passed, got %s\n%s", rep.Status, out)
	}
}

func TestVerify_Summary_Tampered(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "src/foo.go"), "x")
	seedAssignmentClaimRecord(t, ledger, root, "T1", "pi.worker.test", []string{"src/**"}, nil, "src/foo.go", true)

	store := openTestStore(t, ledger)
	d := domain.New(store)
	doc, err := summary.Build(context.Background(), summary.Inputs{
		Store: d, TaskID: "T1", GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	raw, _ := summary.Marshal(doc)

	// Tamper: rewrite the summary's declared assignment_hash so the
	// recomputed hash differs and SUMMARY_MISMATCH fires.
	var docMap map[string]any
	if err := json.Unmarshal(raw, &docMap); err != nil {
		t.Fatal(err)
	}
	docMap["assignment_hash"] = "sha256:deadbeef"
	tampered, _ := json.MarshalIndent(docMap, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "summary.json"), tampered, 0o644); err != nil {
		t.Fatal(err)
	}

	rep := runVerify(t, verify.Inputs{
		Root:        root,
		SummaryFile: "summary.json",
	})
	if rep.Status != verify.StatusFailed {
		t.Fatalf("expected failed, got %s", rep.Status)
	}
	codes := findingsByCode(rep)
	if len(codes[verify.CodeSummaryMismatch]) == 0 {
		t.Fatalf("expected SUMMARY_MISMATCH, got %+v", rep.Findings)
	}
}

func TestVerify_Summary_PathMissing(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "src/foo.go"), "x")
	seedAssignmentClaimRecord(t, ledger, root, "T1", "pi.worker.test", []string{"src/**"}, nil, "src/foo.go", true)
	store := openTestStore(t, ledger)
	d := domain.New(store)
	doc, _ := summary.Build(context.Background(), summary.Inputs{Store: d, TaskID: "T1", GeneratedAt: time.Now().UTC().Format(time.RFC3339)})
	store.Close()
	raw, _ := summary.Marshal(doc)
	// Move to a clean root that lacks the referenced files: triggers
	// SUMMARY_MISMATCH on missing-file detection (also exercises the
	// no-XDG-ledger path).
	cleanRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(cleanRoot, "summary.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	rep := runVerify(t, verify.Inputs{Root: cleanRoot, SummaryFile: "summary.json"})
	if rep.Status != verify.StatusFailed {
		t.Fatalf("expected failed, got %s", rep.Status)
	}
	codes := findingsByCode(rep)
	if len(codes[verify.CodeSummaryMismatch]) == 0 {
		t.Fatalf("expected SUMMARY_MISMATCH for missing file, got %+v", rep.Findings)
	}
}

func TestVerify_StorageError(t *testing.T) {
	// Point at an existing but corrupt sqlite file.
	root := t.TempDir()
	ledger := filepath.Join(t.TempDir(), "ledger")
	if err := os.MkdirAll(ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	// Corrupt: write a non-sqlite blob to the expected db file.
	if err := os.WriteFile(filepath.Join(ledger, "ledger.sqlite"), []byte("not a db"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := verify.Run(context.Background(), verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		ChangedPathsOverride: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != verify.StatusError {
		t.Fatalf("expected error, got %s", rep.Status)
	}
	if rep.ExitCode() != 3 {
		t.Fatalf("expected exit 3, got %d", rep.ExitCode())
	}
}

func TestVerify_ConfigError(t *testing.T) {
	// Bad pointer file → project resolution fails.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".agent-ledger.toml"), []byte("garbage = [[[ not toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := verify.Run(context.Background(), verify.Inputs{
		Root:                 root,
		ChangedPathsOverride: []string{},
		Getenv:               func(string) string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != verify.StatusError {
		t.Fatalf("expected error, got %s\n%+v", rep.Status, rep.Findings)
	}
	if rep.ExitCode() != 2 {
		t.Fatalf("expected exit 2, got %d", rep.ExitCode())
	}
}

func TestVerify_ExitCodes(t *testing.T) {
	// SPEC §19.2 collapses configuration and storage problems into
	// status "error". The verify-specific ExitCode helper distinguishes
	// 2 (config) vs 3 (storage) by inspecting the dominant finding
	// code, so the table includes a finding seed where relevant.
	cases := []struct {
		name     string
		report   verify.Report
		wantCode int
	}{
		{"passed", verify.Report{Status: verify.StatusPassed}, 0},
		{"failed", verify.Report{Status: verify.StatusFailed}, 1},
		{"config_error", verify.Report{Status: verify.StatusError, Findings: []verify.Finding{{Code: verify.CodeConfigError}}}, 2},
		{"storage_error", verify.Report{Status: verify.StatusError, Findings: []verify.Finding{{Code: verify.CodeStorageError}}}, 3},
		{"needs_decision", verify.Report{Status: verify.StatusNeedsDecision}, 4},
		// RV2-006: StatusError without a typed code falls back to exit 2
		// (ExitConfigError) per SPEC §19.1. An untyped error originates from
		// misconfiguration that did not produce a CODE_ERROR finding.
		{"error_no_code", verify.Report{Status: verify.StatusError}, 2},
		// RV2-001: conflict + storage_error still routes to exit 3 via StatusError.
		{"conflict_and_storage_error", verify.Report{
			Status: verify.StatusError,
			Findings: []verify.Finding{
				{Code: verify.CodeActiveConflict, Severity: verify.SevError},
				{Code: verify.CodeStorageError, Severity: verify.SevFatal},
			},
		}, 3},
	}
	for _, c := range cases {
		r := c.report
		if r.ExitCode() != c.wantCode {
			t.Errorf("%s: expected exit %d, got %d", c.name, c.wantCode, r.ExitCode())
		}
	}
}

// TestVerify_ConflictOnly_NeedsDecision is the RV2-001 regression test
// (wv1-rv-f09). A report whose only error-severity findings are
// ACTIVE_CONFLICT must resolve to needs-decision (exit 4), not failed
// (exit 1). The bug was that ACTIVE_CONFLICT findings (SevError) set
// both hasConflict and hasError, causing the hasError&&hasConflict
// branch to win and return StatusFailed.
func TestVerify_ConflictOnly_NeedsDecision(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "shared.md"), "x")
	store := openTestStore(t, ledger)
	d := domain.New(store)
	ctx := context.Background()
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "pi.worker.a", AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID: "TCD", OrchestratorID: "pi.main.test", AssignedAgentID: "pi.worker.a",
		AllowedPaths: []string{"shared.md"}, ConflictPolicy: domain.PolicyWarn, Reason: "x",
	}); err != nil {
		t.Fatal(err)
	}
	n, _ := paths.Normalize(root, "shared.md")
	first, err := d.InsertIntent(ctx, domain.Intent{
		TaskID: "TCD", AgentID: "pi.worker.a", AccessMode: domain.AccessWrite,
		ConflictPolicy: domain.PolicyWarn, Reason: "first",
	}, []domain.IntentPath{{
		Path: n.Display, RealPath: n.RealPath, PathHash: n.PathHash, AccessMode: domain.AccessWrite,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertConflict(ctx, domain.Conflict{
		Path: n.Display, PathHash: n.PathHash, ExistingIntentID: first.IntentID,
		NewIntentID: first.IntentID, Policy: domain.PolicyWarn,
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "TCD",
		ChangedPathsOverride: []string{"shared.md"},
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeActiveConflict]) == 0 {
		t.Fatalf("expected ACTIVE_CONFLICT finding, got %+v", rep.Findings)
	}
	if rep.Status != verify.StatusNeedsDecision {
		t.Fatalf("expected needs-decision, got %s (findings=%+v)", rep.Status, rep.Findings)
	}
	if rep.ExitCode() != 4 {
		t.Fatalf("expected exit 4, got %d", rep.ExitCode())
	}
}

// silence unused import if the file ever drops the errors import.
var _ = errors.New

// silence unused import warning for strings; used in subprocess test.
var _ = strings.HasPrefix
