// Package paths normalizes filesystem paths for storage and comparison.
//
// SPEC §14 rules:
//
//  1. Resolve project scope. For git repos, scope is every worktree
//     toplevel sharing the same common dir; longest realpath prefix wins.
//  2. Convert input path to absolute path.
//  3. Resolve symlinks where possible.
//  4. Convert to display path relative to the matched scope root.
//  5. Normalize Unicode to NFC.
//  6. Normalize separators to "/".
//  7. Preserve case in Display and RealPath; CanonicalHash folds case.
//  8. CanonicalHash = sha256(NFC(case-fold(display))). This is the
//     equality key used by conflict detection, lock sentinel naming,
//     and cross-worktree lookups.
//  9. PathHash = sha256(NFC(realpath)) is preserved as a per-checkout
//     forensic artifact and is not used as an equality key.
//
// Folding the canonical hash matches the case-insensitive behavior
// agents observe on macOS APFS and Windows NTFS. Realpath-derived
// PathHash collapsed intra-project symlink aliases for free; with the
// canonical hash that no longer happens, so the verifier emits a
// SYMLINK_ALIAS finding when active rows share a realpath but not a
// canonical hash.
package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// caseFolder is the Unicode case-folding caser used for canonical hash
// derivation. cases.Fold is locale-independent and stable across
// platforms, so two distinct binaries on macOS and Linux produce the
// same hash for the same display string.
var caseFolder = cases.Fold()

// Normalized is the result of Normalize. The zero value is invalid.
type Normalized struct {
	// Display is the project-relative path with forward slashes and
	// case preserved.
	Display string
	// RealPath is the absolute, symlink-resolved path in NFC form.
	// Stored alongside Display for forensics; no longer used as the
	// equality key.
	RealPath string
	// PathHash is sha256 hex of RealPath. Preserved as a per-checkout
	// forensic artifact for migration and cross-checks; do not use it
	// as an equality key.
	PathHash string
	// CanonicalHash is sha256 hex of NFC(case-fold(Display)). This is
	// the equality key for conflict detection, lock sentinels, and
	// cross-worktree lookups. SPEC §14 #8.
	CanonicalHash string
}

// OutsideProjectError is returned when a path resolves outside the project
// root after symlink resolution. Callers map this to scope violations.
type OutsideProjectError struct {
	Root string
	Path string
}

func (e *OutsideProjectError) Error() string {
	return fmt.Sprintf("paths: %q resolves outside project root %q", e.Path, e.Root)
}

// IsOutsideProject reports whether err is an OutsideProjectError.
func IsOutsideProject(err error) bool {
	var x *OutsideProjectError
	return errors.As(err, &x)
}

// Normalize applies SPEC §14 to input given the project root. Both root
// and input may be relative; root is converted to its realpath first.
//
// input that does not exist is still normalized: the symlink-resolution
// step is best-effort and falls back to lexical normalization.
func Normalize(root, input string) (Normalized, error) {
	if root == "" {
		return Normalized{}, errors.New("paths: empty root")
	}
	if input == "" {
		return Normalized{}, errors.New("paths: empty path")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return Normalized{}, err
	}
	rootReal := evalSymlinksBestEffort(rootAbs)

	abs := input
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(rootReal, abs)
	}
	abs, err = filepath.Abs(abs)
	if err != nil {
		return Normalized{}, err
	}

	real := evalSymlinksBestEffort(abs)

	// NFC-normalize the realpath before hashing.
	realNFC := norm.NFC.String(real)

	// Build display path relative to the realpath root, preserving case.
	rel, err := filepath.Rel(rootReal, real)
	if err != nil {
		return Normalized{}, fmt.Errorf("paths: rel: %w", err)
	}
	if rel == "." {
		rel = ""
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Normalized{}, &OutsideProjectError{Root: rootReal, Path: real}
	}
	display := filepath.ToSlash(norm.NFC.String(rel))

	sum := sha256.Sum256([]byte(realNFC))
	return Normalized{
		Display:       display,
		RealPath:      realNFC,
		PathHash:      hex.EncodeToString(sum[:]),
		CanonicalHash: CanonicalHash(display),
	}, nil
}

// evalSymlinksBestEffort resolves p; if any component is missing or the OS
// rejects the call, it climbs ancestors until resolution succeeds and
// re-attaches the unresolved tail. The returned path is always absolute
// when given an absolute input.
func evalSymlinksBestEffort(p string) string {
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		return rp
	}
	// Walk up until something resolves, then re-attach trailing parts.
	parent := p
	tail := ""
	for {
		next := filepath.Dir(parent)
		if next == parent {
			return p
		}
		base := filepath.Base(parent)
		if tail == "" {
			tail = base
		} else {
			tail = filepath.Join(base, tail)
		}
		parent = next
		if rp, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(rp, tail)
		}
	}
}

