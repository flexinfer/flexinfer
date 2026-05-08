package agentcontext

import (
	"context"
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// Recipe represents a structured, proven solution to a specific problem class.
//
// Recipes are the Tier-1 surface of the engram tech tree (see svc_engrams.go).
// Existing recipe call sites continue to work; new fields default to safe values
// (Tier=1, empty Prerequisites, ProofStatus=unverified). Engram-aware callers
// should prefer agent_engram_* tools.
type Recipe struct {
	Title    string   `json:"title"`
	Problem  string   `json:"problem"`
	Solution string   `json:"solution"`
	Proof    string   `json:"proof"`
	Tags     []string `json:"tags,omitempty"`
	Language string   `json:"language,omitempty"`
	Scope    string   `json:"scope,omitempty"` // "project", "workspace", "universal"

	// Engram extensions (additive; optional for plain recipes).
	Family        string    `json:"family,omitempty"`        // logical group; same problem in another lang shares family
	Tier          int       `json:"tier,omitempty"`          // 1=idiom, 2=composite, 3=system; defaults to 1
	Prerequisites []string  `json:"prerequisites,omitempty"` // engram URIs this depends on
	ProofStatus   string    `json:"proof_status,omitempty"`  // unverified | verified | stale | failing
	UnlockedIn    []string  `json:"unlocked_in,omitempty"`   // repo/branch refs where proof has run green
	LastVerified  time.Time `json:"last_verified,omitempty"`
}

// HandleRecipeAdd adds a structured recipe to the memory hierarchy.
//
// Implementation now delegates to HandleEngramAdd with tier=1 and empty
// prerequisites. The result envelope keeps the legacy recipe shape so
// existing callers remain unaffected.
func (s *Service) HandleRecipeAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	v.Required("title")
	v.Required("problem")
	v.Required("solution")
	v.Required("proof")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Recipes are tier-1 engrams with no prerequisites. Force the contract
	// regardless of any tier/prerequisites the caller smuggled in.
	engramArgs := make(map[string]any, len(args)+2)
	for k, val := range args {
		switch k {
		case "tier", "prerequisites":
			// drop — recipes are always tier 1 with no prereqs
		default:
			engramArgs[k] = val
		}
	}
	engramArgs["tier"] = 1

	result, err := s.HandleEngramAdd(ctx, engramArgs)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return result, nil
	}

	// Preserve the legacy recipe response shape.
	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"title":    v.String("title", ""),
		"scope":    v.String("scope", "project"),
		"language": v.String("language", ""),
		"tags":     buildLegacyRecipeTags(v),
		"proof":    v.String("proof", ""),
	})
}

// HandleRecipeRecall recalls recipes matching a query. Searches both
// engram-category items (new) and legacy recipe-category items so historical
// data continues to surface unchanged.
func (s *Service) HandleRecipeRecall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	recallArgs := map[string]any{
		"query":      query,
		"categories": []string{"engram", "recipe"},
		"tiers":      []string{"long_term"},
	}

	if limit := v.Int("limit", 0); limit > 0 {
		recallArgs["limit"] = limit
	}
	if budget := v.Int("token_budget", 0); budget > 0 {
		recallArgs["token_budget"] = budget
	}

	tags := buildRecipeTagFilters(v)
	if len(tags) > 0 {
		recallArgs["tags"] = tags
	}

	return s.HandleMemoryRecall(ctx, recallArgs)
}

// HandleRecipeList lists recipes, optionally filtered. Searches both engram
// and legacy recipe categories.
func (s *Service) HandleRecipeList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)

	recallArgs := map[string]any{
		"query":      "*",
		"categories": []string{"engram", "recipe"},
		"tiers":      []string{"long_term"},
		"limit":      v.Int("limit", 50),
	}

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

// buildLegacyRecipeTags reconstructs the tag list a recipe-add response used
// to advertise (recipe + lang:X + scope:Y + caller tags). Engram storage
// tags are richer; this helper keeps the legacy response stable.
func buildLegacyRecipeTags(v *validate.Args) []string {
	tags := v.StringSlice("tags")
	tags = append(tags, "recipe")
	if lang := v.String("language", ""); lang != "" {
		tags = append(tags, "lang:"+lang)
	}
	scope := v.String("scope", "project")
	tags = append(tags, "scope:"+scope)
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
