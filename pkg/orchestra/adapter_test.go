package orchestra

import (
	"context"
	"testing"

	"github.com/crb2nu/loom/pkg/openairesponses"
)

// fakeToolLister provides test tools.
type fakeToolLister struct {
	tools []ToolInfo
}

func (f *fakeToolLister) ListTools() ([]ToolInfo, error) {
	return f.tools, nil
}

func TestSubAgentAdapter_BuildTools_Filters(t *testing.T) {
	lister := &fakeToolLister{
		tools: []ToolInfo{
			{Name: "git__git_status", Description: "Git status"},
			{Name: "git__git_diff", Description: "Git diff"},
			{Name: "k8s__k8s_getPods", Description: "K8s pods"},
			{Name: "prometheus__query", Description: "Prometheus query"},
		},
	}

	agent := SubAgent{
		Name:  "codebase",
		Tools: []string{"git__git_status", "git__git_diff"},
	}

	adapter := NewSubAgentAdapter(agent, lister)
	tools, err := adapter.BuildTools(context.Background(), openairesponses.ExecutionIdentity{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}

	if !names["git__git_status"] || !names["git__git_diff"] {
		t.Errorf("expected git tools, got %v", names)
	}
	if names["k8s__k8s_getPods"] || names["prometheus__query"] {
		t.Error("unexpected tools included")
	}
}

func TestSubAgentAdapter_BuildTools_NoMatch(t *testing.T) {
	lister := &fakeToolLister{
		tools: []ToolInfo{
			{Name: "git__git_status", Description: "Git status"},
		},
	}

	agent := SubAgent{
		Name:  "observability",
		Tools: []string{"prometheus__query"},
	}

	adapter := NewSubAgentAdapter(agent, lister)
	tools, err := adapter.BuildTools(context.Background(), openairesponses.ExecutionIdentity{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(tools))
	}
}

func TestSubAgentAdapter_ResolveCall_Passthrough(t *testing.T) {
	adapter := NewSubAgentAdapter(SubAgent{}, &fakeToolLister{})

	call := openairesponses.ToolCall{
		CallID:   "call-1",
		ToolName: "git__git_status",
	}

	resolved, err := adapter.ResolveCall(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.ToolName != call.ToolName {
		t.Errorf("expected passthrough, got %q", resolved.ToolName)
	}
}
