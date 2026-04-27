// Package policy holds project-policy helpers that are shared across
// commands without depending on storage. The first helpers cover scope
// matching for assignment allow/forbid lists.
//
// Pattern syntax (SPEC §11.3 and §15):
//
//   - "**" matches any number of path components.
//   - "*" matches a single path segment.
//   - "?" matches a single non-separator character.
//   - exact strings match exact paths.
//
// All matching is case-sensitive: SPEC §14 normalizes display paths
// with case preserved. Both pattern and target are slash-normalized
// before comparison.
package policy

import (
	"path/filepath"
	"strings"
)

// MatchAny reports whether path matches any pattern.
func MatchAny(patterns []string, path string) bool {
	for _, p := range patterns {
		if Match(p, path) {
			return true
		}
	}
	return false
}

// Match reports whether pattern matches path.
func Match(pattern, path string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	path = filepath.ToSlash(strings.TrimSpace(path))
	if pattern == "" {
		return false
	}
	if pattern == path {
		return true
	}
	pp := strings.Split(pattern, "/")
	tp := strings.Split(path, "/")
	return matchSegments(pp, tp)
}

func matchSegments(pp, tp []string) bool {
	for len(pp) > 0 {
		if pp[0] == "**" {
			rest := pp[1:]
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(tp); i++ {
				if matchSegments(rest, tp[i:]) {
					return true
				}
			}
			return false
		}
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
	return len(tp) == 0
}
