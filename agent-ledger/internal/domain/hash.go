package domain

import (
	"crypto/sha256"
	"encoding/hex"
)

// sha256Hex returns the lowercase hex sha256 of s, or empty string when s
// is empty.
func sha256Hex(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
