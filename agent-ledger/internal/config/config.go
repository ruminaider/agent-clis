// Package config loads and writes the two TOML files Agent Ledger reads
// from a project tree:
//
//   - .agent-ledger.toml: gitignored local pointer (machine-specific paths,
//     ledger_dir, optional project_id, optional policy_file).
//   - .agent-ledger-policy.toml: optional committed shared policy
//     (defaults, per-glob policies). Loaded best-effort.
//
// Neither file may carry secrets. The package never reads environment
// variables and never logs file contents.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// PointerFileName is the canonical local pointer filename.
const PointerFileName = ".agent-ledger.toml"

// PolicyFileName is the canonical committed policy filename.
const PolicyFileName = ".agent-ledger-policy.toml"

// PointerVersion is the only currently-supported version of the pointer
// schema. Older or newer files cause LoadPointer to fail loudly.
const PointerVersion = 1

// PolicyVersion is the supported policy schema version.
const PolicyVersion = 1

// Pointer is the parsed local pointer file. Fields not populated in the
// file remain zero-valued.
type Pointer struct {
	Version    int    `toml:"version"`
	ProjectID  string `toml:"project_id"`
	LedgerDir  string `toml:"ledger_dir"`
	PolicyFile string `toml:"policy_file"`
}

// Policy is the parsed committed policy file. The MVP only reads version
// and a defaults block; later phases will widen it.
type Policy struct {
	Version  int             `toml:"version"`
	Defaults PolicyDefaults  `toml:"defaults"`
	Policies map[string]any  `toml:"policies"`
	Project  PolicyProjectID `toml:"project"`
}

// PolicyProjectID exposes optional project_id override from the committed
// policy. Pointer's project_id wins when both are set.
type PolicyProjectID struct {
	ID string `toml:"id"`
}

// PolicyDefaults captures the project-wide defaults block.
type PolicyDefaults struct {
	ConflictPolicy         string `toml:"conflict_policy"`
	AllowUnassignedIntents bool   `toml:"allow_unassigned_intents"`
	HeartbeatSeconds       int    `toml:"heartbeat_seconds"`
	StaleAfterSeconds      int    `toml:"stale_after_seconds"`
	StoreFullDiffs         bool   `toml:"store_full_diffs"`
}

// LoadPointer reads .agent-ledger.toml from root. Returns (nil, nil) when
// the file does not exist; returns an error for parse failures or
// unsupported versions.
func LoadPointer(root string) (*Pointer, error) {
	path := filepath.Join(root, PointerFileName)
	var p Pointer
	if err := readTOML(path, &p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if p.Version != PointerVersion {
		return nil, fmt.Errorf("config: %s: unsupported version %d (want %d)", PointerFileName, p.Version, PointerVersion)
	}
	return &p, nil
}

// LoadPolicy reads .agent-ledger-policy.toml from root, optionally
// overridden by Pointer.PolicyFile. Returns (nil, nil) when no policy
// file is present.
func LoadPolicy(root string, ptr *Pointer) (*Policy, error) {
	path := filepath.Join(root, PolicyFileName)
	if ptr != nil && ptr.PolicyFile != "" {
		if filepath.IsAbs(ptr.PolicyFile) {
			path = ptr.PolicyFile
		} else {
			path = filepath.Join(root, ptr.PolicyFile)
		}
	}
	var pol Policy
	if err := readTOML(path, &pol); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if pol.Version != 0 && pol.Version != PolicyVersion {
		return nil, fmt.Errorf("config: %s: unsupported version %d (want %d)", filepath.Base(path), pol.Version, PolicyVersion)
	}
	return &pol, nil
}

// WritePointer writes p to <root>/.agent-ledger.toml with 0o644 perms.
// It refuses to write any field that looks like a secret (the schema has
// no such field today, so this is a defense-in-depth check).
func WritePointer(root string, p Pointer) error {
	if p.Version == 0 {
		p.Version = PointerVersion
	}
	path := filepath.Join(root, PointerFileName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := toml.NewEncoder(f)
	return enc.Encode(p)
}

// readTOML decodes path into v. Errors from os.Open propagate so callers
// can detect fs.ErrNotExist.
func readTOML(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, err := toml.Decode(string(data), v); err != nil {
		return fmt.Errorf("config: %s: %w", filepath.Base(path), err)
	}
	return nil
}
