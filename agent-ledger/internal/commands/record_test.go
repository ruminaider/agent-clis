package commands_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
	"github.com/ruminaider/agent-clis/agent-ledger/internal/commands"
)

// runCmdStdin is like runCmd but pipes stdin into the command. Used
// for --include-diff scenarios.
func runCmdStdin(t *testing.T, ledgerDir string, stdin string, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	streams := cli.IOStreams{In: strings.NewReader(stdin), Out: out, Err: errBuf}
	full := append([]string{}, args...)
	full = append(full, "--ledger-dir", ledgerDir)
	for k, v := range env {
		t.Setenv(k, v)
	}
	code := commands.Execute(streams, full)
	return code, out.String(), errBuf.String()
}

// setupClaim creates an assignment + claim for a given path and
// returns the intent_id. Helper for record/adopt/export-summary
// tests.
func setupClaim(t *testing.T, root, ledger, task, agent string, files ...string) string {
	t.Helper()
	for _, f := range files {
		full := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if code, _, e := runCmd(t, ledger, nil,
		append([]string{"assign", "--task", task,
			"--orchestrator", "pi.main.test",
			"--agent", agent,
			"--policy", "warn", "--reason", "test"},
			expandAllow(files)...)...); code != 0 {
		t.Fatalf("assign: %d %s", code, e)
	}
	args := []string{"claim"}
	args = append(args, files...)
	args = append(args, "--task", task, "--reason", "edit", "--json")
	code, out, e := runCmd(t, ledger, map[string]string{"AGENT_ID": agent}, args...)
	if code != 0 {
		t.Fatalf("claim: %d %s %s", code, out, e)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("claim json: %v %s", err, out)
	}
	id, _ := resp["intent_id"].(string)
	if id == "" {
		t.Fatalf("no intent_id in %s", out)
	}
	return id
}

func expandAllow(files []string) []string {
	out := make([]string, 0, len(files)*2)
	for _, f := range files {
		out = append(out, "--allow", f)
	}
	return out
}

func TestRecordWithValidations(t *testing.T) {
	root, ledger := tempLedger(t)
	intentID := setupClaim(t, root, ledger, "T-REC", "pi.worker.rec", "src/foo.go")

	code, out, errStr := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.rec"},
		"record", "src/foo.go",
		"--intent", intentID,
		"--summary", "Tightened error mapping",
		"--validation", "go test ./...:passed",
		"--validation", "uv run ruff check src/foo:bar.py:passed",
		"--json",
	)
	if code != 0 {
		t.Fatalf("record: %d %s %s", code, out, errStr)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("record json: %v %s", err, out)
	}
	if resp["change_id"] == nil || resp["change_id"].(string) == "" {
		t.Fatalf("missing change_id: %v", resp)
	}
	vs, _ := resp["validations"].([]any)
	if len(vs) != 2 {
		t.Fatalf("expected 2 validations, got %v", vs)
	}
}

