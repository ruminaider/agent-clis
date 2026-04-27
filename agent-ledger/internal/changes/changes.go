// Package changes contains the patch normalization, hashing, and
// content-addressed blob storage used by `agent-ledger record`.
//
// SPEC §17 forbids storing full diff content unless the caller opts
// in with --include-diff and acknowledges with --yes. This package
// owns the small primitives that implement that contract:
//
//   - NormalizeDiff strips trailing whitespace and ensures a final
//     newline so the SHA256 of equivalent diffs collide.
//   - HashDiff returns the lowercase hex SHA256 of the normalized
//     bytes.
//   - WriteBlob stores raw patch bytes under blobs/sha256/<hash> in
//     two-character fan-out directories; it is a no-op when the blob
//     already exists.
//
// Higher-level orchestration (validating the intent's claimed paths,
// emitting events, writing change rows) lives in internal/commands
// and internal/domain. This package only handles the on-disk shape.
package changes

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NormalizeDiff returns a canonical byte form of raw suitable for
// hashing. The current normalization is conservative:
//
//   - Trailing whitespace on each line is removed.
//   - CRLF line endings are converted to LF.
//   - Exactly one trailing newline is enforced.
//
// We do not touch the diff body otherwise: hunk markers, file paths,
// and content lines are preserved so the hash binds to the actual
// edit. Empty input yields an empty result; HashDiff treats that as
// the empty-string sha256.
func NormalizeDiff(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	s := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	lines := bytes.Split(s, []byte("\n"))
	for i := range lines {
		lines[i] = bytes.TrimRight(lines[i], " \t")
	}
	out := bytes.Join(lines, []byte("\n"))
	out = bytes.TrimRight(out, "\n")
	out = append(out, '\n')
	return out
}

// HashDiff returns the lowercase hex sha256 of NormalizeDiff(raw).
// Empty input yields the empty string so callers can distinguish
// "no diff supplied" from "explicitly empty diff".
func HashDiff(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	norm := NormalizeDiff(raw)
	sum := sha256.Sum256(norm)
	return hex.EncodeToString(sum[:])
}

// HashBytes returns lowercase hex sha256 of b. Used for non-diff
// content like file before/after digests.
func HashBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// BlobPath returns the on-disk path for hash under blobsRoot. The
// layout is two-character fan-out:
//
//	<blobsRoot>/<aa>/<bb>/<full-hash>
//
// where <aa> and <bb> are the first two pairs of hash. blobsRoot is
// expected to already include the "sha256" segment so the caller can
// reuse storage.Layout.BlobsDir directly.
func BlobPath(blobsRoot, hash string) (string, error) {
	if blobsRoot == "" {
		return "", errors.New("changes: empty blobs root")
	}
	if len(hash) != 64 {
		return "", fmt.Errorf("changes: hash must be 64 hex chars, got %d", len(hash))
	}
	if !isHex(hash) {
		return "", errors.New("changes: hash is not lowercase hex")
	}
	return filepath.Join(blobsRoot, hash[0:2], hash[2:4], hash), nil
}

// WriteBlob stores raw under blobsRoot using the SHA256 fan-out path.
// It is idempotent: if the blob already exists with the same content,
// the call is a no-op. Returns the relative path "<aa>/<bb>/<hash>"
// suitable for embedding as an output_ref-style pointer.
func WriteBlob(blobsRoot string, raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("changes: refusing to write empty blob")
	}
	hash := HashBytes(raw)
	full, err := BlobPath(blobsRoot, hash)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(full); err == nil {
		return relBlobPath(blobsRoot, full), nil
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("changes: mkdir blob dir: %w", err)
	}
	tmp := full + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return "", fmt.Errorf("changes: write blob: %w", err)
	}
	if err := os.Rename(tmp, full); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("changes: rename blob: %w", err)
	}
	return relBlobPath(blobsRoot, full), nil
}

func relBlobPath(root, full string) string {
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return full
	}
	return filepath.ToSlash(rel)
}

func isHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// ParseValidation parses a CLI --validation argument of the form
// "<command>:<status>" where the status is the substring after the
// final colon. This rule lets command strings contain colons (e.g.
// "go test ./...:passed" or "uv run ruff check src:passed"). The
// returned status is lowercased and validated against the allowed
// set; an unknown status yields "unknown" by default per SPEC §11.8.
func ParseValidation(arg string) (cmd, status string, err error) {
	idx := strings.LastIndex(arg, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("validation: %q missing :status suffix", arg)
	}
	cmd = strings.TrimSpace(arg[:idx])
	status = strings.ToLower(strings.TrimSpace(arg[idx+1:]))
	if cmd == "" {
		return "", "", fmt.Errorf("validation: empty command in %q", arg)
	}
	switch status {
	case "passed", "failed", "skipped", "unknown":
		// ok
	default:
		return "", "", fmt.Errorf("validation: unknown status %q in %q (want passed|failed|skipped|unknown)", status, arg)
	}
	return cmd, status, nil
}
