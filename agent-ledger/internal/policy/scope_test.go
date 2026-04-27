package policy

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"README.md", "README.md", true},
		{"src/*.go", "src/foo.go", true},
		{"src/*.go", "src/sub/foo.go", false},
		{"src/**", "src/sub/foo.go", true},
		{"**/*.md", "a/b/c.md", true},
		{"tests/**", "tests/unit/x.go", true},
		{"tests/**", "src/tests/x.go", false},
		{"", "anything", false},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q,%q) = %v want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestMatchAny(t *testing.T) {
	if !MatchAny([]string{"src/*.go", "tests/**"}, "tests/a.go") {
		t.Fatal("expected match")
	}
	if MatchAny([]string{"src/*.go"}, "README.md") {
		t.Fatal("unexpected match")
	}
}
