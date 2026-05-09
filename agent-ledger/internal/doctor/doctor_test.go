package doctor_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/doctor"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

func emptyEnv(string) string { return "" }

func newProject(t *testing.T) (root, ledger string) {
	t.Helper()
	root = t.TempDir()
	ledger = filepath.Join(root, "ledger")
	if err := os.MkdirAll(ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	return
}

func runDoctor(t *testing.T, root, ledger string, env map[string]string) doctor.Report {
	t.Helper()
	getenv := emptyEnv
	if env != nil {
		getenv = func(k string) string { return env[k] }
	}
	return doctor.Run(context.Background(), doctor.Options{
		Root:          root,
		LedgerDirFlag: ledger,
		EnvLookup:     getenv,
	})
}

func findCheck(t *testing.T, rep doctor.Report, name string) doctor.Check {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("missing check %q in %+v", name, rep)
	return doctor.Check{}
}

func TestRun_FreshLedgerWarnsOnSqliteAbsent(t *testing.T) {
	root, ledger := newProject(t)
	rep := runDoctor(t, root, ledger, nil)
	if rep.Schema != doctor.SchemaName {
		t.Fatalf("schema = %q", rep.Schema)
	}
	storage := findCheck(t, rep, "storage")
	if storage.Status != doctor.StatusWarn {
		t.Fatalf("expected warn for missing sqlite, got %s", storage.Status)
	}
}

func TestRun_HealthyAfterMigrate(t *testing.T) {
	root, ledger := newProject(t)
	store, err := sqlite.Open(context.Background(), ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrator().Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.Close()

	rep := runDoctor(t, root, ledger, nil)
	if rep.Overall != doctor.StatusOK && rep.Overall != doctor.StatusWarn {
		t.Fatalf("overall = %s", rep.Overall)
	}
	migrations := findCheck(t, rep, "migrations")
	if migrations.Status != doctor.StatusOK {
		t.Fatalf("migrations status = %s, want ok: %+v", migrations.Status, migrations)
	}
	pragmas := findCheck(t, rep, "sqlite_pragmas")
	if pragmas.Status != doctor.StatusOK {
		t.Fatalf("pragmas status = %s, want ok: %+v", pragmas.Status, pragmas)
	}
	if pragmas.Details["foreign_keys"].(bool) != true {
		t.Fatalf("foreign_keys not on: %+v", pragmas.Details)
	}
}

func TestRun_PointerInvalid(t *testing.T) {
	root, ledger := newProject(t)
	// Write an invalid pointer file (unknown version).
	if err := os.WriteFile(filepath.Join(root, ".agent-ledger.toml"),
		[]byte(`version = 999`), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := runDoctor(t, root, ledger, nil)
	if rep.Overall != doctor.StatusError {
		t.Fatalf("expected error overall, got %s", rep.Overall)
	}
}

func TestRun_PointerDefaultTaskID(t *testing.T) {
	root, ledger := newProject(t)
	body := "version = 1\nledger_dir = \"" + ledger + "\"\ndefault_task_id = \"ambient-2026-05\"\n"
	if err := os.WriteFile(filepath.Join(root, ".agent-ledger.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := runDoctor(t, root, ledger, nil)
	ptr := findCheck(t, rep, "pointer")
	if ptr.Status != doctor.StatusOK {
		t.Fatalf("pointer status = %s, want ok: %+v", ptr.Status, ptr)
	}
	if got := ptr.Details["default_task_id"]; got != "ambient-2026-05" {
		t.Errorf("default_task_id detail = %v, want %q", got, "ambient-2026-05")
	}
	if got := ptr.Details["has_default_task"]; got != true {
		t.Errorf("has_default_task detail = %v, want true", got)
	}
}

func TestRun_PointerWithoutDefaultTaskID(t *testing.T) {
	root, ledger := newProject(t)
	body := "version = 1\nledger_dir = \"" + ledger + "\"\n"
	if err := os.WriteFile(filepath.Join(root, ".agent-ledger.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := runDoctor(t, root, ledger, nil)
	ptr := findCheck(t, rep, "pointer")
	if ptr.Status != doctor.StatusOK {
		t.Fatalf("pointer status = %s, want ok: %+v", ptr.Status, ptr)
	}
	if got := ptr.Details["has_default_task"]; got != false {
		t.Errorf("has_default_task detail = %v, want false", got)
	}
	if got := ptr.Details["default_task_id"]; got != "" {
		t.Errorf("default_task_id detail = %v, want empty", got)
	}
}

func TestRun_PolicyInvalidTOML(t *testing.T) {
	root, ledger := newProject(t)
	if err := os.WriteFile(filepath.Join(root, ".agent-ledger-policy.toml"),
		[]byte("not = valid = toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := runDoctor(t, root, ledger, nil)
	policy := findCheck(t, rep, "policy")
	if policy.Status != doctor.StatusError {
		t.Fatalf("policy status = %s, want error: %+v", policy.Status, policy)
	}
}

func TestRun_PolicyValid(t *testing.T) {
	root, ledger := newProject(t)
	if err := os.WriteFile(filepath.Join(root, ".agent-ledger-policy.toml"),
		[]byte("version = 1\n[defaults]\nconflict_policy = \"warn\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := runDoctor(t, root, ledger, nil)
	policy := findCheck(t, rep, "policy")
	if policy.Status != doctor.StatusOK {
		t.Fatalf("policy status = %s, want ok: %+v", policy.Status, policy)
	}
}

func TestRun_LockSentinelsReported(t *testing.T) {
	root, ledger := newProject(t)
	locksDir := filepath.Join(ledger, "locks")
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No locks held → ok with 0 sentinels.
	rep := runDoctor(t, root, ledger, nil)
	locks := findCheck(t, rep, "locks")
	if locks.Status != doctor.StatusOK {
		t.Fatalf("locks status = %s", locks.Status)
	}
	if _, hasHeld := locks.Details["held"]; hasHeld {
		t.Fatalf("expected no held lock details, got %+v", locks.Details)
	}

	// Drop a sentinel and re-check.
	if err := os.WriteFile(filepath.Join(locksDir, "deadbeef.lock"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	rep = runDoctor(t, root, ledger, nil)
	locks = findCheck(t, rep, "locks")
	held, ok := locks.Details["held"].([]string)
	if !ok || len(held) != 1 {
		t.Fatalf("expected 1 held lock, got %+v", locks.Details)
	}
}

func TestRun_AdapterEnvWarnsWhenAllUnset(t *testing.T) {
	root, ledger := newProject(t)
	rep := runDoctor(t, root, ledger, nil)
	env := findCheck(t, rep, "adapter_env")
	if env.Status != doctor.StatusWarn {
		t.Fatalf("expected warn when env unset, got %s", env.Status)
	}
}

func TestRun_AdapterEnvDoesNotLeakValues(t *testing.T) {
	root, ledger := newProject(t)
	env := map[string]string{
		"AGENT_ID":         "secret-id-do-not-leak",
		"AGENT_KIND":       "worker",
		"AGENT_HARNESS":    "pi",
		"AGENT_LEDGER_DIR": ledger,
	}
	rep := runDoctor(t, root, ledger, env)
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if contains(raw, "secret-id-do-not-leak") {
		t.Fatalf("doctor JSON leaked AGENT_ID value: %s", raw)
	}
}

func TestRun_JSONShape(t *testing.T) {
	root, ledger := newProject(t)
	rep := runDoctor(t, root, ledger, nil)
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["schema"] != doctor.SchemaName {
		t.Fatalf("schema = %v", decoded["schema"])
	}
	if _, ok := decoded["overall"]; !ok {
		t.Fatalf("missing overall: %s", raw)
	}
	if _, ok := decoded["checks"]; !ok {
		t.Fatalf("missing checks: %s", raw)
	}
}

func contains(haystack []byte, needle string) bool {
	return len(haystack) > 0 && len(needle) > 0 && bytesIndex(haystack, []byte(needle)) >= 0
}

// bytesIndex is a tiny stand-in for strings.Contains over []byte.
func bytesIndex(h, n []byte) int {
outer:
	for i := 0; i+len(n) <= len(h); i++ {
		for j := 0; j < len(n); j++ {
			if h[i+j] != n[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}

// TestRun_LockSentinelsCheck_StaleVsOwned exercises the v0.2.2
// lock_sentinels doctor check. A sentinel whose path_hash maps to an
// active intent is healthy and produces StatusOK; an unowned sentinel
// is reported as a stale entry with StatusWarn and recovery hints.
func TestRun_LockSentinelsCheck_StaleVsOwned(t *testing.T) {
	root, ledger := newProject(t)
	store, err := sqlite.Open(context.Background(), ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrator().Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Seed an active exclusive intent owning hash "ownedhash". We bypass
	// the domain layer and write directly: doctor's check only reads the
	// rows it needs.
	now := "2026-05-01T00:00:00.000Z"
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := store.DB().ExecContext(context.Background(), q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	mustExec(`INSERT INTO agents(agent_id,agent_kind,started_at) VALUES('a.test','worker',?)`, now)
	mustExec(`INSERT INTO events(event_id,schema,event_type,created_at,agent_id,task_id,payload_json)
	          VALUES('evt_doc','agent-ledger.v1','intent.opened',?,?,?, '{"intent_id":"int_doc"}')`,
		now, "a.test", "T-DOC")
	mustExec(`INSERT INTO intents(intent_id,event_id,task_id,agent_id,access_mode,conflict_policy,reason,status,opened_at)
	          VALUES('int_doc','evt_doc','T-DOC','a.test','write','exclusive','doctor test','active',?)`, now)
	mustExec(`INSERT INTO intent_paths(intent_id,path,realpath,path_hash,access_mode)
	          VALUES('int_doc','x.txt','x.txt','ownedhash','write')`)
	store.Close()

	locksDir := filepath.Join(ledger, "locks")
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// One owned, one stale.
	for _, name := range []string{"ownedhash.lock", "stalehash.lock"} {
		if err := os.WriteFile(filepath.Join(locksDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rep := runDoctor(t, root, ledger, nil)
	c := findCheck(t, rep, "lock_sentinels")
	if c.Status != doctor.StatusWarn {
		t.Fatalf("lock_sentinels status = %s, want warn: %+v", c.Status, c)
	}
	if got, _ := c.Details["stale_count"].(int); got != 1 {
		t.Fatalf("stale_count = %v, want 1: %+v", c.Details["stale_count"], c)
	}
	stale, _ := c.Details["stale"].([]string)
	if len(stale) != 1 || stale[0] != "stalehash" {
		t.Fatalf("stale = %v, want [stalehash]", stale)
	}

	// Remove the stale sentinel; check should flip to ok.
	if err := os.Remove(filepath.Join(locksDir, "stalehash.lock")); err != nil {
		t.Fatal(err)
	}
	rep = runDoctor(t, root, ledger, nil)
	c = findCheck(t, rep, "lock_sentinels")
	if c.Status != doctor.StatusOK {
		t.Fatalf("expected lock_sentinels ok after cleanup, got %s: %+v", c.Status, c)
	}
}

// TestRun_LockSentinelsCheck_NoLocksDir asserts that the absence of a
// locks directory is healthy (a fresh ledger has not yet held any
// exclusive claim) and produces StatusOK.
func TestRun_LockSentinelsCheck_NoLocksDir(t *testing.T) {
	root, ledger := newProject(t)
	rep := runDoctor(t, root, ledger, nil)
	c := findCheck(t, rep, "lock_sentinels")
	if c.Status != doctor.StatusOK {
		t.Fatalf("lock_sentinels status = %s, want ok: %+v", c.Status, c)
	}
}
