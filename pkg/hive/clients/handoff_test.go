package clients

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/hive/pipeline"
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
		From:        "loom-hive-operator",
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
	for _, want := range []string{"loom-hive-operator", "PIPE-X-1", "BL-X", "https://gitlab.example/issues/99", "stage X exceeded retries", "failure_record"} {
		if !strings.Contains(instr, want) {
			t.Errorf("instructions missing %q; full=%s", want, instr)
		}
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
