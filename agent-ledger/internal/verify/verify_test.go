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

// TestVerify_SymlinkAlias asserts SPEC §14 #8: when two active intents
// share a realpath but differ in canonical_path_hash, the verifier
// surfaces them via SYMLINK_ALIAS so operators can pick one canonical
// display per logical file. PR2 introduced this check because the
// switch from realpath-keyed to display-keyed equality lost free
// symlink-aliasing.
func TestVerify_SymlinkAlias(t *testing.T) {
	root, ledger := setupProject(t)
	store := openTestStore(t, ledger)
	d := domain.New(store)
	ctx := context.Background()
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "pi.worker.a", AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID: "TC", OrchestratorID: "pi.main.test", AssignedAgentID: "pi.worker.a",
		AllowedPaths: []string{"**"}, ConflictPolicy: domain.PolicyWarn, Reason: "x",
	}); err != nil {
		t.Fatal(err)
	}
	// Two display paths that resolve to the same realpath but compute
	// different canonical hashes. The check is a property of the
	// stored rows; we synthesize them directly so the test does not
	// depend on filesystem symlink semantics.
	sharedReal := filepath.Join(root, "src", "real.go")
	ipathsA := []domain.IntentPath{{Path: "src/real.go", RealPath: sharedReal, PathHash: "hashA", CanonicalHash: "canonA", AccessMode: domain.AccessWrite}}
	ipathsB := []domain.IntentPath{{Path: "src/alias.go", RealPath: sharedReal, PathHash: "hashB", CanonicalHash: "canonB", AccessMode: domain.AccessWrite}}
	if _, err := d.InsertIntent(ctx, domain.Intent{TaskID: "TC", AgentID: "pi.worker.a", AccessMode: domain.AccessWrite, ConflictPolicy: domain.PolicyWarn, Reason: "a"}, ipathsA); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertIntent(ctx, domain.Intent{TaskID: "TC", AgentID: "pi.worker.a", AccessMode: domain.AccessWrite, ConflictPolicy: domain.PolicyWarn, Reason: "b"}, ipathsB); err != nil {
		t.Fatal(err)
	}
	store.Close()

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		ChangedPathsOverride: []string{},
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeSymlinkAlias]) == 0 {
		t.Fatalf("expected SYMLINK_ALIAS finding; got %+v", rep.Findings)
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

// TestVerify_SubagentBootstrap_NoAutoAssignedTask confirms that an
// assignment created by the pi-subagents child bootstrap
// (dispatch_origin == "pi-subagent-bootstrap") does NOT trigger
// AUTO_ASSIGNED_TASK. The orchestrator explicitly dispatched the
// child; the child self-assigned on bootstrap. That is not an
// adapter-invented, orchestrator-forgotten session.
func TestVerify_SubagentBootstrap_NoAutoAssignedTask(t *testing.T) {
	root, ledger := setupProject(t)
	store := openTestStore(t, ledger)
	t.Cleanup(func() { store.Close() })
	d := domain.New(store)
	ctx := context.Background()

	const childAgent = "agent:pi:subagent:run-abc-001:0"
	const parentAgent = "agent:pi:main:001"
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: childAgent, AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: parentAgent, AgentKind: "orchestrator"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "SUBAGENT-BOOTSTRAP",
		OrchestratorID:  parentAgent,
		AssignedAgentID: childAgent,
		AllowedPaths:    []string{"**"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "[harness-derived by pi-adapter source=subagent task=parent-task/worker/run-abc-001-0] child self-assignment",
		Metadata: map[string]any{
			"dispatch_origin":      "pi-subagent-bootstrap",
			"task_source":          "subagent",
			"parent_task":          "parent-task",
			"parent_agent_id":      parentAgent,
			"subagent_run_id":      "run-abc-001",
			"subagent_child_index": float64(0),
			"subagent_child_agent": "worker",
		},
	}); err != nil {
		t.Fatal(err)
	}

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "SUBAGENT-BOOTSTRAP",
		ChangedPathsOverride: nil,
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeAutoAssignedTask]) != 0 {
		t.Errorf("AUTO_ASSIGNED_TASK must not fire for a subagent-bootstrap assignment; got %+v", codes[verify.CodeAutoAssignedTask])
	}
	if len(codes[verify.CodeMissingAssignment]) != 0 {
		t.Errorf("MISSING_ASSIGNMENT must not fire when an assignment row exists; got %+v", codes[verify.CodeMissingAssignment])
	}
}

