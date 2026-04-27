// Package id generates the typed `<prefix>_<ulid>` identifiers used
// throughout agent-ledger. Each ID combines a short type prefix with a
// ULID body so IDs sort lexicographically by creation time and remain
// monotonic within a single process.
//
// Prefixes match SPEC §12: `evt_`, `asg_`, `int_`, `chg_`, `cfl_`. The
// spec leaves agent and validation prefixes unspecified; we use `agt_`
// and `vld_` for symmetry. Task IDs are user-supplied (e.g. `W2-A`) and
// are not minted here.
package id

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Type prefixes. Use these with New for typed IDs.
const (
	PrefixEvent      = "evt"
	PrefixAgent      = "agt"
	PrefixAssignment = "asg"
	PrefixIntent     = "int"
	PrefixChange     = "chg"
	PrefixConflict   = "cfl"
	PrefixValidation = "vld"
)

// AllowedPrefixes lists the prefixes minted by this package. Validation
// helpers consult this set.
var AllowedPrefixes = []string{
	PrefixEvent,
	PrefixAgent,
	PrefixAssignment,
	PrefixIntent,
	PrefixChange,
	PrefixConflict,
	PrefixValidation,
}

// idPattern matches `<prefix>_<26-char Crockford base32 ULID>`.
var idPattern = regexp.MustCompile(`^[a-z]{2,8}_[0-9A-HJKMNP-TV-Z]{26}$`)

// Generator mints new IDs. It is safe for concurrent use. Tests may
// inject a deterministic clock and entropy source via NewGenerator.
type Generator struct {
	now func() time.Time

	mu      sync.Mutex
	entropy io.Reader
}

// NewGenerator returns a Generator using the supplied clock and entropy
// reader. nil arguments fall back to time.Now and a thread-safe
// monotonic ULID source seeded from crypto/rand.
func NewGenerator(now func() time.Time, entropy io.Reader) *Generator {
	g := &Generator{now: now, entropy: entropy}
	if g.now == nil {
		g.now = time.Now
	}
	if g.entropy == nil {
		g.entropy = ulid.Monotonic(rand.Reader, 0)
	}
	return g
}

// DefaultGenerator returns a process-global generator. The entropy
// source is monotonic per-process so two IDs minted at the same
// millisecond still order deterministically.
func DefaultGenerator() *Generator { return defaultGen }

var defaultGen = NewGenerator(nil, nil)

// New returns a new typed ID `<prefix>_<ulid>`. Prefix must be in
// AllowedPrefixes. Returns an error if prefix is unknown or entropy is
// exhausted.
func (g *Generator) New(prefix string) (string, error) {
	if !isAllowedPrefix(prefix) {
		return "", fmt.Errorf("id: prefix %q not allowed", prefix)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.now().UTC()
	u, err := ulid.New(ulid.Timestamp(t), g.entropy)
	if err != nil {
		return "", fmt.Errorf("id: ulid: %w", err)
	}
	return prefix + "_" + u.String(), nil
}

// MustNew is like New but panics on error. Use only when prefix is a
// compile-time constant from this package.
func (g *Generator) MustNew(prefix string) string {
	s, err := g.New(prefix)
	if err != nil {
		panic(err)
	}
	return s
}

// New is a convenience wrapper around DefaultGenerator.New.
func New(prefix string) (string, error) { return defaultGen.New(prefix) }

// MustNew is a convenience wrapper around DefaultGenerator.MustNew.
func MustNew(prefix string) string { return defaultGen.MustNew(prefix) }

// Validate returns nil if s matches the typed ID shape and uses one of
// the AllowedPrefixes.
func Validate(s string) error {
	if !idPattern.MatchString(s) {
		return errors.New("id: malformed (want <prefix>_<ulid>)")
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '_' {
			if !isAllowedPrefix(s[:i]) {
				return fmt.Errorf("id: prefix %q not allowed", s[:i])
			}
			return nil
		}
	}
	return errors.New("id: missing prefix separator")
}

func isAllowedPrefix(p string) bool {
	for _, allowed := range AllowedPrefixes {
		if p == allowed {
			return true
		}
	}
	return false
}

// FormatTimestamp renders t as RFC 3339 UTC with `Z` suffix as required
// by SPEC §11. We always upconvert to UTC.
func FormatTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}
