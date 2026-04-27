package migrations

import "testing"

func TestAll_AtLeastOneAndOrdered(t *testing.T) {
	migs, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) == 0 {
		t.Fatal("no migrations embedded")
	}
	for i := 1; i < len(migs); i++ {
		if migs[i].Version <= migs[i-1].Version {
			t.Fatalf("not ascending at %d: %d <= %d", i, migs[i].Version, migs[i-1].Version)
		}
	}
	if migs[0].Version != 1 {
		t.Fatalf("first version = %d, want 1", migs[0].Version)
	}
}
