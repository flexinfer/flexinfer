package clients

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mills/pipeline"
)

func handoffStub(t *testing.T, body any) []byte {
	t.Helper()
	bodyJSON, _ := json.Marshal(body)
	res := mcp.CallToolResult{Content: []mcp.Content{{Type: "text", Text: string(bodyJSON)}}}
	out, _ := json.Marshal(res)
	return out
}

func TestHandoffClient_HappyPath(t *testing.T) {
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{}`),
			"tools/call": handoffStub(t, map[string]any{
				"ok": true, "handoff_id": "ho-123", "token_count": 1500, "entry_count": 3, "summary": "done",
			}),
		},
	}
	hub := newTestHubClient(t, ft)
	hc := NewHandoffClient(hub, "session-op-1")
	resp, err := hc.CreateHandoff(context.Background(), pipeline.HandoffRequest{
		From:        "loom-mills-operator",
		To:          "human-on-call",
		Reason:      "stage X exceeded retries",
		BacklogID:   "BL-X",
		PipelineRun: "PIPE-X-1",
		IssueURL:    "https://gitlab.example/issues/99",
		Context: map[string]any{
			"failure_record": map[string]any{
				"backlog_id": "BL-X",
				"cost_usd":   0.42,
			},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.HandoffID != "ho-123" {
		t.Errorf("HandoffID = %q", resp.HandoffID)
	}

	sent := ft.sentMessages()
	var callMsg mcp.Message
	for _, m := range sent {
		if m.Method == "tools/call" {
			callMsg = m
		}
	}
	var params mcp.CallToolParams
	_ = json.Unmarshal(callMsg.Params, &params)
	if params.Name != "agent_handoff_create" {
		t.Errorf("tool name = %q", params.Name)
	}
	if params.Arguments["session_id"] != "session-op-1" {
		t.Errorf("session_id = %v, want operator session id", params.Arguments["session_id"])
	}
	if params.Arguments["target_agent_id"] != "human-on-call" {
		t.Errorf("target_agent_id = %v", params.Arguments["target_agent_id"])
	}
	if params.Arguments["handoff_type"] != "summary_only" {
		t.Errorf("handoff_type = %v, want summary_only default", params.Arguments["handoff_type"])
	}
	instr, _ := params.Arguments["instructions"].(string)
	for _, want := range []string{"loom-mills-operator", "PIPE-X-1", "BL-X", "https://gitlab.example/issues/99", "stage X exceeded retries", "failure_record"} {
		if !strings.Contains(instr, want) {
			t.Errorf("instructions missing %q; full=%s", want, instr)
		}
	}
}

func TestHandoffClient_UsesDynamicSourceSession(t *testing.T) {
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{}`),
			"tools/call": handoffStub(t, map[string]any{"ok": true, "handoff_id": "ho-dynamic"}),
		},
	}
	hub := newTestHubClient(t, ft)
	hc := NewHandoffClient(hub, "")
	hc.SourceSessionIDFunc = func() string { return "session-late" }

	if _, err := hc.CreateHandoff(context.Background(), pipeline.HandoffRequest{To: "human-on-call"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	var params mcp.CallToolParams
	for _, m := range ft.sentMessages() {
		if m.Method == "tools/call" {
			_ = json.Unmarshal(m.Params, &params)
		}
	}
	if params.Arguments["session_id"] != "session-late" {
		t.Errorf("session_id = %v, want dynamic session", params.Arguments["session_id"])
	}
}

func TestHandoffClient_RequiresSourceSession(t *testing.T) {
	hub := newTestHubClient(t, &fakeTransport{})
	hc := NewHandoffClient(hub, "")
	if _, err := hc.CreateHandoff(context.Background(), pipeline.HandoffRequest{To: "x"}); err == nil {
		t.Error("expected error when SourceSessionID empty")
	}
}

func TestHandoffClient_RequiresTo(t *testing.T) {
	hub := newTestHubClient(t, &fakeTransport{})
	hc := NewHandoffClient(hub, "session-x")
	if _, err := hc.CreateHandoff(context.Background(), pipeline.HandoffRequest{}); err == nil {
		t.Error("expected error when To empty")
	}
}

func TestHandoffClient_HandoffTypeOverride(t *testing.T) {
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{}`),
			"tools/call": handoffStub(t, map[string]any{"ok": true, "handoff_id": "ho-x"}),
		},
	}
	hub := newTestHubClient(t, ft)
	hc := NewHandoffClient(hub, "session-x")
	hc.HandoffType = "full"
	if _, err := hc.CreateHandoff(context.Background(), pipeline.HandoffRequest{To: "h"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	sent := ft.sentMessages()
	var params mcp.CallToolParams
	for _, m := range sent {
		if m.Method == "tools/call" {
			_ = json.Unmarshal(m.Params, &params)
		}
	}
	if params.Arguments["handoff_type"] != "full" {
		t.Errorf("handoff_type override not applied: %v", params.Arguments["handoff_type"])
	}
}

func TestHandoffClient_ServiceFailureBubbles(t *testing.T) {
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{}`),
			"tools/call": handoffStub(t, map[string]any{"ok": false, "handoff_id": ""}),
		},
	}
	hub := newTestHubClient(t, ft)
	hc := NewHandoffClient(hub, "session-x")
	if _, err := hc.CreateHandoff(context.Background(), pipeline.HandoffRequest{To: "h"}); err == nil {
		t.Error("expected error when service reports ok=false with no id")
	}
}

