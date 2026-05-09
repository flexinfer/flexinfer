package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

// registerEngramTools wires the agent_engram_* MCP surface. Engrams are the
// tech-tree extension of recipes: same storage, plus tier/prerequisites/proof
// status/family for graph traversal. See svc_engrams.go for service logic.
func registerEngramTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	server.AddTool(mcp.Tool{
		Name: "agent_engram_add",
		Description: "Add an engram (tier-1 idiom, tier-2 composite, or tier-3 system) with " +
			"prerequisites and proof contract. Engrams form a DAG: tier-2 must include a " +
			"runnable 'command:' line in proof; tier-3 must add 'benchmark:' or 'dashboard:'.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"title":         map[string]any{"type": "string", "description": "Short, descriptive title."},
				"problem":       map[string]any{"type": "string", "description": "Problem this engram solves."},
				"solution":      map[string]any{"type": "string", "description": "Step-by-step solution with code."},
				"proof":         map[string]any{"type": "string", "description": "Required proof: file:line, runnable command (must include 'command:' for tier 2), or URL."},
				"family":        map[string]any{"type": "string", "description": "Logical group; siblings in other languages share family. Defaults to slug(title)."},
				"slug":          map[string]any{"type": "string", "description": "Within-family unique slug. Defaults to language or 'default'."},
				"tier":          map[string]any{"type": "integer", "description": "1=idiom, 2=composite, 3=system. Default 1."},
				"prerequisites": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Prerequisite engram URIs (engram://family/slug)."},
				"tags":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Extra free-form tags."},
				"language":      map[string]any{"type": "string", "description": "Programming language (go, python, typescript, etc.)."},
				"scope":         map[string]any{"type": "string", "enum": []string{"project", "workspace", "universal"}, "description": "Scope. Default project."},
				"session_id":    map[string]any{"type": "string"},
				"agent_id":      map[string]any{"type": "string"},
				"namespace":     map[string]any{"type": "string"},
			},
			Required: []string{"title", "problem", "solution", "proof"},
		},
	}, traced(tracer, "agent_engram_add", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEngramAdd(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name: "agent_engram_recall",
		Description: "Recall engrams matching a query and optionally include their transitive prerequisites. " +
			"Results are ordered lowest-tier-first. When token_budget is exceeded, results are " +
			"progressively degraded by dropping highest-tier prerequisites first, then locked engrams " +
			"(proof_status != verified), then by tail truncation. Direct matches are preserved unless " +
			"they are the only thing left to drop.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query":          map[string]any{"type": "string", "description": "Search query."},
				"depth":          map[string]any{"type": "integer", "description": "Prerequisite traversal depth. 0 = match only. Default 1."},
				"tier_max":       map[string]any{"type": "integer", "description": "Cap returned tier (1, 2, or 3). 0 = unbounded."},
				"limit":          map[string]any{"type": "integer", "description": "Max matched engrams (default 10)."},
				"token_budget":   map[string]any{"type": "integer", "description": "Max tokens to return (default 4000). 0 = no budget."},
				"include_locked": map[string]any{"type": "boolean", "description": "When false, omit engrams whose proof has not been verified in the requested repo. Default true."},
				"repo":           map[string]any{"type": "string", "description": "Repo or branch ref for the locked-check. Empty = unlocked anywhere."},
				"tags":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Filter by tags."},
				"language":       map[string]any{"type": "string", "description": "Filter by language."},
				"scope":          map[string]any{"type": "string", "enum": []string{"project", "workspace", "universal"}},
			},
			Required: []string{"query"},
		},
	}, traced(tracer, "agent_engram_recall", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEngramRecall(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name:        "agent_engram_list",
		Description: "List engrams, optionally filtered by tier, family, language, scope, or proof_status.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"tier":         map[string]any{"type": "integer", "description": "Cap returned tier (1, 2, or 3). 0 = unbounded."},
				"family":       map[string]any{"type": "string", "description": "Filter by family slug."},
				"language":     map[string]any{"type": "string"},
				"scope":        map[string]any{"type": "string", "enum": []string{"project", "workspace", "universal"}},
				"proof_status": map[string]any{"type": "string", "enum": []string{"unverified", "verified", "stale", "failing"}},
				"tags":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"limit":        map[string]any{"type": "integer"},
			},
		},
	}, traced(tracer, "agent_engram_list", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEngramList(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name: "agent_engram_graph",
		Description: "Return the prerequisite-graph adjacency list rooted at a URI. " +
			"direction=down walks prerequisites, direction=up walks dependents.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"root":      map[string]any{"type": "string", "description": "Engram URI (engram://family/slug)."},
				"direction": map[string]any{"type": "string", "enum": []string{"down", "up"}, "description": "Default down (prerequisites)."},
				"max_depth": map[string]any{"type": "integer", "description": "Default 3."},
			},
			Required: []string{"root"},
		},
	}, traced(tracer, "agent_engram_graph", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleEngramGraph(ctx, args)
	}))
}
