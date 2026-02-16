package main

import (
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/trace/noop"
)

func testServer() (*mcp.Server, []mcp.Tool) {
	server := mcp.NewServer("test", "test")
	tracer := noop.NewTracerProvider().Tracer("test")
	registerTools(server, nil, tracer)
	return server, server.Tools()
}

func toolByName(tools []mcp.Tool, name string) *mcp.Tool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

func TestRegisterTools_RegistersCoreAgentToolFamilies(t *testing.T) {
	t.Parallel()

	_, tools := testServer()
	if len(tools) < 70 {
		t.Fatalf("tool count = %d, want >= 70", len(tools))
	}

	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if seen[tool.Name] {
			t.Fatalf("duplicate tool registered: %s", tool.Name)
		}
		seen[tool.Name] = true
	}

	expected := []string{
		"agent_session_start",
		"agent_session_end",
		"agent_context_add",
		"agent_context_recall_enhanced",
		"agent_task_add",
		"agent_task_update",
		"agent_presence_register",
		"agent_presence_heartbeat",
		"agent_memory_recall",
		"agent_workflow_define",
		"agent_workflow_start",
		"agent_file_claim_acquire",
		"agent_worktree_allocate",
		"agent_handoff_create",
		"agent_compaction_status",
	}

	for _, name := range expected {
		if !seen[name] {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestSessionStartSchema_HasRequiredFields(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	tool := toolByName(tools, "agent_session_start")
	if tool == nil {
		t.Fatal("agent_session_start not found")
	}

	if tool.Description == "" {
		t.Error("expected non-empty description")
	}

	props := tool.InputSchema.Properties
	for _, field := range []string{"agent_id", "namespace", "description"} {
		if _, ok := props[field]; !ok {
			t.Errorf("expected property %q in schema", field)
		}
	}
}

func TestSessionEndSchema_RequiresSessionID(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	tool := toolByName(tools, "agent_session_end")
	if tool == nil {
		t.Fatal("agent_session_end not found")
	}

	if len(tool.InputSchema.Required) == 0 {
		t.Error("expected session_id to be required")
	}

	found := false
	for _, r := range tool.InputSchema.Required {
		if r == "session_id" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'session_id' in required fields, got %v", tool.InputSchema.Required)
	}
}

func TestContextAddSchema_HasEntries(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	tool := toolByName(tools, "agent_context_add")
	if tool == nil {
		t.Fatal("agent_context_add not found")
	}

	props := tool.InputSchema.Properties
	if _, ok := props["session_id"]; !ok {
		t.Error("expected session_id property")
	}
	if _, ok := props["entries"]; !ok {
		t.Error("expected entries property")
	}
}

func TestTaskAddSchema_HasTasksArray(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	tool := toolByName(tools, "agent_task_add")
	if tool == nil {
		t.Fatal("agent_task_add not found")
	}

	props := tool.InputSchema.Properties
	if _, ok := props["session_id"]; !ok {
		t.Error("expected session_id property")
	}
	if _, ok := props["tasks"]; !ok {
		t.Error("expected tasks property")
	}
}

func TestTaskUpdateSchema_HasStatusField(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	tool := toolByName(tools, "agent_task_update")
	if tool == nil {
		t.Fatal("agent_task_update not found")
	}

	props := tool.InputSchema.Properties
	if _, ok := props["task_id"]; !ok {
		t.Error("expected task_id property")
	}
	if _, ok := props["status"]; !ok {
		t.Error("expected status property")
	}
}

func TestMemoryAddSchema_HasItems(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	tool := toolByName(tools, "agent_memory_add")
	if tool == nil {
		t.Fatal("agent_memory_add not found")
	}

	props := tool.InputSchema.Properties
	if _, ok := props["items"]; !ok {
		t.Error("expected items property")
	}
}

func TestAllToolsHaveDescriptions(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	for _, tool := range tools {
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
	}
}

func TestAllToolsHaveObjectSchema(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	for _, tool := range tools {
		if tool.InputSchema.Type != "object" {
			t.Errorf("tool %q has schema type %q, expected 'object'", tool.Name, tool.InputSchema.Type)
		}
	}
}
