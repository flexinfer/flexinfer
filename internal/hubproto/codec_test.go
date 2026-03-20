package hubproto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWrapMCP(t *testing.T) {
	msg := json.RawMessage(`{"jsonrpc":"2.0","method":"tools/list","id":1}`)

	env := WrapMCP(msg, "daemon")

	if env.Domain != DomainMCP {
		t.Errorf("domain: got %q, want %q", env.Domain, DomainMCP)
	}
	if env.Method != "mcp" {
		t.Errorf("method: got %q, want %q", env.Method, "mcp")
	}
	if env.Source != "daemon" {
		t.Errorf("source: got %q, want %q", env.Source, "daemon")
	}
	if string(env.Payload) != string(msg) {
		t.Errorf("payload: got %s, want %s", env.Payload, msg)
	}
	if env.Timestamp.IsZero() {
		t.Error("timestamp should not be zero")
	}
}

func TestUnwrapMCPSuccess(t *testing.T) {
	payload := json.RawMessage(`{"jsonrpc":"2.0","result":{"tools":[]}}`)
	env := &Envelope{
		Domain:    DomainMCP,
		Method:    "mcp",
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	}

	got, err := UnwrapMCP(env)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload: got %s, want %s", got, payload)
	}
}

func TestUnwrapMCPWrongDomain(t *testing.T) {
	env := &Envelope{
		Domain:    DomainControl,
		Method:    "ping",
		Timestamp: time.Now().UTC(),
	}

	_, err := UnwrapMCP(env)
	if err == nil {
		t.Fatal("expected error for wrong domain")
	}
	if !strings.Contains(err.Error(), "expected domain") {
		t.Errorf("error message: got %q", err.Error())
	}
}

func TestUnwrapMCPNil(t *testing.T) {
	_, err := UnwrapMCP(nil)
	if err == nil {
		t.Fatal("expected error for nil envelope")
	}
	if !strings.Contains(err.Error(), "nil envelope") {
		t.Errorf("error message: got %q", err.Error())
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	original := &Envelope{
		Domain:    DomainSpawn,
		Method:    "spawn/start",
		RequestID: "req-42",
		Payload:   json.RawMessage(`{"image":"alpine:3.18"}`),
		Source:    "hub",
		Timestamp: time.Date(2025, 3, 20, 10, 30, 0, 0, time.UTC),
	}

	data, err := Encode(original)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
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

func TestEncodeNil(t *testing.T) {
	_, err := Encode(nil)
	if err == nil {
		t.Fatal("expected error for nil envelope")
	}
}

func TestDecodeInvalidJSON(t *testing.T) {
	_, err := Decode([]byte(`{invalid json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error message: got %q", err.Error())
	}
}

func TestDecodeEmptyObject(t *testing.T) {
	env, err := Decode([]byte(`{}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Domain != "" {
		t.Errorf("domain: got %q, want empty", env.Domain)
	}
	if env.Method != "" {
		t.Errorf("method: got %q, want empty", env.Method)
	}
}

func TestWrapUnwrapRoundTrip(t *testing.T) {
	msg := json.RawMessage(`{"jsonrpc":"2.0","method":"tools/call","id":5,"params":{"name":"git_status"}}`)

	env := WrapMCP(msg, "test-proxy")

	got, err := UnwrapMCP(env)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if string(got) != string(msg) {
		t.Errorf("round-trip payload: got %s, want %s", got, msg)
	}
}

func TestEncodeDecodePreservesNullPayload(t *testing.T) {
	env := &Envelope{
		Domain:    DomainControl,
		Method:    "pong",
		Timestamp: time.Now().UTC(),
	}

	data, err := Encode(env)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if decoded.Payload != nil {
		t.Errorf("expected nil payload, got %s", decoded.Payload)
	}
}
