package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return m
}

func TestHookToEvent_ClaudeSessionStart(t *testing.T) {
	raw := mustParse(t, `{"session_id":"sess-abc","hook_event_name":"SessionStart"}`)

	ev, err := hookToEvent("session-start", "claude-code", "claude-code-1", raw)
	if err != nil {
		t.Fatalf("hookToEvent: %v", err)
	}
	if ev.Type != eventSessionStart {
		t.Errorf("Type = %q, want %q", ev.Type, eventSessionStart)
	}
	if ev.Payload["session_id"] != "sess-abc" {
		t.Errorf("session_id = %v, want sess-abc", ev.Payload["session_id"])
	}
	if ev.Payload["agent_id"] != "claude-code-1" {
		t.Errorf("agent_id = %v, want claude-code-1", ev.Payload["agent_id"])
	}
	if ts, ok := ev.Payload["started_at"].(string); !ok || ts == "" {
		t.Errorf("started_at missing or wrong type: %v", ev.Payload["started_at"])
	}
}

func TestHookToEvent_ClaudeSessionEnd(t *testing.T) {
	raw := mustParse(t, `{"session_id":"sess-abc"}`)

	ev, err := hookToEvent("session-end", "claude-code", "claude-code-1", raw)
	if err != nil {
		t.Fatalf("hookToEvent: %v", err)
	}
	if ev.Type != eventSessionEnd {
		t.Errorf("Type = %q, want %q", ev.Type, eventSessionEnd)
	}
	if _, ok := ev.Payload["ended_at"].(string); !ok {
		t.Errorf("ended_at missing")
	}
}

func TestHookToEvent_ClaudePreToolUseRedactsArgs(t *testing.T) {
	// Bash with an inline AWS key in the command — redaction must mask it.
	raw := mustParse(t, `{
		"session_id": "sess-1",
		"tool_name": "Bash",
		"tool_input": {"command": "aws s3 ls --secret AKIAIOSFODNN7EXAMPLE"},
		"hook_event_name": "PreToolUse"
	}`)

	ev, err := hookToEvent("pre-tool-use", "claude-code", "agent-1", raw)
	if err != nil {
		t.Fatalf("hookToEvent: %v", err)
	}
	if ev.Type != eventToolCallStart {
		t.Errorf("Type = %q, want %q", ev.Type, eventToolCallStart)
	}
	if ev.Payload["tool_name"] != "Bash" {
		t.Errorf("tool_name = %v, want Bash", ev.Payload["tool_name"])
	}
	if ev.Payload["args_tier"] != "public" {
		t.Errorf("args_tier = %v, want public", ev.Payload["args_tier"])
	}
	if cid, ok := ev.Payload["call_id"].(string); !ok || cid == "" {
		t.Errorf("call_id missing or wrong type: %v", ev.Payload["call_id"])
	}
	// The redaction primitive should remove or mask the AWS key. We don't
	// pin the exact transform here — that contract belongs to
	// pkg/telemetry/redact tests — but the raw key must NOT pass through
	// verbatim.
	argsBytes, _ := json.Marshal(ev.Payload["args_redacted"])
	if strings.Contains(string(argsBytes), "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("AWS key leaked through public-tier redaction: %s", argsBytes)
	}
}

