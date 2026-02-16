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

func TestAgentBridge_ContextInspect_AggregatesSessionBudget(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("loom/tools", func(_ json.RawMessage) (any, error) {
		return map[string]any{
			"tools": []map[string]any{
				{
					"name":        "agent_context__agent_context_search",
					"description": "Search context entries",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{"type": "string"},
							"limit": map[string]any{"type": "integer"},
						},
						"required": []string{"query"},
					},
				},
			},
			"cachedAt":    "2026-02-16T00:00:00Z",
			"serverCount": 1,
		}, nil
	})

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}

		switch req.Name {
		case "agent_context__agent_session_list":
			return map[string]any{
				"isError": false,
				"content": []map[string]any{
					{"type": "text", "text": `{"sessions":[{"id":"sess-1","agent_id":"codex","namespace":"loom-core/main","status":"active"}]}`},
				},
			}, nil
		case "agent_context__agent_context_search":
			return map[string]any{
				"isError": false,
				"content": []map[string]any{
					{"type": "text", "text": `{"results":[{"score":1,"entry":{"id":"e1","entry_type":"decision","title":"Short title","content":"small body","timestamp":"2026-02-16T00:00:00Z","token_count":14}},{"score":1,"entry":{"id":"e2","entry_type":"finding","title":"Longer title","content":"this is a much longer body for context sizing","timestamp":"2026-02-16T00:01:00Z","token_count":27}},{"score":1,"entry":{"id":"e3","entry_type":"file_read","title":"Read internal/hud/api_agent.go","content":"lines 560-640 reviewed for context inspect handler","file_path":"internal/hud/api_agent.go","line_start":560,"line_end":640,"timestamp":"2026-02-16T00:02:00Z","token_count":42}}]}`},
				},
			}, nil
		case "agent_context__agent_task_list":
			return map[string]any{
				"isError": false,
				"content": []map[string]any{
					{"type": "text", "text": `{"tasks":[{"id":"t1","status":"pending"},{"id":"t2","status":"in_progress"},{"id":"t3","status":"completed"}]}`},
				},
			}, nil
		case "agent_context__agent_memory_stats":
			return map[string]any{
				"isError": false,
				"content": []map[string]any{
					{"type": "text", "text": `{"working_memory":{"item_count":2,"token_count":120},"short_term_memory":{"item_count":3,"token_count":300},"long_term_memory":{"item_count":4,"token_count":500},"total_items":9,"total_tokens":920}`},
				},
			}, nil
		default:
			t.Fatalf("unexpected tool name: %s", req.Name)
			return nil, nil
		}
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	result, err := bridge.ContextInspect("codex", "", true, 3)
	if err != nil {
		t.Fatalf("context inspect failed: %v", err)
	}

	if result.SessionID != "sess-1" {
		t.Fatalf("expected session_id sess-1, got %q", result.SessionID)
	}
	if result.EntryCount != 3 {
		t.Fatalf("expected 3 entries, got %d", result.EntryCount)
	}
	if !result.Truncated {
		t.Fatalf("expected truncated=true when entry_count == limit")
	}
	if len(result.ByEntryType) != 3 {
		t.Fatalf("expected 3 entry-type buckets, got %d", len(result.ByEntryType))
	}
	if len(result.TopEntries) != 3 {
		t.Fatalf("expected top entries in detail mode, got %d", len(result.TopEntries))
	}
	if result.TopEntries[0].ID != "e3" {
		t.Fatalf("expected largest token entry e3 first, got %q", result.TopEntries[0].ID)
	}
	if result.Tasks.Pending != 1 || result.Tasks.InProgress != 1 || result.Tasks.Completed != 1 {
		t.Fatalf("unexpected task summary: %+v", result.Tasks)
	}
	if result.Memory == nil || result.Memory.TotalTokens != 920 {
		t.Fatalf("expected memory stats to be populated, got %+v", result.Memory)
	}
	if result.ContextEstimatedTokens != 83 {
		t.Fatalf("expected context_estimated_tokens 83, got %d", result.ContextEstimatedTokens)
	}
	if len(result.Sections) != 5 {
		t.Fatalf("expected 5 accounting sections, got %d", len(result.Sections))
	}
	sections := make(map[string]ContextInspectSection, len(result.Sections))
	sum := 0
	for _, s := range result.Sections {
		sections[s.Section] = s
		sum += s.EstimatedTokens
	}
	if result.EstimatedTokens != sum {
		t.Fatalf("expected estimated_tokens to reconcile with sections (got %d, sum=%d)", result.EstimatedTokens, sum)
	}
	if sections["tools_schema"].EstimatedTokens <= 0 {
		t.Fatalf("expected tools_schema section to include measured overhead, got %+v", sections["tools_schema"])
	}
	if sections["file_injections"].EstimatedTokens != 42 {
		t.Fatalf("expected file_injections tokens to be 42, got %d", sections["file_injections"].EstimatedTokens)
	}
	if got := sections["context_entries"].EstimatedTokens + sections["file_injections"].EstimatedTokens; got != result.ContextEstimatedTokens {
		t.Fatalf("expected context entries + file injections = context_estimated_tokens (%d), got %d", result.ContextEstimatedTokens, got)
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
