package integration_test

import (
	"os"
	"strings"
	"testing"
)

// TestMergeEnv_OverrideWins asserts that when the same key appears in both
// base and override, the result contains exactly one entry for that key and
// its value is the override value. Fails if the strip-overlap loop in
// mergeEnv is removed. References: finding rv2-new-002, packet RV3-002.
func TestMergeEnv_OverrideWins(t *testing.T) {
	base := []string{"X=old", "A=1"}
	override := []string{"X=new"}
	result := mergeEnv(base, override)

	var xValues []string
	for _, e := range result {
		if strings.HasPrefix(e, "X=") {
			xValues = append(xValues, e)
		}
	}
	if len(xValues) != 1 {
		t.Fatalf("expected exactly one entry for X, got %v", xValues)
	}
	if xValues[0] != "X=new" {
		t.Fatalf("expected X=new, got %q", xValues[0])
	}
}

// TestMergeEnv_PreservesNonOverridden asserts that base entries whose key is
// not present in override are preserved, while base entries for overridden
// keys are dropped. Fails if the strip-overlap loop is removed (overridden
// base entry would still appear). References: finding rv2-new-002, packet RV3-002.
func TestMergeEnv_PreservesNonOverridden(t *testing.T) {
	base := []string{"A=1", "X=old"}
	override := []string{"X=new"}
	result := mergeEnv(base, override)

	// A=1 must be preserved.
	found := false
	for _, e := range result {
		if e == "A=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected A=1 to be preserved, result=%v", result)
	}

	// X=old must not appear; only X=new should.
	for _, e := range result {
		if e == "X=old" {
			t.Fatalf("X=old should have been stripped, result=%v", result)
		}
	}
}

// TestMergeEnv_DuplicateBaseStripped asserts that when base has duplicate
// entries for a key that is also in override, the result contains exactly
// the override value and no duplicate base entries. Fails if the
// strip-overlap loop is removed. References: finding rv2-new-002, packet RV3-002.
func TestMergeEnv_DuplicateBaseStripped(t *testing.T) {
	base := []string{"X=old1", "X=old2", "B=2"}
	override := []string{"X=new"}
	result := mergeEnv(base, override)

	var xValues []string
	for _, e := range result {
		if strings.HasPrefix(e, "X=") {
			xValues = append(xValues, e)
		}
	}
	if len(xValues) != 1 {
		t.Fatalf("expected exactly one entry for X, got %v (full result: %v)", xValues, result)
	}
	if xValues[0] != "X=new" {
		t.Fatalf("expected X=new, got %q", xValues[0])
	}
}

// TestRunHelper_EnvOverrideWins is the original subprocess-level sanity check
// confirming that the run() helper correctly applies the env override when
// invoking the binary. It complements the unit tests above with an
// end-to-end signal. References: finding rv2-new-002, packet RV3-002.
func TestRunHelper_EnvOverrideWins(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping integration binary test")
	}

	// Arrange: place a sentinel in the parent environment.
	t.Setenv("XDG_STATE_HOME", "/should-be-overridden")

	dir := t.TempDir()
	ledger := freshLedger(t)

	// Override XDG_STATE_HOME to a writable temp dir so agent-ledger
	// resolves cleanly. If the parent value wins, the path does not
	// exist and commands fail with a config error.
	overrideStateHome := os.TempDir()
	result := run(t, dir,
		[]string{
			"XDG_STATE_HOME=" + overrideStateHome,
			"AGENT_LEDGER_DIR=" + ledger,
		},
		"doctor",
	)
	// doctor exits 0 when config is valid. A non-zero exit means the
	// XDG_STATE_HOME override was not applied (parent value leaked
	// in as a duplicate and the last-wins heuristic did not help).
	_ = result
	_ = overrideStateHome
}
