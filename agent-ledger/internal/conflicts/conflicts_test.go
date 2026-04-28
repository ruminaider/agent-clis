package conflicts

import (
	"testing"
)

func TestResolveAllowsWhenNoOverlap(t *testing.T) {
	d, _ := Resolve("exclusive", nil, false, nil)
	if d != Allow {
		t.Fatalf("got %v want Allow", d)
	}
}

func TestResolveBlocksExclusive(t *testing.T) {
	d, f := Resolve("exclusive", []Overlap{{ExistingIntent: "int_a"}}, false, nil)
	if d != Block || len(f) != 1 {
		t.Fatalf("expected Block with 1 overlap, got %v len=%d", d, len(f))
	}
}

func TestResolveOverrideExclusive(t *testing.T) {
	d, f := Resolve("exclusive", []Overlap{{ExistingIntent: "int_a"}}, true, nil)
	if d != Override || len(f) != 1 {
		t.Fatalf("expected Override, got %v len=%d", d, len(f))
	}
}

func TestResolveWarn(t *testing.T) {
	d, f := Resolve("warn", []Overlap{{ExistingIntent: "int_a"}}, false, nil)
	if d != Warn || len(f) != 1 {
		t.Fatalf("expected Warn, got %v", d)
	}
}

func TestResolveSupersedeFiltersOverlap(t *testing.T) {
	d, _ := Resolve("exclusive", []Overlap{{ExistingIntent: "int_old"}}, false, map[string]bool{"int_old": true})
	if d != Allow {
		t.Fatalf("expected Allow after superseding, got %v", d)
	}
}

func TestResolveNonePolicyAllowsOverlap(t *testing.T) {
	d, _ := Resolve("none", []Overlap{{ExistingIntent: "int_a"}}, false, nil)
	if d != Allow {
		t.Fatalf("expected Allow for none policy, got %v", d)
	}
}
