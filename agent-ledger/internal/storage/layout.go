package storage

import (
	"os"
	"path/filepath"
)

// Layout describes the on-disk structure of a ledger directory. Task 003
// provides the SQLite implementation; Task 002 only needs to be able to
// create the skeleton directories so init has somewhere to write a
// pointer.
type Layout struct {
	Dir string
}

// SQLitePath returns the canonical ledger.sqlite path.
func (l Layout) SQLitePath() string { return filepath.Join(l.Dir, "ledger.sqlite") }

// AuditDir returns the audit JSONL directory.
func (l Layout) AuditDir() string { return filepath.Join(l.Dir, "audit") }

// BlobsDir returns the content-addressed blob root.
func (l Layout) BlobsDir() string { return filepath.Join(l.Dir, "blobs", "sha256") }

// LocksDir returns the OS-lock sentinel directory.
func (l Layout) LocksDir() string { return filepath.Join(l.Dir, "locks") }

// ConfigPath returns the in-ledger config.toml path.
func (l Layout) ConfigPath() string { return filepath.Join(l.Dir, "config.toml") }

// EnsureLayout creates the directory skeleton for a ledger. It does not
// touch the SQLite file: Task 003's migrator is responsible for that.
// EnsureLayout is idempotent.
func EnsureLayout(dir string) (Layout, error) {
	l := Layout{Dir: dir}
	for _, d := range []string{dir, l.AuditDir(), l.BlobsDir(), l.LocksDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return Layout{}, err
		}
	}
	return l, nil
}
