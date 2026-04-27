// Package summary builds the privacy-safe agent-ledger-summary.v1
// document that `agent-ledger export-summary` writes for a task.
//
// The summary is the CI-visible artifact described in SPEC §20.1 and
// §22. It must contain enough assignment data to verify a clean
// checkout (allowed paths, forbidden paths, conflict policy, assigned
// agent, reason hash) but no diff content, no command output, no
// secrets, no environment dumps.
//
// The Build function loads the relevant rows through a domain.Store
// and returns the marshaled JSON bytes. The output is deterministic:
// path lists, validations, and changes are sorted, and the JSON is
// emitted with two-space indentation through json.MarshalIndent.
package summary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/paths"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/project"
)

// Schema is the schema string embedded in every summary file.
const Schema = "agent-ledger-summary.v1"

// Document is the public shape of the summary file. Field tags
// determine the on-disk JSON. Field order in the struct matches the
// SPEC §22 example output.
type Document struct {
	Schema             string             `json:"schema"`
	GeneratedAt        string             `json:"generated_at"`
	Project            ProjectInfo        `json:"project"`
	Task               TaskInfo           `json:"task"`
	Agent              AgentInfo          `json:"agent"`
	AssignmentSnapshot AssignmentSnapshot `json:"assignment_snapshot"`
	AssignmentHash     string             `json:"assignment_hash"`
	ChangedPaths       []PathRef          `json:"changed_paths"`
	Changes            []ChangeRef        `json:"changes"`
	Validations        []ValidationRef    `json:"validations"`
	Closed             bool               `json:"closed"`
	ClosedAt           string             `json:"closed_at,omitempty"`
}

// ProjectInfo carries the project identity bits CI needs to bind a
// summary to a checkout without leaking working-tree state.
type ProjectInfo struct {
	ID          string `json:"id,omitempty"`
	Slug        string `json:"slug"`
	Fingerprint string `json:"fingerprint"`
}

// TaskInfo carries the task identifier.
type TaskInfo struct {
	ID string `json:"id"`
}

// AgentInfo names the assigned agent (display ID only, no harness or
// model details).
type AgentInfo struct {
	ID string `json:"id,omitempty"`
}

// AssignmentSnapshot captures every field a verifier needs to enforce
// assignment policy. Reasons are stored as a sha256 hash to avoid
// leaking sensitive context that might be embedded in human prose.
type AssignmentSnapshot struct {
	AssignmentID    string   `json:"assignment_id,omitempty"`
	TaskID          string   `json:"task_id"`
	OrchestratorID  string   `json:"orchestrator_id,omitempty"`
	AssignedAgentID string   `json:"assigned_agent_id,omitempty"`
	AllowedPaths    []string `json:"allowed_paths"`
	ForbiddenPaths  []string `json:"forbidden_paths"`
	ConflictPolicy  string   `json:"conflict_policy"`
	ReasonSHA256    string   `json:"reason_sha256,omitempty"`
}

// PathRef is the privacy-safe pointer used in changed_paths: display
// path plus sha256(realpath-normalized).
type PathRef struct {
	Path     string `json:"path"`
	PathHash string `json:"path_hash"`
}

// ChangeRef is the privacy-safe per-path change record. before/after
// hashes are optional and only set when the recorder was able to
// compute them. patch_sha256 binds the change to a normalized diff
// hash without exposing diff content.
type ChangeRef struct {
	ChangeID    string `json:"change_id"`
	Path        string `json:"path"`
	PathHash    string `json:"path_hash"`
	Status      string `json:"status"`
	BeforeSHA   string `json:"before_sha256,omitempty"`
	AfterSHA    string `json:"after_sha256,omitempty"`
	PatchSHA    string `json:"patch_sha256,omitempty"`
	RecordedAt  string `json:"recorded_at"`
	Retroactive bool   `json:"retroactive,omitempty"`
}

// ValidationRef carries the privacy-safe validation summary.
type ValidationRef struct {
	Command    string `json:"command"`
	Status     string `json:"status"`
	RecordedAt string `json:"recorded_at,omitempty"`
}

// Inputs bundles dependencies and parameters for Build.
type Inputs struct {
	Store       *domain.Store
	Identity    project.Identity
	TaskID      string
	GeneratedAt string
}

