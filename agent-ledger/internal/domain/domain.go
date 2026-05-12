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
	"io"
	"strings"
	"time"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/conflicts"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/events"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/id"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/policy"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/privacy"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/storage/sqlite"
	sqlitedrv "modernc.org/sqlite"
)

// Conflict policies (SPEC §15).
const (
	PolicyNone      = policy.None
	PolicyWarn      = policy.Warn
	PolicyExclusive = policy.Exclusive
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

// ErrUnsafeReason is returned by InsertAssignment and InsertIntent when
// the provided reason string fails the privacy safety check (SPEC §17).
// The CLI guard in assign.go and claim.go is the canonical enforcement
// point; the domain check is defense-in-depth for programmatic callers
// that bypass the CLI layer. Callers should detect this sentinel with
// errors.Is and map it to ExitConfigError (2).
var ErrUnsafeReason = errors.New("domain: unsafe reason")

// ErrSupersedeNotActive is returned when a supersede target is no
// longer active by the time the claim attempts to close it.
var ErrSupersedeNotActive = errors.New("domain: supersede target not active")

// ErrAssignmentExists is returned by InsertAssignment when the unique
// index on (task_id, assigned_agent_id) WHERE status='active' rejects
// the insert because another active assignment already exists for the
// same pair. Callers detect this sentinel with errors.Is and map it
// to ExitConflict (4) with code "assignment_exists". The lookup-then-
// reuse path lives in --if-absent and is the only sanctioned replay
// surface; plain assign returning this error forces orchestrators to
// be intentional about reassignment.
var ErrAssignmentExists = errors.New("domain: active assignment already exists for this (task, agent) pair")

// ErrNoActiveAssignment is returned by SupersedeAndInsertAssignment
// when no active assignment exists for the requested (task, agent)
// pair. Callers detect this sentinel with errors.Is and map it to
// ExitConflict (4) with code "no_active_assignment".
var ErrNoActiveAssignment = errors.New("domain: no active assignment exists for this (task, agent) pair")

// ErrStaleUpdate is returned by SupersedeAndInsertAssignment when the
// active assignment row read at the start of the immediate transaction
// has been superseded by a concurrent writer before the UPDATE fires.
// Under BEGIN IMMEDIATE the helper serializes writers, so this case is
// only reachable if a non-CLI caller mutated the row directly. Callers
// detect with errors.Is and map to ExitConflict (4) with code
// "assignment_stale_update".
var ErrStaleUpdate = errors.New("domain: active assignment was superseded by a concurrent writer")

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
	IntentID      string
	Path          string
	RealPath      string
	PathHash      string
	CanonicalHash string
	AccessMode    string
}

// ClaimResult summarizes the outcome of ResolveAndInsertIntent.
type ClaimResult struct {
	Decision conflicts.Decision
	Overlaps []conflicts.Overlap
	Intent   Intent
}

func (r ClaimResult) Blocked() bool { return r.Decision == conflicts.Block }

