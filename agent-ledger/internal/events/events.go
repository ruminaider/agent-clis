// Package events validates the privacy posture of event payloads
// before they reach storage. SPEC §17 forbids storing raw hook inputs,
// raw tool payloads, command output, environment variables, file
// contents, full diffs, headers, tokens, or secrets.
//
// The denylist is enforced at write time. Callers should still build
// payloads with typed structures: ValidatePayload is a defense-in-depth
// check, not a substitute for thoughtful payload design.
package events

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Schema is the wire schema name embedded in every event row.
const Schema = "agent-ledger.v1"

// MaxPayloadBytes caps the serialized payload at a size that keeps the
// SQLite row reasonable and forces large data into the blobs/ store
// with a payload_ref. The number is generous; the privacy denylist
// catches the actual leaks.
const MaxPayloadBytes = 16 * 1024

// forbiddenKeys are JSON keys (case-insensitive, exact match) that may
// never appear anywhere in an event payload. SPEC §17 lists the
// categories; the canonical key names below cover the typical shapes a
// careless caller would emit.
var forbiddenKeys = map[string]struct{}{
	"diff":             {},
	"patch":            {},
	"full_diff":        {},
	"contents":         {},
	"file_contents":    {},
	"content":          {},
	"body":             {},
	"raw":              {},
	"raw_input":        {},
	"raw_payload":      {},
	"raw_tool_input":   {},
	"raw_tool_output":  {},
	"tool_input":       {},
	"tool_output":      {},
	"hook_input":       {},
	"hook_payload":     {},
	"command_output":   {},
	"stdout":           {},
	"stderr":           {},
	"output":           {},
	"env":              {},
	"environment":      {},
	"env_vars":         {},
	"environment_vars": {},
	"headers":          {},
	"authorization":    {},
	"authentication":   {},
	"cookie":           {},
	"cookies":          {},
	"token":            {},
	"tokens":           {},
	"access_token":     {},
	"refresh_token":    {},
	"id_token":         {},
	"api_key":          {},
	"apikey":           {},
	"secret":           {},
	"secrets":          {},
	"password":         {},
	"passphrase":       {},
	"private_key":      {},
	"client_secret":    {},
}

// IsForbiddenKey reports whether k (case-insensitive) is on the
// privacy denylist.
func IsForbiddenKey(k string) bool {
	_, ok := forbiddenKeys[strings.ToLower(k)]
	return ok
}

// ValidatePayload parses raw as JSON and walks every key. It returns
// an error if raw is malformed, too large, or names any forbidden key
// at any depth. Allowed keys with non-JSON-object values (e.g.
// payload_ref string) are fine; we only police keys.
func ValidatePayload(raw []byte) error {
	if len(raw) == 0 {
		return fmt.Errorf("events: payload empty")
	}
	if len(raw) > MaxPayloadBytes {
		return fmt.Errorf("events: payload %d bytes exceeds cap %d", len(raw), MaxPayloadBytes)
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("events: payload not JSON: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("events: payload has trailing data")
	}
	if _, ok := v.(map[string]any); !ok {
		return fmt.Errorf("events: payload must be a JSON object")
	}
	return walk(v)
}

func walk(v any) error {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if IsForbiddenKey(k) {
				return fmt.Errorf("events: forbidden key %q in payload", k)
			}
			if err := walk(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range x {
			if err := walk(child); err != nil {
				return err
			}
		}
	}
	return nil
}

// MustMarshalPayload is a helper that marshals v as a JSON object and
// validates it. Returns the bytes or an error.
func MarshalPayload(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("events: marshal: %w", err)
	}
	if err := ValidatePayload(raw); err != nil {
		return nil, err
	}
	return raw, nil
}
