package bridge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAgentBridge_CallAgentTool_PropagatesToolErrorEnvelope(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "agent_context__agent_presence_heartbeat" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		return map[string]any{
			"isError": true,
			"content": []map[string]any{
				{"type": "text", "text": "agent codex not registered; call agent_presence_register first"},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	err := bridge.PresenceHeartbeat("codex", "active")
	if err == nil {
		t.Fatal("expected tool-level error to be propagated")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("expected error to contain registration detail, got: %v", err)
	}
}

func TestAgentBridge_CallAgentTool_SucceedsWithTargetNil(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": "{\"ok\":true}"},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	if err := bridge.PresenceHeartbeat("codex", "active"); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestAgentBridge_ContextStream_SetsRequiredQuery(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "agent_context__agent_context_search" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		query, _ := req.Arguments["query"].(string)
		if strings.TrimSpace(query) == "" {
			t.Fatalf("expected non-empty query, got %q", query)
		}
		if query != "since:1970-01-01T00:00:00Z" {
			t.Fatalf("expected default since query, got %q", query)
		}
		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": "{\"ok\":true,\"results\":[]}"},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	if _, err := bridge.ContextStream(time.Time{}, 10); err != nil {
		t.Fatalf("context stream failed: %v", err)
	}
}

func TestAgentBridge_ContextStream_UsesSinceQuery(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	since := time.Date(2026, 2, 11, 21, 39, 0, 0, time.UTC)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "agent_context__agent_context_search" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		query, _ := req.Arguments["query"].(string)
		want := "since:2026-02-11T21:39:00Z"
		if query != want {
			t.Fatalf("expected query %q, got %q", want, query)
		}
		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": "{\"ok\":true,\"results\":[]}"},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	if _, err := bridge.ContextStream(since, 10); err != nil {
		t.Fatalf("context stream failed: %v", err)
	}
}

func TestAgentBridge_SessionEntries_SetsRequiredQuery(t *testing.T) {
	sockPath, handlers := mockDaemon(t)
	const sessionID = "sess-123"

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "agent_context__agent_context_search" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		gotSessionID, _ := req.Arguments["session_id"].(string)
		if gotSessionID != sessionID {
			t.Fatalf("expected session_id %q, got %q", sessionID, gotSessionID)
		}
		query, _ := req.Arguments["query"].(string)
		if query != "session context entries" {
			t.Fatalf("expected session query, got %q", query)
		}
		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": "{\"ok\":true,\"results\":[]}"},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	if _, err := bridge.SessionEntries(sessionID, 10); err != nil {
		t.Fatalf("session entries failed: %v", err)
	}
}
