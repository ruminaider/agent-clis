package gc_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/gc"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

func TestParseStaleAfter(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"24h", 24 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false},
		{"500ms", 500 * time.Millisecond, false},
		{"", 0, true},
		{"abc", 0, true},
		{"-1h", 0, true},
		{"0s", 0, true},
	}
	for _, tc := range cases {
		got, err := gc.ParseStaleAfter(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseStaleAfter(%q) expected error, got %s", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseStaleAfter(%q) unexpected err: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseStaleAfter(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// gcStoreSetup spins up a fresh ledger with a fixed clock so tests can
// rewind time deterministically.
func gcStoreSetup(t *testing.T, now func() time.Time) *sqlite.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := sqlite.OpenWithOptions(context.Background(), filepath.Join(dir, "ledger"), sqlite.Options{Now: now})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrator().Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

func mustInsertActiveIntent(t *testing.T, store *sqlite.Store, intentID, agentID, taskID, openedAt, lastHB string) {
	t.Helper()
	d := domain.New(store)
	// Create an agent row so the FK on intents.agent_id passes.
	if err := d.UpsertAgent(context.Background(), domain.Agent{
		AgentID: agentID, AgentKind: "worker", StartedAt: openedAt,
	}); err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	// Insert event row first to satisfy intents.event_id UNIQUE FK
	// requirement is not enforced at schema level (events table has its
	// own PK), but we still need a valid event_id. Use a synthetic ID.
	eventID := "evt_" + intentID
	_, err := store.DB().ExecContext(context.Background(), `
		INSERT INTO events(event_id, schema, event_type, created_at, agent_id, task_id, payload_json)
		VALUES (?, 'agent-ledger.v1', 'intent.opened', ?, ?, ?, '{"intent_id":"`+intentID+`"}')`,
		eventID, openedAt, agentID, taskID)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	_, err = store.DB().ExecContext(context.Background(), `
		INSERT INTO intents(intent_id, event_id, task_id, agent_id, access_mode, conflict_policy, reason, status, opened_at, last_heartbeat_at)
		VALUES (?, ?, ?, ?, 'write', 'warn', 'test', 'active', ?, ?)`,
		intentID, eventID, taskID, agentID, openedAt, nullOrStr(lastHB))
	if err != nil {
		t.Fatalf("insert intent: %v", err)
	}
}

func nullOrStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestRun_OrphansStaleIntentsByOpenedAt(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := gcStoreSetup(t, clock)

	// Active for 48h with no heartbeat.
	openedFresh := now.Add(-1 * time.Hour).Format("2006-01-02T15:04:05.000Z07:00")
	openedStale := now.Add(-48 * time.Hour).Format("2006-01-02T15:04:05.000Z07:00")
	mustInsertActiveIntent(t, store, "int_fresh", "agent.fresh", "T1", openedFresh, "")
	mustInsertActiveIntent(t, store, "int_stale", "agent.stale", "T1", openedStale, "")

	res, err := gc.Run(context.Background(), store, gc.Options{
		StaleAfter: 24 * time.Hour, Now: clock, AgentID: "agent-ledger.gc",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Candidates != 1 || len(res.Orphaned) != 1 || res.Orphaned[0] != "int_stale" {
		t.Fatalf("unexpected result: %+v", res)
	}

	// Confirm DB: stale intent is orphaned, fresh is still active.
	var stStale, stFresh string
	if err := store.DB().QueryRowContext(context.Background(),
		`SELECT status FROM intents WHERE intent_id='int_stale'`).Scan(&stStale); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(context.Background(),
		`SELECT status FROM intents WHERE intent_id='int_fresh'`).Scan(&stFresh); err != nil {
		t.Fatal(err)
	}
	if stStale != "orphaned" {
		t.Fatalf("stale status = %q, want orphaned", stStale)
	}
	if stFresh != "active" {
		t.Fatalf("fresh status = %q, want active", stFresh)
	}

	// intent.orphaned event written.
	var n int
	if err := store.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM events WHERE event_type='intent.orphaned'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("orphaned events = %d, want 1", n)
	}
}

func TestRun_HeartbeatFreshenessWins(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := gcStoreSetup(t, clock)

	openedAt := now.Add(-72 * time.Hour).Format("2006-01-02T15:04:05.000Z07:00")
	hbRecent := now.Add(-15 * time.Minute).Format("2006-01-02T15:04:05.000Z07:00")
	mustInsertActiveIntent(t, store, "int_hb", "agent.hb", "T1", openedAt, hbRecent)

	res, err := gc.Run(context.Background(), store, gc.Options{
		StaleAfter: 24 * time.Hour, Now: clock, AgentID: "agent-ledger.gc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidates != 0 {
		t.Fatalf("expected no candidates with recent heartbeat, got %+v", res)
	}
}

func TestRun_Idempotent(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := gcStoreSetup(t, clock)

	openedStale := now.Add(-48 * time.Hour).Format("2006-01-02T15:04:05.000Z07:00")
	mustInsertActiveIntent(t, store, "int_a", "agent.a", "T1", openedStale, "")

	first, err := gc.Run(context.Background(), store, gc.Options{
		StaleAfter: 24 * time.Hour, Now: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Orphaned) != 1 {
		t.Fatalf("first run orphaned = %d, want 1", len(first.Orphaned))
	}

	second, err := gc.Run(context.Background(), store, gc.Options{
		StaleAfter: 24 * time.Hour, Now: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Candidates != 0 || len(second.Orphaned) != 0 {
		t.Fatalf("second run not idempotent: %+v", second)
	}

	// Only one intent.orphaned event total.
	var n int
	if err := store.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM events WHERE event_type='intent.orphaned'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("orphaned events = %d, want 1", n)
	}
}

func TestRun_RejectsZeroStaleAfter(t *testing.T) {
	store := gcStoreSetup(t, time.Now)
	if _, err := gc.Run(context.Background(), store, gc.Options{StaleAfter: 0}); err == nil {
		t.Fatal("expected error for zero stale-after")
	}
}
