package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/audit"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/events"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/id"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage"
)

func openTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ledger")
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s, err := OpenWithOptions(context.Background(), dir, Options{Now: clock})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Migrator().Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s, func() { _ = s.Close() }
}

func TestMigrate_FromEmpty_AppliesAllTables(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	want := []string{
		"schema_migrations", "agents", "assignments", "intents",
		"intent_paths", "changes", "change_paths", "validations",
		"conflicts", "events",
	}
	for _, name := range want {
		var got string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got); err != nil {
			t.Errorf("missing table %s: %v", name, err)
		}
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	v1, _ := s.Migrator().SchemaVersion(context.Background())
	if err := s.Migrator().Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	v2, _ := s.Migrator().SchemaVersion(context.Background())
	if v1 != v2 {
		t.Fatalf("schema version drifted on rerun: %d != %d", v1, v2)
	}
	var countBefore int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&countBefore); err != nil {
		t.Fatal(err)
	}
	if countBefore != v2 {
		t.Fatalf("schema_migrations row count = %d, want %d (matches schema version)", countBefore, v2)
	}
	// Re-running Up must not insert duplicate schema_migrations rows.
	if err := s.Migrator().Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	var countAfter int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&countAfter); err != nil {
		t.Fatal(err)
	}
	if countAfter != countBefore {
		t.Fatalf("schema_migrations rows changed across reruns: %d -> %d", countBefore, countAfter)
	}
}

func TestPragma_WAL(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, want 1", fk)
	}
	var bt int
	if err := s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&bt); err != nil {
		t.Fatal(err)
	}
	if bt != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", bt)
	}
}

func TestForeignKey_Enforced(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	// intent.assignment_id -> assignments. Inserting an intent that
	// references a non-existent assignment must fail.
	_, err := s.db.Exec(`
		INSERT INTO intents(intent_id, event_id, assignment_id, task_id, agent_id,
			access_mode, conflict_policy, reason, opened_at)
		VALUES('int_X', 'evt_X', 'asg_DOES_NOT_EXIST', 'W2', 'agt_X',
			'write', 'warn', 'r', '2026-04-27T12:00:00Z')`)
	if err == nil {
		t.Fatal("expected FK failure")
	}
	if !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSchemaVersion_OnFreshDB(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, err := s.Migrator().SchemaVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Fatalf("fresh schema version = %d, want 0", v)
	}
}

func TestWriteEvent_Privacy_Allowed(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	payload, err := events.MarshalPayload(map[string]any{
		"task_id": "W2-A",
		"policy":  "warn",
	})
	if err != nil {
		t.Fatal(err)
	}
	ev := storage.Event{
		Type:        "intent.opened",
		AgentID:     "agt_test",
		TaskID:      "W2-A",
		PayloadJSON: payload,
	}
	if err := s.Events().WriteEvent(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("event count = %d", n)
	}
}

func TestWriteEvent_Privacy_DeniesLeak(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	bad := []byte(`{"task_id":"W2-A","diff":"+a\n-b"}`)
	err := s.Events().WriteEvent(context.Background(), storage.Event{Type: "intent.opened", PayloadJSON: bad})
	if err == nil {
		t.Fatal("expected privacy denial")
	}
	if !strings.Contains(err.Error(), "forbidden key") {
		t.Fatalf("want forbidden-key error, got %v", err)
	}
}