func TestHookToEvent_ClaudePreToolUseRequiresToolName(t *testing.T) {
	raw := mustParse(t, `{"session_id":"s","tool_input":{"x":1}}`)

	_, err := hookToEvent("pre-tool-use", "claude-code", "a", raw)
	if err == nil {
		t.Fatal("expected error for missing tool_name")
	}
	if !strings.Contains(err.Error(), "tool_name is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHookToEvent_ClaudePostToolUseCapturesDuration(t *testing.T) {
	raw := mustParse(t, `{
		"session_id": "s1",
		"tool_name": "Read",
		"call_id": "call-xyz",
		"exit_code": 0,
		"duration_ms": 42,
		"tool_response": "file contents here"
	}`)

	ev, err := hookToEvent("post-tool-use", "claude-code", "a1", raw)
	if err != nil {
		t.Fatalf("hookToEvent: %v", err)
	}
	if ev.Type != eventToolCallEnd {
		t.Errorf("Type = %q, want %q", ev.Type, eventToolCallEnd)
	}
	if ev.Payload["call_id"] != "call-xyz" {
		t.Errorf("call_id = %v, want call-xyz", ev.Payload["call_id"])
	}
	if got := ev.Payload["duration_ms"]; got != int64(42) {
		t.Errorf("duration_ms = %v (%T), want 42 (int64)", got, got)
	}
	if got := ev.Payload["exit_code"]; got != int64(0) {
		t.Errorf("exit_code = %v (%T), want 0 (int64)", got, got)
	}
}

func TestHookToEvent_ClaudePostToolUseWithError(t *testing.T) {
	raw := mustParse(t, `{
		"session_id": "s1",
		"tool_name": "Bash",
		"call_id": "call-err",
		"exit_code": 1,
		"error": "command failed"
	}`)

	ev, err := hookToEvent("post-tool-use", "claude-code", "a1", raw)
	if err != nil {
		t.Fatalf("hookToEvent: %v", err)
	}
	if ev.Payload["error"] != "command failed" {
		t.Errorf("error = %v, want \"command failed\"", ev.Payload["error"])
	}
}

func TestHookToEvent_UnknownHookRejected(t *testing.T) {
	_, err := hookToEvent("not-a-hook", "claude-code", "a", map[string]any{})
	if err == nil {
		t.Fatal("expected error for unknown hook")
	}
}

func TestHookToEvent_AgentIDFromPayloadFallback(t *testing.T) {
	// When --agent-id is empty, fall back to payload's agent_id.
	raw := mustParse(t, `{"session_id":"s","agent_id":"from-payload"}`)

	ev, err := hookToEvent("session-start", "claude-code", "", raw)
	if err != nil {
		t.Fatalf("hookToEvent: %v", err)
	}
	if ev.Payload["agent_id"] != "from-payload" {
		t.Errorf("agent_id = %v, want from-payload", ev.Payload["agent_id"])
	}
}

func TestHookToEvent_GenericPlatformPassthrough(t *testing.T) {
	raw := mustParse(t, `{
		"type": "session.start",
		"payload": {"session_id":"s9"}
	}`)

	ev, err := hookToEvent("session-start", "generic", "agent-9", raw)
	if err != nil {
		t.Fatalf("hookToEvent: %v", err)
	}
	if ev.Type != eventSessionStart {
		t.Errorf("Type = %q, want %q", ev.Type, eventSessionStart)
	}
	if ev.Payload["session_id"] != "s9" {
		t.Errorf("session_id = %v, want s9", ev.Payload["session_id"])
	}
	if ev.Payload["agent_id"] != "agent-9" {
		t.Errorf("agent_id = %v, want agent-9 (injected from --agent-id)", ev.Payload["agent_id"])
	}
}

func TestHookToEvent_GenericRejectsBadType(t *testing.T) {
	raw := mustParse(t, `{"type":"server.health","payload":{}}`)

	_, err := hookToEvent("", "generic", "", raw)
	if err == nil {
		t.Fatal("expected error for non-emittable type")
	}
}

func TestHookToEvent_GenericRejectsHookTypeMismatch(t *testing.T) {
	raw := mustParse(t, `{"type":"tool.call.start","payload":{}}`)

	_, err := hookToEvent("session-start", "generic", "", raw)
	if err == nil {
		t.Fatal("expected error for hook/type mismatch")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHookToEvent_UnknownPlatformRejected(t *testing.T) {
	_, err := hookToEvent("session-start", "exotic-cli", "", map[string]any{})
	if err == nil {
		t.Fatal("expected error for unsupported platform")
	}
}

func TestIsAllowedEmittedType(t *testing.T) {
	allowed := []string{
		eventSessionStart, eventSessionEnd, eventAgentStatusChange,
		eventToolCallStart, eventToolCallEnd,
	}
	for _, t1 := range allowed {
		if !isAllowedEmittedType(t1) {
			t.Errorf("%s should be allowed", t1)
		}
	}
	denied := []string{"server.health", "process.start", "bus.backpressure", "", "tool.call"}
	for _, t1 := range denied {
		if isAllowedEmittedType(t1) {
			t.Errorf("%s should NOT be allowed", t1)
		}
	}
}

func TestResolveDaemonHTTPURL_Precedence(t *testing.T) {
	t.Setenv("LOOM_DAEMON_HTTP_URL", "http://env-host:1234")

	// Flag wins over env.
	if got := resolveDaemonHTTPURL("http://flag-host:9999"); got != "http://flag-host:9999" {
		t.Errorf("flag precedence: got %q", got)
	}
	// Env wins over default.
	if got := resolveDaemonHTTPURL(""); got != "http://env-host:1234" {
		t.Errorf("env precedence: got %q", got)
	}
}

func TestResolveDaemonHTTPURL_Default(t *testing.T) {
	t.Setenv("LOOM_DAEMON_HTTP_URL", "")
	if got := resolveDaemonHTTPURL(""); got != "http://127.0.0.1:9876" {
		t.Errorf("default: got %q", got)
	}
}

func TestIntField(t *testing.T) {
	cases := []struct {
		raw  string
		key  string
		want int64
	}{
		{`{"x":42}`, "x", 42},  // float64 from JSON
		{`{"x":"42"}`, "x", 0}, // string is not a number for our purposes
		{`{"x":-1}`, "x", -1},  // negative
		{`{"x":0}`, "x", 0},    // zero
		{`{}`, "x", 0},         // missing
		{`{"x":3.7}`, "x", 3},  // float truncates to int64
	}
	for _, c := range cases {
		var m map[string]any
		_ = json.Unmarshal([]byte(c.raw), &m)
		if got := intField(m, c.key); got != c.want {
			t.Errorf("intField(%s, %q) = %d, want %d", c.raw, c.key, got, c.want)
		}
	}
}
