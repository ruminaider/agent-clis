// Package privacy holds value-level checks that complement the
// key-level denylist in internal/events. SPEC §17 forbids storing
// environment dumps, command output, headers, tokens, secrets, and
// raw hook or tool payloads. Event payload validation already rejects
// known-bad keys; this package adds heuristics for known-bad values
// so callers building free-form summary or reason strings can scrub
// them before they hit storage.
//
// IsLikelySecret returns true when value matches any of the heuristic
// patterns (AWS keys, bearer tokens, SSH keys, env-dump fragments,
// "<KEY>=<VALUE>" pairs that look like exported secrets, etc.). It is
// intentionally conservative: false positives are preferable to
// silently leaking a credential.
//
// IsForbiddenKey re-exports the events-package denylist so callers
// outside the events package (CLI flag validators, summary builders)
// can apply the same rule.
package privacy

import (
	"regexp"
	"strings"

	"github.com/ruminaider/agent-clis/agent-ledger/internal/events"
)

// IsForbiddenKey re-exports the event payload key denylist.
func IsForbiddenKey(k string) bool { return events.IsForbiddenKey(k) }

// secretPatterns are case-insensitive regexes that flag obvious
// credentials and env-dump shapes. Each pattern is anchored to common
// markers used by SDKs and shells.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bAKIA[0-9A-Z]{12,}\b`),               // AWS access key id
	regexp.MustCompile(`(?i)\bASIA[0-9A-Z]{12,}\b`),               // AWS session token id
	regexp.MustCompile(`(?i)aws_secret_access_key\s*[:=]`),        // AWS secret env line
	regexp.MustCompile(`(?i)aws_access_key_id\s*[:=]`),            // AWS key env line
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._\-]{8,}`),      // Authorization: Bearer <token>
	regexp.MustCompile(`(?i)\bBasic\s+[A-Za-z0-9+/=]{8,}`),        // Authorization: Basic <b64>
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),      // PEM private key
	regexp.MustCompile(`(?i)\bssh-rsa\s+[A-Za-z0-9+/=]{20,}`),     // SSH RSA public key (often sibling of private)
	regexp.MustCompile(`(?i)\bssh-ed25519\s+[A-Za-z0-9+/=]{20,}`), // SSH ed25519
	regexp.MustCompile(`(?i)\bgithub_pat_[A-Za-z0-9_]{20,}`),      // GitHub fine-grained PAT
	regexp.MustCompile(`(?i)\bghp_[A-Za-z0-9]{30,}`),              // GitHub classic PAT
	regexp.MustCompile(`(?i)\bglpat-[A-Za-z0-9_\-]{20,}`),         // GitLab PAT
	regexp.MustCompile(`(?i)\bxox[abprs]-[A-Za-z0-9-]{10,}`),      // Slack tokens
	regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9]{20,}`),               // OpenAI / Anthropic key shape
	regexp.MustCompile(`(?i)\b(api|access|secret|auth|client)_(key|token|secret)\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?i)\bauthorization\s*[:=]`),
	regexp.MustCompile(`(?i)\bcookie\s*[:=]`),
	regexp.MustCompile(`(?i)\bx-api-key\s*[:=]`),
}

// envDumpHints are tokens that suggest the caller pasted the output of
// `env`, `printenv`, or a debug log. We trigger on multiple matches to
// reduce false positives on harmless prose.
var envDumpHints = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^\s*PATH=`),
	regexp.MustCompile(`(?m)^\s*HOME=`),
	regexp.MustCompile(`(?m)^\s*SHELL=`),
	regexp.MustCompile(`(?m)^\s*PWD=`),
	regexp.MustCompile(`(?m)^\s*USER=`),
	regexp.MustCompile(`(?m)^\s*LANG=`),
	regexp.MustCompile(`(?m)^\s*TERM=`),
}

// IsLikelySecret reports whether value contains any of the secret
// shapes we recognize. The check is conservative: empty strings,
// short strings, and strings that look like ordinary prose return
// false. Long multi-line blobs that match two or more env-dump hints
// trigger the env-dump rule even without an explicit credential
// pattern.
func IsLikelySecret(value string) bool {
	v := strings.TrimSpace(value)
	if len(v) < 8 {
		return false
	}
	for _, re := range secretPatterns {
		if re.MatchString(v) {
			return true
		}
	}
	hits := 0
	for _, re := range envDumpHints {
		if re.MatchString(v) {
			hits++
			if hits >= 2 {
				return true
			}
		}
	}
	return false
}

// AssertSafe returns a non-nil error if any of the supplied free-form
// values matches IsLikelySecret. Useful for guarding summary, reason,
// and CLI free-text flags before they reach the database.
func AssertSafe(label string, values ...string) error {
	for _, v := range values {
		if IsLikelySecret(v) {
			return &SecretError{Label: label}
		}
	}
	return nil
}

// SecretError is returned by AssertSafe.
type SecretError struct{ Label string }

func (e *SecretError) Error() string {
	if e == nil {
		return ""
	}
	if e.Label == "" {
		return "privacy: input matches a known secret pattern"
	}
	return "privacy: " + e.Label + " matches a known secret pattern"
}
