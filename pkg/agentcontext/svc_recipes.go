package agentcontext

import (
	"context"
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// Recipe represents a structured, proven solution to a specific problem class.
type Recipe struct {
	Title    string   `json:"title"`
	Problem  string   `json:"problem"`
	Solution string   `json:"solution"`
	Proof    string   `json:"proof"`
	Tags     []string `json:"tags,omitempty"`
	Language string   `json:"language,omitempty"`
	Scope    string   `json:"scope,omitempty"` // "project", "workspace", "universal"
}

// HandleRecipeAdd adds a structured recipe to the memory hierarchy.
// Recipes are stored as long-term memory items with category "recipe".
func (s *Service) HandleRecipeAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	title := v.Required("title")
	problem := v.Required("problem")
	solution := v.Required("solution")
	proof := v.Required("proof")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Build tags
	tags := v.StringSlice("tags")
	tags = append(tags, "recipe") // Always tag as recipe

	language := v.String("language", "")
	if language != "" {
		tags = append(tags, "lang:"+language)
	}

	scope := v.String("scope", "project")
	tags = append(tags, "scope:"+scope)

	// Build content as structured markdown
	content := fmt.Sprintf("## Problem\n\n%s\n\n## Solution\n\n%s\n\n## Proof\n\n%s", problem, solution, proof)

	// Build metadata
	metadata := map[string]any{
		"recipe_problem":  problem,
		"recipe_solution": solution,
		"recipe_proof":    proof,
		"recipe_language": language,
		"recipe_scope":    scope,
		"created_at":      time.Now().UTC().Format(time.RFC3339),
	}

	// Store as a long-term memory item via the existing memory infrastructure
	memoryArgs := map[string]any{
		"items": []any{
			map[string]any{
				"title":      title,
				"content":    content,
				"tier":       "long_term",
				"importance": "high",
				"category":   "recipe",
				"tags":       toAnySlice(tags),
				"metadata":   metadata,
			},
		},
	}

	// Pass through optional context fields
	if sid := v.String("session_id", ""); sid != "" {
		memoryArgs["session_id"] = sid
	}
	if aid := v.String("agent_id", ""); aid != "" {
		memoryArgs["agent_id"] = aid
	}
	if ns := v.String("namespace", ""); ns != "" {
		memoryArgs["namespace"] = ns
	}

	result, err := s.HandleMemoryAdd(ctx, memoryArgs)
	if err != nil {
		return nil, err
	}

	// Wrap the result to indicate it is a recipe
	if !result.IsError {
		return mcp.JSONResult(map[string]any{
			"ok":       true,
			"title":    title,
			"scope":    scope,
			"language": language,
			"tags":     tags,
			"proof":    proof,
		})
	}

	return result, nil
}

// HandleRecipeRecall recalls recipes matching a query using the memory recall system.
func (s *Service) HandleRecipeRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Build recall args with recipe-specific filters
	recallArgs := map[string]any{
		"query":      query,
		"categories": []string{"recipe"},
		"tiers":      []string{"long_term"},
	}

	if limit := v.Int("limit", 0); limit > 0 {
		recallArgs["limit"] = limit
	}
	if budget := v.Int("token_budget", 0); budget > 0 {
		recallArgs["token_budget"] = budget
	}

	// Build tag filters
	tags := buildRecipeTagFilters(v)
	if len(tags) > 0 {
		recallArgs["tags"] = tags
	}

	return s.HandleMemoryRecall(ctx, recallArgs)
}

// HandleRecipeList lists recipes, optionally filtered.
func (s *Service) HandleRecipeList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)

	// Build recall args for listing
	recallArgs := map[string]any{
		"query":      "*",
		"categories": []string{"recipe"},
		"tiers":      []string{"long_term"},
		"limit":      v.Int("limit", 50),
	}

	// Build tag filters
	tags := buildRecipeTagFilters(v)
	if len(tags) > 0 {
		recallArgs["tags"] = tags
	}

	result, err := s.HandleMemoryRecall(ctx, recallArgs)
	if err != nil {
		return nil, fmt.Errorf("recipe list: %w", err)
	}

	return result, nil
}

// buildRecipeTagFilters collects language, scope, and explicit tags into a
// combined tag filter slice for the memory recall system.
func buildRecipeTagFilters(v *validate.Args) []string {
	tags := v.StringSlice("tags")

	if lang := v.String("language", ""); lang != "" {
		tags = append(tags, "lang:"+lang)
	}
	if scope := v.String("scope", ""); scope != "" {
		tags = append(tags, "scope:"+scope)
	}

	return tags
}

// toAnySlice converts []string to []any so it can be embedded in map[string]any
// for the memory args interface.
func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
