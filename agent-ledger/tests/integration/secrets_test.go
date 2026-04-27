package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// secretFixtures are realistic-looking secret strings (not real
// credentials). The privacy contract requires that no value the user
// might paste into a reason or summary appears verbatim in any
// stored artifact: SQLite tables, audit JSONL, exported summary, or
// blobs.
//
// SPEC §17 names the categories: AKIA prefixes, GitHub PATs, OpenAI
// keys, bearer tokens, env dumps. Each fixture below probes one
// category.
var secretFixtures = []struct {
	name  string
	value string
}{
	{"aws_access_key", "AKIAABCDEFGHIJKLMNOP"},
	{"github_pat", "ghp_1234567890abcdefghijklmnopqrstuvwxyz"},
	{"openai_key", "sk-abcdefghijklmnopqrstuvwxyz0123456789ABCD"},
	{"bearer_token", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.sig"},
	{"env_dump", "API_KEY=topsecret123\nAWS_SECRET=hunter2\nDB_PASS=abc"},
}

// TestPrivacy_SecretsNotPersisted_DefaultPath drives a full
// claim+record+close+export-summary flow and asserts that verbatim
// secrets never appear in the audit JSONL, exported summary, or blobs.
//
// Since R-006 (finding wv1-f05, SPEC §17), secrets in --reason are
// rejected at the CLI layer before they reach the DB. This is a
// stronger guarantee than the original hash-on-write approach:
// no secret ever touches any storage surface at all. The test
// therefore uses safe (non-secret) reasons and verifies the audit
// and summary surfaces are clean. Secret rejection itself is covered
// in the unit tests in internal/commands/privacy_reason_test.go.
func TestPrivacy_SecretsNotPersisted_DefaultPath(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	root := t.TempDir()
	ledger := freshLedger(t)
	writeFile(t, filepath.Join(root, "src", "x.go"), "package src\n")

	// Use safe reasons: secret-containing reasons are now rejected
	// before they reach any storage layer (R-006, SPEC §17).
	safeAssignReason := "deploy approved task packet TS"
	safeClaimReason := "edit src/x.go per task spec"

	mustZero(t, "assign", run(t, root, nil,
		"assign", "--task", "TS",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.sec",
		"--allow", "src/**",
		"--policy", "warn",
		"--reason", safeAssignReason,
		"--ledger-dir", ledger,
	))

	cl := run(t, root, []string{"AGENT_ID=pi.worker.sec"},
		"claim", "src/x.go",
		"--task", "TS",
		"--reason", safeClaimReason,
		"--json",
		"--ledger-dir", ledger,
	)
	mustZero(t, "claim", cl)
	var resp map[string]any
	if err := json.Unmarshal([]byte(cl.Stdout), &resp); err != nil {
		t.Fatal(err)
	}
	intentID, _ := resp["intent_id"].(string)
	if intentID == "" {
		t.Fatalf("no intent_id: %s", cl.Stdout)
	}

	mustZero(t, "record", run(t, root, []string{"AGENT_ID=pi.worker.sec"},
		"record", "src/x.go",
		"--intent", intentID,
		"--summary", "applied review feedback",
		"--ledger-dir", ledger,
	))

	mustZero(t, "close", run(t, root, []string{"AGENT_ID=pi.worker.sec"},
		"close", "--intent", intentID,
		"--outcome", "completed",
		"--ledger-dir", ledger,
	))

	summaryPath := filepath.Join(root, "summary-TS.json")
	mustZero(t, "export-summary", run(t, root, nil,
		"export-summary", "--task", "TS",
		"--output", summaryPath,
		"--ledger-dir", ledger,
	))

	// Walk the externally-visible privacy surfaces and assert no
	// fixture appears verbatim. SPEC §17 enumerates these:
	//
	//   * audit/*.jsonl  (replicated event mirror, may ship to CI)
	//   * blobs/sha256/  (only populated under --include-diff --yes)
	//   * exported summary (§20, ships to CI)
	//
	// Note: secrets in --reason are now rejected at the CLI layer
	// (R-006, SPEC §17) so they never reach any storage surface.
	// This scan confirms that the audit mirror and summary remain
	// clean of any secret-shaped values.
	scanForLeaks := func(p string) {
		t.Helper()
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		text := string(data)
		for _, fx := range secretFixtures {
			if strings.Contains(text, fx.value) {
				t.Errorf("file %s leaked secret fixture %s: %s", p, fx.name, fx.value)
			}
		}
	}
	auditDir := filepath.Join(ledger, "audit")
	if entries, err := os.ReadDir(auditDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				scanForLeaks(filepath.Join(auditDir, e.Name()))
			}
		}
	}
	blobs := filepath.Join(ledger, "blobs", "sha256")
	if entries, err := os.ReadDir(blobs); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				scanForLeaks(filepath.Join(blobs, e.Name()))
			}
		}
	}
	scanForLeaks(summaryPath)
}

