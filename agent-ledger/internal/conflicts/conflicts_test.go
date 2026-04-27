package conflicts

import (
	"testing"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/domain"
)

func TestResolveAllowsWhenNoOverlap(t *testing.T) {
	d, _ := Resolve(domain.PolicyExclusive, nil, false, nil)
	if d != Allow {
		t.Fatalf("got %v want Allow", d)
	}
}

func TestResolveBlocksExclusive(t *testing.T) {
	d, f := Resolve(domain.PolicyExclusive, []Overlap{{ExistingIntent: "int_a"}}, false, nil)
	if d != Block || len(f) != 1 {
		t.Fatalf("expected Block with 1 overlap, got %v len=%d", d, len(f))
	}
}

func TestResolveOverrideExclusive(t *testing.T) {
	d, f := Resolve(domain.PolicyExclusive, []Overlap{{ExistingIntent: "int_a"}}, true, nil)
	if d != Override || len(f) != 1 {
		t.Fatalf("expected Override, got %v len=%d", d, len(f))
	}
}

func TestResolveWarn(t *testing.T) {
	d, f := Resolve(domain.PolicyWarn, []Overlap{{ExistingIntent: "int_a"}}, false, nil)
	if d != Warn || len(f) != 1 {
		t.Fatalf("expected Warn, got %v", d)
	}
}

func TestResolveSupersedeFiltersOverlap(t *testing.T) {
	d, _ := Resolve(domain.PolicyExclusive, []Overlap{{ExistingIntent: "int_old"}}, false, map[string]bool{"int_old": true})
	if d != Allow {
		t.Fatalf("expected Allow after superseding, got %v", d)
	}
}

func TestResolveNonePolicyAllowsOverlap(t *testing.T) {
	d, _ := Resolve(domain.PolicyNone, []Overlap{{ExistingIntent: "int_a"}}, false, nil)
	if d != Allow {
		t.Fatalf("expected Allow for none policy, got %v", d)
	}
}
