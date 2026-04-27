// Package domain provides typed domain entities and storage helpers
// shared across the Phase 1 command implementations: assignments,
// intents, conflicts, agents, and intent paths.
//
// Storage helpers in this package wrap *sqlite.Store with read and
// write methods scoped to a single domain concept. Writers that emit
// events go through Store.WriteDomainEvent so the domain row, the
// events row, and the audit JSONL line are produced atomically.
package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/events"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/id"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
)

// Conflict policies (SPEC §15).
const (
	PolicyNone      = "none"
	PolicyWarn      = "warn"
	PolicyExclusive = "exclusive"
)

// Intent access modes (SPEC §11.4).
const (
	AccessObserve    = "observe"
	AccessRead       = "read"
	AccessWrite      = "write"
	AccessReviewOnly = "review-only"
)

// Intent statuses (SPEC §11.4).
const (
	IntentActive   = "active"
	IntentClosed   = "closed"
	IntentOrphaned = "orphaned"
)

// Close outcomes (SPEC §18.7).
const (
	OutcomeCompleted  = "completed"
	OutcomeAbandoned  = "abandoned"
	OutcomeSuperseded = "superseded"
)

// Conflict statuses (SPEC §11.9).
const (
	ConflictDetected     = "detected"
	ConflictAcknowledged = "acknowledged"
	ConflictIgnored      = "ignored"
	ConflictEscalated    = "escalated"
	ConflictResolved     = "resolved"
)

// ValidPolicy reports whether p is one of the allowed conflict policies.
func ValidPolicy(p string) bool {
	switch p {
	case PolicyNone, PolicyWarn, PolicyExclusive:
		return true
	}
	return false
}

// ValidAccessMode reports whether m is one of the allowed access modes.
func ValidAccessMode(m string) bool {
	switch m {
	case AccessObserve, AccessRead, AccessWrite, AccessReviewOnly:
		return true
	}
	return false
}

// ValidOutcome reports whether o is one of the allowed close outcomes.
func ValidOutcome(o string) bool {
	switch o {
	case OutcomeCompleted, OutcomeAbandoned, OutcomeSuperseded:
		return true
	}
	return false
}

// Agent mirrors the agents table.
type Agent struct {
	AgentID        string
	AgentKind      string
	Harness        string
	Model          string
	ParentAgentID  string
	OrchestratorID string
	StartedAt      string
	Metadata       map[string]any
}

// Assignment mirrors the assignments table.
type Assignment struct {
	AssignmentID    string
	EventID         string
	TaskID          string
	OrchestratorID  string
	AssignedAgentID string
	AllowedPaths    []string
	ForbiddenPaths  []string
	ConflictPolicy  string
	Reason          string
	Status          string
	CreatedAt       string
	Metadata        map[string]any
}

// Intent mirrors the intents table.
type Intent struct {
	IntentID           string
	EventID            string
	AssignmentID       string
	TaskID             string
	AgentID            string
	AccessMode         string
	ConflictPolicy     string
	Reason             string
	Status             string
	OpenedAt           string
	LastHeartbeatAt    string
	HeartbeatExpiresAt string
	ClosedAt           string
	CloseOutcome       string
	CloseReason        string
	Metadata           map[string]any
}

// IntentPath mirrors a single row in intent_paths.
type IntentPath struct {
	IntentID   string
	Path       string
	RealPath   string
	PathHash   string
	AccessMode string
}

// Conflict mirrors the conflicts table.
type Conflict struct {
	ConflictID            string
	EventID               string
	Path                  string
	PathHash              string
	ExistingIntentID      string
	NewIntentID           string
	Policy                string
	Status                string
	DetectedAt            string
	AcknowledgedAt        string
	AcknowledgedByAgentID string
	Resolution            string
	Metadata              map[string]any
}

// Store wraps a *sqlite.Store with domain query/write helpers. The
// underlying store remains the source of truth for the database
// connection and clock.
type Store struct {
	S *sqlite.Store
}

// New wraps s.
func New(s *sqlite.Store) *Store { return &Store{S: s} }

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nsToString(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}

func encodeMeta(m map[string]any) (string, error) {
	if m == nil {
		return "{}", nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeMeta(raw string) map[string]any {
	if raw == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]any{}
	}
	return m
}

