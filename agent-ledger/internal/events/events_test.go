package events

import (
	"strings"
	"testing"
)

func TestValidatePayload_OK(t *testing.T) {
	raw := []byte(`{"task_id":"W2-A","intent_id":"int_01ARZ3NDEKTSV4RRFFQ69G5FAV","reason_sha256":"abc"}`)
	if err := ValidatePayload(raw); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePayload_RejectsForbiddenKeys(t *testing.T) {
	cases := []string{
		`{"diff":"+ foo"}`,
		`{"nested":{"env":{"API_KEY":"x"}}}`,
		`{"items":[{"token":"abc"}]}`,
		`{"PATCH":"x"}`,
		`{"command_output":"hello"}`,
		`{"stdout":"hi"}`,
		`{"headers":{}}`,
	}
	for _, c := range cases {
		if err := ValidatePayload([]byte(c)); err == nil {
			t.Errorf("expected denial for %s", c)
		}
	}
}

func TestValidatePayload_NotObject(t *testing.T) {
	if err := ValidatePayload([]byte(`[1,2,3]`)); err == nil {
		t.Fatal("expected error for non-object root")
	}
}

func TestValidatePayload_OversizeRejected(t *testing.T) {
	big := `{"a":"` + strings.Repeat("x", MaxPayloadBytes) + `"}`
	if err := ValidatePayload([]byte(big)); err == nil {
		t.Fatal("expected size cap")
	}
}

func TestMarshalPayload_RoundTrip(t *testing.T) {
	raw, err := MarshalPayload(map[string]any{"task_id": "W2-A", "policy": "warn"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePayload(raw); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalPayload_BlocksLeaks(t *testing.T) {
	if _, err := MarshalPayload(map[string]any{"diff": "+x"}); err == nil {
		t.Fatal("expected denial")
	}
}
