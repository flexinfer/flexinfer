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
	_, err := bridge.PresenceHeartbeat("codex", PresenceHeartbeatParams{Status: "active"})
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
	if _, err := bridge.PresenceHeartbeat("codex", PresenceHeartbeatParams{Status: "active"}); err != nil {
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

func TestAgentBridge_CreateTask_UsesTasksArrayShape(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "agent_context__agent_task_add" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		if got, _ := req.Arguments["session_id"].(string); got != "sess-1" {
			t.Fatalf("expected session_id sess-1, got %q", got)
		}
		tasks, ok := req.Arguments["tasks"].([]any)
		if !ok || len(tasks) != 1 {
			t.Fatalf("expected one task in tasks array, got %#v", req.Arguments["tasks"])
		}
		task, ok := tasks[0].(map[string]any)
		if !ok {
			t.Fatalf("task item is not an object: %#v", tasks[0])
		}
		if got, _ := task["title"].(string); got != "Fix HUD sync" {
			t.Fatalf("expected title, got %q", got)
		}
		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": "{\"ok\":true,\"task_ids\":[\"t-1\"]}"},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	err := bridge.CreateTask(CreateTaskParams{
		SessionID: "sess-1",
		Title:     "Fix HUD sync",
		Priority:  "high",
		Context:   "update task payload shape",
	})
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}
}

func TestAgentBridge_MemoryAdd_UsesItemsArrayShape(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "agent_context__agent_memory_add" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		items, ok := req.Arguments["items"].([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("expected one item in items array, got %#v", req.Arguments["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			t.Fatalf("item is not an object: %#v", items[0])
		}
		if got, _ := item["title"].(string); got != "Session Summary: sess-1" {
			t.Fatalf("expected title, got %q", got)
		}
		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": "{\"ok\":true,\"item_ids\":[\"m-1\"]}"},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	if err := bridge.MemoryAdd("Session Summary: sess-1", "summary text", "long_term", "high", "summary"); err != nil {
		t.Fatalf("memory add failed: %v", err)
	}
}

func TestAgentBridge_MemoryDelete_UsesConfirmDeleteShape(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "agent_context__agent_memory_delete" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		if confirm, _ := req.Arguments["confirm"].(bool); !confirm {
			t.Fatalf("expected confirm=true, got %#v", req.Arguments["confirm"])
		}
		itemIDs, ok := req.Arguments["item_ids"].([]any)
		if !ok || len(itemIDs) != 1 {
			t.Fatalf("expected item_ids array, got %#v", req.Arguments["item_ids"])
		}
		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": "{\"ok\":true,\"deleted\":[\"m-1\"]}"},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	if err := bridge.MemoryDelete("m-1"); err != nil {
		t.Fatalf("memory delete failed: %v", err)
	}
}

func TestAgentBridge_WorkflowStatus_UsesWorkflowIDArg(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "agent_context__agent_workflow_status" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		if got, _ := req.Arguments["workflow_id"].(string); got != "wf-1" {
			t.Fatalf("expected workflow_id wf-1, got %q", got)
		}
		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": "{\"workflow_id\":\"wf-1\",\"status\":\"running\",\"current_step\":\"plan\",\"created_at\":\"2026-02-11T00:00:00Z\"}"},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	if _, err := bridge.WorkflowStatus("wf-1"); err != nil {
		t.Fatalf("workflow status failed: %v", err)
	}
}

func TestAgentBridge_WorkflowEvents_UsesWorkflowIDArg(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "agent_context__agent_workflow_events" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		if got, _ := req.Arguments["workflow_id"].(string); got != "wf-1" {
			t.Fatalf("expected workflow_id wf-1, got %q", got)
		}
		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": "{\"events\":[{\"id\":\"e-1\",\"event_type\":\"workflow_started\",\"timestamp\":\"2026-02-12T00:00:00Z\"}]}"},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	events, err := bridge.WorkflowEvents("wf-1")
	if err != nil {
		t.Fatalf("workflow events failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].EventType != "workflow_started" {
		t.Fatalf("unexpected event type: %q", events[0].EventType)
	}
}

func TestAgentBridge_GraphFindPath_UsesSourceTargetAndEnriches(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		switch req.Name {
		case "agent_context__agent_graph_find_path":
			if got, _ := req.Arguments["source_id"].(string); got != "e-1" {
				t.Fatalf("expected source_id e-1, got %q", got)
			}
			if got, _ := req.Arguments["target_id"].(string); got != "e-2" {
				t.Fatalf("expected target_id e-2, got %q", got)
			}
			return map[string]any{
				"isError": false,
				"content": []map[string]any{
					{"type": "text", "text": "{\"ok\":true,\"path\":[\"e-1\",\"e-2\"]}"},
				},
			}, nil
		case "agent_context__agent_entity_get":
			return map[string]any{
				"isError": false,
				"content": []map[string]any{
					{"type": "text", "text": "{\"ok\":true,\"entities\":[{\"id\":\"e-1\",\"name\":\"Source\",\"type\":\"service\"},{\"id\":\"e-2\",\"name\":\"Target\",\"type\":\"database\"}]}"},
				},
			}, nil
		default:
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		return nil, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	path, err := bridge.GraphFindPath("e-1", "e-2", 5)
	if err != nil {
		t.Fatalf("graph find path failed: %v", err)
	}
	if len(path) != 2 {
		t.Fatalf("expected 2 path nodes, got %d", len(path))
	}
	if path[0].Type != "service" || path[1].Type != "database" {
		t.Fatalf("unexpected path types: %#v", path)
	}
}

func TestAgentBridge_KnowledgeRecall_SetsCrossAgent(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "agent_context__agent_context_recall_enhanced" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		crossAgent, _ := req.Arguments["cross_agent"].(bool)
		if !crossAgent {
			t.Fatalf("expected cross_agent=true, got %v", crossAgent)
		}
		query, _ := req.Arguments["query"].(string)
		if query != "test query" {
			t.Fatalf("expected query 'test query', got %q", query)
		}
		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": `{"ok":true,"entries":[{"id":"e1","agent_id":"claude","entry_type":"decision","title":"Chose JWT","content":"Because reasons","token_count":50}],"count":1,"total_tokens":50,"token_budget":8000}`},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	result, err := bridge.KnowledgeRecall("test query", "", 8000)
	if err != nil {
		t.Fatalf("knowledge recall failed: %v", err)
	}
	if result.Count != 1 {
		t.Fatalf("expected 1 entry, got %d", result.Count)
	}
	if result.Entries[0].AgentID != "claude" {
		t.Fatalf("expected agent_id 'claude', got %q", result.Entries[0].AgentID)
	}
	if result.Entries[0].EntryType != "decision" {
		t.Fatalf("expected entry_type 'decision', got %q", result.Entries[0].EntryType)
	}
}
