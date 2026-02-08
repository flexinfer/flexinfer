package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerWorkflowTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
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
	}, traced(tracer, "agent_workflow_start", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleWorkflowStart(ctx, args)
	}))

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
}
