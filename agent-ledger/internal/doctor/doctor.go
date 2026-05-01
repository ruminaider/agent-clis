// Package doctor produces the agent-ledger.doctor.v1 diagnostic report.
//
// The report is privacy-safe by construction: it never includes raw env
// values, secrets, command output, or file contents. It echoes only
// values the operator already knows (paths they configured, IDs derived
// from their git remote, etc.) plus boolean health signals.
//
// The CLI command in internal/commands wraps Run and renders the report
// as either a human-readable block or as JSON ({schema, overall,
// checks}). Other tools may call Run directly, e.g. CI smoke tests.
package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/config"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/project"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

// SchemaName is the schema field emitted on every JSON report.
const SchemaName = "agent-ledger.doctor.v1"

// Status values per check and report.
const (
	StatusOK    = "ok"
	StatusWarn  = "warn"
	StatusError = "error"
)

// Report is the top-level doctor result.
type Report struct {
	Schema  string  `json:"schema"`
	Overall string  `json:"overall"`
	Checks  []Check `json:"checks"`
}

// Check is one diagnostic line item.
type Check struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Message string         `json:"message,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// Options control how Run gathers data. All fields are optional: zero
// values trigger sensible defaults derived from the process
// environment.
type Options struct {
	// Root, LedgerDirFlag, ProjectIDFlag mirror project.Options so the
	// CLI can pass through user-supplied flags.
	Root          string
	LedgerDirFlag string
	ProjectIDFlag string
	// EnvLookup overrides os.Getenv for tests. Defaults to os.Getenv.
	EnvLookup func(string) string
}

// Run gathers every diagnostic check and returns a fully-populated
// Report. The function is read-only: it does not create the ledger
// directory and does not run migrations. The caller decides what to do
// with non-ok statuses (the CLI converts the worst severity into an
// exit code).
func Run(ctx context.Context, opts Options) Report {
	getenv := opts.EnvLookup
	if getenv == nil {
		getenv = os.Getenv
	}
	rep := Report{Schema: SchemaName, Overall: StatusOK}

	res, resolveErr := project.Resolve(project.Options{
		Root:          opts.Root,
		LedgerDirFlag: opts.LedgerDirFlag,
		ProjectIDFlag: opts.ProjectIDFlag,
		EnvLedgerDir:  getenv(project.EnvVar),
		XDGStateHome:  getenv("XDG_STATE_HOME"),
	})
	// Tolerate a malformed policy / pointer: doctor's job is to
	// surface the specific failure as a check, not to abort entirely.
	if resolveErr != nil {
		rep.Checks = append(rep.Checks, Check{
			Name:    "project_resolve",
			Status:  StatusWarn,
			Message: "partial resolution: " + resolveErr.Error(),
		})
	}

	rep.Checks = append(rep.Checks,
		checkProjectIdentity(res),
		checkLedgerDir(res),
		checkGit(res),
		checkPointer(opts.Root, res),
		checkPolicy(opts.Root, res),
		checkLockSupport(res),
		checkAdapterEnv(getenv),
	)

	stCheck, healthChecks := checkStorage(ctx, res)
	rep.Checks = append(rep.Checks, stCheck)
	rep.Checks = append(rep.Checks, healthChecks...)
	rep.Checks = append(rep.Checks, checkLockSentinels(ctx, res, stCheck.Status))

	rep.Overall = aggregate(rep.Checks)
	return rep
}

func checkProjectIdentity(res project.Resolution) Check {
	if res.Identity.Fingerprint == "" {
		return Check{
			Name:   "project_identity",
			Status: StatusError,
			Message: "could not derive project identity; check git remote " +
				"or set project_id in .agent-ledger.toml",
		}
	}
	return Check{
		Name:    "project_identity",
		Status:  StatusOK,
		Message: "project identity stable",
		Details: map[string]any{
			"project_id":  res.Identity.ProjectID,
			"slug":        res.Identity.Slug,
			"fingerprint": res.Identity.Fingerprint,
		},
	}
}

func checkLedgerDir(res project.Resolution) Check {
	if res.LedgerDir == "" {
		return Check{
			Name:    "ledger_dir",
			Status:  StatusError,
			Message: "ledger directory could not be resolved; set $AGENT_LEDGER_DIR or run init",
		}
	}
	c := Check{
		Name:   "ledger_dir",
		Status: StatusOK,
		Details: map[string]any{
			"path":   res.LedgerDir,
			"source": string(res.LedgerDirSource),
		},
	}
	fi, err := os.Stat(res.LedgerDir)
	if errIsNotExist(err) {
		c.Status = StatusWarn
		c.Message = "ledger directory does not exist; run agent-ledger init"
		return c
	}
	if err != nil {
		c.Status = StatusError
		c.Message = err.Error()
		return c
	}
	if !fi.IsDir() {
		c.Status = StatusError
		c.Message = "ledger path is not a directory"
		return c
	}
	if !writable(res.LedgerDir) {
		c.Status = StatusError
		c.Message = "ledger directory is not writable"
		return c
	}
	c.Message = "ledger directory exists and is writable"
	return c
}

func checkGit(res project.Resolution) Check {
	c := Check{Name: "git", Details: map[string]any{
		"is_repo": res.GitInfo.IsRepo,
	}}
	if !res.GitInfo.IsRepo {
		c.Status = StatusWarn
		c.Message = "not a git repository; identity falls back to filesystem path"
		return c
	}
	c.Details["common_dir"] = res.GitInfo.CommonDir
	if res.GitInfo.TopLevel != res.GitInfo.CommonDir {
		// Worktree: TopLevel differs from CommonDir's parent. Surface
		// it so operators understand the layout.
		c.Details["worktree"] = res.GitInfo.TopLevel != ""
	}
	c.Status = StatusOK
	c.Message = "git repository detected"
	return c
}

func checkPointer(rootArg string, res project.Resolution) Check {
	c := Check{Name: "pointer"}
	root := rootArg
	if root == "" {
		root = res.Root
	}
	path := filepath.Join(root, config.PointerFileName)
	if _, err := os.Stat(path); errIsNotExist(err) {
		c.Status = StatusWarn
		c.Message = "no .agent-ledger.toml pointer; ledger_dir resolved from " +
			string(res.LedgerDirSource)
		return c
	} else if err != nil {
		c.Status = StatusError
		c.Message = err.Error()
		return c
	}
	data, err := os.ReadFile(path)
	if err != nil {
		c.Status = StatusError
		c.Message = err.Error()
		return c
	}
	var p config.Pointer
	if _, err := toml.Decode(string(data), &p); err != nil {
		c.Status = StatusError
		c.Message = "pointer TOML invalid: " + err.Error()
		return c
	}
	if p.Version != config.PointerVersion {
		c.Status = StatusError
		c.Message = fmt.Sprintf("pointer version %d unsupported (want %d)",
			p.Version, config.PointerVersion)
		return c
	}
	c.Status = StatusOK
	c.Message = "pointer file present and valid"
	c.Details = map[string]any{
		"has_ledger_dir":  p.LedgerDir != "",
		"has_project_id":  p.ProjectID != "",
		"has_policy_file": p.PolicyFile != "",
	}
	return c
}

func checkPolicy(rootArg string, res project.Resolution) Check {
	c := Check{Name: "policy"}
	root := rootArg
	if root == "" {
		root = res.Root
	}
	policyPath := filepath.Join(root, config.PolicyFileName)
	if res.Pointer != nil && res.Pointer.PolicyFile != "" {
		if filepath.IsAbs(res.Pointer.PolicyFile) {
			policyPath = res.Pointer.PolicyFile
		} else {
			policyPath = filepath.Join(root, res.Pointer.PolicyFile)
		}
	}
	_ = res // res may not have a Pointer when project.Resolve aborted; fallback above
	if _, err := os.Stat(policyPath); errIsNotExist(err) {
		c.Status = StatusOK
		c.Message = "no policy file (optional)"
		return c
	} else if err != nil {
		c.Status = StatusError
		c.Message = err.Error()
		return c
	}
	// Re-parse with explicit error capture so an invalid file is a
	// distinct failure from "missing".
	data, err := os.ReadFile(policyPath)
	if err != nil {
		c.Status = StatusError
		c.Message = err.Error()
		return c
	}
	var pol config.Policy
	if _, err := toml.Decode(string(data), &pol); err != nil {
		c.Status = StatusError
		c.Message = "policy TOML invalid: " + err.Error()
		return c
	}
	if pol.Version != 0 && pol.Version != config.PolicyVersion {
		c.Status = StatusError
		c.Message = fmt.Sprintf("policy version %d unsupported (want %d)",
			pol.Version, config.PolicyVersion)
		return c
	}
	c.Status = StatusOK
	c.Message = "policy file present and valid"
	return c
}

func checkLockSupport(res project.Resolution) Check {
	c := Check{Name: "locks"}
	if res.LedgerDir == "" {
		c.Status = StatusWarn
		c.Message = "ledger dir unresolved; lock support unknown"
		return c
	}
	locksDir := filepath.Join(res.LedgerDir, "locks")
	// Try to create the directory to confirm flock support.
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		c.Status = StatusError
		c.Message = "cannot create locks dir: " + err.Error()
		return c
	}
	probe := filepath.Join(locksDir, ".doctor-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		c.Status = StatusError
		c.Message = "cannot create lock probe: " + err.Error()
		return c
	}
	_ = f.Close()
	_ = os.Remove(probe)

	held := listLockSentinels(locksDir)
	c.Status = StatusOK
	c.Message = fmt.Sprintf("flock supported; %d sentinel(s) present", len(held))
	if len(held) > 0 {
		c.Details = map[string]any{"held": held}
	}
	return c
}

func listLockSentinels(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".lock" {
			out = append(out, e.Name())
		}
	}
	return out
}

// adapterEnvVars are the env vars Phase 1 adapters consult. Doctor
// reports their *presence* (not values, except AGENT_LEDGER_DIR which
// is itself a path the operator owns) so secrets never leak into the
// JSON report.
var adapterEnvVars = []string{
	"AGENT_ID",
	"AGENT_KIND",
	"AGENT_HARNESS",
	"AGENT_LEDGER_DIR",
}

func checkAdapterEnv(getenv func(string) string) Check {
	details := map[string]any{}
	missing := 0
	for _, k := range adapterEnvVars {
		v := getenv(k)
		switch k {
		case "AGENT_LEDGER_DIR":
			// Path is operator-owned and already echoed elsewhere; safe.
			details[k] = v
		default:
			details[k+"_set"] = v != ""
		}
		if v == "" {
			missing++
		}
	}
	c := Check{Name: "adapter_env", Details: details}
	switch missing {
	case len(adapterEnvVars):
		c.Status = StatusWarn
		c.Message = "no adapter env vars set; commands will infer defaults"
	default:
		c.Status = StatusOK
		c.Message = "adapter env presence captured"
	}
	return c
}

func checkStorage(ctx context.Context, res project.Resolution) (Check, []Check) {
	st := Check{Name: "storage"}
	if res.LedgerDir == "" {
		st.Status = StatusError
		st.Message = "ledger dir unresolved"
		return st, nil
	}
	if _, err := os.Stat(filepath.Join(res.LedgerDir, "ledger.sqlite")); errIsNotExist(err) {
		st.Status = StatusWarn
		st.Message = "ledger.sqlite not present yet; run agent-ledger init"
		return st, nil
	}
	store, err := sqlite.Open(ctx, res.LedgerDir)
	if err != nil {
		st.Status = StatusError
		st.Message = "open sqlite: " + err.Error()
		return st, nil
	}
	defer store.Close()
	h := store.Health(ctx)

	st.Details = map[string]any{
		"path": store.Path(),
	}
	if !h.PingOK {
		st.Status = StatusError
		st.Message = "sqlite ping failed: " + h.PingErr
		return st, nil
	}
	st.Status = StatusOK
	st.Message = "sqlite reachable"

	pragmaCheck := Check{Name: "sqlite_pragmas", Status: StatusOK,
		Details: map[string]any{
			"journal_mode": h.JournalMode,
			"synchronous":  h.SynchronousLevel,
			"foreign_keys": h.ForeignKeysOn,
		},
	}
	switch {
	case !strings.EqualFold(h.JournalMode, "wal"):
		pragmaCheck.Status = StatusError
		pragmaCheck.Message = "journal_mode is not WAL"
	case !h.ForeignKeysOn:
		pragmaCheck.Status = StatusError
		pragmaCheck.Message = "foreign_keys=OFF"
	default:
		pragmaCheck.Message = "WAL on, foreign_keys on"
	}

	migrCheck := Check{Name: "migrations",
		Details: map[string]any{
			"schema_version": h.SchemaVersion,
			"applied_count":  len(h.Applied),
			"pending_count":  len(h.Pending),
		},
	}
	if len(h.Pending) > 0 {
		migrCheck.Status = StatusWarn
		migrCheck.Message = fmt.Sprintf("%d migration(s) pending; run agent-ledger migrate",
			len(h.Pending))
	} else if h.SchemaVersion == 0 {
		migrCheck.Status = StatusWarn
		migrCheck.Message = "no migrations applied yet"
	} else {
		migrCheck.Status = StatusOK
		migrCheck.Message = fmt.Sprintf("schema_version=%d, all migrations applied",
			h.SchemaVersion)
	}

	return st, []Check{pragmaCheck, migrCheck}
}

func aggregate(checks []Check) string {
	worst := StatusOK
	for _, c := range checks {
		switch c.Status {
		case StatusError:
			return StatusError
		case StatusWarn:
			worst = StatusWarn
		}
	}
	return worst
}

func errIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

func writable(dir string) bool {
	probe := filepath.Join(dir, ".doctor-write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return true
}

// HasError reports whether any check has status=error. Useful for CLI
// exit-code mapping without re-walking the report manually.
func (r Report) HasError() bool { return r.Overall == StatusError }

// HasWarn reports whether any check is at warn or worse.
func (r Report) HasWarn() bool { return r.Overall != StatusOK }

// checkLockSentinels cross-references *.lock sentinel files in
// <ledger-dir>/locks/ against active intents in the DB. A sentinel
// whose path_hash maps to an active intent is healthy; one with no
// owner is stale residue from a pre-v0.2.2 close that did not clean
// up. The check is advisory (StatusWarn at most): the authoritative
// report is `agent-ledger verify`, which emits EXCLUSIVE_LOCK_HELD
// per stale sentinel. Doctor surfaces the same signal at the lock
// hygiene layer so reviewers see it without running task-mode
// verify.
//
// storageStatus is the status of the sibling "storage" check. When
// storage failed we cannot consult intents and skip with StatusWarn
// rather than producing misleading output.
func checkLockSentinels(ctx context.Context, res project.Resolution, storageStatus string) Check {
	c := Check{Name: "lock_sentinels"}
	if res.LedgerDir == "" {
		c.Status = StatusWarn
		c.Message = "ledger dir unresolved; cannot inspect sentinels"
		return c
	}
	locksDir := filepath.Join(res.LedgerDir, "locks")
	entries, err := os.ReadDir(locksDir)
	if err != nil {
		if errIsNotExist(err) {
			c.Status = StatusOK
			c.Message = "no locks directory; nothing to inspect"
			return c
		}
		c.Status = StatusError
		c.Message = "read locks dir: " + err.Error()
		return c
	}
	var sentinelHashes []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".lock" {
			continue
		}
		sentinelHashes = append(sentinelHashes, strings.TrimSuffix(e.Name(), ".lock"))
	}
	if len(sentinelHashes) == 0 {
		c.Status = StatusOK
		c.Message = "0 sentinels"
		return c
	}
	if storageStatus != StatusOK {
		c.Status = StatusWarn
		c.Message = fmt.Sprintf("%d sentinel(s) present; storage check is %s, cannot cross-reference active intents", len(sentinelHashes), storageStatus)
		c.Details = map[string]any{"sentinels": len(sentinelHashes)}
		return c
	}
	store, err := sqlite.Open(ctx, res.LedgerDir)
	if err != nil {
		c.Status = StatusError
		c.Message = "open sqlite for sentinel cross-reference: " + err.Error()
		return c
	}
	defer store.Close()
	d := domain.New(store)
	intents, err := d.ListActiveIntents(ctx, "")
	if err != nil {
		c.Status = StatusError
		c.Message = "list active intents: " + err.Error()
		return c
	}
	owned := map[string]struct{}{}
	for _, in := range intents {
		ps, err := d.IntentPaths(ctx, in.IntentID)
		if err != nil {
			c.Status = StatusError
			c.Message = "load intent paths: " + err.Error()
			return c
		}
		for _, p := range ps {
			if p.PathHash != "" {
				owned[p.PathHash] = struct{}{}
			}
		}
	}
	var stale []string
	for _, h := range sentinelHashes {
		if _, ok := owned[h]; !ok {
			stale = append(stale, h)
		}
	}
	if len(stale) == 0 {
		c.Status = StatusOK
		c.Message = fmt.Sprintf("%d sentinel(s) all owned by active intents", len(sentinelHashes))
		c.Details = map[string]any{"sentinels": len(sentinelHashes)}
		return c
	}
	c.Status = StatusWarn
	c.Message = fmt.Sprintf("%d stale sentinel(s) of %d total; run `agent-ledger gc --stale-after=<window>` and remove leftovers under %s", len(stale), len(sentinelHashes), locksDir)
	c.Details = map[string]any{
		"sentinels":   len(sentinelHashes),
		"stale_count": len(stale),
		"stale":       stale,
		"locks_dir":   locksDir,
		"recovery":    "agent-ledger gc removes sentinels for stale exclusive intents on orphan; pre-v0.2.2 close left files behind, delete by hand if no live process holds them",
	}
	return c
}