// Conflict mirrors the conflicts table.
type Conflict struct {
	ConflictID            string
	EventID               string
	Path                  string
	PathHash              string
	CanonicalHash         string
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

// hashListSQL renders a list of hash strings as positional placeholders
// and the matching argument slice. An empty input collapses to a single
// placeholder with the sentinel value "\x00", which never matches any
// real hex hash, so the OR branch in the caller's WHERE clause becomes
// a no-op rather than a syntax error.
func hashListSQL(hashes []string) ([]any, string) {
	if len(hashes) == 0 {
		return []any{"\x00"}, "?"
	}
	args := make([]any, 0, len(hashes))
	for _, h := range hashes {
		args = append(args, h)
	}
	placeholders := strings.Repeat("?,", len(hashes))
	placeholders = placeholders[:len(placeholders)-1]
	return args, placeholders
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

// MetadataDecodeError is returned by metadata-decoding readers when
// a row's metadata_json column contains a payload that does not
// parse as a JSON object. Callers that want to read past corrupted
// rows can detect the error with errors.As; callers that want to
// fail loudly on ledger corruption can let it propagate and return
// ExitStorageIO with code metadata_corrupt.
//
// Field is the logical column path (table.column or table.row[id].column)
// for diagnostics. Raw carries up to 200 bytes of the offending
// payload so a reviewer reading the error knows what to repair.
type MetadataDecodeError struct {
	Field string
	RowID string
	Raw   string
	Err   error
}

func (e *MetadataDecodeError) Error() string {
	if e == nil {
		return "<nil MetadataDecodeError>"
	}
	loc := e.Field
	if e.RowID != "" {
		loc = fmt.Sprintf("%s row=%s", e.Field, e.RowID)
	}
	return fmt.Sprintf("domain: metadata decode failed (%s): %v", loc, e.Err)
}

func (e *MetadataDecodeError) Unwrap() error { return e.Err }

// PathsDecodeError is returned by path-scope readers when an
// allowed_paths_json or forbidden_paths_json payload does not parse as
// a JSON array of strings. Callers can detect the error with errors.As
// or let it propagate and return ExitStorageIO with code paths_corrupt.
//
// Field is the logical column path (table.column or table.row[id].column)
// for diagnostics. Raw carries up to 200 bytes of the offending
// payload so a reviewer reading the error knows what to repair.
type PathsDecodeError struct {
	Field string
	RowID string
	Raw   string
	Err   error
}

func (e *PathsDecodeError) Error() string {
	if e == nil {
		return "<nil PathsDecodeError>"
	}
	loc := e.Field
	if e.RowID != "" {
		loc = fmt.Sprintf("%s row=%s", e.Field, e.RowID)
	}
	return fmt.Sprintf("domain: paths decode failed (%s): %v", loc, e.Err)
}

func (e *PathsDecodeError) Unwrap() error { return e.Err }

func truncatedDecodeRaw(raw string) string {
	if len(raw) > 200 {
		return raw[:200] + "..."
	}
	return raw
}

func jsonValueKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "bool"
	case json.Number:
		return "number"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// decodeMeta parses a metadata_json column and returns either the
// decoded map or a typed *MetadataDecodeError. Empty input returns
// an empty map without error so unset metadata is never an error.
//
// Callers MUST pass field (e.g. "assignments.metadata_json") and
// rowID (the row's primary key) so the error surface points
// reviewers at the corrupted row.
func decodeMeta(raw, field, rowID string) (map[string]any, error) {
	if raw == "" {
		return map[string]any{}, nil
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, &MetadataDecodeError{
			Field: field,
			RowID: rowID,
			Raw:   truncatedDecodeRaw(raw),
			Err:   err,
		}
	}
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return nil, &MetadataDecodeError{
			Field: field,
			RowID: rowID,
			Raw:   truncatedDecodeRaw(raw),
			Err:   fmt.Errorf("expected JSON object, got %s", jsonValueKind(v)),
		}
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = fmt.Errorf("expected EOF, found extra JSON value")
		}
		return nil, &MetadataDecodeError{
			Field: field,
			RowID: rowID,
			Raw:   truncatedDecodeRaw(raw),
			Err:   err,
		}
	}
	return m, nil
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

func decodePaths(raw, field, rowID string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, &PathsDecodeError{
			Field: field,
			RowID: rowID,
			Raw:   truncatedDecodeRaw(raw),
			Err:   err,
		}
	}
	arr, ok := v.([]any)
	if !ok || arr == nil {
		return nil, &PathsDecodeError{
			Field: field,
			RowID: rowID,
			Raw:   truncatedDecodeRaw(raw),
			Err:   fmt.Errorf("expected JSON array, got %s", jsonValueKind(v)),
		}
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = fmt.Errorf("expected EOF, found extra JSON value")
		}
		return nil, &PathsDecodeError{
			Field: field,
			RowID: rowID,
			Raw:   truncatedDecodeRaw(raw),
			Err:   err,
		}
	}
	out := make([]string, 0, len(arr))
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, &PathsDecodeError{
				Field: field,
				RowID: rowID,
				Raw:   truncatedDecodeRaw(raw),
				Err:   fmt.Errorf("expected JSON array of strings, got %s at index %d", jsonValueKind(item), i),
			}
		}
		out = append(out, s)
	}
	return out, nil
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
// It returns ErrUnsafeReason wrapped with the original privacy error if
// a.Reason contains a known secret pattern (privacy.AssertSafe, SPEC §17).
// Both errors.Is(err, domain.ErrUnsafeReason) and
// errors.As(err, &privacy.SecretError{}) succeed on the returned error.
func (s *Store) InsertAssignment(ctx context.Context, a Assignment) (Assignment, error) {
	if err := privacy.AssertSafe("assignment.reason", a.Reason); err != nil {
		return a, fmt.Errorf("%w: %w", ErrUnsafeReason, err)
	}
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
	if isActiveAssignmentUniqueViolation(err) {
		// The partial unique index on (task_id, assigned_agent_id)
		// WHERE status='active' (migration 0002) caught a duplicate.
		// Surface as a typed sentinel so the CLI layer can map it to
		// ExitConflict with the assignment_exists error code.
		return a, fmt.Errorf("%w: %w", ErrAssignmentExists, err)
	}
	return a, err
}

// AssignmentUpdateInput describes an additive update to an existing
// active assignment. Only AddAllowedPaths is honored in the MVP
// (allow-list extension only); see SPEC §18.3 for the rationale.
// Reason is required (privacy.AssertSafe) so the audit trail records
// why the scope changed. ExtraMetadata is shallow-merged into the new
// row's metadata_json on top of the prior row's metadata, with the
// lineage keys (`superseded_assignment_id`, `updated_from`) owned and
// overwritten by the helper.
type AssignmentUpdateInput struct {
	TaskID          string
	AssignedAgentID string
	OrchestratorID  string
	AddAllowedPaths []string
	Reason          string
	ExtraMetadata   map[string]any
}

// AssignmentUpdateResult is returned by SupersedeAndInsertAssignment.
// When Reused is true, the prior row is returned unchanged and no new
// event was written; the merge produced no new globs. When Reused is
// false, Assignment is the new active row and PriorAssignmentID names
// the row that was superseded.
type AssignmentUpdateResult struct {
	Assignment        Assignment
	PriorAssignmentID string
	Reused            bool
}

