package main

import (
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestRegisterTools_RegistersCoreAgentToolFamilies(t *testing.T) {
	t.Parallel()

	server := mcp.NewServer("test", "test")
	tracer := noop.NewTracerProvider().Tracer("test")

	// Nil service is safe here because this test only validates tool registration.
	registerTools(server, nil, tracer)

	tools := server.Tools()
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
