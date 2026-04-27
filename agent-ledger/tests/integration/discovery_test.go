package integration_test

// Tests for SPEC §31.2 #6 and #7: worktree pointer discovery and
// separate-clone fingerprint divergence. Both drive the compiled binary
// as a subprocess, satisfying the dp-007 subprocess gate.
//
// Finding wv1-f06 (missing-subprocess-coverage-31.2-6-7), packet R-005.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitCmd runs a git command in dir and fatally fails the test on error.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// parseLedgerInitOutput extracts the ledger path and fingerprint from
// the two lines agent-ledger init prints:
//
//	ledger initialized at <path>
//	project_id=<id> slug=<slug> fingerprint=<fp>
func parseLedgerInitOutput(t *testing.T, stdout string) (ledgerDir, fingerprint string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "ledger initialized at ") {
			ledgerDir = strings.TrimPrefix(line, "ledger initialized at ")
			ledgerDir = strings.TrimSpace(ledgerDir)
		}
		if strings.Contains(line, "fingerprint=") {
			for _, part := range strings.Fields(line) {
				if strings.HasPrefix(part, "fingerprint=") {
					fingerprint = strings.TrimPrefix(part, "fingerprint=")
				}
			}
		}
	}
	if ledgerDir == "" {
		t.Fatalf("could not parse ledger path from init output:\n%s", stdout)
	}
	if fingerprint == "" {
		t.Fatalf("could not parse fingerprint from init output:\n%s", stdout)
	}
	return
}

// TestWorktreePointerDiscovery covers SPEC §31.2 #6: a git worktree
// checkout shares the same git common dir as the primary checkout, so
// agent-ledger init from both should resolve to the same ledger
// directory and fingerprint.
func TestWorktreePointerDiscovery(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess worktree discovery test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	worktreeDir := filepath.Join(root, "extra")
	xdgState := filepath.Join(root, "xdg")

	// Initialise the primary repo and make an initial commit so that
	// git worktree add has a HEAD to check out.
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, primary, "init", primary)
	gitCmd(t, primary, "-C", primary, "config", "user.email", "test@test.invalid")
	gitCmd(t, primary, "-C", primary, "config", "user.name", "Test")
	writeFile(t, filepath.Join(primary, "README.md"), "# test\n")
	gitCmd(t, primary, "-C", primary, "add", ".")
	gitCmd(t, primary, "-C", primary, "commit", "-m", "init")

	// Add a linked worktree at a sibling directory.
	gitCmd(t, primary, "-C", primary, "worktree", "add", worktreeDir)

	// Run agent-ledger init from the primary checkout.
	sharedEnv := []string{"XDG_STATE_HOME=" + xdgState}
	rPrimary := run(t, primary, sharedEnv, "init")
	mustZero(t, "init primary", rPrimary)

	// Run agent-ledger init from the linked worktree checkout.
	rWorktree := run(t, worktreeDir, sharedEnv, "init")
	mustZero(t, "init worktree", rWorktree)

	primaryLedger, primaryFP := parseLedgerInitOutput(t, rPrimary.Stdout)
	worktreeLedger, worktreeFP := parseLedgerInitOutput(t, rWorktree.Stdout)

	// Both checkouts share the same git common dir, so fingerprints
	// and resolved ledger dirs must be identical (SPEC §8.1).
	if primaryFP != worktreeFP {
		t.Errorf("fingerprint mismatch: primary=%q worktree=%q\nprimary stdout:\n%s\nworktree stdout:\n%s",
			primaryFP, worktreeFP, rPrimary.Stdout, rWorktree.Stdout)
	}
	if primaryLedger != worktreeLedger {
		t.Errorf("ledger dir mismatch: primary=%q worktree=%q", primaryLedger, worktreeLedger)
	}
}

