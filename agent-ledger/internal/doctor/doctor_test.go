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
