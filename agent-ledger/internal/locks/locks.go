// Package locks provides best-effort advisory file locks for SPEC §28
// `exclusive` policy enforcement. The lock is keyed on a path hash and
// stored under <ledger-dir>/locks/<path-hash>.lock.
//
// Per dp-006, lock acquisition is best-effort. Failure to acquire does
// not block claim creation; verify reports EXCLUSIVE_LOCK_HELD when it
// detects a held lock. The DB state remains authoritative.
package locks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gofrs/flock"
)

// LockSet is a per-store set of currently-held flock handles.
type LockSet struct {
	dir string

	mu   sync.Mutex
	held map[string]*flock.Flock
}

// NewLockSet returns a LockSet writing sentinels under dir/<path-hash>.lock.
func NewLockSet(dir string) *LockSet {
	return &LockSet{dir: dir, held: map[string]*flock.Flock{}}
}

// TryLock attempts a non-blocking exclusive lock on the sentinel for
// pathHash. The boolean reports whether the lock was acquired by THIS
// process. err is non-nil only for unrecoverable filesystem errors.
func (l *LockSet) TryLock(pathHash string) (bool, error) {
	if l == nil || l.dir == "" || pathHash == "" {
		return false, nil
	}
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return false, err
	}
	p := filepath.Join(l.dir, pathHash+".lock")
	fl := flock.New(p)
	got, err := fl.TryLock()
	if err != nil {
		return false, fmt.Errorf("locks: trylock %s: %w", p, err)
	}
	if !got {
		return false, nil
	}
	l.mu.Lock()
	l.held[pathHash] = fl
	l.mu.Unlock()
	return true, nil
}

// Release drops a previously-acquired lock. Returns nil when no lock is
// tracked for pathHash.
func (l *LockSet) Release(pathHash string) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	fl, ok := l.held[pathHash]
	if ok {
		delete(l.held, pathHash)
	}
	l.mu.Unlock()
	if !ok {
		return nil
	}
	if err := fl.Unlock(); err != nil && !errors.Is(err, os.ErrClosed) {
		return err
	}
	return nil
}

// ReleaseAll releases every lock in the set.
func (l *LockSet) ReleaseAll() {
	if l == nil {
		return
	}
	l.mu.Lock()
	hashes := make([]string, 0, len(l.held))
	for h := range l.held {
		hashes = append(hashes, h)
	}
	l.mu.Unlock()
	for _, h := range hashes {
		_ = l.Release(h)
	}
}

// RemoveSentinel deletes the sentinel file <dir>/<pathHash>.lock if
// present. Used by close and gc paths to keep the locks directory
// clean once the owning intent transitions out of active. The call is
// best-effort: callers expect to log and continue on error rather
// than abort the close/gc transaction. SPEC §28 keeps the DB row
// authoritative; this is purely housekeeping for the verifier's
// EXCLUSIVE_LOCK_HELD scan.
//
// Returns nil if the sentinel does not exist or pathHash/dir is empty.
func RemoveSentinel(dir, pathHash string) error {
	if dir == "" || pathHash == "" {
		return nil
	}
	p := filepath.Join(dir, pathHash+".lock")
	if err := os.Remove(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("locks: remove %s: %w", p, err)
	}
	return nil
}