// TestVerify_SubagentBootstrap_NoAgentMismatch confirms that a
// subagent child recording changes under its own agent id does NOT
// trigger AGENT_MISMATCH, even though the child agent differs from
// the orchestrator stored in OrchestratorID. For subagent-bootstrap
// rows, parent != child by design.
func TestVerify_SubagentBootstrap_NoAgentMismatch(t *testing.T) {
	root, ledger := setupProject(t)
	store := openTestStore(t, ledger)
	t.Cleanup(func() { store.Close() })
	d := domain.New(store)
	ctx := context.Background()

	const childAgent = "agent:pi:subagent:run-abc-001:0"
	const parentAgent = "agent:pi:main:001"
	writeFile(t, filepath.Join(root, "src/child.go"), "x")

	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: childAgent, AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: parentAgent, AgentKind: "orchestrator"}); err != nil {
		t.Fatal(err)
	}
	assignment, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "SUBAGENT-MISMATCH",
		OrchestratorID:  parentAgent,
		AssignedAgentID: childAgent,
		AllowedPaths:    []string{"**"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "child self-assignment",
		Metadata: map[string]any{
			"dispatch_origin":      "pi-subagent-bootstrap",
			"task_source":          "subagent",
			"parent_task":          "parent-task",
			"parent_agent_id":      parentAgent,
			"subagent_run_id":      "run-abc-001",
			"subagent_child_index": float64(0),
			"subagent_child_agent": "worker",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	n, err := paths.Normalize(root, "src/child.go")
	if err != nil {
		t.Fatal(err)
	}
	// Record a change attributed to the child agent. The child agent
	// is not the orchestrator, which is by design for subagent rows.
	if _, err := d.InsertChange(ctx, domain.RecordChangeInput{
		Change: domain.Change{
			TaskID:       "SUBAGENT-MISMATCH",
			AgentID:      childAgent,
			AssignmentID: assignment.AssignmentID,
			Summary:      "child change",
		},
		Paths: []domain.ChangePath{{
			Path:     n.Display,
			RealPath: n.RealPath,
			PathHash: n.PathHash,
			Status:   domain.PathStatusModified,
		}},
		EventType: "change.recorded",
	}); err != nil {
		t.Fatal(err)
	}

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "SUBAGENT-MISMATCH",
		ChangedPathsOverride: []string{"src/child.go"},
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeAgentMismatch]) != 0 {
		t.Errorf("AGENT_MISMATCH must not fire when the child agent records under its own subagent-bootstrap assignment; got %+v", codes[verify.CodeAgentMismatch])
	}
}

// TestVerify_AutoAssignedTask_SourceDetached confirms that an
// assignment with task_source="detached" (HEAD-derived task id)
// still fires AUTO_ASSIGNED_TASK. Regression guard: the
// pi-subagent-bootstrap suppression must not affect other sources.
func TestVerify_AutoAssignedTask_SourceDetached(t *testing.T) {
	root, ledger := setupProject(t)
	store := openTestStore(t, ledger)
	t.Cleanup(func() { store.Close() })
	d := domain.New(store)
	ctx := context.Background()

	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "pi.worker.test", AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "DETACHED",
		OrchestratorID:  "pi-adapter",
		AssignedAgentID: "pi.worker.test",
		AllowedPaths:    []string{"**"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "[harness-derived by pi-adapter source=detached task=abc1234] session bootstrap",
		Metadata: map[string]any{
			"auto_assigned":    true,
			"auto_assigned_by": "pi-adapter",
			"task_source":      "detached",
		},
	}); err != nil {
		t.Fatal(err)
	}

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "DETACHED",
		ChangedPathsOverride: nil,
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeAutoAssignedTask]) == 0 {
		t.Errorf("AUTO_ASSIGNED_TASK must still fire for task_source=detached; got %+v", rep.Findings)
	}
}

