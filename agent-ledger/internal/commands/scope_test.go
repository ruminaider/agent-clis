package commands

import "testing"

func TestMatchGlob(t *testing.T) {
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
	}
	for _, c := range cases {
		got := globMatch(c.pattern, c.path)
		if got != c.want {
			t.Errorf("globMatch(%q,%q) = %v want %v", c.pattern, c.path, got, c.want)
		}
	}
}
