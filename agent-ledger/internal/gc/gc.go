// Package gc implements stale-intent garbage collection per SPEC §18.13.
//
// "Garbage collection" here is a misnomer in the deletion sense: we never
// remove rows. Instead we mark `active` intents whose last heartbeat (or
// opened_at, when the intent never beat) is older than now-staleAfter as
// `orphaned` and emit one `intent.orphaned` event per change. The audit
// JSONL mirror records the same event so downstream systems can
// reconstruct the timeline.
//
// The package is dependency-light: it only needs *sqlite.Store and a
// duration. Callers can inject Now to make tests deterministic.
package gc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

// Options configures a GC run.
type Options struct {
	// StaleAfter is the inactivity window past which an active intent
	// is considered stale. Must be positive.
	StaleAfter time.Duration
	// Now returns the wall clock used to compute the cutoff. Defaults
	// to the store clock if nil.
	Now func() time.Time
	// AgentID is the actor recorded on intent.orphaned events. The
	// command-layer typically passes "agent-ledger.gc" so audit
	// consumers can filter automated GC events.
	AgentID string
}

// Result summarizes a GC run.
type Result struct {
	// Cutoff is the timestamp before which last-activity is considered
	// stale.
	Cutoff time.Time `json:"cutoff"`
	// Candidates is the count of active intents at or older than the
	// cutoff that were inspected.
	Candidates int `json:"candidates"`
	// Orphaned lists the IntentIDs successfully marked orphaned.
	Orphaned []string `json:"orphaned"`
	// Skipped lists IntentIDs that were no longer active when GC tried
	// to flip them (e.g. another process closed the intent in between
	// the list and the update). The presence of skipped IDs is normal
	// and not an error.
	Skipped []string `json:"skipped,omitempty"`
}

// Errors that callers may want to distinguish.
var (
	// ErrInvalidStaleAfter is returned when StaleAfter is non-positive.
	ErrInvalidStaleAfter = errors.New("gc: stale-after must be positive")
)

// ParseStaleAfter parses a Go duration string and validates that it is
// strictly positive. Returns a typed error for the empty / negative
// cases so the CLI can distinguish "bad usage" from storage failure.
func ParseStaleAfter(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("%w: empty value", ErrInvalidStaleAfter)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("gc: parse duration %q: %w", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%w: got %s", ErrInvalidStaleAfter, d)
	}
	return d, nil
}

// Run executes one GC pass against store. It returns a Result covering
// every intent inspected in this call. Re-running with the same window
// is idempotent: already-orphaned intents are not visible to the stale
// listing.
func Run(ctx context.Context, store *sqlite.Store, opts Options) (Result, error) {
	if opts.StaleAfter <= 0 {
		return Result{}, fmt.Errorf("%w: got %s", ErrInvalidStaleAfter, opts.StaleAfter)
	}
	now := opts.Now
	if now == nil {
		now = store.Clock()
	}
	cutoff := now().Add(-opts.StaleAfter)

	stale, err := store.ListStaleActiveIntents(ctx, cutoff)
	if err != nil {
		return Result{}, fmt.Errorf("gc: list stale: %w", err)
	}
	res := Result{
		Cutoff:     cutoff.UTC(),
		Candidates: len(stale),
	}
	reason := fmt.Sprintf("stale-after=%s", opts.StaleAfter)
	for _, si := range stale {
		actor := opts.AgentID
		if actor == "" {
			actor = si.AgentID
		}
		err := store.OrphanIntent(ctx, si.IntentID, actor, reason, now())
		switch {
		case err == nil:
			res.Orphaned = append(res.Orphaned, si.IntentID)
		case errors.Is(err, sqlite.ErrIntentNotActive):
			res.Skipped = append(res.Skipped, si.IntentID)
		default:
			return res, fmt.Errorf("gc: orphan %s: %w", si.IntentID, err)
		}
	}
	return res, nil
}
