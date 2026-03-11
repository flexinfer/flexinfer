package bridge

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentBridge_WorkStart_Success(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	var worktreeSeen bool
	var taskAddSeen bool
	var taskUpdateSeen bool
	var heartbeatSeen bool

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
				"content": []map[string]any{{"type": "text", "text": `{"sessions":[]}`}},
			}, nil
		case "agent_context__agent_session_start":
			return map[string]any{
				"isError": false,
				"content": []map[string]any{{"type": "text", "text": `{"session_id":"sess-1"}`}},
			}, nil
		case "agent_context__agent_presence_register":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"ok":true}`}}}, nil
		case "agent_context__agent_worktree_allocate":
			worktreeSeen = true
			return map[string]any{
				"isError": false,
				"content": []map[string]any{{"type": "text", "text": `{"ok":true,"assignment_id":"wt-1","worktree_path":"/tmp/wt-1","branch":"codex/work","status":"active"}`}},
			}, nil
		case "agent_context__agent_task_add":
			taskAddSeen = true
			return map[string]any{
				"isError": false,
				"content": []map[string]any{{"type": "text", "text": `{"task_ids":["task-1"]}`}},
			}, nil
		case "agent_context__agent_task_update":
			taskUpdateSeen = true
			return map[string]any{
				"isError": false,
				"content": []map[string]any{{"type": "text", "text": `{"ok":true}`}},
			}, nil
		case "agent_context__agent_presence_heartbeat":
			heartbeatSeen = true
			return map[string]any{
				"isError": false,
				"content": []map[string]any{{"type": "text", "text": `{"ok":true}`}},
			}, nil
		default:
			return map[string]any{
				"isError": false,
				"content": []map[string]any{{"type": "text", "text": `{}`}},
			}, nil
		}
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	result, err := bridge.WorkStart(WorkStartParams{
		AgentID:         "codex-1",
		AgentType:       "codex",
		Namespace:       "loom-core/agent-context-consistency",
		Description:     "consistency hardening",
		WorktreeBranch:  "codex/work",
		WorktreePurpose: "hardening",
		TaskTitle:       "Implement consistency hardening",
		TaskPriority:    "high",
		HeartbeatFiles:  []string{"internal/hud/bridge/agent.go"},
	})
	if err != nil {
		t.Fatalf("work-start failed: %v", err)
	}
	if result == nil || result.SessionID != "sess-1" || result.AssignmentID != "wt-1" || result.TaskID != "task-1" {
		t.Fatalf("unexpected work-start result: %#v", result)
	}
	if !worktreeSeen || !taskAddSeen || !taskUpdateSeen || !heartbeatSeen {
		t.Fatalf("expected all workflow steps to execute (worktree=%v task_add=%v task_update=%v heartbeat=%v)", worktreeSeen, taskAddSeen, taskUpdateSeen, heartbeatSeen)
	}
}

func TestAgentBridge_WorkStart_FailsFastOnTaskUpdate(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}

		switch req.Name {
		case "agent_context__agent_session_list":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"sessions":[]}`}}}, nil
		case "agent_context__agent_session_start":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"session_id":"sess-1"}`}}}, nil
		case "agent_context__agent_worktree_allocate":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"assignment_id":"wt-1","worktree_path":"/tmp/wt-1","branch":"codex/work"}`}}}, nil
		case "agent_context__agent_task_add":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"task_ids":["task-1"]}`}}}, nil
		case "agent_context__agent_task_update":
			return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "cannot transition task"}}}, nil
		default:
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{}`}}}, nil
		}
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	_, err := bridge.WorkStart(WorkStartParams{
		AgentID:        "codex-1",
		Namespace:      "loom-core/agent-context-consistency",
		WorktreeBranch: "codex/work",
		TaskTitle:      "Implement consistency hardening",
	})
	if err == nil {
		t.Fatal("expected task-update failure to abort workflow")
	}
	if !strings.Contains(err.Error(), "step=task-update") || !strings.Contains(err.Error(), "session_id=sess-1") || !strings.Contains(err.Error(), "assignment_id=wt-1") || !strings.Contains(err.Error(), "task_id=task-1") {
		t.Fatalf("expected explicit partial-status error, got: %v", err)
	}
}

