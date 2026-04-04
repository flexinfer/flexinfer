package daemon

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/openairesponses"
	"github.com/crb2nu/loom/pkg/weaver"
)

// weaverSyntheticTools returns MCP tool definitions for weaver tools.
// These are added to the visible tool set when weaver is enabled.
func (d *Daemon) weaverSyntheticTools() []mcp.Tool {
	if d.weaver == nil {
		return nil
	}

	tools := []mcp.Tool{
		{
			Name:        "weaver__query",
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
						"description": "Optional domain filter. If empty, the router auto-classifies. Available: agent-fleet, ci-pipeline, cluster-ops, codebase, infra-ops, observability.",
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
			Name:        "weaver__gather",
			Description: "Execute a weaverted query against specific domains (no auto-classification). Useful when you know which domains to query.",
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
						"description": "Domains to query. Required. Available: agent-fleet, ci-pipeline, cluster-ops, codebase, infra-ops, observability.",
					},
				},
				Required: []string{"query", "domains"},
			},
		},
	}

	// Add compound tools.
	for _, ct := range weaver.DefaultCompoundTools() {
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

// isWeaverTool returns true if the tool name is an weaver synthetic tool.
func isWeaverTool(name string) bool {
	switch name {
	case "weaver__query", "weaver__gather":
		return true
	}
	return weaver.IsCompoundTool(name)
}

// isWeaverTool checks whether the current pipeline call targets an weaver tool.
func (p *callPipeline) isWeaverTool() bool {
	return p.daemon.weaver != nil && isWeaverTool(p.toolName)
}

// executeWeaverTool dispatches an weaver synthetic tool call.
func (p *callPipeline) executeWeaverTool() *mcp.Message {
	p.stage = stageExecute

	toolName := p.toolName
	if p.serverName != "" {
		toolName = p.serverName + "__" + p.toolName
	}

	switch toolName {
	case "weaver__query":
		return p.daemon.handleOrchestraToolQuery(p.ctx, p.msg)
	case "weaver__gather":
		return p.daemon.handleOrchestraToolGather(p.ctx, p.msg)
	default:
		// Compound tools.
		return p.daemon.handleWeaverToolCompound(p.ctx, p.msg, toolName)
	}
}

// handleOrchestraToolQuery handles weaver__query via the call pipeline.
func (d *Daemon) handleOrchestraToolQuery(ctx context.Context, msg *mcp.Message) *mcp.Message {
	resp, err := d.handleOrchestraQuery(ctx, msg)
	if err != nil {
		return newErrorResponse(msg.ID, mcp.InternalError, err.Error(), nil)
	}
	return resp
}

// handleOrchestraToolGather handles weaver__gather via the call pipeline.
func (d *Daemon) handleOrchestraToolGather(ctx context.Context, msg *mcp.Message) *mcp.Message {
	resp, err := d.handleOrchestraGather(ctx, msg)
	if err != nil {
		return newErrorResponse(msg.ID, mcp.InternalError, err.Error(), nil)
	}
	return resp
}

// handleWeaverToolCompound handles compound weaver tools via the call pipeline.
func (d *Daemon) handleWeaverToolCompound(ctx context.Context, msg *mcp.Message, toolName string) *mcp.Message {
	if d.weaver == nil {
		return newErrorResponse(msg.ID, mcp.InternalError, "weaver is not enabled", nil)
	}

	identity := openairesponses.ExecutionIdentity{}
	result, ok := weaver.HandleCompoundTool(ctx, d.weaver, toolName, msg.Params, identity, d.logger)
	if !ok {
		return newErrorResponse(msg.ID, mcp.MethodNotFound, "unknown compound tool: "+toolName, nil)
	}

	resp, _ := mcp.NewResponse(msg.ID, result)
	return resp
}
