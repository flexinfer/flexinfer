package main

import (
	"fmt"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestFilterProxyTools_AntigravityCoreDefaults(t *testing.T) {
	tools := make([]mcp.Tool, 0, 160)
	for i := 0; i < 130; i++ {
		tools = append(tools, mcp.Tool{Name: fmt.Sprintf("misc__tool_%03d", i)})
	}

	// Include several core tools that should survive profile filtering.
	tools = append(tools,
		mcp.Tool{Name: "git__git_status"},
		mcp.Tool{Name: "gitlab__pipeline_summary"},
		mcp.Tool{Name: "codebase_memory__codebase_search"},
		mcp.Tool{Name: "quality__quality_check"},
		mcp.Tool{Name: "agent_context__agent_session_start"},
		mcp.Tool{Name: "agent_context__agent_session_list"},
		mcp.Tool{Name: "agent_context__agent_recall"},
		mcp.Tool{Name: "agent_context__agent_handoff_create"},
		mcp.Tool{Name: "agent_context__agent_handoff_accept"},
		mcp.Tool{Name: "agent_context__agent_handoff_inbox"},
		mcp.Tool{Name: "agent_context__agent_worktree_allocate"},
		mcp.Tool{Name: "k8s_apps_k3s__k8s_getPods"},
		mcp.Tool{Name: "prometheus__query"},
		mcp.Tool{Name: "loki__loki_query"},
	)

	filtered := filterProxyTools(tools, "antigravity", "", 0)
	if len(filtered) != proxyToolLimitAntigravity {
		t.Fatalf("filtered tools = %d, want %d", len(filtered), proxyToolLimitAntigravity)
	}

	got := make(map[string]struct{}, len(filtered))
	for _, tool := range filtered {
		got[tool.Name] = struct{}{}
	}

	for _, want := range []string{
		"git__git_status",
		"gitlab__pipeline_summary",
		"codebase_memory__codebase_search",
		"quality__quality_check",
		"agent_context__agent_session_start",
		"agent_context__agent_session_list",
		"agent_context__agent_recall",
		"agent_context__agent_handoff_create",
		"agent_context__agent_handoff_accept",
		"agent_context__agent_handoff_inbox",
		"agent_context__agent_worktree_allocate",
		"k8s_apps_k3s__k8s_getPods",
		"prometheus__query",
		"loki__loki_query",
	} {
		if _, ok := got[want]; !ok {
			t.Fatalf("expected core tool %q in filtered set", want)
		}
	}
}

func TestFilterProxyTools_MaxToolsOnly(t *testing.T) {
	tools := []mcp.Tool{
		{Name: "git__git_status"},
		{Name: "git__git_diff"},
		{Name: "git__git_log"},
		{Name: "git__git_show"},
	}

	filtered := filterProxyTools(tools, "", "", 2)
	if len(filtered) != 2 {
		t.Fatalf("filtered length = %d, want 2", len(filtered))
	}
	if filtered[0].Name != "git__git_status" || filtered[1].Name != "git__git_diff" {
		t.Fatalf("unexpected filtered tools: %#v", filtered)
	}
}
