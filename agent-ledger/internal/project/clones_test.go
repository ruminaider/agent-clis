package project

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSeparateClones_DistinctFingerprints proves SPEC §8.1: two
// clones of the same origin URL but in different directories get
// *different* fingerprints by default. “Separate clones intentionally
// get separate local ledgers in the Product MVP.” Worktrees, which
// share a git_common_dir, are exercised in resolve_test.go.
func TestSeparateClones_DistinctFingerprints(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	upstream := filepath.Join(root, "upstream")
	a := filepath.Join(root, "clone-a")
	b := filepath.Join(root, "clone-b")
	runOrFatal(t, root, "git", "init", "--bare", upstream)

	// Seed an initial commit on the upstream by cloning, committing,
	// and pushing.
	seed := filepath.Join(root, "seed")
	runOrFatal(t, root, "git", "clone", upstream, seed)
	runOrFatal(t, seed, "git", "-c", "user.email=t@t", "-c", "user.name=t",
		"commit", "--allow-empty", "-m", "init")
	runOrFatal(t, seed, "git", "push", "origin", "HEAD:refs/heads/main")

	runOrFatal(t, root, "git", "clone", upstream, a)
	runOrFatal(t, root, "git", "clone", upstream, b)

	// Default: distinct fingerprints because git_common_dir differs.
	idA := computeForRepo(t, a, "")
	idB := computeForRepo(t, b, "")
	if idA.OriginURL == "" || idB.OriginURL == "" {
		t.Fatal("expected both clones to expose origin url")
	}
	if idA.OriginURL != idB.OriginURL {
		t.Fatalf("origin mismatch: %q vs %q", idA.OriginURL, idB.OriginURL)
	}
	if idA.Fingerprint == idB.Fingerprint {
		t.Fatalf("expected distinct fingerprints across clones, got %q", idA.Fingerprint)
	}
	// Slugs derive from origin and should be identical: both clones
	// surface the same human-readable label even though they keep
	// distinct ledgers.
	if idA.Slug != idB.Slug {
		t.Errorf("slugs should match (origin-derived): %q vs %q", idA.Slug, idB.Slug)
	}
}

// computeForRepo simulates the resolver's fingerprint inputs without
// going through the full Resolve helper. It uses git rev-parse to
// pull origin and common dir, mirroring SPEC §8.1.
func computeForRepo(t *testing.T, dir, projectID string) Identity {
	t.Helper()
	origin := gitOutput(t, dir, "config", "--get", "remote.origin.url")
	common := gitOutput(t, dir, "rev-parse", "--git-common-dir")
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	abs, err := filepath.EvalSymlinks(common)
	if err == nil {
		common = abs
	}
	return Compute(Inputs{
		ProjectID:    projectID,
		OriginURL:    origin,
		GitCommonDir: common,
	})
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	// Trim trailing newline.
	s := string(out)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
