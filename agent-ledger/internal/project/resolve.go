package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/config"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/git"
)

// Source identifies which input the resolver picked for the ledger dir.
type Source string

const (
	// SourceEnv: $AGENT_LEDGER_DIR.
	SourceEnv Source = "env"
	// SourcePointer: ledger_dir from .agent-ledger.toml.
	SourcePointer Source = "pointer"
	// SourceFlag: explicit --ledger-dir flag passed by caller.
	SourceFlag Source = "flag"
	// SourceXDG: derived from XDG state default.
	SourceXDG Source = "xdg"
	// SourceGitPointer: discovered via git common-dir agent-ledger
	// symlink or pointer.toml fallback.
	SourceGitPointer Source = "git-pointer"
)

// EnvVar is the environment variable that overrides ledger directory.
const EnvVar = "AGENT_LEDGER_DIR"

// GitPointerName is the symlink/file name used under
// $(git rev-parse --git-common-dir) for ledger discoverability.
const GitPointerName = "agent-ledger"

// GitPointerFallbackName is used when symlinks are not available.
const GitPointerFallbackName = "pointer.toml"

// Options control resolution.
type Options struct {
	// Root is the project root; defaults to cwd when empty.
	Root string
	// LedgerDirFlag is the explicit --ledger-dir flag value, if any.
	LedgerDirFlag string
	// ProjectIDFlag is the explicit --project-id flag value, if any.
	ProjectIDFlag string
	// EnvLedgerDir is the value of $AGENT_LEDGER_DIR; pass os.Getenv
	// at the call site so tests can inject overrides.
	EnvLedgerDir string
	// HomeDir is used to compute the default XDG fallback. Defaults to
	// os.UserHomeDir() when empty.
	HomeDir string
	// XDGStateHome is the value of $XDG_STATE_HOME; empty falls back to
	// "<HomeDir>/.local/state".
	XDGStateHome string
}

// Resolution is the resolved view of a project.
type Resolution struct {
	Identity        Identity
	Root            string
	GitInfo         git.Info
	Pointer         *config.Pointer
	Policy          *config.Policy
	LedgerDir       string
	LedgerDirSource Source
	// Roots are every realpath-resolved directory considered "in the
	// project" for path scope checks. For git repos, this is the set of
	// worktree toplevels sharing the same common dir, with Root listed
	// first when present. For non-git projects, this is just [Root].
	// Path normalization picks the longest-prefix match across these
	// roots so that paths inside any worktree of the same repo are
	// accepted regardless of the invocation cwd.
	Roots []string
}

// Resolve performs the full discovery flow described in SPEC §8 and §8.1.
// It is read-only: it never creates files. Callers (`init`) decide whether
// to materialize anything.
func Resolve(opts Options) (Resolution, error) {
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return Resolution{}, err
	}
	res := Resolution{Root: root}

	gi, err := git.Discover(root)
	if err != nil && !errors.Is(err, git.ErrNoGit) {
		return Resolution{}, err
	}
	res.GitInfo = gi

	ptr, err := config.LoadPointer(root)
	if err != nil {
		return Resolution{}, err
	}
	res.Pointer = ptr

	pol, err := config.LoadPolicy(root, ptr)
	if err != nil {
		return Resolution{}, err
	}
	res.Policy = pol

	// Determine project_id with the precedence: flag > pointer > policy.
	projectID := opts.ProjectIDFlag
	if projectID == "" && ptr != nil {
		projectID = ptr.ProjectID
	}
	if projectID == "" && pol != nil {
		projectID = pol.Project.ID
	}

	// Non-git root only contributes when there is no git repo at all.
	nonGitRoot := ""
	if !gi.IsRepo {
		nonGitRoot = realpath(root)
	}

	res.Identity = Compute(Inputs{
		ProjectID:    projectID,
		OriginURL:    gi.OriginURL,
		GitCommonDir: gi.CommonDir,
		NonGitRoot:   nonGitRoot,
	})

	res.LedgerDir, res.LedgerDirSource = resolveLedgerDir(opts, res.Identity, gi, ptr)
	res.Roots = resolveRoots(root, gi)

	return res, nil
}

