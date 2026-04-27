package integration_test

import (
	"encoding/json"
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

func TestRunHelper_EnvOverrideWins(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping integration binary test")
	}

	parentXDG := t.TempDir()
	overrideXDG := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", parentXDG)

	r := run(t, projectDir, []string{"XDG_STATE_HOME=" + overrideXDG}, "doctor", "--json")
	if r.Code != 0 {
		t.Fatalf("doctor --json exited %d\nstdout=%s\nstderr=%s", r.Code, r.Stdout, r.Stderr)
	}

	var rep struct {
		Checks []struct {
			Name    string         `json:"name"`
			Status  string         `json:"status"`
			Details map[string]any `json:"details"`
		}
	}
	if err := json.Unmarshal([]byte(r.Stdout), &rep); err != nil {
		t.Fatalf("parse doctor json: %v\nstdout=%s\nstderr=%s", err, r.Stdout, r.Stderr)
	}

	var ledgerPath string
	for _, check := range rep.Checks {
		if check.Name == "ledger_dir" {
			if v, ok := check.Details["path"].(string); ok {
				ledgerPath = v
			}
		}
	}
	if ledgerPath == "" {
		t.Fatalf("ledger path not found in doctor json\nstdout=%s\nstderr=%s", r.Stdout, r.Stderr)
	}
	if !strings.HasPrefix(ledgerPath, overrideXDG) {
		t.Fatalf("expected ledger path %q to be rooted under override %q", ledgerPath, overrideXDG)
	}
	if strings.HasPrefix(ledgerPath, parentXDG) {
		t.Fatalf("expected ledger path %q not to be rooted under parent %q", ledgerPath, parentXDG)
	}
}