func encodePaths(p []string) (string, error) {
	if p == nil {
		p = []string{}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodePaths(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var p []string
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return []string{}
	}
	return p
}

// UpsertAgent inserts or replaces an agents row. Idempotent on agent_id.
func (s *Store) UpsertAgent(ctx context.Context, a Agent) error {
	if a.AgentID == "" || a.AgentKind == "" {
		return errors.New("domain: agent_id and agent_kind required")
	}
	if a.StartedAt == "" {
		a.StartedAt = id.FormatTimestamp(s.S.Clock()())
	}
	meta, err := encodeMeta(a.Metadata)
	if err != nil {
		return err
	}
	_, err = s.S.DB().ExecContext(ctx, `
		INSERT INTO agents(agent_id, agent_kind, harness, model, parent_agent_id, orchestrator_id, started_at, metadata_json)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			agent_kind=excluded.agent_kind,
			harness=COALESCE(excluded.harness, agents.harness),
			model=COALESCE(excluded.model, agents.model),
			parent_agent_id=COALESCE(excluded.parent_agent_id, agents.parent_agent_id),
			orchestrator_id=COALESCE(excluded.orchestrator_id, agents.orchestrator_id)
	`, a.AgentID, a.AgentKind, nullable(a.Harness), nullable(a.Model), nullable(a.ParentAgentID), nullable(a.OrchestratorID), a.StartedAt, meta)
	return err
}

// AgentExists reports whether an agent row with the given id is
// present.
func (s *Store) AgentExists(ctx context.Context, agentID string) (bool, error) {
	row := s.S.DB().QueryRowContext(ctx, `SELECT 1 FROM agents WHERE agent_id = ? LIMIT 1`, agentID)
	var n int
	if err := row.Scan(&n); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// InsertAssignment writes an assignments row plus a task.assigned event.
func (s *Store) InsertAssignment(ctx context.Context, a Assignment) (Assignment, error) {
	if a.AssignmentID == "" {
		nid, err := s.S.IDGen().New(id.PrefixAssignment)
		if err != nil {
			return a, err
		}
		a.AssignmentID = nid
	}
	if a.EventID == "" {
		nid, err := s.S.IDGen().New(id.PrefixEvent)
		if err != nil {
			return a, err
		}
		a.EventID = nid
	}
	if a.CreatedAt == "" {
		a.CreatedAt = id.FormatTimestamp(s.S.Clock()())
	}
	if a.Status == "" {
		a.Status = "active"
	}
	allowed, err := encodePaths(a.AllowedPaths)
	if err != nil {
		return a, err
	}
	forbid, err := encodePaths(a.ForbiddenPaths)
	if err != nil {
		return a, err
	}
	meta, err := encodeMeta(a.Metadata)
	if err != nil {
		return a, err
	}
	payload, err := events.MarshalPayload(map[string]any{
		"assignment_id":   a.AssignmentID,
		"task_id":         a.TaskID,
		"orchestrator_id": a.OrchestratorID,
		"assigned_agent":  a.AssignedAgentID,
		"allowed_paths":   a.AllowedPaths,
		"forbidden_paths": a.ForbiddenPaths,
		"conflict_policy": a.ConflictPolicy,
		"reason_sha256":   sha256Hex(a.Reason),
	})
	if err != nil {
		return a, err
	}
	ev := storage.Event{
		EventID:      a.EventID,
		Type:         "task.assigned",
		AgentID:      a.OrchestratorID,
		TaskID:       a.TaskID,
		AssignmentID: a.AssignmentID,
		OccurredAt:   a.CreatedAt,
		PayloadJSON:  payload,
	}
	err = s.S.WriteDomainEvent(ctx, ev, func(ctx context.Context, tx *sql.Tx) error {
		_, ierr := tx.ExecContext(ctx, `
			INSERT INTO assignments(assignment_id, event_id, task_id, orchestrator_id, assigned_agent_id, allowed_paths_json, forbidden_paths_json, conflict_policy, reason, status, created_at, metadata_json)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, a.AssignmentID, a.EventID, a.TaskID, a.OrchestratorID, nullable(a.AssignedAgentID), allowed, forbid, a.ConflictPolicy, a.Reason, a.Status, a.CreatedAt, meta)
		return ierr
	})
	return a, err
}

// LatestActiveAssignmentForTask returns the most recent active
// assignment for taskID, or sql.ErrNoRows when none exists.
func (s *Store) LatestActiveAssignmentForTask(ctx context.Context, taskID string) (Assignment, error) {
	row := s.S.DB().QueryRowContext(ctx, `
		SELECT assignment_id, event_id, task_id, orchestrator_id, COALESCE(assigned_agent_id, ''),
		       allowed_paths_json, forbidden_paths_json, conflict_policy, reason, status, created_at, metadata_json
		FROM assignments
		WHERE task_id = ? AND status = 'active'
		ORDER BY created_at DESC, assignment_id DESC
		LIMIT 1
	`, taskID)
	var a Assignment
	var allowed, forbid, meta string
	if err := row.Scan(&a.AssignmentID, &a.EventID, &a.TaskID, &a.OrchestratorID, &a.AssignedAgentID, &allowed, &forbid, &a.ConflictPolicy, &a.Reason, &a.Status, &a.CreatedAt, &meta); err != nil {
		return Assignment{}, err
	}
	a.AllowedPaths = decodePaths(allowed)
	a.ForbiddenPaths = decodePaths(forbid)
	a.Metadata = decodeMeta(meta)
	return a, nil
}

// InsertIntent writes an intents row plus intent_paths plus an
// intent.opened event in one transaction.
func (s *Store) InsertIntent(ctx context.Context, in Intent, ipaths []IntentPath) (Intent, error) {
	if in.IntentID == "" {
		nid, err := s.S.IDGen().New(id.PrefixIntent)
		if err != nil {
			return in, err
		}
		in.IntentID = nid
	}
	if in.EventID == "" {
		nid, err := s.S.IDGen().New(id.PrefixEvent)
		if err != nil {
			return in, err
		}
		in.EventID = nid
	}
	if in.OpenedAt == "" {
		in.OpenedAt = id.FormatTimestamp(s.S.Clock()())
	}
	if in.Status == "" {
		in.Status = IntentActive
	}
	meta, err := encodeMeta(in.Metadata)
	if err != nil {
		return in, err
	}

	pathsForPayload := make([]map[string]any, 0, len(ipaths))
	for _, p := range ipaths {
		pathsForPayload = append(pathsForPayload, map[string]any{
			"path":      p.Path,
			"path_hash": p.PathHash,
		})
	}

	payload, err := events.MarshalPayload(map[string]any{
		"intent_id":       in.IntentID,
		"task_id":         in.TaskID,
		"assignment_id":   in.AssignmentID,
		"access_mode":     in.AccessMode,
		"conflict_policy": in.ConflictPolicy,
		"reason_sha256":   sha256Hex(in.Reason),
		"paths":           pathsForPayload,
	})
	if err != nil {
		return in, err
	}
	ev := storage.Event{
		EventID:      in.EventID,
		Type:         "intent.opened",
		AgentID:      in.AgentID,
		TaskID:       in.TaskID,
		IntentID:     in.IntentID,
		AssignmentID: in.AssignmentID,
		OccurredAt:   in.OpenedAt,
		PayloadJSON:  payload,
	}
	for i := range ipaths {
		ipaths[i].IntentID = in.IntentID
	}
	err = s.S.WriteDomainEvent(ctx, ev, func(ctx context.Context, tx *sql.Tx) error {
		if _, ierr := tx.ExecContext(ctx, `
			INSERT INTO intents(intent_id, event_id, assignment_id, task_id, agent_id, access_mode, conflict_policy, reason, status, opened_at, last_heartbeat_at, heartbeat_expires_at, metadata_json)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, in.IntentID, in.EventID, nullable(in.AssignmentID), in.TaskID, in.AgentID, in.AccessMode, in.ConflictPolicy, in.Reason, in.Status, in.OpenedAt, nullable(in.LastHeartbeatAt), nullable(in.HeartbeatExpiresAt), meta); ierr != nil {
			return ierr
		}
		for _, p := range ipaths {
			if _, ierr := tx.ExecContext(ctx, `
				INSERT INTO intent_paths(intent_id, path, realpath, path_hash, access_mode)
				VALUES(?, ?, ?, ?, ?)
			`, p.IntentID, p.Path, p.RealPath, p.PathHash, p.AccessMode); ierr != nil {
				return ierr
			}
		}
		return nil
	})
	return in, err
}

// IntentByID loads a single intent.
func (s *Store) IntentByID(ctx context.Context, intentID string) (Intent, error) {
	row := s.S.DB().QueryRowContext(ctx, `
		SELECT intent_id, event_id, COALESCE(assignment_id, ''), task_id, agent_id, access_mode, conflict_policy, reason, status, opened_at,
		       COALESCE(last_heartbeat_at, ''), COALESCE(heartbeat_expires_at, ''), COALESCE(closed_at, ''), COALESCE(close_outcome, ''), COALESCE(close_reason, ''), metadata_json
		FROM intents WHERE intent_id = ?
	`, intentID)
	var in Intent
	var meta string
	if err := row.Scan(&in.IntentID, &in.EventID, &in.AssignmentID, &in.TaskID, &in.AgentID, &in.AccessMode, &in.ConflictPolicy, &in.Reason, &in.Status, &in.OpenedAt, &in.LastHeartbeatAt, &in.HeartbeatExpiresAt, &in.ClosedAt, &in.CloseOutcome, &in.CloseReason, &meta); err != nil {
		return Intent{}, err
	}
	in.Metadata = decodeMeta(meta)
	return in, nil
}

// IntentPaths returns the paths for an intent.
func (s *Store) IntentPaths(ctx context.Context, intentID string) ([]IntentPath, error) {
	rows, err := s.S.DB().QueryContext(ctx, `SELECT intent_id, path, realpath, path_hash, access_mode FROM intent_paths WHERE intent_id = ? ORDER BY path`, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IntentPath
	for rows.Next() {
		var p IntentPath
		if err := rows.Scan(&p.IntentID, &p.Path, &p.RealPath, &p.PathHash, &p.AccessMode); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ActiveIntentsByPathHashes returns active intents that overlap any of
// pathHashes. Each result row is paired with the matching path hash.
func (s *Store) ActiveIntentsByPathHashes(ctx context.Context, pathHashes []string) ([]IntentPath, error) {
	if len(pathHashes) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(pathHashes))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(pathHashes))
	for _, h := range pathHashes {
		args = append(args, h)
	}
	q := fmt.Sprintf(`
		SELECT ip.intent_id, ip.path, ip.realpath, ip.path_hash, ip.access_mode
		FROM intent_paths ip
		JOIN intents i ON i.intent_id = ip.intent_id
		WHERE i.status = 'active' AND ip.path_hash IN (%s)
	`, placeholders)
	rows, err := s.S.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IntentPath
	for rows.Next() {
		var p IntentPath
		if err := rows.Scan(&p.IntentID, &p.Path, &p.RealPath, &p.PathHash, &p.AccessMode); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Heartbeat updates the heartbeat columns and writes intent.heartbeat.
func (s *Store) Heartbeat(ctx context.Context, intentID, agentID string, now time.Time, expires time.Time) error {
	occurred := id.FormatTimestamp(now)
	expStr := id.FormatTimestamp(expires)
	payload, err := events.MarshalPayload(map[string]any{
		"intent_id":            intentID,
		"heartbeat_expires_at": expStr,
	})
	if err != nil {
		return err
	}
	ev := storage.Event{
		Type:        "intent.heartbeat",
		AgentID:     agentID,
		IntentID:    intentID,
		OccurredAt:  occurred,
		PayloadJSON: payload,
	}
	return s.S.WriteDomainEvent(ctx, ev, func(ctx context.Context, tx *sql.Tx) error {
		res, ierr := tx.ExecContext(ctx, `
			UPDATE intents SET last_heartbeat_at = ?, heartbeat_expires_at = ?
			WHERE intent_id = ? AND status = 'active'
		`, occurred, expStr, intentID)
		if ierr != nil {
			return ierr
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("intent %s not active", intentID)
		}
		return nil
	})
}

// Close updates intent.status to closed and writes intent.closed.
func (s *Store) Close(ctx context.Context, intentID, agentID, outcome, summary string, now time.Time) error {
	occurred := id.FormatTimestamp(now)
	if outcome == "" {
		outcome = OutcomeCompleted
	}
	if !ValidOutcome(outcome) {
		return fmt.Errorf("invalid close outcome %q", outcome)
	}
	payload, err := events.MarshalPayload(map[string]any{
		"intent_id":      intentID,
		"close_outcome":  outcome,
		"summary_sha256": sha256Hex(summary),
	})
	if err != nil {
		return err
	}
	ev := storage.Event{
		Type:        "intent.closed",
		AgentID:     agentID,
		IntentID:    intentID,
		OccurredAt:  occurred,
		PayloadJSON: payload,
	}
	return s.S.WriteDomainEvent(ctx, ev, func(ctx context.Context, tx *sql.Tx) error {
		res, ierr := tx.ExecContext(ctx, `
			UPDATE intents SET status = 'closed', closed_at = ?, close_outcome = ?, close_reason = ?
			WHERE intent_id = ? AND status = 'active'
		`, occurred, outcome, summary, intentID)
		if ierr != nil {
			return ierr
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("intent %s not active or already closed", intentID)
		}
		return nil
	})
}

// SupersedeIntent closes oldID with close_outcome=superseded and emits
// intent.superseded. Used by claim --supersede.
func (s *Store) SupersedeIntent(ctx context.Context, oldID, newID, agentID string, now time.Time) error {
	occurred := id.FormatTimestamp(now)
	payload, err := events.MarshalPayload(map[string]any{
		"intent_id":     oldID,
		"superseded_by": newID,
	})
	if err != nil {
		return err
	}
	ev := storage.Event{
		Type:        "intent.superseded",
		AgentID:     agentID,
		IntentID:    oldID,
		OccurredAt:  occurred,
		PayloadJSON: payload,
	}
	return s.S.WriteDomainEvent(ctx, ev, func(ctx context.Context, tx *sql.Tx) error {
		// Append metadata JSON note via SQL; SQLite supports json_set.
		_, ierr := tx.ExecContext(ctx, `
			UPDATE intents
			SET status = 'closed',
			    closed_at = ?,
			    close_outcome = 'superseded',
			    metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.superseded_by', ?)
			WHERE intent_id = ? AND status = 'active'
		`, occurred, newID, oldID)
		return ierr
	})
}

// InsertConflict writes a conflicts row and a conflict.detected event.
func (s *Store) InsertConflict(ctx context.Context, c Conflict) (Conflict, error) {
	if c.ConflictID == "" {
		nid, err := s.S.IDGen().New(id.PrefixConflict)
		if err != nil {
			return c, err
		}
		c.ConflictID = nid
	}
	if c.EventID == "" {
		nid, err := s.S.IDGen().New(id.PrefixEvent)
		if err != nil {
			return c, err
		}
		c.EventID = nid
	}
	if c.DetectedAt == "" {
		c.DetectedAt = id.FormatTimestamp(s.S.Clock()())
	}
	if c.Status == "" {
		c.Status = ConflictDetected
	}
	meta, err := encodeMeta(c.Metadata)
	if err != nil {
		return c, err
	}
	payload, err := events.MarshalPayload(map[string]any{
		"conflict_id":        c.ConflictID,
		"path_hash":          c.PathHash,
		"existing_intent_id": c.ExistingIntentID,
		"new_intent_id":      c.NewIntentID,
		"policy":             c.Policy,
	})
	if err != nil {
		return c, err
	}
	ev := storage.Event{
		EventID:     c.EventID,
		Type:        "conflict.detected",
		IntentID:    c.NewIntentID,
		OccurredAt:  c.DetectedAt,
		PayloadJSON: payload,
	}
	err = s.S.WriteDomainEvent(ctx, ev, func(ctx context.Context, tx *sql.Tx) error {
		_, ierr := tx.ExecContext(ctx, `
			INSERT INTO conflicts(conflict_id, event_id, path, path_hash, existing_intent_id, new_intent_id, policy, status, detected_at, metadata_json)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, c.ConflictID, c.EventID, c.Path, c.PathHash, nullable(c.ExistingIntentID), nullable(c.NewIntentID), c.Policy, c.Status, c.DetectedAt, meta)
		return ierr
	})
	return c, err
}

// AcknowledgeConflict marks a conflict as acknowledged and emits the
// conflict.acknowledged event. When asOverride is true, the resolution
// is stored as "override" and metadata_json.override = true.
func (s *Store) AcknowledgeConflict(ctx context.Context, conflictID, agentID, reason string, asOverride bool, now time.Time) error {
	occurred := id.FormatTimestamp(now)
	resolution := "acknowledged"
	if asOverride {
		resolution = "override"
	}
	payload, err := events.MarshalPayload(map[string]any{
		"conflict_id":   conflictID,
		"resolution":    resolution,
		"reason_sha256": sha256Hex(reason),
		"override":      asOverride,
	})
	if err != nil {
		return err
	}
	ev := storage.Event{
		Type:        "conflict.acknowledged",
		AgentID:     agentID,
		OccurredAt:  occurred,
		PayloadJSON: payload,
	}
	return s.S.WriteDomainEvent(ctx, ev, func(ctx context.Context, tx *sql.Tx) error {
		var meta = "{}"
		if asOverride {
			meta = `{"override":true}`
		}
		res, ierr := tx.ExecContext(ctx, `
			UPDATE conflicts
			SET status = 'acknowledged',
			    acknowledged_at = ?,
			    acknowledged_by_agent_id = ?,
			    resolution = ?,
			    metadata_json = json_patch(COALESCE(metadata_json, '{}'), ?)
			WHERE conflict_id = ?
		`, occurred, nullable(agentID), resolution, meta, conflictID)
		if ierr != nil {
			return ierr
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("conflict %s not found", conflictID)
		}
		return nil
	})
}

// ConflictByID returns a conflict.
func (s *Store) ConflictByID(ctx context.Context, conflictID string) (Conflict, error) {
	row := s.S.DB().QueryRowContext(ctx, `
		SELECT conflict_id, event_id, path, path_hash, COALESCE(existing_intent_id, ''), COALESCE(new_intent_id, ''),
		       policy, status, detected_at, COALESCE(acknowledged_at, ''), COALESCE(acknowledged_by_agent_id, ''),
		       COALESCE(resolution, ''), metadata_json
		FROM conflicts WHERE conflict_id = ?
	`, conflictID)
	var c Conflict
	var meta string
	if err := row.Scan(&c.ConflictID, &c.EventID, &c.Path, &c.PathHash, &c.ExistingIntentID, &c.NewIntentID, &c.Policy, &c.Status, &c.DetectedAt, &c.AcknowledgedAt, &c.AcknowledgedByAgentID, &c.Resolution, &meta); err != nil {
		return Conflict{}, err
	}
	c.Metadata = decodeMeta(meta)
	return c, nil
}

// ListConflicts lists conflicts, optionally filtered by task via the
// joined intent. Status filter "" returns everything.
func (s *Store) ListConflicts(ctx context.Context, taskID, status string) ([]Conflict, error) {
	q := `
		SELECT c.conflict_id, c.event_id, c.path, c.path_hash, COALESCE(c.existing_intent_id, ''), COALESCE(c.new_intent_id, ''),
		       c.policy, c.status, c.detected_at, COALESCE(c.acknowledged_at, ''), COALESCE(c.acknowledged_by_agent_id, ''),
		       COALESCE(c.resolution, ''), c.metadata_json
		FROM conflicts c
	`
	args := []any{}
	where := []string{}
	if taskID != "" {
		q += " LEFT JOIN intents i ON i.intent_id = c.new_intent_id "
		where = append(where, "i.task_id = ?")
		args = append(args, taskID)
	}
	if status != "" {
		where = append(where, "c.status = ?")
		args = append(args, status)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY c.detected_at DESC"
	rows, err := s.S.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conflict
	for rows.Next() {
		var c Conflict
		var meta string
		if err := rows.Scan(&c.ConflictID, &c.EventID, &c.Path, &c.PathHash, &c.ExistingIntentID, &c.NewIntentID, &c.Policy, &c.Status, &c.DetectedAt, &c.AcknowledgedAt, &c.AcknowledgedByAgentID, &c.Resolution, &meta); err != nil {
			return nil, err
		}
		c.Metadata = decodeMeta(meta)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListActiveIntents lists intents with status='active', optionally filtered by task.
func (s *Store) ListActiveIntents(ctx context.Context, taskID string) ([]Intent, error) {
	q := `
		SELECT intent_id, event_id, COALESCE(assignment_id, ''), task_id, agent_id, access_mode, conflict_policy, reason, status, opened_at,
		       COALESCE(last_heartbeat_at, ''), COALESCE(heartbeat_expires_at, ''), COALESCE(closed_at, ''), COALESCE(close_outcome, ''), COALESCE(close_reason, ''), metadata_json
		FROM intents WHERE status = 'active'
	`
	var args []any
	if taskID != "" {
		q += " AND task_id = ?"
		args = append(args, taskID)
	}
	q += " ORDER BY opened_at DESC"
	rows, err := s.S.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Intent
	for rows.Next() {
		var in Intent
		var meta string
		if err := rows.Scan(&in.IntentID, &in.EventID, &in.AssignmentID, &in.TaskID, &in.AgentID, &in.AccessMode, &in.ConflictPolicy, &in.Reason, &in.Status, &in.OpenedAt, &in.LastHeartbeatAt, &in.HeartbeatExpiresAt, &in.ClosedAt, &in.CloseOutcome, &in.CloseReason, &meta); err != nil {
			return nil, err
		}
		in.Metadata = decodeMeta(meta)
		out = append(out, in)
	}
	return out, rows.Err()
}
