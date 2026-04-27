package id

import (
	"regexp"
	"testing"
	"time"
)

func TestNew_Shape(t *testing.T) {
	g := NewGenerator(nil, nil)
	for _, p := range AllowedPrefixes {
		got, err := g.New(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if !regexp.MustCompile(`^` + p + `_[0-9A-HJKMNP-TV-Z]{26}$`).MatchString(got) {
			t.Errorf("bad shape for %s: %s", p, got)
		}
		if err := Validate(got); err != nil {
			t.Errorf("Validate %s: %v", got, err)
		}
	}
}

func TestNew_RejectsUnknownPrefix(t *testing.T) {
	g := NewGenerator(nil, nil)
	if _, err := g.New("bogus"); err == nil {
		t.Fatal("expected error for unknown prefix")
	}
}

func TestNew_TimeOrdered(t *testing.T) {
	now := time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { now = now.Add(time.Millisecond); return now }
	g := NewGenerator(clock, nil)
	prev := ""
	for i := 0; i < 10; i++ {
		cur, err := g.New(PrefixEvent)
		if err != nil {
			t.Fatal(err)
		}
		if cur <= prev {
			t.Fatalf("not monotonic: %s <= %s", cur, prev)
		}
		prev = cur
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		s  string
		ok bool
	}{
		{"evt_01ARZ3NDEKTSV4RRFFQ69G5FAV", true},
		{"agt_01ARZ3NDEKTSV4RRFFQ69G5FAV", true},
		{"foo_01ARZ3NDEKTSV4RRFFQ69G5FAV", false},
		{"evt_01ARZ3NDEKTSV4RRFFQ69G5FA", false},
		{"01ARZ3NDEKTSV4RRFFQ69G5FAV", false},
		{"", false},
	}
	for _, c := range cases {
		err := Validate(c.s)
		if c.ok && err != nil {
			t.Errorf("%q: expected ok, got %v", c.s, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%q: expected error", c.s)
		}
	}
}

func TestFormatTimestamp_UTCZ(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	got := FormatTimestamp(time.Date(2026, 4, 27, 9, 30, 0, 0, loc))
	if got[len(got)-1] != 'Z' {
		t.Fatalf("want trailing Z, got %s", got)
	}
}
