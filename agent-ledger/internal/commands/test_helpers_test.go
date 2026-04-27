package commands_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

func openTestStore(t *testing.T, ledger string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Clean(ledger))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Migrator().Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

func countEvents(t *testing.T, ledger string) int {
	return countRows(t, ledger, "events")
}

func countRows(t *testing.T, ledger, table string) int {
	t.Helper()
	store := openTestStore(t, ledger)
	defer store.Close()
	var n int
	if err := store.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func eventTypes(t *testing.T, ledger string) []string {
	t.Helper()
	store := openTestStore(t, ledger)
	defer store.Close()
	rows, err := store.DB().QueryContext(context.Background(), "SELECT event_type FROM events ORDER BY created_at")
	if err != nil {
		t.Fatalf("event_type query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func queryString(t *testing.T, ledger, q string, args ...any) string {
	t.Helper()
	store := openTestStore(t, ledger)
	defer store.Close()
	var s string
	if err := store.DB().QueryRowContext(context.Background(), q, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return s
}