func TestHandoffTypeDefault(t *testing.T) {
	cases := map[string]string{
		"":             "summary_only",
		"unknown":      "summary_only",
		"full":         "full",
		"selective":    "selective",
		"summary_only": "summary_only",
	}
	for input, want := range cases {
		if got := handoffTypeOrDefault(input); got != want {
			t.Errorf("handoffTypeOrDefault(%q) = %q, want %q", input, got, want)
		}
	}
}

// liveHandoffYAMLBody is the exact raw text observed in the operator
// WARN log when agent_handoff_create returned YAML and the legacy
// JSON-only decoder failed (see fix/mills-escalator-yaml-handoff-decode).
const liveHandoffYAMLBody = "entry_count: 0\n" +
	"handoff_id: bcd72b1b8f9ad438\n" +
	"ok: true\n" +
	"summary: \"\"\n" +
	"token_count: 0\n"

func TestDecodeHandoffCreateResponse_YAML(t *testing.T) {
	parsed, err := decodeHandoffCreateResponse(liveHandoffYAMLBody)
	if err != nil {
		t.Fatalf("decode YAML: %v", err)
	}
	if !parsed.OK {
		t.Errorf("OK = false, want true")
	}
	if parsed.HandoffID != "bcd72b1b8f9ad438" {
		t.Errorf("HandoffID = %q, want bcd72b1b8f9ad438", parsed.HandoffID)
	}
	if parsed.Summary != "" {
		t.Errorf("Summary = %q, want empty", parsed.Summary)
	}
	if parsed.TokenCount != 0 || parsed.EntryCount != 0 {
		t.Errorf("counts = (%d,%d), want (0,0)", parsed.TokenCount, parsed.EntryCount)
	}
}

func TestDecodeHandoffCreateResponse_JSON(t *testing.T) {
	body := `{"ok":true,"handoff_id":"ho-json","token_count":42,"entry_count":3,"summary":"hi"}`
	parsed, err := decodeHandoffCreateResponse(body)
	if err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if !parsed.OK || parsed.HandoffID != "ho-json" || parsed.TokenCount != 42 || parsed.EntryCount != 3 || parsed.Summary != "hi" {
		t.Errorf("parsed = %+v", parsed)
	}
}

func TestDecodeHandoffCreateResponse_Empty(t *testing.T) {
	if _, err := decodeHandoffCreateResponse(""); err == nil {
		t.Error("expected error for empty body")
	}
	if _, err := decodeHandoffCreateResponse("   \n  "); err == nil {
		t.Error("expected error for whitespace-only body")
	}
}

func TestDecodeHandoffCreateResponse_Invalid(t *testing.T) {
	// Neither valid JSON nor YAML mapping.
	if _, err := decodeHandoffCreateResponse("not: : valid: yaml: ::"); err == nil {
		t.Error("expected error for malformed payload")
	}
}

// handoffRawStub returns a CallToolResult whose content[0].text is the
// raw body verbatim (no JSON wrapping). Used to simulate YAML payloads
// from servers running in concise-text-output mode.
func handoffRawStub(t *testing.T, rawText string) []byte {
	t.Helper()
	res := mcp.CallToolResult{Content: []mcp.Content{{Type: "text", Text: rawText}}}
	out, _ := json.Marshal(res)
	return out
}

func TestHandoffClient_AcceptsYAMLBody(t *testing.T) {
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{}`),
			"tools/call": handoffRawStub(t, liveHandoffYAMLBody),
		},
	}
	hub := newTestHubClient(t, ft)
	hc := NewHandoffClient(hub, "session-op-yaml")
	resp, err := hc.CreateHandoff(context.Background(), pipeline.HandoffRequest{
		From: "loom-mills-operator",
		To:   "human-on-call",
	})
	if err != nil {
		t.Fatalf("create with YAML body: %v", err)
	}
	if resp.HandoffID != "bcd72b1b8f9ad438" {
		t.Errorf("HandoffID = %q, want bcd72b1b8f9ad438", resp.HandoffID)
	}
}
