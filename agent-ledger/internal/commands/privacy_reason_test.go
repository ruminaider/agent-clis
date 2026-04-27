package commands_test

// Tests for R-006: privacy.AssertSafe guards on assignment.reason and
// intent.reason (finding wv1-f05, SPEC §17).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/cli"
)

// unsafeReason is a value that matches the OpenAI/Anthropic key shape
// detected by privacy.IsLikelySecret (pattern: \bsk-[A-Za-z0-9]{20,}).
const unsafeReason = "sk-abc1234567890abcdef01"

// TestAssignRejectsUnsafeReason verifies that `assign` exits with
// ExitConfigError (code 2) when --reason contains a known secret
// pattern, and does not write any rows to the database.
func TestAssignRejectsUnsafeReason(t *testing.T) {
	_, ledger := tempLedger(t)

	code, _, errStr := runCmd(t, ledger, nil,
		"assign",
		"--task", "T-priv-assign",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.test",
		"--allow", "README.md",
		"--policy", "warn",
		"--reason", unsafeReason,
	)
	if code != cli.ExitConfigError {
		t.Fatalf("expected exit code %d (ExitConfigError), got %d; stderr: %s", cli.ExitConfigError, code, errStr)
	}
	if !strings.Contains(errStr, "privacy") && !strings.Contains(errStr, "secret") && !strings.Contains(errStr, "unsafe") {
		t.Fatalf("expected privacy-related error message, got: %s", errStr)
	}
	// No rows should have been written.
	if n := countRows(t, ledger, "assignments"); n != 0 {
		t.Fatalf("expected 0 assignment rows, got %d", n)
	}
}

// TestAssignAcceptsSafeReason verifies that a normal, secret-free reason
// is accepted without error (regression guard).
func TestAssignAcceptsSafeReason(t *testing.T) {
	_, ledger := tempLedger(t)

	code, _, errStr := runCmd(t, ledger, nil,
		"assign",
		"--task", "T-priv-safe",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.test",
		"--allow", "README.md",
		"--policy", "warn",
		"--reason", "Implement approved task packet",
	)
	if code != cli.ExitOK {
		t.Fatalf("expected exit 0, got %d; stderr: %s", code, errStr)
	}
}

// TestClaimRejectsUnsafeReason verifies that `claim` exits with
// ExitConfigError (code 2) when --reason contains a known secret
// pattern, and does not write any rows to the database.
func TestClaimRejectsUnsafeReason(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	// First create a valid assignment so claim can resolve it.
	if code, _, e := runCmd(t, ledger, nil,
		"assign",
		"--task", "T-priv-claim",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.test",
		"--allow", "README.md",
		"--policy", "warn",
		"--reason", "safe reason for setup",
	); code != 0 {
		t.Fatalf("setup assign: %d %s", code, e)
	}

	code, _, errStr := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.test"},
		"claim", "README.md",
		"--task", "T-priv-claim",
		"--reason", unsafeReason,
	)
	if code != cli.ExitConfigError {
		t.Fatalf("expected exit code %d (ExitConfigError), got %d; stderr: %s", cli.ExitConfigError, code, errStr)
	}
	if !strings.Contains(errStr, "privacy") && !strings.Contains(errStr, "secret") && !strings.Contains(errStr, "unsafe") {
		t.Fatalf("expected privacy-related error message, got: %s", errStr)
	}
	// No intents should have been written.
	if n := countRows(t, ledger, "intents"); n != 0 {
		t.Fatalf("expected 0 intent rows, got %d", n)
	}
}

// TestClaimAcceptsSafeReason verifies that a normal reason is accepted
// (regression guard).
func TestClaimAcceptsSafeReason(t *testing.T) {
	root, ledger := tempLedger(t)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, _, e := runCmd(t, ledger, nil,
		"assign",
		"--task", "T-priv-claim-safe",
		"--orchestrator", "pi.main.test",
		"--agent", "pi.worker.test",
		"--allow", "README.md",
		"--policy", "warn",
		"--reason", "safe orchestrator reason",
	); code != 0 {
		t.Fatalf("setup assign: %d %s", code, e)
	}

	code, _, errStr := runCmd(t, ledger, map[string]string{"AGENT_ID": "pi.worker.test"},
		"claim", "README.md",
		"--task", "T-priv-claim-safe",
		"--reason", "edit the readme",
	)
	if code != cli.ExitOK {
		t.Fatalf("expected exit 0, got %d; stderr: %s", code, errStr)
	}
}
