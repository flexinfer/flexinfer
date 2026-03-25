package main

import (
	"encoding/json"
	"testing"
)

func TestTaskSyncPayloadMarshal(t *testing.T) {
	p := taskSyncPayload{
		AgentID:  "claude-code-123",
		ToolName: "TaskCreate",
		ToolInput: map[string]any{
			"subject":     "Implement feature X",
			"description": "Details about feature X",
		},
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got taskSyncPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.AgentID != p.AgentID {
		t.Errorf("agent_id: got %q, want %q", got.AgentID, p.AgentID)
	}
	if got.ToolName != p.ToolName {
		t.Errorf("tool_name: got %q, want %q", got.ToolName, p.ToolName)
	}
	if got.ToolInput["subject"] != "Implement feature X" {
		t.Errorf("tool_input.subject: got %v, want %q", got.ToolInput["subject"], "Implement feature X")
	}
}

func TestTaskSyncPayloadFromHookInput(t *testing.T) {
	// Simulate what Claude Code PostToolUse hook sends via stdin.
	hookJSON := `{"tool_name":"TodoWrite","tool_input":{"todos":[{"content":"Fix bug","status":"pending"},{"content":"Write tests","status":"pending"}]}}`

	var hookInput struct {
		ToolName  string         `json:"tool_name"`
		ToolInput map[string]any `json:"tool_input"`
	}
	if err := json.Unmarshal([]byte(hookJSON), &hookInput); err != nil {
		t.Fatalf("unmarshal hook input: %v", err)
	}

	payload := taskSyncPayload{
		AgentID:   "test-agent",
		ToolName:  hookInput.ToolName,
		ToolInput: hookInput.ToolInput,
	}

	if payload.ToolName != "TodoWrite" {
		t.Errorf("tool_name: got %q, want %q", payload.ToolName, "TodoWrite")
	}
	todos, ok := payload.ToolInput["todos"].([]any)
	if !ok {
		t.Fatal("expected todos to be []any")
	}
	if len(todos) != 2 {
		t.Errorf("todos length: got %d, want 2", len(todos))
	}
}

func TestTaskSyncPayloadTaskUpdate(t *testing.T) {
	hookJSON := `{"tool_name":"TaskUpdate","tool_input":{"id":"task-abc-123","status":"completed","description":"All done"}}`

	var hookInput struct {
		ToolName  string         `json:"tool_name"`
		ToolInput map[string]any `json:"tool_input"`
	}
	if err := json.Unmarshal([]byte(hookJSON), &hookInput); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	payload := taskSyncPayload{
		AgentID:   "agent-1",
		ToolName:  hookInput.ToolName,
		ToolInput: hookInput.ToolInput,
	}

	if payload.ToolName != "TaskUpdate" {
		t.Errorf("tool_name: got %q, want %q", payload.ToolName, "TaskUpdate")
	}
	if payload.ToolInput["id"] != "task-abc-123" {
		t.Errorf("id: got %v, want %q", payload.ToolInput["id"], "task-abc-123")
	}
	if payload.ToolInput["status"] != "completed" {
		t.Errorf("status: got %v, want %q", payload.ToolInput["status"], "completed")
	}
}

func TestTaskSyncPayloadEmptyToolName(t *testing.T) {
	hookJSON := `{"tool_name":"","tool_input":{}}`

	var hookInput struct {
		ToolName  string         `json:"tool_name"`
		ToolInput map[string]any `json:"tool_input"`
	}
	if err := json.Unmarshal([]byte(hookJSON), &hookInput); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if hookInput.ToolName != "" {
		t.Errorf("expected empty tool_name, got %q", hookInput.ToolName)
	}
}
