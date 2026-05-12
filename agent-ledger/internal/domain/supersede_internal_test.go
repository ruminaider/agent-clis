package domain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

// openStoreInternal mirrors the external test helper but lives inside
// package domain so internal tests can reach unexported helpers.
func openStoreInternal(t *testing.T) *sqlite.Store {
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

// TestSupersedeAssignmentTxZeroRowsReturnsErrStaleUpdate exercises the
// zero-row branch of supersedeAssignmentTx directly. The CLI path
// covers ErrStaleUpdate only by observable proxy because the in-callback
// race requires an interleaving an external test cannot inject; this
// internal test calls the helper against a non-existent active row so
// the UPDATE affects zero rows and the sentinel surfaces cleanly.
func TestSupersedeAssignmentTxZeroRowsReturnsErrStaleUpdate(t *testing.T) {
	s := openStoreInternal(t)
	ctx := context.Background()

	// *sql.DB satisfies the sqlExecer interface, so we can call the
	// unexported helper directly without a transaction. The helper's
	// only branch that matters here is RowsAffected == 0.
	err := supersedeAssignmentTx(ctx, s.DB(), "does-not-exist", "new-id", "2026-05-12T00:00:00Z")
	if !errors.Is(err, ErrStaleUpdate) {
		t.Fatalf("expected ErrStaleUpdate when no row matches, got %v", err)
	}
}
