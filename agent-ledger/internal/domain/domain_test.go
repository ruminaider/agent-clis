package domain_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/privacy"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
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
	if strings.Contains(err.Error(), "\n") {
		t.Fatalf("err.Error() should be single line, got %q", err.Error())
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
