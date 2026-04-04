package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/flexinfer"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/openairesponses"
	"github.com/crb2nu/loom/pkg/weaver"
)

var version = "1.0.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	// Initialize OTel tracing (noop when OTEL_EXPORTER_OTLP_ENDPOINT is unset).
	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-weaver", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed, continuing without tracing", "error", err)
	}
	defer shutdownTracer(ctx)

	// Load configuration.
	cfg := weaver.LoadConfigFromEnv()
	cfg.Enabled = true // standalone binary is always enabled

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid weaver config: %w", err)
	}

	// Require FLEXINFER_URL and MCP_HUB_URL.
	flexinferURL := env.String("FLEXINFER_URL", "")
	if flexinferURL == "" {
		return fmt.Errorf("FLEXINFER_URL is required")
	}
	hubURL := env.String("MCP_HUB_URL", "")
	if hubURL == "" {
		return fmt.Errorf("MCP_HUB_URL is required")
	}

	// Create FlexInfer client.
	apiKey := env.String("FLEXINFER_API_KEY", "")
	breaker := flexinfer.NewCircuitBreaker(5, 30*time.Second)
	flexClient := flexinfer.NewClient(flexinferURL, apiKey, cfg.Timeout, breaker, logger)

	// Create hub-based tool lister and executor.
	lister := NewHubToolLister(hubURL)
	caller := NewHubToolCaller(hubURL)
	executor := weaver.NewDaemonToolExecutor(caller, cfg.Timeout)

	// Create the weaver router.
	router := weaver.NewRouter(cfg, flexClient, executor, lister, logger)

	tracer := mcpotel.Tracer(tp, "mcp-weaver")
	router.SetTracer(tracer)

	logger.Info("starting server",
		"name", "mcp-weaver",
		"version", version,
		"flexinfer_url", flexinferURL,
		"hub_url", hubURL,
		"router_model", cfg.RouterModel,
		"subagent_model", cfg.SubagentModel,
	)

	server := mcp.NewServer("mcp-weaver", version)
	server.SetInstructions(`Orchestra MCP Server (standalone)

This server provides multi-tool orchestrated queries using local AI models
via FlexInfer. It routes queries to domain-specific subagents that use
tools from the MCP gateway, then synthesizes compressed answers.

Tools:
- weaver__query: Auto-classify and dispatch to relevant domains
- weaver__gather: Dispatch to specified domains (no auto-classification)
- weaver__cluster_status: Cluster health overview (pods, deployments, alerts)
- weaver__ci_status: CI/CD pipeline status and merge requests
- weaver__system_health: Comprehensive system health report
- loom/weaver/status: Show weaver configuration and available domains

Environment:
- FLEXINFER_URL: FlexInfer proxy endpoint (required)
- MCP_HUB_URL: MCP gateway URL for tool routing (required)
- WEAVER_ROUTER_MODEL: Model for query classification
- WEAVER_SUBAGENT_MODEL: Model for domain subagents
- ORCHESTRA_MAX_ITERATIONS: Max tool-call iterations per subagent
- ORCHESTRA_MAX_CONCURRENT: Max parallel domain dispatches`)

	registerWeaverTools(server, router, logger)

	return server.Run(ctx)
}

// registerWeaverTools registers all weaver tools on the MCP server.
func registerWeaverTools(server *mcp.Server, router *weaver.Router, logger *slog.Logger) {
	// weaver__query
	server.AddTool(mcp.Tool{
		Name:        "weaver__query",
		Description: "Execute a multi-tool orchestrated query using local AI models. Routes the query to domain-specific subagents that use 5-10 tools in parallel, then synthesizes a compressed answer.",
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
	}, handleQuery(router, logger))

	// weaver__gather
	server.AddTool(mcp.Tool{
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
					"description": "Domains to query. Required. Available: cluster-ops, codebase, ci-pipeline, observability.",
				},
			},
			Required: []string{"query", "domains"},
		},
	}, handleGather(router, logger))

	// Compound tools.
	for _, ct := range weaver.DefaultCompoundTools() {
		ct := ct // capture loop variable
		server.AddTool(mcp.Tool{
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
		}, handleCompound(router, ct, logger))
	}

	// loom/weaver/status
	server.AddTool(mcp.Tool{
		Name:        "loom/weaver/status",
		Description: "Show weaver configuration and available domains.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleStatus(router))
}

// handleQuery returns a tool handler for weaver__query.
func handleQuery(router *weaver.Router, logger *slog.Logger) mcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		query, _ := args["query"].(string)
		if query == "" {
			return mcp.ErrorResult(fmt.Errorf("query is required")), nil
		}

		domains := parseStringSlice(args["domains"])

		var maxTokens int
		if mt, ok := args["max_tokens"].(float64); ok {
			maxTokens = int(mt)
		}

		req := weaver.QueryRequest{
			Query:     query,
			Domains:   domains,
			MaxTokens: maxTokens,
		}

		result, err := router.Query(ctx, req)
		if err != nil {
			logger.Warn("weaver query failed", "error", err)
			return mcp.ErrorResult(fmt.Errorf("weaver query failed: %w", err)), nil
		}

		return mcp.JSONResult(result)
	}
}

// handleGather returns a tool handler for weaver__gather.
func handleGather(router *weaver.Router, logger *slog.Logger) mcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		query, _ := args["query"].(string)
		if query == "" {
			return mcp.ErrorResult(fmt.Errorf("query is required")), nil
		}

		domains := parseStringSlice(args["domains"])
		if len(domains) == 0 {
			return mcp.ErrorResult(fmt.Errorf("domains is required for gather")), nil
		}

		result, err := router.Gather(ctx, domains, query, openairesponses.ExecutionIdentity{})
		if err != nil {
			logger.Warn("weaver gather failed", "error", err)
			return mcp.ErrorResult(fmt.Errorf("weaver gather failed: %w", err)), nil
		}

		return mcp.JSONResult(result)
	}
}

// handleCompound returns a tool handler for a compound weaver tool.
func handleCompound(router *weaver.Router, ct weaver.CompoundTool, logger *slog.Logger) mcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		raw, _ := json.Marshal(args)
		result, ok := weaver.HandleCompoundTool(ctx, router, ct.Name, raw, openairesponses.ExecutionIdentity{}, logger)
		if !ok {
			return mcp.ErrorResult(fmt.Errorf("unknown compound tool: %s", ct.Name)), nil
		}
		return mcp.TextResult(string(result)), nil
	}
}

// handleStatus returns a tool handler for loom/weaver/status.
func handleStatus(router *weaver.Router) mcp.ToolHandler {
	return func(_ context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
		return mcp.JSONResult(router.Status())
	}
}

// parseStringSlice extracts a []string from a JSON-decoded interface value
// (which is typically []any after JSON unmarshaling).
func parseStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
