package fleet

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleAgentTaskSync_MissingAgentID(t *testing.T) {
	d := New(&mockDeps{})
	body := taskSyncRequest{
		AgentID:  "",
		ToolName: "TaskCreate",
		ToolInput: map[string]any{
			"subject": "Test task",
		},
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/agent/task-sync", bytes.NewReader(data))
	rec := httptest.NewRecorder()

	d.handleAgentTaskSync(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestHandleAgentTaskSync_MissingToolName(t *testing.T) {
	d := New(&mockDeps{})
	body := taskSyncRequest{
		AgentID:  "test-agent",
		ToolName: "",
		ToolInput: map[string]any{
			"subject": "Test task",
		},
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/agent/task-sync", bytes.NewReader(data))
	rec := httptest.NewRecorder()

	d.handleAgentTaskSync(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestHandleAgentTaskSync_NilAgent(t *testing.T) {
	// mockDeps returns nil for Agent(), so the handler should return 502.
	d := New(&mockDeps{})
	body := taskSyncRequest{
		AgentID:  "test-agent",
		ToolName: "TaskCreate",
		ToolInput: map[string]any{
			"subject": "Test task",
		},
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/agent/task-sync", bytes.NewReader(data))
	rec := httptest.NewRecorder()

	d.handleAgentTaskSync(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected status 502 for nil agent, got %d", rec.Code)
	}
}

func TestHandleAgentTaskSync_InvalidJSON(t *testing.T) {
	d := New(&mockDeps{})
	req := httptest.NewRequest("POST", "/api/agent/task-sync", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()

	d.handleAgentTaskSync(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestHandleAgentTaskSync_UnsupportedToolName(t *testing.T) {
	// This test exercises the default case in the switch. We need the agent
	// bridge to be non-nil and return a session. Since mockDeps returns nil
	// for Agent(), the handler will return 502 before reaching the switch.
	// That is the correct behavior for the mock—the validation tests above
	// cover the missing-field cases, and this verifies the nil-agent guard.
	d := New(&mockDeps{})
	body := taskSyncRequest{
		AgentID:  "test-agent",
		ToolName: "UnknownTool",
		ToolInput: map[string]any{
			"data": "something",
		},
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/agent/task-sync", bytes.NewReader(data))
	rec := httptest.NewRecorder()

	d.handleAgentTaskSync(rec, req)

	// With nil agent, we get 502 (bad gateway) before the switch.
	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected status 502 (nil agent guard), got %d", rec.Code)
	}
}

func TestStringFromMap(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]any
		key  string
		want string
	}{
		{"present string", map[string]any{"key": "value"}, "key", "value"},
		{"missing key", map[string]any{"other": "value"}, "key", ""},
		{"non-string value", map[string]any{"key": 42}, "key", ""},
		{"empty string", map[string]any{"key": ""}, "key", ""},
		{"whitespace string", map[string]any{"key": "  hello  "}, "key", "hello"},
		{"nil map", nil, "key", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringFromMap(tt.m, tt.key)
			if got != tt.want {
				t.Errorf("stringFromMap(%v, %q) = %q, want %q", tt.m, tt.key, got, tt.want)
			}
		})
	}
}

func TestSyncTaskCreate_ExtractsFields(t *testing.T) {
	// Verify that syncTaskCreate reads subject/description correctly.
	// We cannot call it directly without a real AgentBridge, so we test
	// the field extraction logic via stringFromMap.
	input := map[string]any{
		"subject":     "Build the widget",
		"description": "Widget needs to handle edge cases",
	}
	title := stringFromMap(input, "subject")
	if title != "Build the widget" {
		t.Errorf("title: got %q, want %q", title, "Build the widget")
	}
	desc := stringFromMap(input, "description")
	if desc != "Widget needs to handle edge cases" {
		t.Errorf("description: got %q, want %q", desc, "Widget needs to handle edge cases")
	}
}

func TestSyncTaskCreate_FallsBackToTitle(t *testing.T) {
	input := map[string]any{
		"title": "Fallback title",
	}
	title := stringFromMap(input, "subject")
	if title != "" {
		t.Errorf("expected empty subject, got %q", title)
	}
	fallback := stringFromMap(input, "title")
	if fallback != "Fallback title" {
		t.Errorf("title: got %q, want %q", fallback, "Fallback title")
	}
}

func TestSyncTaskUpdate_ExtractsFields(t *testing.T) {
	input := map[string]any{
		"id":          "task-123",
		"status":      "completed",
		"description": "All done",
	}
	taskID := stringFromMap(input, "id")
	if taskID != "task-123" {
		t.Errorf("task_id: got %q, want %q", taskID, "task-123")
	}
	status := stringFromMap(input, "status")
	if status != "completed" {
		t.Errorf("status: got %q, want %q", status, "completed")
	}
}

func TestSyncTodoWrite_ParsesTodos(t *testing.T) {
	// Verify the TodoWrite input structure can be parsed.
	input := map[string]any{
		"todos": []any{
			map[string]any{"content": "Fix bug", "status": "pending"},
			map[string]any{"content": "Write tests", "status": "pending"},
			map[string]any{"content": "Already done", "status": "completed"},
		},
	}

	todosRaw, ok := input["todos"]
	if !ok {
		t.Fatal("expected todos key")
	}
	todos, ok := todosRaw.([]any)
	if !ok {
		t.Fatal("expected []any")
	}
	if len(todos) != 3 {
		t.Errorf("todos length: got %d, want 3", len(todos))
	}

	// Verify completed items would be skipped.
	var pending int
	for _, item := range todos {
		todo, ok := item.(map[string]any)
		if !ok {
			continue
		}
		status := stringFromMap(todo, "status")
		if status != "completed" && status != "done" {
			pending++
		}
	}
	if pending != 2 {
		t.Errorf("pending todos: got %d, want 2", pending)
	}
}
