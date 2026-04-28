package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/events"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/id"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage"
)

// Actor kinds (SPEC §11.6).
const (
	ActorAgent     = "agent"
	ActorHuman     = "human"
	ActorUnknown   = "unknown"
	ActorFormatter = "formatter"
	ActorIDE       = "ide"
	ActorHook      = "hook"
)

// Path statuses (SPEC §11.7).
const (
	PathStatusAdded    = "added"
	PathStatusModified = "modified"
	PathStatusDeleted  = "deleted"
	PathStatusRenamed  = "renamed"
	PathStatusCopied   = "copied"
	PathStatusUnknown  = "unknown"
)

// Validation statuses (SPEC §11.8).
const (
	ValidationPassed  = "passed"
	ValidationFailed  = "failed"
	ValidationSkipped = "skipped"
	ValidationUnknown = "unknown"
)

// Change mirrors the changes table.
type Change struct {
	ChangeID     string
	EventID      string
	IntentID     string
	AssignmentID string
	TaskID       string
	AgentID      string
	ActorKind    string
	Summary      string
	CreatedAt    string
	CommitSHA    string
	Metadata     map[string]any
}

// ChangePath mirrors a row in change_paths.
type ChangePath struct {
	ChangeID   string
	Path       string
	RealPath   string
	PathHash   string
	BeforeSHA  string
	AfterSHA   string
	PatchSHA   string
	LineRanges []map[string]any
	Status     string
	OutputRef  string
	PatchRef   string // not stored; passthrough for callers
}

// Validation mirrors a row in validations.
type Validation struct {
	ValidationID string
	ChangeID     string
	TaskID       string
	Command      string
	Status       string
	StartedAt    string
	CompletedAt  string
	ExitCode     *int
	OutputRef    string
	Metadata     map[string]any
}

// RecordChangeInput bundles the arguments for InsertChange.
type RecordChangeInput struct {
	Change      Change
	Paths       []ChangePath
	EventType   string // "change.recorded" or "change.adopted"
	PatchRef    string // optional blob ref to record on the event
	PatchSHA256 string // optional canonical patch hash for the event
}

// InsertChange writes a changes row, change_paths rows, and the
// configured event in a single transaction. The event payload is
// privacy-safe: it carries only path display values, path hashes, the
// summary, the patch hash (if any), and an optional patch_ref. The
// caller is responsible for filling Change.ActorKind (defaults to
// "agent") and supplying the path metadata.
func (s *Store) InsertChange(ctx context.Context, in RecordChangeInput) (Change, error) {
	c := in.Change
	if c.TaskID == "" {
		return c, errors.New("domain: change task_id required")
	}
	if c.Summary == "" {
		return c, errors.New("domain: change summary required")
	}
	if c.ActorKind == "" {
		c.ActorKind = ActorAgent
	}
	if in.EventType == "" {
		return c, errors.New("domain: change event type required")
	}
	if c.ChangeID == "" {
		nid, err := s.S.IDGen().New(id.PrefixChange)
		if err != nil {
			return c, err
		}
		c.ChangeID = nid
	}
	if c.EventID == "" {
		nid, err := s.S.IDGen().New(id.PrefixEvent)
		if err != nil {
			return c, err
		}
		c.EventID = nid
	}
	if c.CreatedAt == "" {
		c.CreatedAt = id.FormatTimestamp(s.S.Clock()())
	}
	meta, err := encodeMeta(c.Metadata)
	if err != nil {
		return c, err
	}

	pathPayload := make([]map[string]any, 0, len(in.Paths))
	for _, p := range in.Paths {
		entry := map[string]any{
			"path":      p.Path,
			"path_hash": p.PathHash,
			"status":    p.Status,
		}
		if p.PatchSHA != "" {
			entry["patch_sha256"] = p.PatchSHA
		}
		pathPayload = append(pathPayload, entry)
	}
	payload := map[string]any{
		"change_id":      c.ChangeID,
		"task_id":        c.TaskID,
		"intent_id":      c.IntentID,
		"assignment_id":  c.AssignmentID,
		"agent_id":       c.AgentID,
		"actor_kind":     c.ActorKind,
		"summary_sha256": sha256Hex(c.Summary),
		"paths":          pathPayload,
	}
	if in.PatchSHA256 != "" {
		payload["patch_sha256"] = in.PatchSHA256
	}
	if in.PatchRef != "" {
		payload["patch_ref"] = in.PatchRef
	}
	if v, ok := c.Metadata["retroactive"].(bool); ok && v {
		payload["retroactive"] = true
	}
	encPayload, err := events.MarshalPayload(payload)
	if err != nil {
		return c, err
	}
	ev := storage.Event{
		EventID:      c.EventID,
		Type:         in.EventType,
		AgentID:      c.AgentID,
		TaskID:       c.TaskID,
		IntentID:     c.IntentID,
		AssignmentID: c.AssignmentID,
		OccurredAt:   c.CreatedAt,
		PayloadJSON:  encPayload,
	}

	err = s.S.WriteDomainEvent(ctx, ev, func(ctx context.Context, tx *sql.Tx) error {
		if _, ierr := tx.ExecContext(ctx, `
			INSERT INTO changes(change_id, event_id, intent_id, assignment_id, task_id, agent_id, actor_kind, summary, created_at, commit_sha, metadata_json)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, c.ChangeID, c.EventID, nullable(c.IntentID), nullable(c.AssignmentID), c.TaskID, nullable(c.AgentID), c.ActorKind, c.Summary, c.CreatedAt, nullable(c.CommitSHA), meta); ierr != nil {
			return ierr
		}
		for _, p := range in.Paths {
			lr := "[]"
			if len(p.LineRanges) > 0 {
				raw, err := jsonMarshal(p.LineRanges)
				if err != nil {
					return err
				}
				lr = raw
			}
			status := p.Status
			if status == "" {
				status = PathStatusUnknown
			}
			if _, ierr := tx.ExecContext(ctx, `
				INSERT INTO change_paths(change_id, path, realpath, path_hash, before_sha256, after_sha256, patch_sha256, line_ranges_json, status)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, c.ChangeID, p.Path, p.RealPath, p.PathHash, nullable(p.BeforeSHA), nullable(p.AfterSHA), nullable(p.PatchSHA), lr, status); ierr != nil {
				return ierr
			}
		}
		return nil
	})
	return c, err
}

