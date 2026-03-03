package bridge

import (
	"encoding/json"
	"testing"
)

func TestAgentBridge_EndSession_DefaultsSummarizeToTrue(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	var seenSummarize any
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "agent_context__agent_session_end" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		seenSummarize = req.Arguments["summarize"]

		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": `{"ok":true}`},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	ended, err := bridge.EndSession(SessionEndParams{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("EndSession failed: %v", err)
	}
	if !ended {
		t.Fatal("expected ended=true")
	}
	if got, ok := seenSummarize.(bool); !ok || !got {
		t.Fatalf("expected summarize=true, got %#v", seenSummarize)
	}
}

func TestAgentBridge_EndSession_AllowsExplicitSummarizeFalse(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	var seenSummarize any
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "agent_context__agent_session_end" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		seenSummarize = req.Arguments["summarize"]

		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": `{"ok":true}`},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	summarize := false
	bridge := NewAgentBridge(client)
	ended, err := bridge.EndSession(SessionEndParams{
		SessionID: "sess-1",
		Summarize: &summarize,
	})
	if err != nil {
		t.Fatalf("EndSession failed: %v", err)
	}
	if !ended {
		t.Fatal("expected ended=true")
	}
	if got, ok := seenSummarize.(bool); !ok || got {
		t.Fatalf("expected summarize=false, got %#v", seenSummarize)
	}
}
