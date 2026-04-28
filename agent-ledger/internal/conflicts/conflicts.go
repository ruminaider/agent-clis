// Package conflicts provides the SPEC §15/§16 policy decisions for
// claim overlap detection.
//
// The package is intentionally pure: it consumes a list of incoming
// path hashes and a list of currently-active overlapping intent paths,
// then returns the policy decision (allow, warn, block) plus the
// individual overlaps the caller should record as conflict rows.
package conflicts

// Policy values are duplicated here to keep this package pure and
// avoid a dependency cycle with internal/domain.
const (
	policyNone      = "none"
	policyWarn      = "warn"
	policyExclusive = "exclusive"
)

// Decision describes how a claim should proceed.
type Decision int

const (
	// Allow: no overlapping active intents detected. Open the intent.
	Allow Decision = iota
	// Warn: overlapping intents exist under warn policy. Open the
	// intent but record a conflict row per overlap.
	Warn
	// Block: overlapping intents exist under exclusive policy. Do not
	// open the intent.
	Block
	// Override: overlapping intents exist under exclusive policy but
	// the caller supplied an acknowledged --override-conflict.
	Override
)

// Overlap pairs an incoming claim path with the existing active intent
// path that conflicts with it.
type Overlap struct {
	NewPath        string
	NewPathHash    string
	ExistingIntent string
	ExistingPath   string
}

// Resolve computes a decision given the requested policy, the list of
// existing active overlaps, and an optional override (acknowledged
// conflict id consumed by claim --override-conflict).
//
// The supersedeIntents set lists intent IDs that the caller is
// superseding. Overlaps with those intents are filtered out of the
// decision because the caller is replacing them.
func Resolve(policy string, overlaps []Overlap, hasOverride bool, supersedeIntents map[string]bool) (Decision, []Overlap) {
	// Filter overlaps that belong to intents the caller is superseding.
	filtered := make([]Overlap, 0, len(overlaps))
	for _, o := range overlaps {
		if supersedeIntents[o.ExistingIntent] {
			continue
		}
		filtered = append(filtered, o)
	}
	if len(filtered) == 0 {
		return Allow, nil
	}
	switch policy {
	case policyExclusive:
		if hasOverride {
			return Override, filtered
		}
		return Block, filtered
	case policyNone:
		return Allow, nil
	default:
		// warn (default)
		return Warn, filtered
	}
}