// NormalizeAt is a multi-root variant of Normalize. It accepts a list of
// candidate project roots (typically the toplevels of every git worktree
// sharing a common dir) and picks the one that is the longest realpath
// prefix of the resolved input path. The display path is computed
// relative to that chosen root.
//
// Roots are tried in order; ties are broken by longest match length, then
// by appearance order. An input that escapes every root returns
// OutsideProjectError keyed to the first root, matching the historical
// single-root error shape.
//
// Empty roots are silently skipped. An empty roots slice yields an error.
func NormalizeAt(roots []string, input string) (Normalized, error) {
	if len(roots) == 0 {
		return Normalized{}, errors.New("paths: no roots")
	}
	if input == "" {
		return Normalized{}, errors.New("paths: empty path")
	}

	// Resolve each root to a realpath up front so prefix matching is
	// stable. Skip empties; preserve order for tie breaks.
	type rootEntry struct {
		display string // realpath form used for filepath.Rel and length compare
	}
	rrs := make([]rootEntry, 0, len(roots))
	var firstReal string
	for _, r := range roots {
		if strings.TrimSpace(r) == "" {
			continue
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		real := evalSymlinksBestEffort(abs)
		if firstReal == "" {
			firstReal = real
		}
		rrs = append(rrs, rootEntry{display: real})
	}
	if len(rrs) == 0 {
		return Normalized{}, errors.New("paths: no usable roots")
	}

	// Resolve the input. Use the first root as the base for relative
	// inputs; this preserves the historical "join with cwd-derived root"
	// behavior.
	abs := input
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(firstReal, abs)
	}
	abs, err := filepath.Abs(abs)
	if err != nil {
		return Normalized{}, err
	}
	real := evalSymlinksBestEffort(abs)
	realNFC := norm.NFC.String(real)

	// Pick the longest matching root prefix. A root matches when real is
	// equal to it or starts with root + separator.
	sep := string(filepath.Separator)
	bestRoot := ""
	bestLen := -1
	for _, e := range rrs {
		switch {
		case real == e.display:
			if len(e.display) > bestLen {
				bestRoot = e.display
				bestLen = len(e.display)
			}
		case strings.HasPrefix(real, e.display+sep):
			if len(e.display) > bestLen {
				bestRoot = e.display
				bestLen = len(e.display)
			}
		}
	}
	if bestRoot == "" {
		return Normalized{}, &OutsideProjectError{Root: firstReal, Path: real}
	}

	rel, err := filepath.Rel(bestRoot, real)
	if err != nil {
		return Normalized{}, fmt.Errorf("paths: rel: %w", err)
	}
	if rel == "." {
		rel = ""
	}
	display := filepath.ToSlash(norm.NFC.String(rel))

	sum := sha256.Sum256([]byte(realNFC))
	return Normalized{
		Display:       display,
		RealPath:      realNFC,
		PathHash:      hex.EncodeToString(sum[:]),
		CanonicalHash: CanonicalHash(display),
	}, nil
}

// CanonicalHash returns sha256 hex of the case-folded NFC display path.
// Callers must pass a display path with forward slashes; it is the
// caller's responsibility to apply filepath.ToSlash for platform-native
// separators (the same contract as PortableHash).
//
// Case folding uses Unicode-aware case folding via golang.org/x/text/cases
// so non-ASCII filenames hash consistently across locales and platforms.
// SPEC §14 #8.
func CanonicalHash(display string) string {
	folded := caseFolder.String(norm.NFC.String(display))
	sum := sha256.Sum256([]byte(folded))
	return hex.EncodeToString(sum[:])
}

// Hash returns sha256 hex of the NFC-normalized realpath of p.
func Hash(p string) string {
	abs, _ := filepath.Abs(p)
	real := evalSymlinksBestEffort(abs)
	sum := sha256.Sum256([]byte(norm.NFC.String(real)))
	return hex.EncodeToString(sum[:])
}

// PortableHash returns sha256 hex of the NFC-normalized project-relative path.
// Unlike [Hash], this value is stable across machines because it depends only
// on the relative path, not on the absolute realpath. Summaries must use this
// form so that verify --summary works in any checkout. SPEC §32.
//
// Callers are responsible for converting platform-native separators to forward
// slashes before calling PortableHash (use filepath.ToSlash at the call site).
// On POSIX, backslash is a valid filename character, so folding it here would
// alias two distinct paths to the same hash and silently corrupt summary
// keying. The conversion belongs at the call site, not inside the hash.
func PortableHash(display string) string {
	s := norm.NFC.String(display)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
