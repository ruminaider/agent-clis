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