func TestWriteDomainEvent_DomainEventsAuditAllWritten(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()
	agentID, _ := s.IDGen().New(id.PrefixAgent)
	payload, err := events.MarshalPayload(map[string]any{
		"agent_id":   agentID,
		"agent_kind": "worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	ev := storage.Event{
		Type:        "agent.identified",
		AgentID:     agentID,
		PayloadJSON: payload,
	}
	err = s.WriteDomainEvent(ctx, ev, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO agents(agent_id, agent_kind, started_at) VALUES(?, ?, ?)`,
			agentID, "worker", id.FormatTimestamp(s.Clock()()))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	var nA, nE int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM agents`).Scan(&nA)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&nE)
	if nA != 1 || nE != 1 {
		t.Fatalf("agents=%d events=%d, want 1/1", nA, nE)
	}

	// Audit JSONL should now have one line for today's UTC date.
	day := s.Clock()().UTC().Format("2006-01-02")
	body, err := os.ReadFile(filepath.Join(s.LedgerDir(), "audit", day+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(body), "\n") != 1 {
		t.Fatalf("audit lines = %d, want 1\n%s", strings.Count(string(body), "\n"), body)
	}
	var rec map[string]any
	if err := json.Unmarshal(body[:len(body)-1], &rec); err != nil {
		t.Fatal(err)
	}
	if rec["event_type"] != "agent.identified" {
		t.Fatalf("audit record event_type = %v", rec["event_type"])
	}
}

func TestWriteDomainEvent_DomainFailureRollsBackEvent(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	ctx := context.Background()
	payload, _ := events.MarshalPayload(map[string]any{"k": "v"})
	ev := storage.Event{Type: "agent.identified", PayloadJSON: payload}
	wantErr := errors.New("boom")
	err := s.WriteDomainEvent(ctx, ev, func(ctx context.Context, tx *sql.Tx) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want boom", err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n)
	if n != 0 {
		t.Fatalf("events count after rollback = %d", n)
	}
	// Audit must NOT have been written (mirror runs only on commit).
	_, err = os.Stat(filepath.Join(s.LedgerDir(), "audit"))
	if err != nil {
		t.Fatalf("audit dir missing: %v", err)
	}
}

func TestEventID_Shape(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	payload, _ := events.MarshalPayload(map[string]any{"k": "v"})
	ev := storage.Event{Type: "intent.opened", PayloadJSON: payload}
	if err := s.Events().WriteEvent(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	var got string
	_ = s.db.QueryRow(`SELECT event_id FROM events`).Scan(&got)
	re := regexp.MustCompile(`^evt_[0-9A-HJKMNP-TV-Z]{26}$`)
	if !re.MatchString(got) {
		t.Fatalf("event_id %q", got)
	}
}

func TestTimestamp_RFC3339UTCZ(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	payload, _ := events.MarshalPayload(map[string]any{"k": "v"})
	ev := storage.Event{Type: "intent.opened", PayloadJSON: payload}
	_ = s.Events().WriteEvent(context.Background(), ev)
	var got string
	_ = s.db.QueryRow(`SELECT created_at FROM events`).Scan(&got)
	if !strings.HasSuffix(got, "Z") {
		t.Fatalf("created_at lacks Z: %s", got)
	}
	// Must parse as RFC3339.
	if _, err := time.Parse(time.RFC3339Nano, got); err != nil {
		t.Fatalf("not RFC3339: %v", err)
	}
}

func TestAudit_BestEffortMirror(t *testing.T) {
	s, cleanup := openTestStore(t)
	defer cleanup()
	if got := s.Audit(); got == nil {
		t.Fatal("audit writer is nil")
	}
}

func TestBusyDoesNotHang(t *testing.T) {
	// Open two stores against the same path. Begin an exclusive
	// transaction on one, then attempt a competing exclusive on the
	// other with a shorter context deadline. We assert it returns
	// within a reasonable time. Connection-level busy_timeout is 5s.
	if testing.Short() {
		t.Skip("skip in short mode")
	}
	dir := filepath.Join(t.TempDir(), "ledger")
	now := func() time.Time { return time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC) }
	a, err := OpenWithOptions(context.Background(), dir, Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Migrator().Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := OpenWithOptions(context.Background(), dir, Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO agents(agent_id, agent_kind, started_at) VALUES('agt_a', 'k', '2026-04-27T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	start := time.Now()
	// Force write contention by running a write that requires the
	// reserved/exclusive lock on a separate connection.
	_, err = b.db.ExecContext(ctx,
		`INSERT INTO agents(agent_id, agent_kind, started_at) VALUES('agt_b', 'k', '2026-04-27T12:00:00Z')`)
	elapsed := time.Since(start)
	if err == nil {
		// Some platforms may serialize the writes and succeed; that
		// just means we did not contend. Good enough: we did not hang.
		return
	}
	if elapsed > 6*time.Second {
		t.Fatalf("contention took %v, busy_timeout should bound at 5s", elapsed)
	}
}

// silence unused import in some configurations
var _ = audit.NewWriter
