package commands

import (
	"path/filepath"
	"strings"
)

// matchGlob returns true when path (project-relative, slash-separated)
// matches one of patterns. Patterns support **, *, ?, and absolute or
// relative forms.
func matchGlob(patterns []string, path string) bool {
	for _, p := range patterns {
		if globMatch(p, path) {
			return true
		}
	}
	return false
}

// globMatch implements the matching used for assignment allow/forbid
// lists. Supported syntax:
//
//   - "**" matches any number of path components.
//   - "*" matches a single path segment.
//   - "?" matches a single non-separator char.
//   - exact strings match exact paths.
//
// Both pattern and target are lowercased on Windows-style filesystems
// for portability; we keep case-sensitive matching here because SPEC
// §14 normalizes display paths with case preserved.
func globMatch(pattern, path string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	path = filepath.ToSlash(strings.TrimSpace(path))
	if pattern == "" {
		return false
	}
	if pattern == path {
		return true
	}
	// Convert ** patterns: split by "/" and match component-wise.
	pp := strings.Split(pattern, "/")
	tp := strings.Split(path, "/")
	return globMatchSegments(pp, tp)
}

func globMatchSegments(pp, tp []string) bool {
	for len(pp) > 0 {
		switch pp[0] {
		case "**":
			// Match zero or more segments.
			rest := pp[1:]
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(tp); i++ {
				if globMatchSegments(rest, tp[i:]) {
					return true
				}
			}
			return false
		default:
			if len(tp) == 0 {
				return false
			}
			ok, _ := filepath.Match(pp[0], tp[0])
			if !ok {
				return false
			}
			pp = pp[1:]
			tp = tp[1:]
		}
	}
	return len(tp) == 0
}
