package main

import (
	"context"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

func registerRecipeTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	server.AddTool(mcp.Tool{
		Name:        "agent_recipe_add",
		Description: "Add a structured recipe with problem, solution, and required proof. Recipes are deterministic, proven answers to specific problem classes.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Short, descriptive title for the recipe.",
				},
				"problem": map[string]any{
					"type":        "string",
					"description": "Description of the problem this recipe solves.",
				},
				"solution": map[string]any{
					"type":        "string",
					"description": "Step-by-step solution with code examples.",
				},
				"proof": map[string]any{
					"type":        "string",
					"description": "Required proof that the solution works: file path + line range, test command, or URL.",
				},
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Tags for categorization and recall.",
				},
				"language": map[string]any{
					"type":        "string",
					"description": "Programming language (e.g., 'go', 'python', 'typescript').",
				},
				"scope": map[string]any{
					"type":        "string",
					"enum":        []string{"project", "workspace", "universal"},
					"description": "Recipe scope: project (this repo), workspace (all repos), universal (any context). Default: project.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Optional session ID for context.",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Agent ID.",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace for organization.",
				},
			},
			Required: []string{"title", "problem", "solution", "proof"},
		},
	}, traced(tracer, "agent_recipe_add", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleRecipeAdd(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name:        "agent_recipe_recall",
		Description: "Recall recipes matching a query. Returns structured recipes with problem, solution, and proof.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query to match against recipe titles, problems, and solutions.",
				},
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by tags.",
				},
				"language": map[string]any{
					"type":        "string",
					"description": "Filter by programming language.",
				},
				"scope": map[string]any{
					"type":        "string",
					"enum":        []string{"project", "workspace", "universal"},
					"description": "Filter by scope.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum recipes to return (default: 10).",
				},
				"token_budget": map[string]any{
					"type":        "integer",
					"description": "Maximum tokens to return (default: 4000).",
				},
			},
			Required: []string{"query"},
		},
	}, traced(tracer, "agent_recipe_recall", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleRecipeRecall(ctx, args)
	}))

	server.AddTool(mcp.Tool{
		Name:        "agent_recipe_list",
		Description: "List all recipes, optionally filtered by tags, language, or scope.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"tags": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by tags.",
				},
				"language": map[string]any{
					"type":        "string",
					"description": "Filter by programming language.",
				},
				"scope": map[string]any{
					"type":        "string",
					"enum":        []string{"project", "workspace", "universal"},
					"description": "Filter by scope.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum recipes to return (default: 50).",
				},
			},
		},
	}, traced(tracer, "agent_recipe_list", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return svc.HandleRecipeList(ctx, args)
	}))
}
