package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"
)

func registerTools(server *mcp.Server, svc *agentcontext.Service) {
	// Session Management Tools

	server.AddTool(mcp.Tool{
		Name:        "agent_session_start",
		Description: "Start a new agent context session. Returns session_id for subsequent calls. Use resume_session_id to continue an existing session. After starting, check agent_handoff_inbox for pending work from other agents.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Unique agent identifier (e.g., 'claude-code-1'). Required unless AGENT_CONTEXT_DEFAULT_AGENT_ID is set.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Optional project/task namespace for grouping sessions.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Optional session description.",
				},
				"working_dir": map[string]any{
					"type":        "string",
					"description": "Optional working directory path.",
				},
				"resume_session_id": map[string]any{
					"type":        "string",
					"description": "Resume an existing session instead of creating a new one.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleSessionStart(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_session_end",
		Description: "End an agent session. Optionally triggers summarization of session context. Accepts cleanup=true (default) to auto-release file claims, deregister presence, and mark worktrees as orphaned.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID to end.",
				},
				"summarize": map[string]any{
					"type":        "boolean",
					"description": "Generate session summary on end (default: true).",
				},
				"cleanup": map[string]any{
					"type":        "boolean",
					"description": "Auto-release file claims, deregister presence, and mark worktrees as orphaned (default: true).",
				},
			},
			Required: []string{"session_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleSessionEnd(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_session_list",
		Description: "List sessions for an agent, optionally filtered by namespace or status.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID to list sessions for.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"active", "ended", "summarized"},
					"description": "Filter by session status.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum sessions to return (default: 20).",
				},
			},
			Required: []string{"agent_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleSessionList(ctx, args)
	})

	// Context Storage Tools

	server.AddTool(mcp.Tool{
		Name:        "agent_context_add",
		Description: "Add one or more context entries to a session. Each entry represents something the agent learned, decided, or read.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID to add context to.",
				},
				"entries": map[string]any{
					"type":        "array",
					"description": "Array of context entries to add.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"entry_type": map[string]any{
								"type":        "string",
								"enum":        []string{"file_read", "decision", "finding", "question", "note", "error", "code_context"},
								"description": "Type of context entry.",
							},
							"title": map[string]any{
								"type":        "string",
								"description": "Short descriptive title.",
							},
							"content": map[string]any{
								"type":        "string",
								"description": "Full content text.",
							},
							"file_path": map[string]any{
								"type":        "string",
								"description": "File path (for file_read entries).",
							},
							"line_start": map[string]any{
								"type":        "integer",
								"description": "Start line (for file_read entries).",
							},
							"line_end": map[string]any{
								"type":        "integer",
								"description": "End line (for file_read entries).",
							},
							"tags": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Tags for categorization.",
							},
							"metadata": map[string]any{
								"type":        "object",
								"description": "Additional structured metadata.",
							},
							"visibility": map[string]any{
								"type":        "string",
								"enum":        []string{"private", "shared", "public"},
								"description": "Who can access this entry (default: private).",
							},
							"shared_with": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Agent IDs to share with (for visibility=shared).",
							},
						},
						"required": []string{"entry_type", "title", "content"},
					},
				},
			},
			Required: []string{"session_id", "entries"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextAdd(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_context_get",
		Description: "Retrieve specific context entries by ID.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"entry_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entry IDs to retrieve.",
				},
			},
			Required: []string{"entry_ids"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextGet(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_context_delete",
		Description: "Delete context entries by ID. Requires confirm=true.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"entry_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entry IDs to delete.",
				},
				"confirm": map[string]any{
					"type":        "boolean",
					"description": "Must be true to confirm deletion.",
				},
			},
			Required: []string{"entry_ids", "confirm"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextDelete(ctx, args)
	})

	// Context Retrieval Tools

	server.AddTool(mcp.Tool{
		Name:        "agent_context_search",
		Description: "Semantic search across agent context entries. Returns entries most similar to the query.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query text.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter to specific agent.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter to specific session.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"entry_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by entry types.",
				},
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by tags (any match).",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "Filter to entries about a specific file.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results (default: 10).",
				},
				"include_content": map[string]any{
					"type":        "boolean",
					"description": "Include full content in results (default: true).",
				},
			},
			Required: []string{"query"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextSearch(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_context_recall",
		Description: "Intelligently recall relevant context for a task, optimized for token budget. Prioritizes decisions and summaries, then semantic matches.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "What are you trying to do? (used for relevance).",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter to specific agent.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter to specific session.",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Maximum tokens to return (default: 4000).",
				},
				"include_summaries": map[string]any{
					"type":        "boolean",
					"description": "Include session summaries (default: true).",
				},
				"include_decisions": map[string]any{
					"type":        "boolean",
					"description": "Prioritize decisions (default: true).",
				},
				"file_context": map[string]any{
					"type":        "string",
					"description": "Current file being worked on (for relevance boost).",
				},
			},
			Required: []string{"query"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextRecall(ctx, args)
	})

	// Cross-Agent Coordination Tools

	server.AddTool(mcp.Tool{
		Name:        "agent_context_share",
		Description: "Share context entries with other agents.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"entry_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entry IDs to share.",
				},
				"target_agents": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Agent IDs to share with.",
				},
				"visibility": map[string]any{
					"type":        "string",
					"enum":        []string{"shared", "public"},
					"description": "Visibility level (default: shared).",
				},
			},
			Required: []string{"entry_ids", "target_agents"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextShare(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_context_query_shared",
		Description: "Query context shared by other agents. Only returns entries explicitly shared with you or marked public.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query text.",
				},
				"requesting_agent_id": map[string]any{
					"type":        "string",
					"description": "Your agent ID (for access control).",
				},
				"source_agent_id": map[string]any{
					"type":        "string",
					"description": "Specific agent to query (or empty for all shared).",
				},
				"entry_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by entry types.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results (default: 10).",
				},
			},
			Required: []string{"query", "requesting_agent_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextQueryShared(ctx, args)
	})

	// Summarization Tool

	server.AddTool(mcp.Tool{
		Name:        "agent_context_summarize",
		Description: "Generate a summary of session context. Useful for long-running sessions or before ending a session.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID to summarize.",
				},
			},
			Required: []string{"session_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextSummarize(ctx, args)
	})

	// Codebase Integration Tool

	server.AddTool(mcp.Tool{
		Name:        "agent_context_link_codebase",
		Description: "Link agent context to codebase-memory entries. Creates a code_context entry referencing a specific file/symbol.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID.",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "File path being referenced.",
				},
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repository ID from codebase-memory.",
				},
				"symbol": map[string]any{
					"type":        "string",
					"description": "Symbol name (function, class, etc.).",
				},
				"note": map[string]any{
					"type":        "string",
					"description": "Context note about this code.",
				},
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Tags for categorization.",
				},
			},
			Required: []string{"session_id", "file_path"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextLinkCodebase(ctx, args)
	})

	// Statistics Tool

	server.AddTool(mcp.Tool{
		Name:        "agent_context_stats",
		Description: "Get statistics about agent context storage.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by agent ID.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter by session ID.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleContextStats(ctx, args)
	})

	// =========================================================================
	// Task Tracking Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_task_add",
		Description: "Add tasks/todos discovered during agent sessions. Tasks are prioritized in context recall.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID to add tasks to.",
				},
				"tasks": map[string]any{
					"type":        "array",
					"description": "Array of tasks to add.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title": map[string]any{
								"type":        "string",
								"description": "Short task title.",
							},
							"context": map[string]any{
								"type":        "string",
								"description": "Additional context about the task.",
							},
							"priority": map[string]any{
								"type":        "string",
								"enum":        []string{"low", "medium", "high", "critical"},
								"description": "Task priority (default: medium).",
							},
							"file_path": map[string]any{
								"type":        "string",
								"description": "Related file path.",
							},
							"line_number": map[string]any{
								"type":        "integer",
								"description": "Related line number.",
							},
							"tags": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Tags for categorization.",
							},
							"blocked_by": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "IDs of tasks blocking this one.",
							},
						},
						"required": []string{"title"},
					},
				},
			},
			Required: []string{"session_id", "tasks"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleTaskAdd(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_task_update",
		Description: "Update task status or resolution.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task ID to update.",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"pending", "in_progress", "completed", "blocked"},
					"description": "New task status.",
				},
				"resolution": map[string]any{
					"type":        "string",
					"description": "Resolution description (for completed tasks).",
				},
			},
			Required: []string{"task_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleTaskUpdate(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_task_list",
		Description: "List tasks with filtering options.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter by session ID.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by agent ID.",
				},
				"status": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by status(es).",
				},
				"include_completed": map[string]any{
					"type":        "boolean",
					"description": "Include completed tasks (default: false).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum tasks to return (default: 50).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleTaskList(ctx, args)
	})

	// =========================================================================
	// Code Annotation Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_code_annotate",
		Description: "Create a code annotation attached to a specific file location.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID.",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "File path to annotate.",
				},
				"line_start": map[string]any{
					"type":        "integer",
					"description": "Starting line number.",
				},
				"line_end": map[string]any{
					"type":        "integer",
					"description": "Ending line number (optional).",
				},
				"annotation_type": map[string]any{
					"type":        "string",
					"enum":        []string{"todo", "fixme", "note", "question", "important", "bug", "perf"},
					"description": "Type of annotation (default: note).",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Annotation content.",
				},
				"symbol": map[string]any{
					"type":        "string",
					"description": "Related symbol name.",
				},
				"repo_id": map[string]any{
					"type":        "string",
					"description": "Repository ID for codebase-memory integration.",
				},
			},
			Required: []string{"session_id", "file_path", "line_start", "content"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleAnnotationAdd(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_code_annotations_get",
		Description: "Get code annotations for a file or range.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "File path to get annotations for.",
				},
				"line_start": map[string]any{
					"type":        "integer",
					"description": "Filter by line range start.",
				},
				"line_end": map[string]any{
					"type":        "integer",
					"description": "Filter by line range end.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by agent ID.",
				},
				"annotation_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by annotation types.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum annotations to return (default: 50).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleAnnotationsGet(ctx, args)
	})

	// =========================================================================
	// Agent Handoff Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_handoff_create",
		Description: "Create a handoff package for another agent. Packages session context with instructions.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Source session ID.",
				},
				"target_agent_id": map[string]any{
					"type":        "string",
					"description": "Target agent to hand off to.",
				},
				"handoff_type": map[string]any{
					"type":        "string",
					"enum":        []string{"full", "selective", "summary_only"},
					"description": "Type of handoff (default: summary_only).",
				},
				"entry_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entry IDs for selective handoff.",
				},
				"instructions": map[string]any{
					"type":        "string",
					"description": "Instructions for the target agent.",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Maximum tokens in handoff (default: 8000).",
				},
			},
			Required: []string{"session_id", "target_agent_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleHandoffCreate(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_handoff_accept",
		Description: "Accept and import a handoff from another agent.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"handoff_id": map[string]any{
					"type":        "string",
					"description": "Handoff ID to accept.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Your session ID.",
				},
				"import_entries": map[string]any{
					"type":        "boolean",
					"description": "Import full entries (default: false, summary only).",
				},
			},
			Required: []string{"handoff_id", "session_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleHandoffAccept(ctx, args)
	})

	// =========================================================================
	// Session Template Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_template_create",
		Description: "Create a reusable session template.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Template name.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Template description.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace for the template.",
				},
				"from_session_id": map[string]any{
					"type":        "string",
					"description": "Copy from existing session.",
				},
				"entry_types_to_include": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entry types to include when copying.",
				},
				"created_by": map[string]any{
					"type":        "string",
					"description": "Creator agent ID.",
				},
			},
			Required: []string{"name"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleTemplateCreate(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_template_list",
		Description: "List available session templates.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum templates to return (default: 50).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleTemplateList(ctx, args)
	})

	// =========================================================================
	// Enhanced Recall Tool
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_context_recall_enhanced",
		Description: "Enhanced recall with task priority, symbol context, recency weighting, and code annotations.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "What are you trying to do? (used for relevance).",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter to specific agent.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter to specific session.",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Maximum tokens to return (default: 4000).",
				},
				"include_summaries": map[string]any{
					"type":        "boolean",
					"description": "Include session summaries (default: true).",
				},
				"include_decisions": map[string]any{
					"type":        "boolean",
					"description": "Prioritize decisions (default: true).",
				},
				"file_context": map[string]any{
					"type":        "string",
					"description": "Current file being worked on (for relevance boost).",
				},
				"symbol_context": map[string]any{
					"type":        "string",
					"description": "Current symbol for relevance boost.",
				},
				"recency_weight": map[string]any{
					"type":        "number",
					"description": "Weight for recency (0.0-1.0, default: 0.2).",
				},
				"include_tasks": map[string]any{
					"type":        "boolean",
					"description": "Include active tasks (default: true).",
				},
			},
			Required: []string{"query"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEnhancedRecall(ctx, args)
	})

	// =========================================================================
	// Workflow Orchestration Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_workflow_define",
		Description: "Define a reusable workflow with steps that can be executed as a DAG with parallel execution, approval gates, and rollback support.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Workflow name.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Workflow description.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace for the workflow.",
				},
				"created_by": map[string]any{
					"type":        "string",
					"description": "Agent ID of creator.",
				},
				"steps": map[string]any{
					"type":        "array",
					"description": "Array of workflow steps.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id": map[string]any{
								"type":        "string",
								"description": "Unique step ID (auto-generated if not provided).",
							},
							"name": map[string]any{
								"type":        "string",
								"description": "Step name.",
							},
							"description": map[string]any{
								"type":        "string",
								"description": "Step description.",
							},
							"step_type": map[string]any{
								"type":        "string",
								"enum":        []string{"tool", "approval", "gate", "parallel", "subflow"},
								"description": "Type of step (default: tool).",
							},
							"tool_name": map[string]any{
								"type":        "string",
								"description": "MCP tool name (for tool steps).",
							},
							"tool_args": map[string]any{
								"type":        "object",
								"description": "Arguments for the tool. Use ${input.key} or ${step_id.key} for variable references.",
							},
							"server_name": map[string]any{
								"type":        "string",
								"description": "MCP server name (for routing).",
							},
							"depends_on": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Step IDs this step depends on.",
							},
							"requires_approval": map[string]any{
								"type":        "boolean",
								"description": "Wait for approval before executing.",
							},
							"approval_message": map[string]any{
								"type":        "string",
								"description": "Message shown when requesting approval.",
							},
							"condition": map[string]any{
								"type":        "string",
								"description": "Condition expression for gate steps.",
							},
							"max_retries": map[string]any{
								"type":        "integer",
								"description": "Maximum retry attempts on failure.",
							},
							"retry_delay_ms": map[string]any{
								"type":        "integer",
								"description": "Delay between retries in milliseconds.",
							},
							"timeout_seconds": map[string]any{
								"type":        "integer",
								"description": "Step timeout in seconds.",
							},
							"rollback_step_id": map[string]any{
								"type":        "string",
								"description": "Step ID to execute on rollback.",
							},
							"subflow_id": map[string]any{
								"type":        "string",
								"description": "Workflow definition ID (for subflow steps).",
							},
						},
						"required": []string{"name"},
					},
				},
				"input_schema": map[string]any{
					"type":        "object",
					"description": "JSON Schema for workflow input validation.",
				},
				"rollback_on_failure": map[string]any{
					"type":        "boolean",
					"description": "Execute rollback steps on failure (default: false).",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Global workflow timeout.",
				},
			},
			Required: []string{"name", "steps"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorkflowDefine(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_workflow_start",
		Description: "Start a workflow instance from a definition. Returns immediately while workflow executes in background.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"definition_id": map[string]any{
					"type":        "string",
					"description": "Workflow definition ID to start.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Agent session ID for context.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID.",
				},
				"input": map[string]any{
					"type":        "object",
					"description": "Input parameters for the workflow. Referenced via ${input.key} in steps.",
				},
			},
			Required: []string{"definition_id", "session_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorkflowStart(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_workflow_status",
		Description: "Get the current status of a running or completed workflow.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"workflow_id": map[string]any{
					"type":        "string",
					"description": "Workflow instance ID.",
				},
			},
			Required: []string{"workflow_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorkflowStatus(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_workflow_list",
		Description: "List workflows with filtering options.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter by session ID.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by agent ID.",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"pending", "running", "paused", "waiting_approval", "completed", "failed", "cancelled", "rolled_back"},
					"description": "Filter by status.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorkflowList(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_workflow_approve",
		Description: "Approve a workflow step that is waiting for approval.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"workflow_id": map[string]any{
					"type":        "string",
					"description": "Workflow instance ID.",
				},
				"step_id": map[string]any{
					"type":        "string",
					"description": "Step ID to approve.",
				},
				"approver_id": map[string]any{
					"type":        "string",
					"description": "ID of approver.",
				},
				"comment": map[string]any{
					"type":        "string",
					"description": "Approval comment.",
				},
			},
			Required: []string{"workflow_id", "step_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorkflowApprove(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_workflow_reject",
		Description: "Reject a workflow step that is waiting for approval, failing the workflow.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"workflow_id": map[string]any{
					"type":        "string",
					"description": "Workflow instance ID.",
				},
				"step_id": map[string]any{
					"type":        "string",
					"description": "Step ID to reject.",
				},
				"rejecter_id": map[string]any{
					"type":        "string",
					"description": "ID of rejecter.",
				},
				"comment": map[string]any{
					"type":        "string",
					"description": "Rejection reason.",
				},
			},
			Required: []string{"workflow_id", "step_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorkflowReject(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_workflow_cancel",
		Description: "Cancel a running workflow.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"workflow_id": map[string]any{
					"type":        "string",
					"description": "Workflow instance ID.",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Cancellation reason.",
				},
			},
			Required: []string{"workflow_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorkflowCancel(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_workflow_events",
		Description: "Get the event history for a workflow.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"workflow_id": map[string]any{
					"type":        "string",
					"description": "Workflow instance ID.",
				},
			},
			Required: []string{"workflow_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorkflowEvents(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_workflow_definitions",
		Description: "List available workflow definitions.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorkflowDefinitionList(ctx, args)
	})

	// =========================================================================
	// Knowledge Graph Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_entity_add",
		Description: "Add entities to the knowledge graph. Entities represent code elements, concepts, decisions, or other important items.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID for provenance.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID for provenance.",
				},
				"entities": map[string]any{
					"type":        "array",
					"description": "Array of entities to add.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type": map[string]any{
								"type":        "string",
								"enum":        []string{"file", "function", "class", "module", "variable", "concept", "decision", "issue", "pr", "commit", "agent", "session", "task", "error", "service", "api", "database", "config"},
								"description": "Entity type.",
							},
							"name": map[string]any{
								"type":        "string",
								"description": "Entity name.",
							},
							"description": map[string]any{
								"type":        "string",
								"description": "Entity description.",
							},
							"namespace": map[string]any{
								"type":        "string",
								"description": "Namespace for grouping.",
							},
							"file_path": map[string]any{
								"type":        "string",
								"description": "File path (for code entities).",
							},
							"line_start": map[string]any{
								"type":        "integer",
								"description": "Starting line number.",
							},
							"line_end": map[string]any{
								"type":        "integer",
								"description": "Ending line number.",
							},
							"language": map[string]any{
								"type":        "string",
								"description": "Programming language.",
							},
							"signature": map[string]any{
								"type":        "string",
								"description": "Function/method signature.",
							},
							"properties": map[string]any{
								"type":        "object",
								"description": "Additional properties.",
							},
							"tags": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Tags for filtering.",
							},
						},
						"required": []string{"type", "name"},
					},
				},
			},
			Required: []string{"entities"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEntityAdd(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_entity_get",
		Description: "Retrieve entities by ID.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"entity_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entity IDs to retrieve.",
				},
			},
			Required: []string{"entity_ids"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEntityGet(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_entity_find",
		Description: "Search for entities by type, namespace, or name pattern.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"type": map[string]any{
					"type":        "string",
					"enum":        []string{"file", "function", "class", "module", "variable", "concept", "decision", "issue", "pr", "commit", "agent", "session", "task", "error", "service", "api", "database", "config"},
					"description": "Filter by entity type.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"name_pattern": map[string]any{
					"type":        "string",
					"description": "Regex pattern to match entity names.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results (default: 50).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEntityFind(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_entity_delete",
		Description: "Delete entities and their relations from the graph.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"entity_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Entity IDs to delete.",
				},
				"confirm": map[string]any{
					"type":        "boolean",
					"description": "Must be true to confirm deletion.",
				},
			},
			Required: []string{"entity_ids", "confirm"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEntityDelete(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_relation_add",
		Description: "Add relations between entities in the knowledge graph.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID for provenance.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID for provenance.",
				},
				"relations": map[string]any{
					"type":        "array",
					"description": "Array of relations to add.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type": map[string]any{
								"type":        "string",
								"enum":        []string{"depends_on", "implements", "extends", "calls", "imports", "defines", "contains", "references", "overrides", "caused", "resolved", "blocked_by", "triggered", "created_by", "modified_by", "discovered_by", "assigned_to", "related_to", "similar_to", "opposite_of", "part_of", "version_of", "precedes", "follows", "occurred_with"},
								"description": "Relation type.",
							},
							"source_id": map[string]any{
								"type":        "string",
								"description": "Source entity ID.",
							},
							"target_id": map[string]any{
								"type":        "string",
								"description": "Target entity ID.",
							},
							"weight": map[string]any{
								"type":        "number",
								"description": "Relation strength/confidence (default: 1.0).",
							},
							"bidirectional": map[string]any{
								"type":        "boolean",
								"description": "Create reverse relation too.",
							},
							"evidence": map[string]any{
								"type":        "string",
								"description": "Evidence supporting this relation.",
							},
							"reasoning": map[string]any{
								"type":        "string",
								"description": "Reasoning for this relation.",
							},
							"properties": map[string]any{
								"type":        "object",
								"description": "Additional properties.",
							},
						},
						"required": []string{"type", "source_id", "target_id"},
					},
				},
			},
			Required: []string{"relations"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleRelationAdd(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_relation_get",
		Description: "Get relations for an entity.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"entity_id": map[string]any{
					"type":        "string",
					"description": "Entity ID to get relations for.",
				},
				"relation_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by relation types.",
				},
				"outgoing": map[string]any{
					"type":        "boolean",
					"description": "Include outgoing relations (default: true).",
				},
				"incoming": map[string]any{
					"type":        "boolean",
					"description": "Include incoming relations (default: true).",
				},
			},
			Required: []string{"entity_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleRelationGet(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_relation_delete",
		Description: "Delete relations from the graph.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"relation_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Relation IDs to delete.",
				},
				"confirm": map[string]any{
					"type":        "boolean",
					"description": "Must be true to confirm deletion.",
				},
			},
			Required: []string{"relation_ids", "confirm"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleRelationDelete(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_graph_query",
		Description: "Query the knowledge graph. Supports pattern matching like (file)-[calls]->(function) or traversal from an entity.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Pattern to match: (type)-[relation]->(type). Types and relations are optional.",
				},
				"entity_id": map[string]any{
					"type":        "string",
					"description": "Start traversal from this entity.",
				},
				"source_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter source entity types.",
				},
				"target_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter target entity types.",
				},
				"relation_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter relation types.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"max_depth": map[string]any{
					"type":        "integer",
					"description": "Maximum traversal depth (default: 2).",
				},
				"bidirectional": map[string]any{
					"type":        "boolean",
					"description": "Follow relations in both directions.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleGraphQuery(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_graph_find_path",
		Description: "Find a path between two entities in the knowledge graph.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"source_id": map[string]any{
					"type":        "string",
					"description": "Source entity ID.",
				},
				"target_id": map[string]any{
					"type":        "string",
					"description": "Target entity ID.",
				},
				"max_depth": map[string]any{
					"type":        "integer",
					"description": "Maximum path length (default: 5).",
				},
				"relation_types": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by relation types.",
				},
			},
			Required: []string{"source_id", "target_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleFindPath(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_reasoning_chain_add",
		Description: "Store a reasoning chain documenting how conclusions were reached using the knowledge graph.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID.",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "The question being answered.",
				},
				"steps": map[string]any{
					"type":        "array",
					"description": "Reasoning steps.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"description": map[string]any{
								"type":        "string",
								"description": "Step description.",
							},
							"entity_ids": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Entities involved in this step.",
							},
							"relation_ids": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Relations involved in this step.",
							},
							"conclusion": map[string]any{
								"type":        "string",
								"description": "Step conclusion.",
							},
							"confidence": map[string]any{
								"type":        "number",
								"description": "Confidence (0-1).",
							},
						},
					},
				},
				"conclusion": map[string]any{
					"type":        "string",
					"description": "Final conclusion.",
				},
				"confidence": map[string]any{
					"type":        "number",
					"description": "Overall confidence (0-1).",
				},
			},
			Required: []string{"query"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleReasoningChainAdd(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_reasoning_chain_get",
		Description: "Retrieve a reasoning chain by ID.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"chain_id": map[string]any{
					"type":        "string",
					"description": "Reasoning chain ID.",
				},
			},
			Required: []string{"chain_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleReasoningChainGet(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_reasoning_chain_list",
		Description: "List reasoning chains.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter by session ID.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by agent ID.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results (default: 50).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleReasoningChainList(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_graph_stats",
		Description: "Get statistics about the knowledge graph.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleGraphStats(ctx, args)
	})

	// Memory Hierarchy Tools

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_add",
		Description: "Add items to the tiered memory hierarchy. Items start in working memory and can be promoted to short-term or long-term memory.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID for the memories.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace for organization.",
				},
				"items": map[string]any{
					"type":        "array",
					"description": "Memory items to add.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title": map[string]any{
								"type":        "string",
								"description": "Short title for the memory.",
							},
							"content": map[string]any{
								"type":        "string",
								"description": "Full content of the memory.",
							},
							"tier": map[string]any{
								"type":        "string",
								"enum":        []string{"working", "short_term", "long_term"},
								"description": "Memory tier (default: working).",
							},
							"importance": map[string]any{
								"type":        "string",
								"enum":        []string{"low", "medium", "high", "critical"},
								"description": "Importance level (default: medium).",
							},
							"category": map[string]any{
								"type":        "string",
								"description": "Category for grouping (e.g., 'decision', 'insight', 'task').",
							},
							"tags": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Tags for filtering.",
							},
							"metadata": map[string]any{
								"type":        "object",
								"description": "Additional metadata.",
							},
							"related_ids": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Related memory item IDs.",
							},
						},
						"required": []string{"title", "content"},
					},
				},
			},
			Required: []string{"items"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryAdd(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_get",
		Description: "Retrieve memory items by ID.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"item_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Memory item IDs to retrieve.",
				},
			},
			Required: []string{"item_ids"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryGet(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_recall",
		Description: "Recall memories matching criteria. Returns most relevant items within token budget, prioritized by importance and recency.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query (matches title and content).",
				},
				"tiers": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Tiers to search: working, short_term, long_term (default: all).",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter by session.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by agent.",
				},
				"categories": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by categories.",
				},
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by tags.",
				},
				"min_importance": map[string]any{
					"type":        "number",
					"description": "Minimum importance score (0-1).",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Maximum tokens to return (default: 8000).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum items (default: 100).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryRecall(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_delete",
		Description: "Delete memory items.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"item_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Memory item IDs to delete.",
				},
				"confirm": map[string]any{
					"type":        "boolean",
					"description": "Must be true to confirm deletion.",
				},
			},
			Required: []string{"item_ids", "confirm"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryDelete(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_promote",
		Description: "Promote memory items to a higher tier (working -> short_term -> long_term).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"item_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Memory item IDs to promote.",
				},
			},
			Required: []string{"item_ids"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryPromote(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_demote",
		Description: "Demote memory items to a lower tier (long_term -> short_term -> working).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"item_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Memory item IDs to demote.",
				},
			},
			Required: []string{"item_ids"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryDemote(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_compress",
		Description: "Compress memory items to reduce token usage. Can compress specific items or run tier-wide compression.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"item_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Specific item IDs to compress.",
				},
				"tier": map[string]any{
					"type":        "string",
					"enum":        []string{"working", "short_term", "long_term"},
					"description": "Run compression on entire tier.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryCompress(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_merge",
		Description: "Merge multiple memory items into a single item. Original items are archived.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"item_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "IDs of items to merge (minimum 2).",
				},
				"new_title": map[string]any{
					"type":        "string",
					"description": "Title for the merged item.",
				},
			},
			Required: []string{"item_ids"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryMerge(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_stats",
		Description: "Get statistics about the memory hierarchy.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryStats(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_policy_get",
		Description: "Get the retention policy for a memory tier.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"tier": map[string]any{
					"type":        "string",
					"enum":        []string{"working", "short_term", "long_term"},
					"description": "Memory tier.",
				},
			},
			Required: []string{"tier"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryPolicyGet(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_policy_set",
		Description: "Update the retention policy for a memory tier.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"tier": map[string]any{
					"type":        "string",
					"enum":        []string{"working", "short_term", "long_term"},
					"description": "Memory tier to configure.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Policy name.",
				},
				"default_ttl_hours": map[string]any{
					"type":        "integer",
					"description": "Default time-to-live in hours (0 = no expiry).",
				},
				"compress_after_hours": map[string]any{
					"type":        "integer",
					"description": "Hours before auto-compression.",
				},
				"compression_ratio": map[string]any{
					"type":        "number",
					"description": "Target compression ratio (0-1).",
				},
				"merge_threshold": map[string]any{
					"type":        "number",
					"description": "Similarity threshold for auto-merge (0-1).",
				},
				"promotion_threshold": map[string]any{
					"type":        "number",
					"description": "Importance threshold for promotion (0-1).",
				},
				"demotion_threshold": map[string]any{
					"type":        "number",
					"description": "Importance threshold for demotion (0-1).",
				},
				"access_count_threshold": map[string]any{
					"type":        "integer",
					"description": "Access count for auto-promotion.",
				},
				"max_items": map[string]any{
					"type":        "integer",
					"description": "Maximum items in tier.",
				},
				"max_tokens": map[string]any{
					"type":        "integer",
					"description": "Maximum tokens in tier.",
				},
				"dedupe_enabled": map[string]any{
					"type":        "boolean",
					"description": "Enable deduplication.",
				},
				"dedupe_similarity": map[string]any{
					"type":        "number",
					"description": "Similarity threshold for deduplication (0-1).",
				},
			},
			Required: []string{"tier"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryPolicySet(ctx, args)
	})

	// =========================================================================
	// Agent Presence Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_presence_register",
		Description: "Register an agent's presence. Announces the agent is active and available for coordination. Call this at the start of your work session for multi-agent discovery.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Unique agent identifier.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Current session ID.",
				},
				"agent_type": map[string]any{
					"type":        "string",
					"description": "Agent type (e.g., 'claude-code', 'codex', 'gemini').",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "What the agent is working on.",
				},
				"heartbeat_ttl_seconds": map[string]any{
					"type":        "integer",
					"description": "Heartbeat TTL in seconds (default: 120). Agent is considered offline after this.",
				},
			},
			Required: []string{"agent_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandlePresenceRegister(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_presence_heartbeat",
		Description: "Send a heartbeat to keep presence alive. Returns file conflicts if active_files overlap with other agents. Recommended every 30-60 seconds. Response includes file conflicts if detected.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent identifier.",
				},
				"active_files": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Files currently being edited/viewed.",
				},
				"current_task": map[string]any{
					"type":        "string",
					"description": "Current task description.",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Current git branch.",
				},
			},
			Required: []string{"agent_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandlePresenceHeartbeat(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_presence_list",
		Description: "List active agents. Discover what other agents are working on.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"include_offline": map[string]any{
					"type":        "boolean",
					"description": "Include agents whose heartbeat has expired (default: false).",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandlePresenceList(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_presence_deregister",
		Description: "Deregister an agent's presence. Clean exit releasing resources.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent identifier.",
				},
				"release_claims": map[string]any{
					"type":        "boolean",
					"description": "Release all file claims (default: true).",
				},
				"release_worktrees": map[string]any{
					"type":        "boolean",
					"description": "Mark assigned worktrees as orphaned (default: false).",
				},
			},
			Required: []string{"agent_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandlePresenceDeregister(ctx, args)
	})

	// =========================================================================
	// File Claims (Advisory Locks) Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_file_claim_acquire",
		Description: "Claim a file for editing/review. Advisory lock — returns conflict info if other agents hold claims.",
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
				"file_path": map[string]any{
					"type":        "string",
					"description": "File path to claim.",
				},
				"claim_type": map[string]any{
					"type":        "string",
					"enum":        []string{"edit", "review", "reserve"},
					"description": "Type of claim (default: 'edit').",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Why this file is being claimed.",
				},
			},
			Required: []string{"agent_id", "session_id", "file_path"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleFileClaimAcquire(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_file_claim_release",
		Description: "Release file claims. Set file_path='all' to release all claims for the agent.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent identifier.",
				},
				"file_path": map[string]any{
					"type":        "string",
					"description": "File path to release, or 'all' for all claims.",
				},
			},
			Required: []string{"agent_id", "file_path"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleFileClaimRelease(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_file_claim_query",
		Description: "Check who holds claims on specific files.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"file_paths": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "File paths to check.",
				},
				"exclude_agent": map[string]any{
					"type":        "string",
					"description": "Exclude this agent from results.",
				},
			},
			Required: []string{"file_paths"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleFileClaimQuery(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_file_claim_list",
		Description: "List file claims, optionally filtered by agent or claim type.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Filter by agent ID.",
				},
				"claim_type": map[string]any{
					"type":        "string",
					"enum":        []string{"edit", "review", "reserve"},
					"description": "Filter by claim type.",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleFileClaimList(ctx, args)
	})

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

	// =========================================================================
	// Memory Export/Import Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_export",
		Description: "Export memory to universal JSON format. Supports filtering by namespace, session, tags, and time range.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID for export metadata.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Filter by session.",
				},
				"format": map[string]any{
					"type":        "string",
					"enum":        []string{"loom", "mem0", "supermemory"},
					"description": "Export format (default: 'loom').",
				},
				"tiers": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by memory tiers (working, short_term, long_term).",
				},
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by tags.",
				},
				"include_graph": map[string]any{
					"type":        "boolean",
					"description": "Include knowledge graph (default: true).",
				},
				"include_workflows": map[string]any{
					"type":        "boolean",
					"description": "Include workflows (default: false).",
				},
				"include_embeddings": map[string]any{
					"type":        "boolean",
					"description": "Include embedding vectors (default: false).",
				},
			},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryExport(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_memory_import",
		Description: "Import memory from universal JSON format.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"data": map[string]any{
					"type":        "object",
					"description": "Universal memory format data to import.",
				},
				"conflict_strategy": map[string]any{
					"type":        "string",
					"enum":        []string{"skip", "overwrite", "merge"},
					"description": "How to handle ID conflicts (default: 'skip').",
				},
				"id_prefix": map[string]any{
					"type":        "string",
					"description": "Prefix for imported IDs to avoid collisions.",
				},
				"target_tier": map[string]any{
					"type":        "string",
					"description": "Override tier for imported memories.",
				},
				"target_namespace": map[string]any{
					"type":        "string",
					"description": "Override namespace for imported items.",
				},
				"import_graph": map[string]any{
					"type":        "boolean",
					"description": "Import knowledge graph (default: true).",
				},
				"import_workflows": map[string]any{
					"type":        "boolean",
					"description": "Import workflows (default: false).",
				},
				"regenerate_embeddings": map[string]any{
					"type":        "boolean",
					"description": "Regenerate embeddings for imported items (default: false).",
				},
			},
			Required: []string{"data"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleMemoryImport(ctx, args)
	})

	// =========================================================================
	// Compaction Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_compaction_status",
		Description: "Get compaction scheduler status and last run statistics.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleCompactionStatus(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_compaction_trigger",
		Description: "Manually trigger a compaction cycle. Returns statistics about what was processed.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleCompactionTrigger(ctx, args)
	})

	// =========================================================================
	// Handoff Inbox Tools
	// =========================================================================

	server.AddTool(mcp.Tool{
		Name:        "agent_handoff_inbox",
		Description: "List pending handoffs for an agent. The agent's 'inbox' for receiving work from other agents.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID to check inbox for.",
				},
				"include_viewed": map[string]any{
					"type":        "boolean",
					"description": "Include already-viewed handoffs (default: false).",
				},
			},
			Required: []string{"agent_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleHandoffInbox(ctx, args)
	})

	server.AddTool(mcp.Tool{
		Name:        "agent_handoff_reject",
		Description: "Reject a handoff with an optional reason.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"handoff_id": map[string]any{
					"type":        "string",
					"description": "Handoff ID to reject.",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Reason for rejection.",
				},
			},
			Required: []string{"handoff_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleHandoffReject(ctx, args)
	})
}
