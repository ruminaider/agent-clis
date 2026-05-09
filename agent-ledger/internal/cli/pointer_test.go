package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/config"
)

// chdir temporarily changes the working directory to dir for the duration
// of the test. Tests run with t.Parallel disabled because runPointerShow
// reads os.Getwd directly.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestPointerShow_AbsentJSON(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	streams, out, _ := newTestStreams()
	code := Execute(streams, []string{"pointer", "show", "--json"})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (stderr buffer not captured)", code, ExitOK)
	}

	var got pointerShowReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\noutput: %s", err, out.String())
	}
	if got.Present {
		t.Errorf("Present = true, want false")
	}
	if got.DefaultTaskID != "" {
		t.Errorf("DefaultTaskID = %q, want empty", got.DefaultTaskID)
	}
	if !strings.HasSuffix(got.Path, config.PointerFileName) {
		t.Errorf("Path = %q, want suffix %q", got.Path, config.PointerFileName)
	}
}

func TestPointerShow_PresentWithDefaultTaskID(t *testing.T) {
	dir := t.TempDir()
	body := `version = 1
project_id = "scratch/example"
ledger_dir = "/tmp/foo"
default_task_id = "ambient-2026-05"
`
	if err := os.WriteFile(filepath.Join(dir, config.PointerFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	streams, out, _ := newTestStreams()
	code := Execute(streams, []string{"pointer", "show", "--json"})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}
	var got pointerShowReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\noutput: %s", err, out.String())
	}
	if !got.Present {
		t.Errorf("Present = false, want true")
	}
	if got.DefaultTaskID != "ambient-2026-05" {
		t.Errorf("DefaultTaskID = %q, want %q", got.DefaultTaskID, "ambient-2026-05")
	}
	if got.ProjectID != "scratch/example" {
		t.Errorf("ProjectID = %q, want %q", got.ProjectID, "scratch/example")
	}
	if got.LedgerDir != "/tmp/foo" {
		t.Errorf("LedgerDir = %q, want %q", got.LedgerDir, "/tmp/foo")
	}
}

func TestPointerShow_MalformedReturnsError(t *testing.T) {
	dir := t.TempDir()
	// Unsupported version triggers the LoadPointer error path.
	body := `version = 99
project_id = "x"
`
	if err := os.WriteFile(filepath.Join(dir, config.PointerFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	streams, _, errBuf := newTestStreams()
	code := Execute(streams, []string{"pointer", "show", "--json"})
	if code == ExitOK {
		t.Fatalf("expected non-zero exit, stderr=%s", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "pointer_parse_failed") {
		t.Errorf("stderr does not mention pointer_parse_failed: %s", errBuf.String())
	}
}

func TestPointerShow_TextOmitsFieldsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	streams, out, _ := newTestStreams()
	code := Execute(streams, []string{"pointer", "show"})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}
	got := out.String()
	if !strings.Contains(got, "present:         false") {
		t.Errorf("text output missing present=false line: %s", got)
	}
	if strings.Contains(got, "default_task_id:") {
		t.Errorf("text output should omit default_task_id when absent: %s", got)
	}
}

func TestPointerShow_TextRendersDefaultTaskID(t *testing.T) {
	dir := t.TempDir()
	body := `version = 1
ledger_dir = "/tmp/foo"
default_task_id = "ambient-2026-05"
`
	if err := os.WriteFile(filepath.Join(dir, config.PointerFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	streams, out, _ := newTestStreams()
	code := Execute(streams, []string{"pointer", "show"})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(out.String(), "default_task_id: ambient-2026-05") {
		t.Errorf("text output missing default_task_id line: %s", out.String())
	}
}