// InsertValidation writes a validations row and a validation.recorded
// event. ChangeID may be empty when the validation is recorded
// without a parent change (uncommon but allowed by SPEC §11.8).
func (s *Store) InsertValidation(ctx context.Context, v Validation) (Validation, error) {
	if v.TaskID == "" {
		return v, errors.New("domain: validation task_id required")
	}
	if v.Command == "" {
		return v, errors.New("domain: validation command required")
	}
	if v.Status == "" {
		v.Status = ValidationUnknown
	}
	if v.ValidationID == "" {
		nid, err := s.S.IDGen().New(id.PrefixValidation)
		if err != nil {
			return v, err
		}
		v.ValidationID = nid
	}
	if v.CompletedAt == "" {
		v.CompletedAt = id.FormatTimestamp(s.S.Clock()())
	}
	meta, err := encodeMeta(v.Metadata)
	if err != nil {
		return v, err
	}
	payload := map[string]any{
		"validation_id":  v.ValidationID,
		"change_id":      v.ChangeID,
		"task_id":        v.TaskID,
		"command_sha256": sha256Hex(v.Command),
		"status":         v.Status,
	}
	if v.ExitCode != nil {
		payload["exit_code"] = *v.ExitCode
	}
	encPayload, err := events.MarshalPayload(payload)
	if err != nil {
		return v, err
	}
	ev := storage.Event{
		Type:        "validation.recorded",
		AgentID:     "",
		TaskID:      v.TaskID,
		OccurredAt:  v.CompletedAt,
		PayloadJSON: encPayload,
	}
	err = s.S.WriteDomainEvent(ctx, ev, func(ctx context.Context, tx *sql.Tx) error {
		var exit any
		if v.ExitCode != nil {
			exit = *v.ExitCode
		}
		_, ierr := tx.ExecContext(ctx, `
			INSERT INTO validations(validation_id, change_id, task_id, command, status, started_at, completed_at, exit_code, output_ref, metadata_json)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, v.ValidationID, nullable(v.ChangeID), v.TaskID, v.Command, v.Status, nullable(v.StartedAt), nullable(v.CompletedAt), exit, nullable(v.OutputRef), meta)
		return ierr
	})
	return v, err
}

// IntentPathHashes returns the set of path hashes attached to an
// intent. Used by `record` to validate that every supplied path is
// already claimed.
func (s *Store) IntentPathHashes(ctx context.Context, intentID string) (map[string]string, error) {
	rows, err := s.S.DB().QueryContext(ctx, `SELECT path_hash, path FROM intent_paths WHERE intent_id = ?`, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var h, p string
		if err := rows.Scan(&h, &p); err != nil {
			return nil, err
		}
		out[h] = p
	}
	return out, rows.Err()
}

// ChangesForTask returns every change row attached to taskID, ordered
// by created_at ascending so summaries are deterministic.
func (s *Store) ChangesForTask(ctx context.Context, taskID string) ([]Change, error) {
	rows, err := s.S.DB().QueryContext(ctx, `
		SELECT change_id, event_id, COALESCE(intent_id, ''), COALESCE(assignment_id, ''), task_id,
		       COALESCE(agent_id, ''), actor_kind, summary, created_at, COALESCE(commit_sha, ''), metadata_json
		FROM changes WHERE task_id = ? ORDER BY created_at, change_id
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		var meta string
		if err := rows.Scan(&c.ChangeID, &c.EventID, &c.IntentID, &c.AssignmentID, &c.TaskID, &c.AgentID, &c.ActorKind, &c.Summary, &c.CreatedAt, &c.CommitSHA, &meta); err != nil {
			return nil, err
		}
		decoded, err := decodeMeta(meta, "changes.metadata_json", c.ChangeID)
		if err != nil {
			return nil, err
		}
		c.Metadata = decoded
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChangePaths returns the change_paths rows for changeID, ordered by
// path so the resulting slice is deterministic.
func (s *Store) ChangePaths(ctx context.Context, changeID string) ([]ChangePath, error) {
	rows, err := s.S.DB().QueryContext(ctx, `
		SELECT change_id, path, realpath, path_hash,
		       COALESCE(before_sha256, ''), COALESCE(after_sha256, ''), COALESCE(patch_sha256, ''),
		       line_ranges_json, status
		FROM change_paths WHERE change_id = ? ORDER BY path
	`, changeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChangePath
	for rows.Next() {
		var p ChangePath
		var lr string
		if err := rows.Scan(&p.ChangeID, &p.Path, &p.RealPath, &p.PathHash, &p.BeforeSHA, &p.AfterSHA, &p.PatchSHA, &lr, &p.Status); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ValidationsForTask returns all validations attached to taskID,
// ordered by completed_at ascending then validation_id for stability.
func (s *Store) ValidationsForTask(ctx context.Context, taskID string) ([]Validation, error) {
	rows, err := s.S.DB().QueryContext(ctx, `
		SELECT validation_id, COALESCE(change_id, ''), task_id, command, status,
		       COALESCE(started_at, ''), COALESCE(completed_at, ''),
		       exit_code, COALESCE(output_ref, ''), metadata_json
		FROM validations WHERE task_id = ? ORDER BY completed_at, validation_id
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Validation
	for rows.Next() {
		var v Validation
		var exit sql.NullInt64
		var meta string
		if err := rows.Scan(&v.ValidationID, &v.ChangeID, &v.TaskID, &v.Command, &v.Status, &v.StartedAt, &v.CompletedAt, &exit, &v.OutputRef, &meta); err != nil {
			return nil, err
		}
		if exit.Valid {
			n := int(exit.Int64)
			v.ExitCode = &n
		}
		decoded, err := decodeMeta(meta, "validations.metadata_json", v.ValidationID)
		if err != nil {
			return nil, err
		}
		v.Metadata = decoded
		out = append(out, v)
	}
	return out, rows.Err()
}

// IntentsForTask returns every intent (any status) attached to taskID,
// most recently opened first.
func (s *Store) IntentsForTask(ctx context.Context, taskID string) ([]Intent, error) {
	rows, err := s.S.DB().QueryContext(ctx, `
		SELECT intent_id, event_id, COALESCE(assignment_id, ''), task_id, agent_id, access_mode, conflict_policy, reason, status, opened_at,
		       COALESCE(last_heartbeat_at, ''), COALESCE(heartbeat_expires_at, ''), COALESCE(closed_at, ''), COALESCE(close_outcome, ''), COALESCE(close_reason, ''), metadata_json
		FROM intents WHERE task_id = ? ORDER BY opened_at DESC, intent_id DESC
	`, taskID)
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
		decoded, err := decodeMeta(meta, "intents.metadata_json", in.IntentID)
		if err != nil {
			return nil, err
		}
		in.Metadata = decoded
		out = append(out, in)
	}
	return out, rows.Err()
}

// jsonMarshal returns the JSON encoding of v as a string.
func jsonMarshal(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
