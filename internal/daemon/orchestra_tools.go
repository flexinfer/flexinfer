package daemon

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/openairesponses"
	"github.com/crb2nu/loom/pkg/orchestra"
)

// orchestraSyntheticTools returns MCP tool definitions for orchestra tools.
// These are added to the visible tool set when orchestra is enabled.
func (d *Daemon) orchestraSyntheticTools() []mcp.Tool {
	if d.orchestra == nil {
		return nil
	}

	tools := []mcp.Tool{
		{
			Name:        "orchestra__query",
			Description: "Execute a multi-tool orchestrated query using local AI models. Routes the query to domain-specific subagents that use 5-10 tools in parallel, then synthesizes a compressed answer. Replaces multiple sequential tool calls with a single request.",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The natural language query to answer using multiple tools.",
					},
					"domains": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional domain filter. If empty, the router auto-classifies. Available: cluster-ops, codebase, ci-pipeline, observability.",
					},
					"max_tokens": map[string]any{
						"type":        "integer",
						"description": "Optional max tokens for the synthesized response.",
					},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "orchestra__gather",
			Description: "Execute an orchestrated query against specific domains (no auto-classification). Useful when you know which domains to query.",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The natural language query to answer.",
					},
					"domains": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Domains to query. Required. Available: cluster-ops, codebase, ci-pipeline, observability.",
					},
				},
				Required: []string{"query", "domains"},
			},
		},
	}

	// Add compound tools.
	for _, ct := range orchestra.DefaultCompoundTools() {
		tools = append(tools, mcp.Tool{
			Name:        ct.Name,
			Description: ct.Description,
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Optional custom query to override the default.",
					},
				},
			},
		})
	}

	return tools
}

// isOrchestraTool returns true if the tool name is an orchestra synthetic tool.
func isOrchestraTool(name string) bool {
	switch name {
	case "orchestra__query", "orchestra__gather":
		return true
	}
	return orchestra.IsCompoundTool(name)
}

// isOrchestraTool checks whether the current pipeline call targets an orchestra tool.
func (p *callPipeline) isOrchestraTool() bool {
	return p.daemon.orchestra != nil && isOrchestraTool(p.toolName)
}

// executeOrchestraTool dispatches an orchestra synthetic tool call.
func (p *callPipeline) executeOrchestraTool() *mcp.Message {
	p.stage = stageExecute

	toolName := p.toolName
	if p.serverName != "" {
		toolName = p.serverName + "__" + p.toolName
	}

	switch toolName {
	case "orchestra__query":
		return p.daemon.handleOrchestraToolQuery(p.ctx, p.msg)
	case "orchestra__gather":
		return p.daemon.handleOrchestraToolGather(p.ctx, p.msg)
	default:
		// Compound tools.
		return p.daemon.handleOrchestraToolCompound(p.ctx, p.msg, toolName)
	}
}

// handleOrchestraToolQuery handles orchestra__query via the call pipeline.
func (d *Daemon) handleOrchestraToolQuery(ctx context.Context, msg *mcp.Message) *mcp.Message {
	resp, err := d.handleOrchestraQuery(ctx, msg)
	if err != nil {
		return newErrorResponse(msg.ID, mcp.InternalError, err.Error(), nil)
	}
	return resp
}

// handleOrchestraToolGather handles orchestra__gather via the call pipeline.
func (d *Daemon) handleOrchestraToolGather(ctx context.Context, msg *mcp.Message) *mcp.Message {
	resp, err := d.handleOrchestraGather(ctx, msg)
	if err != nil {
		return newErrorResponse(msg.ID, mcp.InternalError, err.Error(), nil)
	}
	return resp
}

// handleOrchestraToolCompound handles compound orchestra tools via the call pipeline.
func (d *Daemon) handleOrchestraToolCompound(ctx context.Context, msg *mcp.Message, toolName string) *mcp.Message {
	if d.orchestra == nil {
		return newErrorResponse(msg.ID, mcp.InternalError, "orchestra is not enabled", nil)
	}

	identity := openairesponses.ExecutionIdentity{}
	result, ok := orchestra.HandleCompoundTool(ctx, d.orchestra, toolName, msg.Params, identity, d.logger)
	if !ok {
		return newErrorResponse(msg.ID, mcp.MethodNotFound, "unknown compound tool: "+toolName, nil)
	}

	resp, _ := mcp.NewResponse(msg.ID, result)
	return resp
}