// TestVerify_AutoAssignedTask_SourceAuto confirms that an assignment
// with task_source="auto" (harness auto-generated id) still fires
// AUTO_ASSIGNED_TASK. Regression guard: the pi-subagent-bootstrap
// suppression must not affect other sources.
func TestVerify_AutoAssignedTask_SourceAuto(t *testing.T) {
	root, ledger := setupProject(t)
	store := openTestStore(t, ledger)
	t.Cleanup(func() { store.Close() })
	d := domain.New(store)
	ctx := context.Background()

	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "pi.worker.test", AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "AUTO-DERIVED",
		OrchestratorID:  "pi-adapter",
		AssignedAgentID: "pi.worker.test",
		AllowedPaths:    []string{"**"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "[harness-derived by pi-adapter source=auto task=auto/some-id] session bootstrap",
		Metadata: map[string]any{
			"auto_assigned":    true,
			"auto_assigned_by": "pi-adapter",
			"task_source":      "auto",
		},
	}); err != nil {
		t.Fatal(err)
	}

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "AUTO-DERIVED",
		ChangedPathsOverride: nil,
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeAutoAssignedTask]) == 0 {
		t.Errorf("AUTO_ASSIGNED_TASK must still fire for task_source=auto; got %+v", rep.Findings)
	}
}

// TestVerify_AgentMismatch_ThirdPartyStillFires confirms that
// AGENT_MISMATCH still fires when a third-party agent records a
// change under a subagent-bootstrap task. The suppression applies
// only to the correct assignee; it must not over-suppress.
func TestVerify_AgentMismatch_ThirdPartyStillFires(t *testing.T) {
	root, ledger := setupProject(t)
	store := openTestStore(t, ledger)
	t.Cleanup(func() { store.Close() })
	d := domain.New(store)
	ctx := context.Background()

	const childAgent = "agent:pi:subagent:run-abc-001:0"
	const parentAgent = "agent:pi:main:001"
	const rogueAgent = "pi.rogue.agent"
	writeFile(t, filepath.Join(root, "src/child.go"), "x")

	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: childAgent, AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: parentAgent, AgentKind: "orchestrator"}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: rogueAgent, AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "SUBAGENT-THIRD-PARTY",
		OrchestratorID:  parentAgent,
		AssignedAgentID: childAgent,
		AllowedPaths:    []string{"**"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "child self-assignment",
		Metadata: map[string]any{
			"dispatch_origin":      "pi-subagent-bootstrap",
			"task_source":          "subagent",
			"parent_task":          "parent-task",
			"parent_agent_id":      parentAgent,
			"subagent_run_id":      "run-abc-001",
			"subagent_child_index": float64(0),
			"subagent_child_agent": "worker",
		},
	}); err != nil {
		t.Fatal(err)
	}

	n, err := paths.Normalize(root, "src/child.go")
	if err != nil {
		t.Fatal(err)
	}
	// A third-party agent records a change on the child's task.
	// AGENT_MISMATCH must still fire.
	if _, err := d.InsertChange(ctx, domain.RecordChangeInput{
		Change: domain.Change{
			TaskID:  "SUBAGENT-THIRD-PARTY",
			AgentID: rogueAgent,
			Summary: "unauthorized change",
		},
		Paths: []domain.ChangePath{{
			Path:     n.Display,
			RealPath: n.RealPath,
			PathHash: n.PathHash,
			Status:   domain.PathStatusModified,
		}},
		EventType: "change.recorded",
	}); err != nil {
		t.Fatal(err)
	}

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "SUBAGENT-THIRD-PARTY",
		ChangedPathsOverride: []string{"src/child.go"},
	})
	codes := findingsByCode(rep)
	if len(codes[verify.CodeAgentMismatch]) == 0 {
		t.Errorf("AGENT_MISMATCH must still fire when a third-party agent records a change on a subagent-bootstrap task; got %+v", rep.Findings)
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

// TestVerify_AgentMismatch_HistoricAssignmentAccepted is the v0.1.x
// regression guard for reassignment scenarios. When an orchestrator
// supersedes assignment v1 (agent A) and creates assignment v2
// (agent B) on the same task, A's prior changes recorded under v1
// must NOT be flagged as AGENT_MISMATCH. Pre-fix code consulted only
// the latest active assignment and falsely flagged every historic
// change after a reassignment.
func TestVerify_AgentMismatch_HistoricAssignmentAccepted(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "src/foo.go"), "x")

	store := openTestStore(t, ledger)
	d := domain.New(store)
	ctx := context.Background()

	// Both agents must exist for FK constraints.
	for _, ag := range []string{"pi.worker.A", "pi.worker.B"} {
		if err := d.UpsertAgent(ctx, domain.Agent{AgentID: ag, AgentKind: "worker"}); err != nil {
			t.Fatal(err)
		}
	}
	// v1: agent A, already superseded.
	v1, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "T-REASSIGN",
		OrchestratorID:  "pi.main",
		AssignedAgentID: "pi.worker.A",
		AllowedPaths:    []string{"src/**"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "v1",
		Status:          "superseded",
	})
	if err != nil {
		t.Fatal(err)
	}
	// v2: agent B, currently active.
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "T-REASSIGN",
		OrchestratorID:  "pi.main",
		AssignedAgentID: "pi.worker.B",
		AllowedPaths:    []string{"src/**"},
		ConflictPolicy:  domain.PolicyWarn,
		Reason:          "v2",
	}); err != nil {
		t.Fatal(err)
	}
	// Change recorded by A under v1.
	n, _ := paths.Normalize(root, "src/foo.go")
	if _, err := d.InsertChange(ctx, domain.RecordChangeInput{
		Change: domain.Change{
			TaskID:       "T-REASSIGN",
			AgentID:      "pi.worker.A",
			AssignmentID: v1.AssignmentID,
			Summary:      "historic change under v1",
		},
		Paths:     []domain.ChangePath{{Path: n.Display, RealPath: n.RealPath, PathHash: n.PathHash, Status: domain.PathStatusModified}},
		EventType: "change.recorded",
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		TaskID:               "T-REASSIGN",
		ChangedPathsOverride: []string{"src/foo.go"},
	})
	codes := findingsByCode(rep)
	if got := len(codes[verify.CodeAgentMismatch]); got != 0 {
		t.Fatalf("expected no AGENT_MISMATCH after reassignment; got %d: %+v", got, codes[verify.CodeAgentMismatch])
	}
}

