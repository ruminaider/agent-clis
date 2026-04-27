package version

import (
	"strings"
	"testing"
)

func TestVersionNonEmpty(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}

func TestCommitAndBuildDateHaveDefaults(t *testing.T) {
	if Commit == "" {
		t.Fatal("Commit must not be empty")
	}
	if BuildDate == "" {
		t.Fatal("BuildDate must not be empty")
	}
}

func TestStringFormat(t *testing.T) {
	got := String()
	for _, want := range []string{"agent-ledger version ", " commit ", " built ", Version, Commit, BuildDate} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}

// TestStringWithInjectedValues simulates link-time injection by mutating
// the package vars and asserting String() reflects them.
func TestStringWithInjectedValues(t *testing.T) {
	origV, origC, origD := Version, Commit, BuildDate
	t.Cleanup(func() { Version, Commit, BuildDate = origV, origC, origD })

	Version = "v1.2.3"
	Commit = "abc1234"
	BuildDate = "2026-04-27T00:00:00Z"

	want := "agent-ledger version v1.2.3 commit abc1234 built 2026-04-27T00:00:00Z"
	if got := String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
