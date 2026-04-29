package domain

import (
	"context"
	"errors"
	"fmt"
)

// IntegrityIssue describes one row whose JSON column failed to decode.
// Issues are aggregated by IntegrityScan instead of returned as the
// first error so a single scan reports every corrupted row, not just
// the earliest one a normal reader would have hit.
type IntegrityIssue struct {
	Table   string `json:"table"`
	Column  string `json:"column"`
	RowID   string `json:"row_id"`
	Kind    string `json:"kind"` // "metadata" | "paths" | "payload"
	Message string `json:"message"`
}

// IntegrityReport is the aggregate result of IntegrityScan.
// Tables maps each scanned table to the number of rows examined.
// Issues is the per-row corruption list (empty when the ledger is
// clean).
type IntegrityReport struct {
	Tables map[string]int   `json:"tables"`
	Issues []IntegrityIssue `json:"issues"`
}

// HasIssues reports whether the scan found any decode failures.
func (r *IntegrityReport) HasIssues() bool { return len(r.Issues) > 0 }

// Total returns the total number of rows examined across all tables.
func (r *IntegrityReport) Total() int {
	n := 0
	for _, c := range r.Tables {
		n += c
	}
	return n
}

// IntegrityScan walks every table whose schema includes a JSON
// column, attempts to decode each row's metadata / paths / payload
// columns independently, and returns one aggregate report. Decode
// errors are collected; the scan does not stop on the first failure.
//
// Operational use: agent-ledger scan exposes this as a CLI surface
// so reviewers can audit a ledger for corruption without writing a
// custom SQL query. Routine sessions never call this; it is a
// targeted diagnostic for "did anything corrupt?" investigations.
func (s *Store) IntegrityScan(ctx context.Context) (IntegrityReport, error) {
	r := IntegrityReport{Tables: map[string]int{}}

	if err := s.scanMetaTable(ctx, &r, "agents", "agent_id"); err != nil {
		return r, err
	}
	if err := s.scanAssignments(ctx, &r); err != nil {
		return r, err
	}
	if err := s.scanMetaTable(ctx, &r, "intents", "intent_id"); err != nil {
		return r, err
	}
	if err := s.scanMetaTable(ctx, &r, "changes", "change_id"); err != nil {
		return r, err
	}
	if err := s.scanMetaTable(ctx, &r, "validations", "validation_id"); err != nil {
		return r, err
	}
	if err := s.scanMetaTable(ctx, &r, "conflicts", "conflict_id"); err != nil {
		return r, err
	}
	if err := s.scanEvents(ctx, &r); err != nil {
		return r, err
	}
	return r, nil
}

// scanMetaTable walks any table that exposes a single metadata_json
// column keyed by the supplied id column. Generic so the only
// per-table difference is the table name and the id column.
func (s *Store) scanMetaTable(ctx context.Context, r *IntegrityReport, table, idCol string) error {
	q := fmt.Sprintf("SELECT %s, metadata_json FROM %s", idCol, table)
	rows, err := s.S.DB().QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("integrity scan: %s: %w", table, err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var rowID, raw string
		if err := rows.Scan(&rowID, &raw); err != nil {
			return fmt.Errorf("integrity scan: %s scan: %w", table, err)
		}
		count++
		if _, err := decodeMeta(raw, table+".metadata_json", rowID); err != nil {
			r.Issues = append(r.Issues, issueFromError(table, "metadata_json", rowID, "metadata", err))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("integrity scan: %s rows: %w", table, err)
	}
	r.Tables[table] = count
	return nil
}

// scanAssignments handles the assignment row's three JSON columns
// (allowed_paths_json, forbidden_paths_json, metadata_json) so a
// single corrupt assignment surfaces all of its decode failures
// rather than aborting after the first one.
func (s *Store) scanAssignments(ctx context.Context, r *IntegrityReport) error {
	rows, err := s.S.DB().QueryContext(ctx,
		`SELECT assignment_id, allowed_paths_json, forbidden_paths_json, metadata_json FROM assignments`)
	if err != nil {
		return fmt.Errorf("integrity scan: assignments: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, allowed, forbid, meta string
		if err := rows.Scan(&id, &allowed, &forbid, &meta); err != nil {
			return fmt.Errorf("integrity scan: assignments scan: %w", err)
		}
		count++
		if _, err := decodePaths(allowed, "assignments.allowed_paths_json", id); err != nil {
			r.Issues = append(r.Issues, issueFromError("assignments", "allowed_paths_json", id, "paths", err))
		}
		if _, err := decodePaths(forbid, "assignments.forbidden_paths_json", id); err != nil {
			r.Issues = append(r.Issues, issueFromError("assignments", "forbidden_paths_json", id, "paths", err))
		}
		if _, err := decodeMeta(meta, "assignments.metadata_json", id); err != nil {
			r.Issues = append(r.Issues, issueFromError("assignments", "metadata_json", id, "metadata", err))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("integrity scan: assignments rows: %w", err)
	}
	r.Tables["assignments"] = count
	return nil
}

// scanEvents walks the events table and tries to decode each
// payload_json. Corrupt payloads are not necessarily user-facing
// (events are append-only and most readers do not deserialize the
// whole payload), but a corrupt row indicates ledger corruption
// that operators should know about.
func (s *Store) scanEvents(ctx context.Context, r *IntegrityReport) error {
	rows, err := s.S.DB().QueryContext(ctx, `SELECT event_id, payload_json FROM events`)
	if err != nil {
		return fmt.Errorf("integrity scan: events: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return fmt.Errorf("integrity scan: events scan: %w", err)
		}
		count++
		if _, err := decodeMeta(raw, "events.payload_json", id); err != nil {
			r.Issues = append(r.Issues, issueFromError("events", "payload_json", id, "payload", err))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("integrity scan: events rows: %w", err)
	}
	r.Tables["events"] = count
	return nil
}

func issueFromError(table, column, rowID, kind string, err error) IntegrityIssue {
	msg := err.Error()
	var mde *MetadataDecodeError
	if errors.As(err, &mde) {
		msg = fmt.Sprintf("%v", mde.Err)
	}
	var pde *PathsDecodeError
	if errors.As(err, &pde) {
		msg = fmt.Sprintf("%v", pde.Err)
	}
	return IntegrityIssue{
		Table:   table,
		Column:  column,
		RowID:   rowID,
		Kind:    kind,
		Message: msg,
	}
}
