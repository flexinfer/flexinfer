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
		"agent_recall",
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

	if _, ok := tool.InputSchema.Properties["summary_async"]; !ok {
		t.Error("expected summary_async property in session end schema")
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

func TestRemovedMemoryLifecycleToolsAreGone(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	removed := []string{
		"agent_memory_promote",
		"agent_memory_demote",
		"agent_memory_compress",
		"agent_memory_merge",
		"agent_memory_policy_set",
	}
	for _, name := range removed {
		if tool := toolByName(tools, name); tool != nil {
			t.Errorf("tool %q should have been removed (SIMP-2)", name)
		}
	}

	// agent_memory_policy_get should still be present (read-only introspection)
	if tool := toolByName(tools, "agent_memory_policy_get"); tool == nil {
		t.Error("agent_memory_policy_get should be retained as read-only")
	}
}

func TestRemovedCompactionToolsAreGone(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	removed := []string{
		"agent_compaction_status",
		"agent_compaction_trigger",
		"agent_reconcile_trigger",
	}
	for _, name := range removed {
		if tool := toolByName(tools, name); tool != nil {
			t.Errorf("tool %q should have been removed (SIMP-6)", name)
		}
	}
}

func TestRemovedTemplateToolsAreGone(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	removed := []string{"agent_template_create", "agent_template_list"}
	for _, name := range removed {
		if tool := toolByName(tools, name); tool != nil {
			t.Errorf("tool %q should have been removed (SIMP-7)", name)
		}
	}
}

func TestRemovedLowUtilityContextToolsAreGone(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	removed := []string{
		"agent_context_get",
		"agent_context_delete",
		"agent_context_share",
		"agent_context_query_shared",
		"agent_context_link_codebase",
		"agent_context_stats",
	}
	for _, name := range removed {
		if tool := toolByName(tools, name); tool != nil {
			t.Errorf("tool %q should have been removed (SIMP-8)", name)
		}
	}

	// Core context tools should still be present
	retained := []string{
		"agent_context_add", "agent_context_search",
		"agent_context_recall", "agent_context_summarize",
		"agent_context_recall_enhanced", "agent_recall",
	}
	for _, name := range retained {
		if tool := toolByName(tools, name); tool == nil {
			t.Errorf("core context tool %q should still be registered", name)
		}
	}
}

func TestRemovedMemoryExportImportToolsAreGone(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	removed := []string{"agent_memory_export", "agent_memory_import"}
	for _, name := range removed {
		if tool := toolByName(tools, name); tool != nil {
			t.Errorf("tool %q should have been removed (SIMP-3)", name)
		}
	}
}

func TestRemovedGraphToolsAreGone(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	removed := []string{
		"agent_graph_find_path",
		"agent_reasoning_chain_add",
		"agent_reasoning_chain_get",
		"agent_reasoning_chain_list",
	}
	for _, name := range removed {
		if tool := toolByName(tools, name); tool != nil {
			t.Errorf("tool %q should have been removed (SIMP-4)", name)
		}
	}

	// Core graph tools should still be present
	retained := []string{
		"agent_entity_add", "agent_entity_get", "agent_entity_find",
		"agent_entity_delete", "agent_relation_add", "agent_relation_get",
		"agent_relation_delete", "agent_graph_query", "agent_graph_stats",
	}
	for _, name := range retained {
		if tool := toolByName(tools, name); tool == nil {
			t.Errorf("core graph tool %q should still be registered", name)
		}
	}
}

func TestUnifiedRecallSchema_HasScopeAndQuery(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	tool := toolByName(tools, "agent_recall")
	if tool == nil {
		t.Fatal("agent_recall not found")
	}

	if tool.Description == "" {
		t.Error("expected non-empty description")
	}

	props := tool.InputSchema.Properties
	for _, field := range []string{"query", "scope", "agent_id", "token_budget", "file_context", "memory_tiers"} {
		if _, ok := props[field]; !ok {
			t.Errorf("expected property %q in agent_recall schema", field)
		}
	}

	if len(tool.InputSchema.Required) == 0 {
		t.Error("expected query to be required")
	}
	found := false
	for _, r := range tool.InputSchema.Required {
		if r == "query" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'query' in required fields")
	}
}

func TestDeprecatedRecallToolsHaveDeprecationNotice(t *testing.T) {
	t.Parallel()
	_, tools := testServer()

	deprecated := []string{"agent_context_recall", "agent_context_recall_enhanced", "agent_memory_recall"}
	for _, name := range deprecated {
		tool := toolByName(tools, name)
		if tool == nil {
			t.Errorf("deprecated tool %q should still be registered", name)
			continue
		}
		if len(tool.Description) < 12 || tool.Description[:12] != "[Deprecated:" {
			t.Errorf("tool %q should have [Deprecated:] prefix, got %q", name, tool.Description[:30])
		}
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
