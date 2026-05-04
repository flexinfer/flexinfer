package clients

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mills/pipeline"
)

func wtStub(t *testing.T, body any) []byte {
	t.Helper()
	bodyJSON, _ := json.Marshal(body)
	res := mcp.CallToolResult{Content: []mcp.Content{{Type: "text", Text: string(bodyJSON)}}}
	out, _ := json.Marshal(res)
	return out
}

// routedTransport is a fake mcp.Transport that picks its response based
// on the tool name embedded in the tools/call params (so a single
// transport can serve allocate + list + release in sequence).
type routedTransport struct {
	*fakeTransport
	byTool map[string][]byte
}

func newRoutedTransport(byTool map[string][]byte) *routedTransport {
	return &routedTransport{
		fakeTransport: &fakeTransport{
			responses: map[string][]byte{
				"initialize": []byte(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"x","version":"1"}}`),
			},
		},
		byTool: byTool,
	}
}

func (r *routedTransport) Send(ctx context.Context, msg *mcp.Message) error {
	if msg.Method == "tools/call" {
		var params mcp.CallToolParams
		_ = json.Unmarshal(msg.Params, &params)
		if body, ok := r.byTool[params.Name]; ok {
			r.mu.Lock()
			r.responses["tools/call"] = body
			r.mu.Unlock()
		}
	}
	return r.fakeTransport.Send(ctx, msg)
}

func newRoutedHub(t *testing.T, byTool map[string][]byte) (*MCPHubClient, *routedTransport) {
	t.Helper()
	rt := newRoutedTransport(byTool)
	c := newMCPHubClientWithDefaults(MCPHubConfig{
		HubURL: "wss://stub",
	}, func(_ context.Context, _ string) (mcp.Transport, error) {
		return rt, nil
	})
	return c, rt
}

func TestWorktreeAllocator_Allocate_HappyPath(t *testing.T) {
	hub, rt := newRoutedHub(t, map[string][]byte{
		"agent_worktree_allocate": wtStub(t, map[string]any{
			"assignment_id": "asg-001",
			"worktree_path": "/workspaces/loom-core/.worktrees/feat-x-alpha",
			"branch":        "feat/BL-X/alpha",
			"base_branch":   "main",
		}),
	})
	a := NewWorktreeAllocator(hub, "loom-mills-operator", "session-op-1", "/workspaces/loom-core")
	got, err := a.Allocate(context.Background(), pipeline.WorktreeRequest{
		BacklogID: "BL-X",
		SliceName: "alpha",
		Purpose:   "mills parallel slice alpha",
	})
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if got.Branch != "feat/BL-X/alpha" {
		t.Errorf("branch = %q", got.Branch)
	}
	if !strings.Contains(got.Path, "feat-x-alpha") {
		t.Errorf("path = %q", got.Path)
	}
	sent := rt.sentMessages()
	var params mcp.CallToolParams
	for _, m := range sent {
		if m.Method == "tools/call" {
			_ = json.Unmarshal(m.Params, &params)
		}
	}
	if params.Arguments["agent_id"] != "loom-mills-operator" {
		t.Errorf("agent_id = %v", params.Arguments["agent_id"])
	}
	if params.Arguments["session_id"] != "session-op-1" {
		t.Errorf("session_id = %v", params.Arguments["session_id"])
	}
	if params.Arguments["branch_name"] != "feat/BL-X/alpha" {
		t.Errorf("branch_name = %v", params.Arguments["branch_name"])
	}
	if params.Arguments["repo_path"] != "/workspaces/loom-core" {
		t.Errorf("repo_path = %v", params.Arguments["repo_path"])
	}
	if params.Arguments["base_branch"] != "main" {
		t.Errorf("base_branch should default to main, got %v", params.Arguments["base_branch"])
	}
}

func TestWorktreeAllocator_Allocate_BaseBranchOverride(t *testing.T) {
	hub, rt := newRoutedHub(t, map[string][]byte{
		"agent_worktree_allocate": wtStub(t, map[string]any{
			"assignment_id": "asg-002",
			"worktree_path": "/wt", "branch": "feat/BL-Y/x", "base_branch": "develop",
		}),
	})
	a := NewWorktreeAllocator(hub, "agent", "session", "/repo")
	if _, err := a.Allocate(context.Background(), pipeline.WorktreeRequest{
		BacklogID: "BL-Y", SliceName: "x", BaseBranch: "develop",
	}); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	var params mcp.CallToolParams
	for _, m := range rt.sentMessages() {
		if m.Method == "tools/call" {
			_ = json.Unmarshal(m.Params, &params)
		}
	}
	if params.Arguments["base_branch"] != "develop" {
		t.Errorf("base_branch override not applied: %v", params.Arguments["base_branch"])
	}
}

func TestWorktreeAllocator_Allocate_RequiresAgentSessionAndSlice(t *testing.T) {
	cases := []struct {
		name          string
		mutator       func(*WorktreeAllocator, *pipeline.WorktreeRequest)
		expectError   bool
		failureReason string
	}{
		{"missing agent", func(a *WorktreeAllocator, _ *pipeline.WorktreeRequest) { a.AgentID = "" }, true, "agent"},
		{"missing session", func(a *WorktreeAllocator, _ *pipeline.WorktreeRequest) { a.SourceSessionID = "" }, true, "session"},
		{"missing slice", func(_ *WorktreeAllocator, r *pipeline.WorktreeRequest) { r.SliceName = "" }, true, "slice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub, _ := newRoutedHub(t, map[string][]byte{})
			a := NewWorktreeAllocator(hub, "ag", "ses", "/r")
			req := pipeline.WorktreeRequest{BacklogID: "BL", SliceName: "s"}
			tc.mutator(a, &req)
			if _, err := a.Allocate(context.Background(), req); (err != nil) != tc.expectError {
				t.Errorf("expectError=%v got err=%v", tc.expectError, err)
			}
		})
	}
}

func TestWorktreeAllocator_Allocate_IncompletePayloadErrors(t *testing.T) {
	hub, _ := newRoutedHub(t, map[string][]byte{
		"agent_worktree_allocate": wtStub(t, map[string]any{"assignment_id": "x"}),
	})
	a := NewWorktreeAllocator(hub, "ag", "ses", "/r")
	if _, err := a.Allocate(context.Background(), pipeline.WorktreeRequest{BacklogID: "B", SliceName: "s"}); err == nil {
		t.Error("expected error for missing path/branch in response")
	}
}

func TestWorktreeAllocator_Release_MatchesByBranch(t *testing.T) {
	listBody := wtStub(t, map[string]any{
		"worktrees": []map[string]any{
			{"assignment_id": "asg-A", "branch": "feat/BL-X/alpha"},
			{"assignment_id": "asg-B", "branch": "feat/BL-X/beta"},
		},
	})
	releaseBody := wtStub(t, map[string]any{
		"assignment_id": "asg-B", "removed": true, "status": "released",
	})
	hub, rt := newRoutedHub(t, map[string][]byte{
		"agent_worktree_list":    listBody,
		"agent_worktree_release": releaseBody,
	})
	a := NewWorktreeAllocator(hub, "ag", "ses", "/r")
	if err := a.Release(context.Background(), pipeline.WorktreeHandle{
		Path: "/wt", Branch: "feat/BL-X/beta",
	}); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Find the release call and confirm assignment_id resolved correctly.
	var releaseParams mcp.CallToolParams
	for _, m := range rt.sentMessages() {
		if m.Method != "tools/call" {
			continue
		}
		var params mcp.CallToolParams
		_ = json.Unmarshal(m.Params, &params)
		if params.Name == "agent_worktree_release" {
			releaseParams = params
		}
	}
	if releaseParams.Arguments["assignment_id"] != "asg-B" {
		t.Errorf("release assignment_id = %v, want asg-B", releaseParams.Arguments["assignment_id"])
	}
	if releaseParams.Arguments["remove_worktree"] != true {
		t.Errorf("remove_worktree = %v, want true", releaseParams.Arguments["remove_worktree"])
	}
}

func TestWorktreeAllocator_Release_BareArrayFallback(t *testing.T) {
	// Some hub deployments wrap list responses as a bare array.
	listBody := wtStub(t, []map[string]any{
		{"assignment_id": "asg-1", "branch": "feat/x/a"},
	})
	releaseBody := wtStub(t, map[string]any{"assignment_id": "asg-1", "status": "released"})
	hub, _ := newRoutedHub(t, map[string][]byte{
		"agent_worktree_list":    listBody,
		"agent_worktree_release": releaseBody,
	})
	a := NewWorktreeAllocator(hub, "ag", "ses", "/r")
	if err := a.Release(context.Background(), pipeline.WorktreeHandle{Branch: "feat/x/a"}); err != nil {
		t.Errorf("release should fall back to bare-array decode: %v", err)
	}
}

func TestWorktreeAllocator_Release_NoMatchErrors(t *testing.T) {
	listBody := wtStub(t, map[string]any{
		"worktrees": []map[string]any{{"assignment_id": "x", "branch": "feat/y/z"}},
	})
	hub, _ := newRoutedHub(t, map[string][]byte{
		"agent_worktree_list": listBody,
	})
	a := NewWorktreeAllocator(hub, "ag", "ses", "/r")
	if err := a.Release(context.Background(), pipeline.WorktreeHandle{Branch: "feat/missing/branch"}); err == nil {
		t.Error("expected error when no assignment matches branch")
	}
}

func TestWorktreeAllocator_Release_RequiresBranch(t *testing.T) {
	hub, _ := newRoutedHub(t, map[string][]byte{})
	a := NewWorktreeAllocator(hub, "ag", "ses", "/r")
	if err := a.Release(context.Background(), pipeline.WorktreeHandle{Path: "/wt"}); err == nil {
		t.Error("expected error when handle.Branch empty")
	}
}