func TestRecordRejectsUnclaimedPath(t *testing.T) {
	root, ledger := tempLedger(t)
	intentID := setupClaim(t, root, ledger, "T-REJ", "pi.worker.rej", "src/foo.go")

	// Create a second file that is NOT in the intent.
	if err := os.WriteFile(filepath.Join(root, "other.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Count events before.
	beforeEvents := countEvents(t, ledger)
	beforeChanges := countRows(t, ledger, "changes")

	code, out, errStr := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.rej"},
		"record", "other.txt",
		"--intent", intentID,
		"--summary", "Sneaky write",
	)
	if code != cli.ExitGeneric {
		t.Fatalf("expected exit 1, got %d (%s %s)", code, out, errStr)
	}
	if !strings.Contains(errStr, "unclaimed_path") && !strings.Contains(errStr, "not in intent") {
		t.Fatalf("expected unclaimed_path message: %s", errStr)
	}

	afterEvents := countEvents(t, ledger)
	afterChanges := countRows(t, ledger, "changes")
	if afterEvents != beforeEvents {
		t.Fatalf("expected no new events, before=%d after=%d", beforeEvents, afterEvents)
	}
	if afterChanges != beforeChanges {
		t.Fatalf("expected no new change rows, before=%d after=%d", beforeChanges, afterChanges)
	}
}

func TestRecordIncludeDiffRequiresYes(t *testing.T) {
	root, ledger := tempLedger(t)
	intentID := setupClaim(t, root, ledger, "T-DIFF", "pi.worker.diff", "src/x.go")

	// Without --yes (non-interactive stdin) we should refuse.
	code, _, errStr := runCmdStdin(t, ledger, "diff body\n", map[string]string{"AGENT_ID": "pi.worker.diff"},
		"record", "src/x.go",
		"--intent", intentID,
		"--summary", "Tweaked thing",
		"--include-diff",
	)
	if code != cli.ExitValidation {
		t.Fatalf("expected exit %d, got %d err=%s", cli.ExitValidation, code, errStr)
	}

	// With --yes we should accept and store the blob.
	diff := "--- a/src/x.go\n+++ b/src/x.go\n@@\n-old\n+new\n"
	code, out, errStr := runCmdStdin(t, ledger, diff, map[string]string{"AGENT_ID": "pi.worker.diff"},
		"record", "src/x.go",
		"--intent", intentID,
		"--summary", "Tweaked thing",
		"--include-diff", "--yes", "--json",
	)
	if code != 0 {
		t.Fatalf("record diff: %d %s %s", code, out, errStr)
	}
	var resp map[string]any
	_ = json.Unmarshal([]byte(out), &resp)
	if resp["patch_sha256"] == nil || resp["patch_sha256"].(string) == "" {
		t.Fatalf("expected patch_sha256 in %v", resp)
	}
	ref, _ := resp["patch_ref"].(string)
	if !strings.HasPrefix(ref, "blobs/sha256/") {
		t.Fatalf("expected patch_ref to be blobs/sha256/..., got %q", ref)
	}
	// Confirm blob exists on disk.
	rel := strings.TrimPrefix(ref, "blobs/sha256/")
	if _, err := os.Stat(filepath.Join(ledger, "blobs", "sha256", rel)); err != nil {
		t.Fatalf("blob missing: %v", err)
	}
}

func TestAdoptEmitsAdoptedNotRecorded(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "missed.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Adopt a file that was never claimed.
	code, out, errStr := runCmd(t, ledger, nil,
		"adopt", "missed.go",
		"--task", "T-ADOPT",
		"--agent", "pi.worker.late",
		"--reason", "Backfill missed claim after verifier found unclaimed change",
		"--json",
	)
	if code != 0 {
		t.Fatalf("adopt: %d %s %s", code, out, errStr)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("adopt json: %v %s", err, out)
	}
	if resp["retroactive"] != true {
		t.Fatalf("expected retroactive true, got %v", resp)
	}

	// Inspect events: change.adopted present, change.recorded absent.
	types := eventTypes(t, ledger)
	hasAdopted := false
	hasRecorded := false
	for _, et := range types {
		if et == "change.adopted" {
			hasAdopted = true
		}
		if et == "change.recorded" {
			hasRecorded = true
		}
	}
	if !hasAdopted {
		t.Fatalf("expected change.adopted event in %v", types)
	}
	if hasRecorded {
		t.Fatalf("did NOT expect change.recorded in %v", types)
	}

	// Confirm metadata_json.retroactive = true on the change row.
	meta := queryString(t, ledger, "SELECT metadata_json FROM changes WHERE change_id = ?", resp["change_id"].(string))
	if !strings.Contains(meta, `"retroactive":true`) {
		t.Fatalf("expected retroactive=true in metadata, got %s", meta)
	}
}

func TestExportSummary(t *testing.T) {
	root, ledger := tempLedger(t)
	intentID := setupClaim(t, root, ledger, "T-SUM", "pi.worker.sum", "src/sum.go")

	if code, _, e := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.sum"},
		"record", "src/sum.go",
		"--intent", intentID,
		"--summary", "Summary build test",
		"--validation", "go test ./...:passed",
	); code != 0 {
		t.Fatalf("record: %d %s", code, e)
	}

	out := filepath.Join(root, "tasks/agent-ledger/T-SUM.json")
	if code, _, e := runCmd(t, ledger, nil, "export-summary", "--task", "T-SUM", "--output", out); code != 0 {
		t.Fatalf("export-summary: %d %s", code, e)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("summary not JSON: %v", err)
	}
	if doc["schema"] != "agent-ledger-summary.v1" {
		t.Fatalf("wrong schema: %v", doc["schema"])
	}
	snap, ok := doc["assignment_snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("missing assignment_snapshot")
	}
	if snap["conflict_policy"] == "" {
		t.Fatalf("missing conflict_policy in snapshot")
	}
	if snap["allowed_paths"] == nil {
		t.Fatalf("missing allowed_paths")
	}
	// Privacy: must not contain forbidden keys anywhere.
	rawText := string(raw)
	for _, banned := range []string{`"diff"`, `"patch"`, `"contents"`, `"env"`, `"environment"`, `"tokens"`, `"stdout"`} {
		if strings.Contains(rawText, banned) {
			t.Fatalf("summary leaked %s: %s", banned, rawText)
		}
	}
	// Privacy: reason should be hashed, not echoed.
	if strings.Contains(rawText, "test") && !strings.Contains(rawText, "reason_sha256") {
		t.Fatalf("expected reason_sha256 to bind reason without echoing it")
	}
	cp, _ := doc["changed_paths"].([]any)
	if len(cp) != 1 {
		t.Fatalf("expected 1 changed path, got %v", cp)
	}
	first := cp[0].(map[string]any)
	if first["path"] != "src/sum.go" {
		t.Fatalf("expected src/sum.go, got %v", first)
	}
	if h, _ := first["path_hash"].(string); len(h) != 64 {
		t.Fatalf("expected 64-char path hash, got %q", h)
	}
	vs, _ := doc["validations"].([]any)
	if len(vs) != 1 {
		t.Fatalf("expected 1 validation, got %v", vs)
	}
}
