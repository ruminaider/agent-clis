package summary

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarshalDeterministic(t *testing.T) {
	d := Document{
		Schema:      "agent-ledger-summary.v1",
		GeneratedAt: "2026-04-27T00:00:00Z",
		Project:     ProjectInfo{Slug: "demo", Fingerprint: "abc"},
		Task:        TaskInfo{ID: "T-1"},
		AssignmentSnapshot: AssignmentSnapshot{
			TaskID:         "T-1",
			AllowedPaths:   []string{"a", "b"},
			ForbiddenPaths: []string{},
			ConflictPolicy: "warn",
		},
		ChangedPaths: []PathRef{},
		Changes:      []ChangeRef{},
		Validations:  []ValidationRef{},
	}
	a, err := Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("non-deterministic marshal: %s vs %s", a, b)
	}
	// Sanity: parses as JSON.
	var v map[string]any
	if err := json.Unmarshal(a, &v); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if !strings.Contains(string(a), `"schema": "agent-ledger-summary.v1"`) {
		t.Fatalf("missing schema: %s", a)
	}
}