// Build assembles a Document for in.TaskID. It returns ErrTaskNotFound
// when no assignment exists for the task. Other failures (storage,
// JSON encoding) propagate as wrapped errors.
func Build(ctx context.Context, in Inputs) (Document, error) {
	if in.Store == nil {
		return Document{}, errors.New("summary: nil store")
	}
	if in.TaskID == "" {
		return Document{}, errors.New("summary: empty task id")
	}
	d := Document{
		Schema:      Schema,
		GeneratedAt: in.GeneratedAt,
		Project: ProjectInfo{
			ID:          in.Identity.ProjectID,
			Slug:        in.Identity.Slug,
			Fingerprint: in.Identity.Fingerprint,
		},
		Task: TaskInfo{ID: in.TaskID},
	}

	assignment, err := in.Store.LatestActiveAssignmentForTask(ctx, in.TaskID)
	if err != nil {
		return Document{}, fmt.Errorf("summary: load assignment: %w", err)
	}
	d.AssignmentSnapshot = AssignmentSnapshot{
		AssignmentID:    assignment.AssignmentID,
		TaskID:          assignment.TaskID,
		OrchestratorID:  assignment.OrchestratorID,
		AssignedAgentID: assignment.AssignedAgentID,
		AllowedPaths:    sortedCopy(assignment.AllowedPaths),
		ForbiddenPaths:  sortedCopy(assignment.ForbiddenPaths),
		ConflictPolicy:  assignment.ConflictPolicy,
		ReasonSHA256:    sha256Hex(assignment.Reason),
	}
	d.Agent = AgentInfo{ID: assignment.AssignedAgentID}
	d.AssignmentHash = assignmentHash(d.AssignmentSnapshot)

	// Changes for the task, with their per-path entries.
	changes, err := in.Store.ChangesForTask(ctx, in.TaskID)
	if err != nil {
		return Document{}, fmt.Errorf("summary: load changes: %w", err)
	}
	pathSeen := map[string]PathRef{}
	for _, c := range changes {
		changePaths, err := in.Store.ChangePaths(ctx, c.ChangeID)
		if err != nil {
			return Document{}, fmt.Errorf("summary: load change paths: %w", err)
		}
		retro := false
		if v, ok := c.Metadata["retroactive"].(bool); ok {
			retro = v
		}
		for _, p := range changePaths {
			// Use the portable hash (sha256 of NFC-normalized relative path with
			// forward slashes) so that verify --summary succeeds in any checkout
			// regardless of the absolute realpath. SPEC §20.1, §32.
			portableHash := paths.PortableHash(p.Path)
			d.Changes = append(d.Changes, ChangeRef{
				ChangeID:    c.ChangeID,
				Path:        p.Path,
				PathHash:    portableHash,
				Status:      p.Status,
				BeforeSHA:   p.BeforeSHA,
				AfterSHA:    p.AfterSHA,
				PatchSHA:    p.PatchSHA,
				RecordedAt:  c.CreatedAt,
				Retroactive: retro,
			})
			pathSeen[portableHash] = PathRef{Path: p.Path, PathHash: portableHash}
		}
	}
	for _, ref := range pathSeen {
		d.ChangedPaths = append(d.ChangedPaths, ref)
	}
	sort.Slice(d.ChangedPaths, func(i, j int) bool {
		return d.ChangedPaths[i].Path < d.ChangedPaths[j].Path
	})
	sort.SliceStable(d.Changes, func(i, j int) bool {
		if d.Changes[i].RecordedAt != d.Changes[j].RecordedAt {
			return d.Changes[i].RecordedAt < d.Changes[j].RecordedAt
		}
		if d.Changes[i].ChangeID != d.Changes[j].ChangeID {
			return d.Changes[i].ChangeID < d.Changes[j].ChangeID
		}
		return d.Changes[i].Path < d.Changes[j].Path
	})

	// Validations.
	vals, err := in.Store.ValidationsForTask(ctx, in.TaskID)
	if err != nil {
		return Document{}, fmt.Errorf("summary: load validations: %w", err)
	}
	for _, v := range vals {
		d.Validations = append(d.Validations, ValidationRef{
			Command:    v.Command,
			Status:     v.Status,
			RecordedAt: v.CompletedAt,
		})
	}

	// Closed = at least one intent for this task is closed and none
	// remain active. We also surface the most recent closed_at.
	intents, err := in.Store.IntentsForTask(ctx, in.TaskID)
	if err != nil {
		return Document{}, fmt.Errorf("summary: load intents: %w", err)
	}
	hasActive := false
	hasClosed := false
	latestClosed := ""
	for _, in := range intents {
		if in.Status == domain.IntentActive {
			hasActive = true
		}
		if in.Status == domain.IntentClosed && in.ClosedAt != "" {
			hasClosed = true
			if in.ClosedAt > latestClosed {
				latestClosed = in.ClosedAt
			}
		}
	}
	d.Closed = hasClosed && !hasActive
	if d.Closed {
		d.ClosedAt = latestClosed
	}

	return d, nil
}

// Marshal renders d as deterministic indented JSON suitable for
// committing under tasks/agent-ledger/<task>.json.
func Marshal(d Document) ([]byte, error) {
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("summary: marshal: %w", err)
	}
	raw = append(raw, '\n')
	return raw, nil
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

func sha256Hex(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// assignmentHash returns a stable hash over the privacy-safe
// assignment snapshot. Verifiers can use this to detect tampering of
// the committed summary.
func assignmentHash(s AssignmentSnapshot) string {
	raw, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
