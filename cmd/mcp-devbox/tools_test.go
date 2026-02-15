package main

import (
	"path/filepath"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestRegisterTools_RegistersExpectedToolset(t *testing.T) {
	t.Parallel()

	server := mcp.NewServer("test", "test")
	registerTools(server, &manager{}, noop.NewTracerProvider().Tracer("test"))

	tools := server.Tools()
	if len(tools) != 11 {
		t.Fatalf("tool count = %d, want 11", len(tools))
	}

	expected := []string{
		"devbox_exec",
		"devbox_build",
		"devbox_status",
		"devbox_stop",
		"devbox_detect",
		"devbox_read_file",
		"devbox_write_file",
		"devbox_exec_async",
		"devbox_exec_poll",
		"devbox_metrics",
		"devbox_summary",
	}

	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if seen[tool.Name] {
			t.Fatalf("duplicate tool registered: %s", tool.Name)
		}
		seen[tool.Name] = true
	}
	for _, name := range expected {
		if !seen[name] {
			t.Errorf("expected tool %q to be registered", name)
		}
	}

	execTool, ok := findToolByName(tools, "devbox_exec")
	if !ok {
		t.Fatal("devbox_exec not found")
	}
	if !contains(execTool.InputSchema.Required, "project") || !contains(execTool.InputSchema.Required, "command") {
		t.Fatalf("devbox_exec required fields = %v, want project+command", execTool.InputSchema.Required)
	}
}

func TestProjectWorkDir_UsesWorkspaceRelativePath(t *testing.T) {
	t.Parallel()

	workspace := filepath.Join("/tmp", "workspace-root")
	m := &manager{cfg: managerConfig{workspaceRoot: workspace}}

	inside := filepath.Join(workspace, "services", "loom-core")
	if got := m.projectWorkDir(inside); got != filepath.Join("/workspace", "services", "loom-core") {
		t.Fatalf("projectWorkDir(inside) = %q", got)
	}

	outside := "/opt/other-repo"
	if got := m.projectWorkDir(outside); got != "/workspace" {
		t.Fatalf("projectWorkDir(outside) = %q, want /workspace", got)
	}
}

func TestValidateMountPath_RejectsPathOutsideAllowlist(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	m := &manager{cfg: managerConfig{workspaceRoot: workspace}}

	inside := filepath.Join(workspace, "repo")
	if err := m.validateMountPath(inside); err != nil {
		t.Fatalf("validateMountPath(inside) returned error: %v", err)
	}

	outside := filepath.Dir(workspace)
	if err := m.validateMountPath(outside); err == nil {
		t.Fatalf("validateMountPath(outside=%q) expected error", outside)
	}
}

func findToolByName(tools []mcp.Tool, name string) (mcp.Tool, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return mcp.Tool{}, false
}

func contains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}
