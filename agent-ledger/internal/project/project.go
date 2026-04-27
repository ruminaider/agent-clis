// Package project derives a stable project identity from explicit overrides
// and best-effort git metadata. Two artifacts matter:
//
//   - Fingerprint: 24-char lowercase hex sha256 prefix that is stable across
//     moves of the working directory and distinct across separate projects.
//   - Slug: human-friendly, filesystem-safe label derived from project_id or
//     origin URL.
//
// See SPEC.md §8.1.
package project

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

// FingerprintLen is the number of hex chars retained from sha256.
const FingerprintLen = 24

// SlugMaxLen caps the slug to avoid pathological filesystem paths.
const SlugMaxLen = 64

// Identity is the resolved project identity.
type Identity struct {
	// ProjectID is the explicit identifier from the local pointer or
	// committed policy file. May be empty.
	ProjectID string
	// OriginURL is git's remote.origin.url, if any.
	OriginURL string
	// GitCommonDir is the realpath of the git common dir, if any.
	GitCommonDir string
	// Root is the realpath of the project root for non-git projects. It
	// is intentionally empty when GitCommonDir is set so worktrees of one
	// repo share a fingerprint.
	Root string
	// Slug is the sanitized display label.
	Slug string
	// Fingerprint is the 24-char hex digest.
	Fingerprint string
}

// Inputs are the raw values used to compute the canonical fingerprint
// string. SPEC §8.1.
type Inputs struct {
	ProjectID    string
	OriginURL    string
	GitCommonDir string
	NonGitRoot   string
}

// Compute derives the identity from inputs. It does not touch the
// filesystem; callers pass in already-resolved realpaths.
func Compute(in Inputs) Identity {
	id := Identity{
		ProjectID:    strings.TrimSpace(in.ProjectID),
		OriginURL:    strings.TrimSpace(in.OriginURL),
		GitCommonDir: strings.TrimSpace(in.GitCommonDir),
	}
	// Non-git root only contributes when there is no git common dir, so
	// worktrees of the same repo share one fingerprint.
	if id.GitCommonDir == "" {
		id.Root = strings.TrimSpace(in.NonGitRoot)
	}
	id.Fingerprint = fingerprint(id)
	id.Slug = Slug(slugSource(id))
	return id
}

// Canonical returns the canonical string used as the sha256 input.
func Canonical(id Identity) string {
	var b strings.Builder
	b.WriteString("project_id=")
	b.WriteString(id.ProjectID)
	b.WriteString("\norigin=")
	b.WriteString(id.OriginURL)
	b.WriteString("\ngit_common_dir=")
	b.WriteString(id.GitCommonDir)
	b.WriteString("\nroot=")
	b.WriteString(id.Root)
	b.WriteString("\n")
	return b.String()
}

func fingerprint(id Identity) string {
	sum := sha256.Sum256([]byte(Canonical(id)))
	return hex.EncodeToString(sum[:])[:FingerprintLen]
}

// slugSource picks the best human-readable string available.
func slugSource(id Identity) string {
	if id.ProjectID != "" {
		return id.ProjectID
	}
	if id.OriginURL != "" {
		return originSlugSource(id.OriginURL)
	}
	if id.Root != "" {
		return filepath.Base(id.Root)
	}
	if id.GitCommonDir != "" {
		// Walk up one level: ".../repo/.git" → "repo".
		parent := filepath.Dir(id.GitCommonDir)
		base := filepath.Base(parent)
		if base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	return "project"
}

// originSlugSource turns a remote URL into a candidate slug input.
// Examples:
//
//	https://github.com/foo/bar.git → foo/bar
//	git@github.com:foo/bar.git    → foo/bar
//	ssh://git@host/foo/bar         → foo/bar
func originSlugSource(url string) string {
	s := strings.TrimSuffix(url, ".git")
	// SCP-style without scheme: user@host:path → path.
	if !strings.Contains(s, "://") {
		if at := strings.Index(s, "@"); at >= 0 {
			if colon := strings.Index(s[at:], ":"); colon >= 0 {
				return s[at+colon+1:]
			}
		}
	}
	// URL form: scheme://[user@]host/path → path.
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if slash := strings.Index(s, "/"); slash >= 0 {
			return s[slash+1:]
		}
	}
	return s
}

var slugInvalid = regexp.MustCompile(`[^a-z0-9_-]+`)

// Slug sanitizes raw to lowercase [a-z0-9_-], collapses runs of dashes,
// and truncates to SlugMaxLen. Empty input yields "project".
func Slug(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "project"
	}
	s = slugInvalid.ReplaceAllString(s, "-")
	// Collapse repeats.
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-_")
	if s == "" {
		return "project"
	}
	if len(s) > SlugMaxLen {
		s = strings.TrimRight(s[:SlugMaxLen], "-_")
	}
	return s
}

// DirName returns the conventional "<slug>-<fingerprint>" directory name.
func (id Identity) DirName() string {
	return id.Slug + "-" + id.Fingerprint
}
