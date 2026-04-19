package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerWorktreeTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	// =========================================================================
	// Git Worktree Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_worktree_allocate",
		Description: "Allocate a git worktree for an agent. Creates a new branch and worktree directory.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent identifier.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Current session ID.",
				},
				"branch_name": map[string]any{
					"type":        "string",
					"description": "Branch name to create.",
				},
				"base_branch": map[string]any{
					"type":        "string",
					"description": "Base branch/commit (default: HEAD).",
				},
				"purpose": map[string]any{
					"type":        "string",
					"description": "Purpose of this worktree.",
				},
				"worktree_path": map[string]any{
					"type":        "string",
					"description": "Custom worktree path (default: auto-generated).",
				},
				"repo_path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the git repository to allocate from. Overrides session.working_dir and AGENT_CONTEXT_GIT_REPO_PATH/REPO_PATH.",
				},
				"ttl_hours": map[string]any{
					"type":        "integer",
					"description": "Max lifetime in hours. Worktree is auto-orphaned after this period (0 = no limit).",
				},
			},
			Required: []string{"agent_id", "session_id", "branch_name"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorktreeAllocate(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_worktree_release",
		Description: "Release a worktree assignment. Optionally removes the worktree from disk.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"assignment_id": map[string]any{
					"type":        "string",
					"description": "Worktree assignment ID.",
				},
				"remove_worktree": map[string]any{
					"type":        "boolean",
					"description": "Remove worktree directory from disk (default: false).",
				},
				"force": map[string]any{
					"type":        "boolean",
					"description": "Force removal even with uncommitted changes (default: false).",
				},
			},
			Required: []string{"assignment_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorktreeRelease(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_worktree_list",
		Description: "List worktree assignments, optionally filtered by agent or status.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by agent ID.",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"active", "released", "orphaned"},
					"description": "Filter by status.",
				},
				"include_git_status": map[string]any{
					"type":        "boolean",
					"description": "Include git status for active worktrees (default: false).",
				},
				"include_disk_usage": map[string]any{
					"type":        "boolean",
					"description": "Include disk usage per worktree and total (default: false).",
				},
				"include_untracked": map[string]any{
					"type":        "boolean",
					"description": "Detect and include worktrees not tracked by agent-context (default: false).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorktreeList(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_worktree_status",
		Description: "Get detailed status of a worktree assignment including git info.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"assignment_id": map[string]any{
					"type":        "string",
					"description": "Worktree assignment ID.",
				},
			},
			Required: []string{"assignment_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorktreeStatus(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_worktree_cleanup",
		Description: "Clean up orphaned worktrees. Use dry_run=true (default) to preview.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"dry_run": map[string]any{
					"type":        "boolean",
					"description": "Preview what would be cleaned up (default: true).",
				},
				"force": map[string]any{
					"type":        "boolean",
					"description": "Force removal of worktrees with uncommitted changes.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorktreeCleanup(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_worktree_reconcile",
		Description: "Manually trigger worktree lifecycle reconciliation: disk scan, TTL expiration, orphan removal, untracked detection, and git prune.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorktreeReconcile(ctx, args)
	})
}