// TestVerify_ExclusiveLockHeld_ActiveIntentNotFlagged is the v0.1.x
// regression guard for the orphan-lock scanner. A sentinel whose
// path_hash maps to an active intent is in policy and must not be
// reported. Pre-fix code reported every sentinel because the "known"
// set was constructed from a no-op loop.
func TestVerify_ExclusiveLockHeld_ActiveIntentNotFlagged(t *testing.T) {
	root, ledger := setupProject(t)
	writeFile(t, filepath.Join(root, "src/foo.go"), "x")
	// Seed one closed intent + change. We want the sentinel to be
	// owned by an OPEN intent though, so seed a second intent below.
	store := openTestStore(t, ledger)
	d := domain.New(store)
	ctx := context.Background()
	if err := d.UpsertAgent(ctx, domain.Agent{AgentID: "pi.worker.test", AgentKind: "worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InsertAssignment(ctx, domain.Assignment{
		TaskID:          "T-LOCK",
		OrchestratorID:  "pi.main",
		AssignedAgentID: "pi.worker.test",
		AllowedPaths:    []string{"src/**"},
		ConflictPolicy:  domain.PolicyExclusive,
		Reason:          "lock-test",
	}); err != nil {
		t.Fatal(err)
	}
	n, _ := paths.Normalize(root, "src/foo.go")
	if _, err := d.InsertIntent(ctx, domain.Intent{
		TaskID:         "T-LOCK",
		AgentID:        "pi.worker.test",
		AccessMode:     domain.AccessWrite,
		ConflictPolicy: domain.PolicyExclusive,
		Reason:         "edit",
	}, []domain.IntentPath{{Path: n.Display, RealPath: n.RealPath, PathHash: n.PathHash, AccessMode: domain.AccessWrite}}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	// Drop a sentinel for the owned hash AND a sentinel for an
	// unrelated hash. Only the unrelated one should be flagged.
	writeFile(t, filepath.Join(ledger, "locks", n.PathHash+".lock"), "")
	writeFile(t, filepath.Join(ledger, "locks", "deadbeefcafefade.lock"), "")

	rep := runVerify(t, verify.Inputs{
		Root:                 root,
		LedgerDirFlag:        ledger,
		ChangedPathsOverride: []string{},
	})
	codes := findingsByCode(rep)
	if got := len(codes[verify.CodeExclusiveLockHeld]); got != 1 {
		t.Fatalf("expected exactly 1 EXCLUSIVE_LOCK_HELD (the unowned sentinel); got %d: %+v", got, codes[verify.CodeExclusiveLockHeld])
	}
	if h, _ := codes[verify.CodeExclusiveLockHeld][0].Details["path_hash"].(string); h != "deadbeefcafefade" {
		t.Fatalf("unexpected sentinel flagged: %+v", codes[verify.CodeExclusiveLockHeld][0])
	}
}
