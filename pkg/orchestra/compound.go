package orchestra

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/crb2nu/loom/pkg/openairesponses"
)

// CompoundTool defines a deterministic multi-domain workflow that skips
// LLM classification and directly dispatches to specified domains.
type CompoundTool struct {
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Domains     []string                    `json:"domains"`
	Query       string                      `json:"query"`
	Schema      map[string]any              `json:"schema,omitempty"`
	OutputFn    func([]DomainResult) string `json:"-"`
}

// DefaultCompoundTools returns the built-in compound tool definitions.
func DefaultCompoundTools() []CompoundTool {
	return []CompoundTool{
		{
			Name:        "orchestra__cluster_status",
			Description: "Get a comprehensive cluster status overview including pods, deployments, and alerts.",
			Domains:     []string{"cluster-ops", "observability"},
			Query:       "What is the current status of the cluster? List any unhealthy pods, pending deployments, and active alerts.",
		},
		{
			Name:        "orchestra__ci_status",
			Description: "Get the current CI/CD pipeline status including recent builds and merge requests.",
			Domains:     []string{"ci-pipeline", "codebase"},
			Query:       "What is the current CI/CD status? Show recent pipeline runs, their status, and any open merge requests.",
		},
		{
			Name:        "orchestra__system_health",
			Description: "Get a comprehensive system health report across cluster, CI, and observability.",
			Domains:     []string{"cluster-ops", "ci-pipeline", "observability"},
			Query:       "Provide a comprehensive system health report covering cluster state, CI pipelines, and any active alerts or anomalies.",
		},
	}
}

// ExecuteCompound runs a compound tool by dispatching directly to specified domains.
func ExecuteCompound(ctx context.Context, r *Router, tool CompoundTool, params map[string]any, identity openairesponses.ExecutionIdentity) (QueryResult, error) {
	query := tool.Query
	if customQuery, ok := params["query"].(string); ok && customQuery != "" {
		query = customQuery
	}

	result, err := r.Gather(ctx, tool.Domains, query, identity)
	if err != nil {
		return QueryResult{}, fmt.Errorf("compound %s: %w", tool.Name, err)
	}

	if tool.OutputFn != nil {
		result.Answer = tool.OutputFn(result.DomainResults)
	}

	return result, nil
}

// CompoundToolDefinitions returns MCP tool definitions for all compound tools.
func CompoundToolDefinitions() []map[string]any {
	var defs []map[string]any
	for _, ct := range DefaultCompoundTools() {
		schema := ct.Schema
		if schema == nil {
			schema = map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Optional custom query to override the default compound query.",
					},
				},
			}
		}
		defs = append(defs, map[string]any{
			"name":        ct.Name,
			"description": ct.Description,
			"inputSchema": schema,
		})
	}
	return defs
}

// HandleCompoundTool dispatches a compound tool call. Returns the result as
// a JSON-encoded response or nil if the tool name doesn't match any compound.
func HandleCompoundTool(ctx context.Context, r *Router, toolName string, args json.RawMessage, identity openairesponses.ExecutionIdentity, logger *slog.Logger) (json.RawMessage, bool) {
	for _, ct := range DefaultCompoundTools() {
		if ct.Name != toolName {
			continue
		}

		var params map[string]any
		if len(args) > 0 {
			_ = json.Unmarshal(args, &params)
		}

		start := time.Now()
		result, err := ExecuteCompound(ctx, r, ct, params, identity)
		if err != nil {
			logger.Warn("compound tool failed", "tool", toolName, "error", err)
			errResp, _ := json.Marshal(map[string]any{
				"error": err.Error(),
			})
			return errResp, true
		}

		logger.Debug("compound tool completed",
			"tool", toolName,
			"domains", ct.Domains,
			"latency_ms", time.Since(start).Milliseconds(),
		)

		resp, _ := json.Marshal(result)
		return resp, true
	}
	return nil, false
}

// IsCompoundTool returns true if the given tool name is a compound tool.
func IsCompoundTool(name string) bool {
	for _, ct := range DefaultCompoundTools() {
		if ct.Name == name {
			return true
		}
	}
	return false
}