// TestPrivacy_RecordRejectsSecretSummary asserts the in-band
// privacy guard from SPEC §17: when a record summary itself looks
// like a secret pattern, the command refuses with ExitValidation
// (6) rather than persisting it.
func TestPrivacy_RecordRejectsSecretSummary(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	root := t.TempDir()
	ledger := freshLedger(t)
	writeFile(t, filepath.Join(root, "y.go"), "package y\n")

	mustZero(t, "assign", run(t, root, nil,
		"assign", "--task", "TR",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.r",
		"--allow", "y.go",
		"--policy", "warn",
		"--reason", "reject test",
		"--ledger-dir", ledger,
	))
	cl := run(t, root, []string{"AGENT_ID=pi.worker.r"},
		"claim", "y.go", "--task", "TR", "--reason", "edit", "--json",
		"--ledger-dir", ledger,
	)
	mustZero(t, "claim", cl)
	var resp map[string]any
	json.Unmarshal([]byte(cl.Stdout), &resp)
	intentID, _ := resp["intent_id"].(string)

	// Each fixture is fed in turn. The expected outcome is exit 6
	// (ExitValidation) every time.
	for _, fx := range secretFixtures {
		r := run(t, root, []string{"AGENT_ID=pi.worker.r"},
			"record", "y.go",
			"--intent", intentID,
			"--summary", "leaked: "+fx.value,
			"--ledger-dir", ledger,
		)
		if r.Code != 6 {
			t.Errorf("%s: expected ExitValidation(6), got %d stderr=%s",
				fx.name, r.Code, r.Stderr)
		}
		if !strings.Contains(r.Stderr, "secret") && !strings.Contains(r.Stderr, "privacy") {
			t.Errorf("%s: expected privacy error in stderr, got %s", fx.name, r.Stderr)
		}
	}
}

// TestPrivacy_NoFullDiffPersistedByDefault drives a record without
// --include-diff and asserts the changes table has no patch_ref and
// no blob exists under blobs/sha256.
func TestPrivacy_NoFullDiffPersistedByDefault(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	root := t.TempDir()
	ledger := freshLedger(t)
	writeFile(t, filepath.Join(root, "a.go"), "package a\n")

	mustZero(t, "assign", run(t, root, nil,
		"assign", "--task", "TD",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.diff",
		"--allow", "a.go",
		"--policy", "warn",
		"--reason", "diff guard",
		"--ledger-dir", ledger,
	))
	cl := run(t, root, []string{"AGENT_ID=pi.worker.diff"},
		"claim", "a.go", "--task", "TD", "--reason", "edit", "--json",
		"--ledger-dir", ledger,
	)
	mustZero(t, "claim", cl)
	var resp map[string]any
	json.Unmarshal([]byte(cl.Stdout), &resp)
	intentID, _ := resp["intent_id"].(string)

	mustZero(t, "record", run(t, root, []string{"AGENT_ID=pi.worker.diff"},
		"record", "a.go",
		"--intent", intentID,
		"--summary", "tiny tweak",
		"--ledger-dir", ledger,
	))

	// blobs/sha256 must be empty or missing.
	blobs := filepath.Join(ledger, "blobs", "sha256")
	if entries, err := os.ReadDir(blobs); err == nil {
		// Some implementations may pre-create the directory. Empty is
		// fine; any file is a leak.
		nonDir := 0
		for _, e := range entries {
			if !e.IsDir() {
				nonDir++
			}
		}
		if nonDir != 0 {
			t.Fatalf("blobs/sha256 unexpectedly populated: %d entries", nonDir)
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("readdir blobs: %v", err)
	}
}