// resolveRoots returns the set of project roots used for path scope
// checks. For git repos it enumerates worktrees of the same common dir
// (best-effort; falls back to [root] if git fails). For non-git projects
// it returns just [root]. The supplied root is always present and listed
// first so relative-path joins remain stable.
func resolveRoots(root string, gi git.Info) []string {
	rootReal := realpath(root)
	if !gi.IsRepo {
		return []string{rootReal}
	}
	tops, err := git.Worktrees(rootReal)
	if err != nil || len(tops) == 0 {
		return []string{rootReal}
	}
	// Place rootReal first when it is one of the toplevels; otherwise
	// prepend it so relative-path joins still work from arbitrary cwds
	// (subdirectories of the main checkout, for example).
	out := make([]string, 0, len(tops)+1)
	seen := map[string]struct{}{}
	if _, ok := indexOf(tops, rootReal); !ok {
		out = append(out, rootReal)
		seen[rootReal] = struct{}{}
	}
	for _, t := range tops {
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

func indexOf(ss []string, want string) (int, bool) {
	for i, s := range ss {
		if s == want {
			return i, true
		}
	}
	return 0, false
}

func resolveLedgerDir(opts Options, id Identity, gi git.Info, ptr *config.Pointer) (string, Source) {
	if opts.LedgerDirFlag != "" {
		return absOrRaw(opts.LedgerDirFlag), SourceFlag
	}
	if opts.EnvLedgerDir != "" {
		return absOrRaw(opts.EnvLedgerDir), SourceEnv
	}
	if ptr != nil && ptr.LedgerDir != "" {
		return absOrRaw(ptr.LedgerDir), SourcePointer
	}
	// XDG default.
	if d := xdgDefault(opts, id); d != "" {
		// Optional discoverability check via git common-dir pointer:
		// only used when the XDG path has not yet been created and the
		// pointer is present, so existing setups remain consistent.
		if gi.CommonDir != "" {
			if alt := readGitPointer(gi.CommonDir); alt != "" {
				if _, err := os.Stat(d); errors.Is(err, os.ErrNotExist) {
					return alt, SourceGitPointer
				}
			}
		}
		return d, SourceXDG
	}
	return "", SourceXDG
}

// xdgDefault returns the default
// "<XDG_STATE_HOME>/agent-ledger/repos/<slug>-<fingerprint>" path. When
// XDG_STATE_HOME is unset, falls back to "~/.local/state".
func xdgDefault(opts Options, id Identity) string {
	state := opts.XDGStateHome
	if state == "" {
		home := opts.HomeDir
		if home == "" {
			h, err := os.UserHomeDir()
			if err != nil {
				return ""
			}
			home = h
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "agent-ledger", "repos", id.DirName())
}

// readGitPointer returns the resolved ledger dir from a git common-dir
// pointer (symlink or pointer.toml) or empty if none.
func readGitPointer(commonDir string) string {
	link := filepath.Join(commonDir, GitPointerName)
	if target, err := os.Readlink(link); err == nil {
		if !filepath.IsAbs(target) {
			target = filepath.Join(commonDir, target)
		}
		return target
	}
	if fi, err := os.Stat(link); err == nil && fi.IsDir() {
		return link
	}
	// pointer.toml fallback inside the agent-ledger dir.
	fallback := filepath.Join(commonDir, GitPointerName, GitPointerFallbackName)
	if data, err := os.ReadFile(fallback); err == nil {
		var p config.Pointer
		// Re-using the same TOML schema (just ledger_dir matters).
		if err := unmarshalLedgerDirOnly(data, &p); err == nil && p.LedgerDir != "" {
			return p.LedgerDir
		}
	}
	return ""
}

func unmarshalLedgerDirOnly(data []byte, p *config.Pointer) error {
	_, err := toml.Decode(string(data), p)
	return err
}

// resolveRoot returns absolute root, defaulting to cwd.
func resolveRoot(root string) (string, error) {
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root = cwd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func absOrRaw(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func realpath(p string) string {
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		return rp
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// Validate returns a non-nil error if the resolution is unusable for
// init or doctor (currently: missing ledger dir).
func (r Resolution) Validate() error {
	if r.LedgerDir == "" {
		return fmt.Errorf("project: could not resolve ledger directory; set %s or run init --ledger-dir", EnvVar)
	}
	return nil
}
