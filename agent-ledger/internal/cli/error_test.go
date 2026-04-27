package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNotImplementedHasExitCode3(t *testing.T) {
	err := NotImplemented("claim")
	if err.ExitCode != ExitNotImplemented {
		t.Fatalf("ExitCode = %d, want %d", err.ExitCode, ExitNotImplemented)
	}
	if err.Code != "not_implemented" {
		t.Fatalf("Code = %q", err.Code)
	}
	if !strings.Contains(err.Message, "claim") {
		t.Fatalf("Message missing command name: %q", err.Message)
	}
	if got, _ := err.Details["command"].(string); got != "claim" {
		t.Fatalf("Details command = %v", err.Details["command"])
	}
}

func TestErrorWriteJSON(t *testing.T) {
	err := NewError(ExitConflict, "conflict_overlap", "overlap detected").
		WithDetails(map[string]any{"path": "src/foo.go"})

	var buf bytes.Buffer
	if writeErr := err.WriteJSON(&buf); writeErr != nil {
		t.Fatalf("WriteJSON: %v", writeErr)
	}

	var decoded map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &decoded); jerr != nil {
		t.Fatalf("invalid JSON: %v", jerr)
	}
	if decoded["status"] != "error" {
		t.Errorf("status = %v", decoded["status"])
	}
	if decoded["code"] != "conflict_overlap" {
		t.Errorf("code = %v", decoded["code"])
	}
	details, ok := decoded["details"].(map[string]any)
	if !ok {
		t.Fatalf("details missing or wrong type: %v", decoded["details"])
	}
	if details["path"] != "src/foo.go" {
		t.Errorf("details.path = %v", details["path"])
	}
}

func TestErrorWriteText(t *testing.T) {
	err := NewError(ExitUsage, "bad_flag", "unknown flag --foo")
	var buf bytes.Buffer
	if werr := err.WriteText(&buf); werr != nil {
		t.Fatalf("WriteText: %v", werr)
	}
	got := buf.String()
	if !strings.Contains(got, "ERROR:") || !strings.Contains(got, "[bad_flag]") || !strings.Contains(got, "unknown flag --foo") {
		t.Fatalf("text envelope unexpected: %q", got)
	}
}