// SupersedeAndInsertAssignment extends an active assignment's allow
// list by superseding the existing row and inserting a fresh active
// row that merges the prior paths with the new globs.
// All work happens inside one BEGIN IMMEDIATE transaction so the
// (task_id, assigned_agent_id) WHERE status='active' partial unique
// index never sees two active rows and the lookup-merge-write cycle
// is atomic.
//
// Returns ErrNoActiveAssignment when no active row exists for the
// pair. Returns ErrUnsafeReason when in.Reason fails the privacy
// safety check. Returns AssignmentUpdateResult{Reused: true} when the
// merge produces zero new globs (idempotent ensure-shape calls).
// Returns ErrStaleUpdate if the active row vanished between the SELECT
// and the UPDATE inside the transaction; under BEGIN IMMEDIATE this is
// only reachable when a non-CLI caller mutated the row directly.
func (s *Store) SupersedeAndInsertAssignment(ctx context.Context, in AssignmentUpdateInput) (AssignmentUpdateResult, error) {
	if err := privacy.AssertSafe("assignment.reason", in.Reason); err != nil {
		return AssignmentUpdateResult{}, fmt.Errorf("%w: %w", ErrUnsafeReason, err)
	}

	var res AssignmentUpdateResult
	if werr := s.S.WriteDomainEventImmediate(ctx, func(ctx context.Context, conn *sql.Conn) ([]storage.Event, error) {
		// Generate ids and timestamp INSIDE the immediate transaction
		// so a concurrent writer that captured `now` first cannot commit
		// timestamps that pre-date a winner's commit (which would invert
		// the audit chain). Under BEGIN IMMEDIATE the writer lock is
		// already held when this callback runs.
		newID, err := s.S.IDGen().New(id.PrefixAssignment)
		if err != nil {
			return nil, err
		}
		newEventID, err := s.S.IDGen().New(id.PrefixEvent)
		if err != nil {
			return nil, err
		}
		now := id.FormatTimestamp(s.S.Clock()())
		prior, err := selectActiveAssignmentForUpdate(ctx, conn, in.TaskID, in.AssignedAgentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrNoActiveAssignment
			}
			return nil, err
		}
		mergedAllowed, allowChanged := mergeGlobs(prior.AllowedPaths, in.AddAllowedPaths)
		if !allowChanged {
			res = AssignmentUpdateResult{Assignment: prior, PriorAssignmentID: prior.AssignmentID, Reused: true}
			return nil, nil
		}

		// Reserved lineage keys are owned by this helper. Strip them
		// from both the prior row's metadata (we set fresh values for
		// the new row) and from caller-supplied metadata (otherwise a
		// caller could mark the new active row as already-superseded,
		// breaking the SPEC §11.3.1 chain convention).
		meta := map[string]any{}
		for k, v := range prior.Metadata {
			if isReservedLineageKey(k) {
				continue
			}
			meta[k] = v
		}
		for k, v := range in.ExtraMetadata {
			if isReservedLineageKey(k) {
				continue
			}
			meta[k] = v
		}
		meta["superseded_assignment_id"] = prior.AssignmentID
		meta["updated_from"] = prior.AssignmentID

		orchestratorID := in.OrchestratorID
		if orchestratorID == "" {
			orchestratorID = prior.OrchestratorID
		}

		newRow := Assignment{
			AssignmentID:    newID,
			EventID:         newEventID,
			TaskID:          prior.TaskID,
			OrchestratorID:  orchestratorID,
			AssignedAgentID: prior.AssignedAgentID,
			AllowedPaths:    mergedAllowed,
			ForbiddenPaths:  append([]string(nil), prior.ForbiddenPaths...),
			ConflictPolicy:  prior.ConflictPolicy,
			Reason:          in.Reason,
			Status:          "active",
			CreatedAt:       now,
			Metadata:        meta,
		}
		allowedJSON, err := encodePaths(newRow.AllowedPaths)
		if err != nil {
			return nil, err
		}
		forbidJSON, err := encodePaths(newRow.ForbiddenPaths)
		if err != nil {
			return nil, err
		}
		metaJSON, err := encodeMeta(newRow.Metadata)
		if err != nil {
			return nil, err
		}

		if err := supersedeAssignmentTx(ctx, conn, prior.AssignmentID, newRow.AssignmentID, now); err != nil {
			return nil, err
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO assignments(assignment_id, event_id, task_id, orchestrator_id, assigned_agent_id, allowed_paths_json, forbidden_paths_json, conflict_policy, reason, status, created_at, metadata_json)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, newRow.AssignmentID, newRow.EventID, newRow.TaskID, newRow.OrchestratorID, nullable(newRow.AssignedAgentID), allowedJSON, forbidJSON, newRow.ConflictPolicy, newRow.Reason, newRow.Status, newRow.CreatedAt, metaJSON); err != nil {
			if isActiveAssignmentUniqueViolation(err) {
				return nil, fmt.Errorf("%w: %w", ErrAssignmentExists, err)
			}
			return nil, err
		}

		supersededEvent, err := supersedeAssignmentEvent(prior.AssignmentID, newRow.AssignmentID, prior.TaskID, orchestratorID, now)
		if err != nil {
			return nil, err
		}
		assignedPayload, err := events.MarshalPayload(map[string]any{
			"assignment_id":            newRow.AssignmentID,
			"task_id":                  newRow.TaskID,
			"orchestrator_id":          newRow.OrchestratorID,
			"assigned_agent":           newRow.AssignedAgentID,
			"allowed_paths":            newRow.AllowedPaths,
			"forbidden_paths":          newRow.ForbiddenPaths,
			"conflict_policy":          newRow.ConflictPolicy,
			"reason_sha256":            sha256Hex(newRow.Reason),
			"superseded_assignment_id": prior.AssignmentID,
		})
		if err != nil {
			return nil, err
		}
		assignedEvent := storage.Event{
			EventID:      newRow.EventID,
			Type:         "task.assigned",
			AgentID:      orchestratorID,
			TaskID:       newRow.TaskID,
			AssignmentID: newRow.AssignmentID,
			OccurredAt:   now,
			PayloadJSON:  assignedPayload,
		}
		res = AssignmentUpdateResult{Assignment: newRow, PriorAssignmentID: prior.AssignmentID, Reused: false}
		return []storage.Event{supersededEvent, assignedEvent}, nil
	}); werr != nil {
		return AssignmentUpdateResult{}, werr
	}
	return res, nil
}

