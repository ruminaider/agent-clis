package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/events"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/id"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage"
)

// StaleIntent is the projection used by gc to identify intents that
// should be marked orphaned. It carries just the columns gc needs;
// callers that want the full Intent row should query domain helpers.
type StaleIntent struct {
	IntentID           string
	AgentID            string
	TaskID             string
	OpenedAt           string
	LastHeartbeatAt    string
	HeartbeatExpiresAt string
}

// ListStaleActiveIntents returns active intents whose most recent
// activity timestamp (heartbeat if present, opened_at otherwise) is
// strictly before cutoff. Already-orphaned or closed intents are
// excluded.
func (s *Store) ListStaleActiveIntents(ctx context.Context, cutoff time.Time) ([]StaleIntent, error) {
	cutoffStr := id.FormatTimestamp(cutoff.UTC())
	q := `
		SELECT intent_id, agent_id, task_id, opened_at,
		       COALESCE(last_heartbeat_at, ''),
		       COALESCE(heartbeat_expires_at, '')
		FROM intents
		WHERE status = 'active'
		  AND COALESCE(NULLIF(last_heartbeat_at, ''), opened_at) < ?
		ORDER BY opened_at ASC
	`
	rows, err := s.db.QueryContext(ctx, q, cutoffStr)
	if err != nil {
		return nil, mapStorageError(err)
	}
	defer rows.Close()
	var out []StaleIntent
	for rows.Next() {
		var si StaleIntent
		if err := rows.Scan(&si.IntentID, &si.AgentID, &si.TaskID,
			&si.OpenedAt, &si.LastHeartbeatAt, &si.HeartbeatExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, si)
	}
	return out, rows.Err()
}

// OrphanIntent flips intent.status from 'active' to 'orphaned' and
// emits an intent.orphaned event in the same transaction. Already-
// orphaned or closed intents are not modified and the call returns
// ErrIntentNotActive so callers can distinguish "did nothing" from
// "I/O failure".
func (s *Store) OrphanIntent(ctx context.Context, intentID, agentID, reason string, now time.Time) error {
	if intentID == "" {
		return fmt.Errorf("orphan: empty intent_id")
	}
	occurred := id.FormatTimestamp(now.UTC())
	payloadMap := map[string]any{
		"intent_id": intentID,
	}
	if reason != "" {
		// reason is privacy-safe: it is the operator-provided GC
		// summary ("stale-after=24h"), never user content.
		payloadMap["reason"] = reason
	}
	payload, err := events.MarshalPayload(payloadMap)
	if err != nil {
		return err
	}
	ev := storage.Event{
		Type:        "intent.orphaned",
		AgentID:     agentID,
		IntentID:    intentID,
		OccurredAt:  occurred,
		PayloadJSON: payload,
	}
	return s.WriteDomainEvent(ctx, ev, func(ctx context.Context, tx *sql.Tx) error {
		res, ierr := tx.ExecContext(ctx, `
			UPDATE intents
			SET status = 'orphaned', closed_at = ?, close_outcome = 'orphaned', close_reason = ?
			WHERE intent_id = ? AND status = 'active'
		`, occurred, reason, intentID)
		if ierr != nil {
			return ierr
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrIntentNotActive
		}
		return nil
	})
}

// ErrIntentNotActive is returned by OrphanIntent when the target
// intent is already non-active (closed or orphaned).
var ErrIntentNotActive = fmt.Errorf("intent not active")
