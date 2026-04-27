package verify_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	if rep.Summary.Counts.ClaimedPaths != 1 {
		t.Fatalf("expected 1 claimed, got %+v", rep.Summary.Counts)
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
	if len(codes[verify.CodeForbiddenPath]) == 0 {
		t.Fatalf("expected FORBIDDEN_PATH, findings=%+v", rep.Findings)
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
	if len(codes[verify.CodeOutsideAssignment]) == 0 {
		t.Fatalf("expected OUTSIDE_ASSIGNMENT, findings=%+v", rep.Findings)
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
	if rep.Status != verify.StatusStorageError {
		t.Fatalf("expected storage_error, got %s", rep.Status)
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
	if rep.Status != verify.StatusConfigError {
		t.Fatalf("expected config_error, got %s\n%+v", rep.Status, rep.Findings)
	}
	if rep.ExitCode() != 2 {
		t.Fatalf("expected exit 2, got %d", rep.ExitCode())
	}
}

func TestVerify_ExitCodes(t *testing.T) {
	cases := []struct {
		status string
		code   int
	}{
		{verify.StatusPassed, 0},
		{verify.StatusFailed, 1},
		{verify.StatusConfigError, 2},
		{verify.StatusStorageError, 3},
		{verify.StatusConflict, 4},
	}
	for _, c := range cases {
		r := &verify.Report{Status: c.status}
		if r.ExitCode() != c.code {
			t.Errorf("status=%s expected exit %d, got %d", c.status, c.code, r.ExitCode())
		}
	}
}

// silence unused import if the file ever drops the errors import.
var _ = errors.New

// silence unused import warning for strings; used in subprocess test.
var _ = strings.HasPrefix