// isReservedLineageKey reports whether k is a metadata key that the
// supersede helper owns. Callers cannot set or pass through these keys
// via ExtraMetadata; the helper writes them with the correct values.
func isReservedLineageKey(k string) bool {
	switch k {
	case "superseded_by", "superseded_assignment_id", "updated_from":
		return true
	}
	return false
}

func selectActiveAssignmentForUpdate(ctx context.Context, conn *sql.Conn, taskID, agentID string) (Assignment, error) {
	row := conn.QueryRowContext(ctx, `
		SELECT assignment_id, event_id, task_id, orchestrator_id, COALESCE(assigned_agent_id, ''),
		       allowed_paths_json, forbidden_paths_json, conflict_policy, reason, status, created_at, metadata_json
		FROM assignments
		WHERE task_id = ? AND status = 'active' AND COALESCE(assigned_agent_id, '') = ?
		ORDER BY created_at DESC, assignment_id DESC
		LIMIT 1
	`, taskID, agentID)
	var a Assignment
	var allowed, forbid, meta string
	if err := row.Scan(&a.AssignmentID, &a.EventID, &a.TaskID, &a.OrchestratorID, &a.AssignedAgentID, &allowed, &forbid, &a.ConflictPolicy, &a.Reason, &a.Status, &a.CreatedAt, &meta); err != nil {
		return Assignment{}, err
	}
	var err error
	a.AllowedPaths, err = decodePaths(allowed, "assignments.allowed_paths_json", a.AssignmentID)
	if err != nil {
		return Assignment{}, err
	}
	a.ForbiddenPaths, err = decodePaths(forbid, "assignments.forbidden_paths_json", a.AssignmentID)
	if err != nil {
		return Assignment{}, err
	}
	decoded, err := decodeMeta(meta, "assignments.metadata_json", a.AssignmentID)
	if err != nil {
		return Assignment{}, err
	}
	a.Metadata = decoded
	return a, nil
}

func supersedeAssignmentTx(ctx context.Context, exec sqlExecer, oldID, newID, occurredAt string) error {
	res, err := exec.ExecContext(ctx, `
		UPDATE assignments
		SET status = 'superseded',
		    closed_at = ?,
		    metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.superseded_by', ?)
		WHERE assignment_id = ? AND status = 'active'
	`, occurredAt, newID, oldID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrStaleUpdate
	}
	return nil
}

func supersedeAssignmentEvent(oldID, newID, taskID, agentID, occurredAt string) (storage.Event, error) {
	payload, err := events.MarshalPayload(map[string]any{
		"assignment_id": oldID,
		"superseded_by": newID,
	})
	if err != nil {
		return storage.Event{}, err
	}
	return storage.Event{
		Type:         "assignment.superseded",
		AgentID:      agentID,
		TaskID:       taskID,
		AssignmentID: oldID,
		OccurredAt:   occurredAt,
		PayloadJSON:  payload,
	}, nil
}

// mergeGlobs returns a deduplicated union of base and adds, preserving
// base's order and appending only the additions not already present.
// The bool return reports whether the merge introduced any new globs.
// Globs are compared as raw strings; near-equivalents like "src/*" vs
// "src/**" are not collapsed.
func mergeGlobs(base, adds []string) ([]string, bool) {
	if len(adds) == 0 {
		return append([]string(nil), base...), false
	}
	seen := make(map[string]struct{}, len(base))
	out := make([]string, 0, len(base)+len(adds))
	for _, g := range base {
		if _, ok := seen[g]; ok {
			continue
		}
		seen[g] = struct{}{}
		out = append(out, g)
	}
	changed := false
	for _, g := range adds {
		if _, ok := seen[g]; ok {
			continue
		}
		seen[g] = struct{}{}
		out = append(out, g)
		changed = true
	}
	return out, changed
}

func isActiveAssignmentUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var se *sqlitedrv.Error
	if !errors.As(err, &se) {
		return false
	}
	if se.Code() != 2067 {
		return false
	}
	return strings.Contains(err.Error(), "assignments.task_id, assignments.assigned_agent_id") ||
		strings.Contains(err.Error(), "idx_assignments_active_task_agent")
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
	var err error
	var allowed, forbid, meta string
	if err := row.Scan(&a.AssignmentID, &a.EventID, &a.TaskID, &a.OrchestratorID, &a.AssignedAgentID, &allowed, &forbid, &a.ConflictPolicy, &a.Reason, &a.Status, &a.CreatedAt, &meta); err != nil {
		return Assignment{}, err
	}
	a.AllowedPaths, err = decodePaths(allowed, "assignments.allowed_paths_json", a.AssignmentID)
	if err != nil {
		return Assignment{}, err
	}
	a.ForbiddenPaths, err = decodePaths(forbid, "assignments.forbidden_paths_json", a.AssignmentID)
	if err != nil {
		return Assignment{}, err
	}
	decoded, err := decodeMeta(meta, "assignments.metadata_json", a.AssignmentID)
	if err != nil {
		return Assignment{}, err
	}
	a.Metadata = decoded
	return a, nil
}

// LatestActiveAssignmentForTaskAndAgent returns the most recent active
// assignment for taskID and agentID, or sql.ErrNoRows when none exists.
func (s *Store) LatestActiveAssignmentForTaskAndAgent(ctx context.Context, taskID, agentID string) (Assignment, error) {
	row := s.S.DB().QueryRowContext(ctx, `
		SELECT assignment_id, event_id, task_id, orchestrator_id, COALESCE(assigned_agent_id, ''),
		       allowed_paths_json, forbidden_paths_json, conflict_policy, reason, status, created_at, metadata_json
		FROM assignments
		WHERE task_id = ? AND status = 'active' AND COALESCE(assigned_agent_id, '') = ?
		ORDER BY created_at DESC, assignment_id DESC
		LIMIT 1
	`, taskID, agentID)
	var a Assignment
	var err error
	var allowed, forbid, meta string
	if err := row.Scan(&a.AssignmentID, &a.EventID, &a.TaskID, &a.OrchestratorID, &a.AssignedAgentID, &allowed, &forbid, &a.ConflictPolicy, &a.Reason, &a.Status, &a.CreatedAt, &meta); err != nil {
		return Assignment{}, err
	}
	a.AllowedPaths, err = decodePaths(allowed, "assignments.allowed_paths_json", a.AssignmentID)
	if err != nil {
		return Assignment{}, err
	}
	a.ForbiddenPaths, err = decodePaths(forbid, "assignments.forbidden_paths_json", a.AssignmentID)
	if err != nil {
		return Assignment{}, err
	}
	decoded, err := decodeMeta(meta, "assignments.metadata_json", a.AssignmentID)
	if err != nil {
		return Assignment{}, err
	}
	a.Metadata = decoded
	return a, nil
}

// AssignmentFilter narrows ListAssignments. Empty fields are no-ops.
type AssignmentFilter struct {
	TaskID         string
	OrchestratorID string
	AgentID        string
	// Status accepts "active", "superseded", "closed", or "all".
	// Empty string means "active" by default.
	Status string
	Limit  int
}

// ListAssignments returns assignments matching filter, ordered by
// created_at DESC. Used by the agent-ledger assignments query command
// so reviewers can find auto-assigned, harness-derived, or explicit
// assignments without reading SQLite directly.
func (s *Store) ListAssignments(ctx context.Context, filter AssignmentFilter) ([]Assignment, error) {
	var (
		where []string
		args  []any
	)
	if filter.TaskID != "" {
		where = append(where, "task_id = ?")
		args = append(args, filter.TaskID)
	}
	if filter.OrchestratorID != "" {
		where = append(where, "orchestrator_id = ?")
		args = append(args, filter.OrchestratorID)
	}
	if filter.AgentID != "" {
		where = append(where, "COALESCE(assigned_agent_id, '') = ?")
		args = append(args, filter.AgentID)
	}
	status := filter.Status
	if status == "" {
		status = "active"
	}
	if status != "all" {
		where = append(where, "status = ?")
		args = append(args, status)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit)
	query := `
		SELECT assignment_id, event_id, task_id, orchestrator_id, COALESCE(assigned_agent_id, ''),
		       allowed_paths_json, forbidden_paths_json, conflict_policy, reason, status, created_at, metadata_json
		FROM assignments`
	if len(where) > 0 {
		query += "\n\t\tWHERE " + strings.Join(where, " AND ")
	}
	query += "\n\t\tORDER BY created_at DESC, assignment_id DESC\n\t\tLIMIT ?"
	rows, err := s.S.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Assignment
	for rows.Next() {
		var a Assignment
		var allowed, forbid, meta string
		if err := rows.Scan(&a.AssignmentID, &a.EventID, &a.TaskID, &a.OrchestratorID, &a.AssignedAgentID, &allowed, &forbid, &a.ConflictPolicy, &a.Reason, &a.Status, &a.CreatedAt, &meta); err != nil {
			return nil, err
		}
		a.AllowedPaths, err = decodePaths(allowed, "assignments.allowed_paths_json", a.AssignmentID)
		if err != nil {
			return nil, err
		}
		a.ForbiddenPaths, err = decodePaths(forbid, "assignments.forbidden_paths_json", a.AssignmentID)
		if err != nil {
			return nil, err
		}
		decoded, err := decodeMeta(meta, "assignments.metadata_json", a.AssignmentID)
		if err != nil {
			return nil, err
		}
		a.Metadata = decoded
		out = append(out, a)
	}
	return out, rows.Err()
}