func TestAgentBridge_WorkHandoff_SharesContextAndCreatesDispatchTask(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	var shareSeen bool
	var handoffSeen bool
	var dispatchTaskSeen bool

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
			agentID, _ := req.Arguments["agent_id"].(string)
			switch agentID {
			case "source-agent":
				return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"sessions":[{"id":"sess-source","agent_id":"source-agent","status":"active"}]}`}}}, nil
			case "target-agent":
				return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"sessions":[{"id":"sess-target","agent_id":"target-agent","status":"active"}]}`}}}, nil
			default:
				return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"sessions":[]}`}}}, nil
			}
		case "agent_context__agent_context_add":
			shareSeen = true
			entries, _ := req.Arguments["entries"].([]any)
			if len(entries) != 1 {
				t.Fatalf("expected one shared entry, got %#v", req.Arguments["entries"])
			}
			entry, _ := entries[0].(map[string]any)
			if got, _ := entry["visibility"].(string); got != "shared" {
				t.Fatalf("expected visibility=shared, got %q", got)
			}
			sharedWith, _ := entry["shared_with"].([]any)
			if len(sharedWith) != 1 || sharedWith[0] != "target-agent" {
				t.Fatalf("expected shared_with target-agent, got %#v", entry["shared_with"])
			}
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"ok":true}`}}}, nil
		case "agent_context__agent_handoff_create":
			handoffSeen = true
			if got, _ := req.Arguments["session_id"].(string); got != "sess-source" {
				t.Fatalf("expected source session sess-source, got %q", got)
			}
			if got, _ := req.Arguments["target_agent_id"].(string); got != "target-agent" {
				t.Fatalf("expected target_agent_id=target-agent, got %q", got)
			}
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"handoff_id":"handoff-9"}`}}}, nil
		case "agent_context__agent_task_add":
			dispatchTaskSeen = true
			if got, _ := req.Arguments["session_id"].(string); got != "sess-target" {
				t.Fatalf("expected dispatch task session sess-target, got %q", got)
			}
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"task_ids":["task-77"]}`}}}, nil
		default:
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{}`}}}, nil
		}
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	result, err := bridge.WorkHandoff(WorkHandoffParams{
		SourceAgentID:      "source-agent",
		TargetAgentID:      "target-agent",
		Instructions:       "Continue implementation and validate tests",
		CreateDispatchTask: true,
		DispatchTaskTitle:  "Continue handoff execution",
		DispatchTaskTags:   []string{"handoff"},
	})
	if err != nil {
		t.Fatalf("work-handoff failed: %v", err)
	}
	if result == nil || result.SourceSessionID != "sess-source" || result.HandoffID != "handoff-9" || result.DispatchTaskID != "task-77" {
		t.Fatalf("unexpected work-handoff result: %#v", result)
	}
	if !shareSeen || !handoffSeen || !dispatchTaskSeen {
		t.Fatalf("expected share + handoff + dispatch task steps (share=%v handoff=%v dispatch=%v)", shareSeen, handoffSeen, dispatchTaskSeen)
	}
}

func TestAgentBridge_WorkHandoff_FailsWhenDispatchTargetSessionMissing(t *testing.T) {
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
		case "agent_context__agent_session_list":
			agentID, _ := req.Arguments["agent_id"].(string)
			if agentID == "source-agent" {
				return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"sessions":[{"id":"sess-source","agent_id":"source-agent","status":"active"}]}`}}}, nil
			}
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"sessions":[]}`}}}, nil
		case "agent_context__agent_context_add":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"ok":true}`}}}, nil
		case "agent_context__agent_handoff_create":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"handoff_id":"handoff-404"}`}}}, nil
		default:
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{}`}}}, nil
		}
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	_, err := bridge.WorkHandoff(WorkHandoffParams{
		SourceAgentID:      "source-agent",
		TargetAgentID:      "target-agent",
		Instructions:       "Continue implementation",
		CreateDispatchTask: true,
	})
	if err == nil {
		t.Fatal("expected missing target session to fail dispatch step")
	}
	if !strings.Contains(err.Error(), "step=resolve-target-session") || !strings.Contains(err.Error(), "handoff_id=handoff-404") {
		t.Fatalf("expected explicit dispatch-step error with handoff context, got: %v", err)
	}
}