// TestSeparateCloneFingerprints covers SPEC §31.2 #7: two independent
// clones of the same upstream have different git common dirs, so they
// must produce distinct fingerprints and distinct ledger paths.
func TestSeparateCloneFingerprints(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess separate-clone fingerprint test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	upstream := filepath.Join(root, "upstream")
	seed := filepath.Join(root, "seed")
	cloneA := filepath.Join(root, "clone-a")
	cloneB := filepath.Join(root, "clone-b")
	xdgState := filepath.Join(root, "xdg")

	// Create a bare upstream repository.
	gitCmd(t, root, "init", "--bare", upstream)

	// Seed an initial commit via a temporary clone so the upstream has
	// at least one ref for subsequent clones.
	gitCmd(t, root, "clone", upstream, seed)
	gitCmd(t, seed, "-C", seed, "config", "user.email", "seed@test.invalid")
	gitCmd(t, seed, "-C", seed, "config", "user.name", "Seed")
	writeFile(t, filepath.Join(seed, "README.md"), "# seed\n")
	gitCmd(t, seed, "-C", seed, "add", ".")
	gitCmd(t, seed, "-C", seed, "commit", "-m", "init")
	gitCmd(t, seed, "-C", seed, "push", "origin", "HEAD:refs/heads/main")

	// Clone the upstream twice into separate directories.
	gitCmd(t, root, "clone", upstream, cloneA)
	gitCmd(t, root, "clone", upstream, cloneB)

	// Set per-clone git identity (needed if any later commands commit).
	gitCmd(t, cloneA, "-C", cloneA, "config", "user.email", "a@test.invalid")
	gitCmd(t, cloneA, "-C", cloneA, "config", "user.name", "CloneA")
	gitCmd(t, cloneB, "-C", cloneB, "config", "user.email", "b@test.invalid")
	gitCmd(t, cloneB, "-C", cloneB, "config", "user.name", "CloneB")

	sharedEnv := []string{"XDG_STATE_HOME=" + xdgState}

	rA := run(t, cloneA, sharedEnv, "init")
	mustZero(t, "init clone-a", rA)

	rB := run(t, cloneB, sharedEnv, "init")
	mustZero(t, "init clone-b", rB)

	ledgerA, fpA := parseLedgerInitOutput(t, rA.Stdout)
	ledgerB, fpB := parseLedgerInitOutput(t, rB.Stdout)

	// Separate clones diverge on git_common_dir; fingerprints must differ
	// (SPEC §8.1: "separate clones intentionally get separate local ledgers").
	if fpA == fpB {
		t.Errorf("expected distinct fingerprints for separate clones, both got %q\nclone-a stdout:\n%s\nclone-b stdout:\n%s",
			fpA, rA.Stdout, rB.Stdout)
	}
	if ledgerA == ledgerB {
		t.Errorf("expected distinct ledger dirs for separate clones, both got %q", ledgerA)
	}

	// Slugs derive from origin URL, which is identical for both clones.
	// Verify by checking that ledger directory basenames share a slug
	// prefix (both dirs live under the same XDG root with the same slug).
	baseA := filepath.Base(ledgerA)
	baseB := filepath.Base(ledgerB)
	// Each basename is "<slug>-<fingerprint>". Strip the fingerprint suffix.
	slugA := slugFromDirName(baseA)
	slugB := slugFromDirName(baseB)
	if slugA != slugB {
		t.Errorf("expected same slug for clones of the same remote, got %q and %q", slugA, slugB)
	}
}

// slugFromDirName extracts the slug prefix from a "<slug>-<fingerprint>"
// directory name. The fingerprint is the last hyphen-separated field of
// exactly FingerprintLen (24) lowercase hex characters.
func slugFromDirName(name string) string {
	const fpLen = 24
	if len(name) <= fpLen+1 {
		return name
	}
	// Fingerprint is the last segment after the final '-'.
	idx := strings.LastIndex(name, "-")
	if idx < 0 {
		return name
	}
	candidate := name[idx+1:]
	if len(candidate) == fpLen && isHex(candidate) {
		return name[:idx]
	}
	return name
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