// InsertIntent writes an intents row plus intent_paths plus an
// intent.opened event in one transaction.
// It returns ErrUnsafeReason wrapped with the original privacy error if
// in.Reason contains a known secret pattern (privacy.AssertSafe, SPEC §17).
// Both errors.Is(err, domain.ErrUnsafeReason) and
// errors.As(err, &privacy.SecretError{}) succeed on the returned error.
func (s *Store) InsertIntent(ctx context.Context, in Intent, ipaths []IntentPath) (Intent, error) {
	prepared, ipaths, ev, meta, err := s.prepareIntentForInsert(in, ipaths)
	if err != nil {
		return prepared, err
	}
	if err := s.S.WriteDomainEvent(ctx, ev, func(ctx context.Context, tx *sql.Tx) error {
		return insertIntentRows(ctx, tx, prepared, ipaths, meta)
	}); err != nil {
		return prepared, err
	}
	return prepared, nil
}

func (s *Store) prepareIntentForInsert(in Intent, ipaths []IntentPath) (Intent, []IntentPath, storage.Event, string, error) {
	if err := privacy.AssertSafe("intent.reason", in.Reason); err != nil {
		return in, nil, storage.Event{}, "", fmt.Errorf("%w: %w", ErrUnsafeReason, err)
	}
	if in.IntentID == "" {
		nid, err := s.S.IDGen().New(id.PrefixIntent)
		if err != nil {
			return in, nil, storage.Event{}, "", err
		}
		in.IntentID = nid
	}
	if in.EventID == "" {
		nid, err := s.S.IDGen().New(id.PrefixEvent)
		if err != nil {
			return in, nil, storage.Event{}, "", err
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
		return in, nil, storage.Event{}, "", err
	}
	pathsForPayload := make([]map[string]any, 0, len(ipaths))
	for i := range ipaths {
		ipaths[i].IntentID = in.IntentID
		pathsForPayload = append(pathsForPayload, map[string]any{
			"path":      ipaths[i].Path,
			"path_hash": ipaths[i].PathHash,
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
		return in, nil, storage.Event{}, "", err
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
	return in, ipaths, ev, meta, nil
}

func insertIntentRows(ctx context.Context, exec sqlExecer, in Intent, ipaths []IntentPath, meta string) error {
	for i := range ipaths {
		ipaths[i].IntentID = in.IntentID
	}
	if _, err := exec.ExecContext(ctx, `
		INSERT INTO intents(intent_id, event_id, assignment_id, task_id, agent_id, access_mode, conflict_policy, reason, status, opened_at, last_heartbeat_at, heartbeat_expires_at, metadata_json)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, in.IntentID, in.EventID, nullable(in.AssignmentID), in.TaskID, in.AgentID, in.AccessMode, in.ConflictPolicy, in.Reason, in.Status, in.OpenedAt, nullable(in.LastHeartbeatAt), nullable(in.HeartbeatExpiresAt), meta); err != nil {
		return err
	}
	for _, p := range ipaths {
		if _, err := exec.ExecContext(ctx, `
			INSERT INTO intent_paths(intent_id, path, realpath, path_hash, canonical_path_hash, access_mode)
			VALUES(?, ?, ?, ?, ?, ?)
		`, p.IntentID, p.Path, p.RealPath, p.PathHash, nullable(p.CanonicalHash), p.AccessMode); err != nil {
			return err
		}
	}
	return nil
}

// ResolveAndInsertIntent is a claim-specific helper. It is not a
// general transaction abstraction, it exists so claim.go can resolve
// overlaps and insert the intent under one BEGIN IMMEDIATE lock.
func (s *Store) ResolveAndInsertIntent(ctx context.Context, in Intent, ipaths []IntentPath, conflictPolicy string, hasOverride bool, supersede string) (ClaimResult, error) {
	prepared, ipaths, openedEvent, meta, err := s.prepareIntentForInsert(in, ipaths)
	if err != nil {
		return ClaimResult{}, err
	}
	var supersedeEvent storage.Event
	if supersede != "" {
		supersedeEvent, err = supersedeIntentEvent(supersede, prepared.IntentID, prepared.AgentID, prepared.OpenedAt)
		if err != nil {
			return ClaimResult{}, err
		}
	}
	var (
		decision conflicts.Decision
		filtered []conflicts.Overlap
	)
	if err := s.S.WriteDomainEventImmediate(ctx, func(ctx context.Context, conn *sql.Conn) ([]storage.Event, error) {
		// SPEC §14 #8: prefer canonical hash for cross-worktree conflict
		// detection; pass path_hash too so rows that have not been
		// backfilled are still matched by the legacy key.
		canonicalHashes := make([]string, 0, len(ipaths))
		legacyHashes := make([]string, 0, len(ipaths))
		for _, p := range ipaths {
			if p.CanonicalHash != "" {
				canonicalHashes = append(canonicalHashes, p.CanonicalHash)
			}
			legacyHashes = append(legacyHashes, p.PathHash)
		}
		rowHashes, err := activeIntentsByPathHashes(ctx, conn, canonicalHashes, legacyHashes)
		if err != nil {
			return nil, err
		}
		overlaps := make([]conflicts.Overlap, 0, len(rowHashes))
		for _, row := range rowHashes {
			display := row.Path
			canonical := row.CanonicalHash
			legacy := row.PathHash
			// Match by canonical first, fall back to path_hash so the
			// returned overlap reports the caller's display when one
			// of the two columns matched.
			for _, p := range ipaths {
				if (p.CanonicalHash != "" && p.CanonicalHash == row.CanonicalHash) || p.PathHash == row.PathHash {
					display = p.Path
					if canonical == "" {
						canonical = p.CanonicalHash
					}
					legacy = p.PathHash
					break
				}
			}
			overlaps = append(overlaps, conflicts.Overlap{
				NewPath:          display,
				NewPathHash:      legacy,
				NewCanonicalHash: canonical,
				ExistingIntent:   row.IntentID,
				ExistingPath:     row.Path,
			})
		}
		supersedeSet := map[string]bool{}
		if supersede != "" {
			supersedeSet[supersede] = true
		}
		decision, filtered = conflicts.Resolve(conflictPolicy, overlaps, hasOverride, supersedeSet)
		if decision == conflicts.Block {
			return nil, nil
		}
		if supersede != "" {
			if err := supersedeIntentTx(ctx, conn, supersede, prepared.IntentID, prepared.OpenedAt); err != nil {
				return nil, err
			}
			if err := insertIntentRows(ctx, conn, prepared, ipaths, meta); err != nil {
				return nil, err
			}
			return []storage.Event{supersedeEvent, openedEvent}, nil
		}
		if err := insertIntentRows(ctx, conn, prepared, ipaths, meta); err != nil {
			return nil, err
		}
		return []storage.Event{openedEvent}, nil
	}); err != nil {
		return ClaimResult{}, err
	}
	if decision == conflicts.Block {
		return ClaimResult{Decision: decision, Overlaps: filtered}, nil
	}
	return ClaimResult{Decision: decision, Overlaps: filtered, Intent: prepared}, nil
}

func supersedeIntentEvent(oldID, newID, agentID, occurredAt string) (storage.Event, error) {
	payload, err := events.MarshalPayload(map[string]any{
		"intent_id":     oldID,
		"superseded_by": newID,
	})
	if err != nil {
		return storage.Event{}, err
	}
	return storage.Event{
		Type:        "intent.superseded",
		AgentID:     agentID,
		IntentID:    oldID,
		OccurredAt:  occurredAt,
		PayloadJSON: payload,
	}, nil
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func supersedeIntentTx(ctx context.Context, exec sqlExecer, oldID, newID, occurredAt string) error {
	res, err := exec.ExecContext(ctx, `
		UPDATE intents
		SET status = 'closed',
		    closed_at = ?,
		    close_outcome = 'superseded',
		    metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.superseded_by', ?)
		WHERE intent_id = ? AND status = 'active'
	`, occurredAt, newID, oldID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 && oldID != "" {
		return ErrSupersedeNotActive
	}
	return nil
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
	decoded, err := decodeMeta(meta, "intents.metadata_json", in.IntentID)
	if err != nil {
		return Intent{}, err
	}
	in.Metadata = decoded
	return in, nil
}

// IntentPaths returns the paths for an intent.
func (s *Store) IntentPaths(ctx context.Context, intentID string) ([]IntentPath, error) {
	rows, err := s.S.DB().QueryContext(ctx, `SELECT intent_id, path, realpath, path_hash, COALESCE(canonical_path_hash,''), access_mode FROM intent_paths WHERE intent_id = ? ORDER BY path`, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IntentPath
	for rows.Next() {
		var p IntentPath
		if err := rows.Scan(&p.IntentID, &p.Path, &p.RealPath, &p.PathHash, &p.CanonicalHash, &p.AccessMode); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// ActiveIntentsByPathHashes returns active intents that overlap any of
// pathHashes. Caller-supplied hashes are matched against canonical_path_hash
// when set, falling back to the legacy path_hash column for rows that
// have not been backfilled yet (SPEC §14 #8 transitional).
//
// To preserve cross-worktree conflict detection during the transition,
// callers should pass canonical hashes; for backward compatibility we
// also accept path_hash values via the same interface, since the
// backfill rewrites only the canonical column.
func (s *Store) ActiveIntentsByPathHashes(ctx context.Context, pathHashes []string) ([]IntentPath, error) {
	return activeIntentsByPathHashes(ctx, s.S.DB(), pathHashes, pathHashes)
}

// ActiveIntentsByCanonicalAndPathHashes is the explicit two-key variant
// used by claim resolution. canonicalHashes are matched against
// intent_paths.canonical_path_hash; pathHashes are matched against
// intent_paths.path_hash for rows whose canonical column is still NULL
// (pre-backfill state). Both lists must align by index with the
// caller's request order, but they need not be the same length: any
// non-empty value in either is sufficient.
func (s *Store) ActiveIntentsByCanonicalAndPathHashes(ctx context.Context, canonicalHashes, pathHashes []string) ([]IntentPath, error) {
	return activeIntentsByPathHashes(ctx, s.S.DB(), canonicalHashes, pathHashes)
}

func activeIntentsByPathHashes(ctx context.Context, q sqlQueryer, canonicalHashes, pathHashes []string) ([]IntentPath, error) {
	if len(canonicalHashes) == 0 && len(pathHashes) == 0 {
		return nil, nil
	}
	// Build the WHERE clause in two parts: canonical match (preferred)
	// and legacy path_hash match for rows whose canonical column is
	// still NULL. The branches are unioned with OR so a single query
	// covers both cases. Empty input lists collapse to a never-matches
	// branch via a placeholder that no real hash equals.
	canArgs, canPlaceholders := hashListSQL(canonicalHashes)
	phArgs, phPlaceholders := hashListSQL(pathHashes)
	query := fmt.Sprintf(`
		SELECT ip.intent_id, ip.path, ip.realpath, ip.path_hash, COALESCE(ip.canonical_path_hash,''), ip.access_mode
		FROM intent_paths ip
		JOIN intents i ON i.intent_id = ip.intent_id
		WHERE i.status = 'active' AND (
			ip.canonical_path_hash IN (%s)
			OR (ip.canonical_path_hash IS NULL AND ip.path_hash IN (%s))
		)
	`, canPlaceholders, phPlaceholders)
	args := append(canArgs, phArgs...)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IntentPath
	for rows.Next() {
		var p IntentPath
		if err := rows.Scan(&p.IntentID, &p.Path, &p.RealPath, &p.PathHash, &p.CanonicalHash, &p.AccessMode); err != nil {
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
	ev, err := supersedeIntentEvent(oldID, newID, agentID, occurred)
	if err != nil {
		return err
	}
	return s.S.WriteDomainEventImmediate(ctx, func(ctx context.Context, conn *sql.Conn) ([]storage.Event, error) {
		if err := supersedeIntentTx(ctx, conn, oldID, newID, occurred); err != nil {
			return nil, err
		}
		return []storage.Event{ev}, nil
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
			INSERT INTO conflicts(conflict_id, event_id, path, path_hash, canonical_path_hash, existing_intent_id, new_intent_id, policy, status, detected_at, metadata_json)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, c.ConflictID, c.EventID, c.Path, c.PathHash, nullable(c.CanonicalHash), nullable(c.ExistingIntentID), nullable(c.NewIntentID), c.Policy, c.Status, c.DetectedAt, meta)
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
		SELECT conflict_id, event_id, path, path_hash, COALESCE(canonical_path_hash, ''),
		       COALESCE(existing_intent_id, ''), COALESCE(new_intent_id, ''),
		       policy, status, detected_at, COALESCE(acknowledged_at, ''), COALESCE(acknowledged_by_agent_id, ''),
		       COALESCE(resolution, ''), metadata_json
		FROM conflicts WHERE conflict_id = ?
	`, conflictID)
	var c Conflict
	var meta string
	if err := row.Scan(&c.ConflictID, &c.EventID, &c.Path, &c.PathHash, &c.CanonicalHash, &c.ExistingIntentID, &c.NewIntentID, &c.Policy, &c.Status, &c.DetectedAt, &c.AcknowledgedAt, &c.AcknowledgedByAgentID, &c.Resolution, &meta); err != nil {
		return Conflict{}, err
	}
	decoded, err := decodeMeta(meta, "conflicts.metadata_json", c.ConflictID)
	if err != nil {
		return Conflict{}, err
	}
	c.Metadata = decoded
	return c, nil
}

// ListConflicts lists conflicts, optionally filtered by task via the
// joined intent. Status filter "" returns everything.
func (s *Store) ListConflicts(ctx context.Context, taskID, status string) ([]Conflict, error) {
	q := `
		SELECT c.conflict_id, c.event_id, c.path, c.path_hash, COALESCE(c.canonical_path_hash, ''),
		       COALESCE(c.existing_intent_id, ''), COALESCE(c.new_intent_id, ''),
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
		if err := rows.Scan(&c.ConflictID, &c.EventID, &c.Path, &c.PathHash, &c.CanonicalHash, &c.ExistingIntentID, &c.NewIntentID, &c.Policy, &c.Status, &c.DetectedAt, &c.AcknowledgedAt, &c.AcknowledgedByAgentID, &c.Resolution, &meta); err != nil {
			return nil, err
		}
		decoded, err := decodeMeta(meta, "conflicts.metadata_json", c.ConflictID)
		if err != nil {
			return nil, err
		}
		c.Metadata = decoded
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
		decoded, err := decodeMeta(meta, "intents.metadata_json", in.IntentID)
		if err != nil {
			return nil, err
		}
		in.Metadata = decoded
		out = append(out, in)
	}
	return out, rows.Err()
}
