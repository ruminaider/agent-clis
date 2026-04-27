// Package paths normalizes filesystem paths for storage and comparison.
//
// SPEC §14 rules:
//
//  1. Resolve project root.
//  2. Convert input path to absolute path.
//  3. Resolve symlinks where possible.
//  4. Convert to project-root-relative display path.
//  5. Normalize Unicode to NFC.
//  6. Normalize separators to "/".
//  7. Preserve case in display path.
//  8. Store path_hash = sha256(realpath-normalized).
//
// Display paths preserve case so humans can recognize them; the hash is
// derived from the realpath form so equality comparisons are robust.
package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Normalized is the result of Normalize. The zero value is invalid.
type Normalized struct {
	// Display is the project-relative path with forward slashes and
	// case preserved.
	Display string
	// RealPath is the absolute, symlink-resolved path in NFC form.
	// Used for the hash; not intended for display.
	RealPath string
	// PathHash is sha256 hex of RealPath.
	PathHash string
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
		Display:  display,
		RealPath: realNFC,
		PathHash: hex.EncodeToString(sum[:]),
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

// Hash returns sha256 hex of the NFC-normalized realpath of p.
func Hash(p string) string {
	abs, _ := filepath.Abs(p)
	real := evalSymlinksBestEffort(abs)
	sum := sha256.Sum256([]byte(norm.NFC.String(real)))
	return hex.EncodeToString(sum[:])
}

// PortableHash returns sha256 hex of the NFC-normalized project-relative path
// with forward slashes. Unlike [Hash], this value is stable across machines
// because it depends only on the relative path, not on the absolute realpath.
// Summaries must use this form so that verify --summary works in any checkout.
func PortableHash(display string) string {
	sum := sha256.Sum256([]byte(norm.NFC.String(display)))
	return hex.EncodeToString(sum[:])
}
