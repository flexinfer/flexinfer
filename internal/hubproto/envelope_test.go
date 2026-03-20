package hubproto

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	original := &Envelope{
		Domain:    DomainMCP,
		Method:    "tools/call",
		RequestID: "req-123",
		Payload:   json.RawMessage(`{"tool":"git_status"}`),
		Source:    "daemon",
		Timestamp: time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Domain != original.Domain {
		t.Errorf("domain: got %q, want %q", decoded.Domain, original.Domain)
	}
	if decoded.Method != original.Method {
		t.Errorf("method: got %q, want %q", decoded.Method, original.Method)
	}
	if decoded.RequestID != original.RequestID {
		t.Errorf("request_id: got %q, want %q", decoded.RequestID, original.RequestID)
	}
	if decoded.Source != original.Source {
		t.Errorf("source: got %q, want %q", decoded.Source, original.Source)
	}
	if !decoded.Timestamp.Equal(original.Timestamp) {
		t.Errorf("timestamp: got %v, want %v", decoded.Timestamp, original.Timestamp)
	}
	if string(decoded.Payload) != string(original.Payload) {
		t.Errorf("payload: got %s, want %s", decoded.Payload, original.Payload)
	}
}

func TestEnvelopeOmitsEmptyOptionalFields(t *testing.T) {
	env := &Envelope{
		Domain:    DomainControl,
		Method:    "ping",
		Timestamp: time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	// request_id, payload, source should be omitted when empty
	if _, ok := raw["request_id"]; ok {
		t.Error("expected request_id to be omitted when empty")
	}
	if _, ok := raw["payload"]; ok {
		t.Error("expected payload to be omitted when empty/nil")
	}
	if _, ok := raw["source"]; ok {
		t.Error("expected source to be omitted when empty")
	}
}

func TestEnvelopeAllDomains(t *testing.T) {
	domains := []Domain{
		DomainMCP,
		DomainSpawn,
		DomainDevbox,
		DomainAgent,
		DomainControl,
	}

	for _, d := range domains {
		env := &Envelope{
			Domain:    d,
			Method:    "test",
			Timestamp: time.Now().UTC(),
		}
		data, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("marshal domain %q: %v", d, err)
		}
		var decoded Envelope
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal domain %q: %v", d, err)
		}
		if decoded.Domain != d {
			t.Errorf("round-trip domain: got %q, want %q", decoded.Domain, d)
		}
	}
}

func TestEnvelopeWithLargePayload(t *testing.T) {
	// Ensure large payloads survive serialization.
	payload := make([]byte, 0, 1024)
	payload = append(payload, '{')
	payload = append(payload, `"data":"`...)
	for i := 0; i < 500; i++ {
		payload = append(payload, 'A')
	}
	payload = append(payload, `"`...)
	payload = append(payload, '}')

	env := &Envelope{
		Domain:    DomainSpawn,
		Method:    "spawn/start",
		RequestID: "big-req",
		Payload:   json.RawMessage(payload),
		Source:    "test",
		Timestamp: time.Now().UTC(),
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if string(decoded.Payload) != string(env.Payload) {
		t.Error("large payload not preserved through round-trip")
	}
}
