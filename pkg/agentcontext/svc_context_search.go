package agentcontext

import (
	"context"
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// --- Search & Recall ---

func (cs *ContextSvc) Search(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	agentID := v.String("agent_id", "")
	sessionID := v.String("session_id", "")
	namespace := v.String("namespace", "")
	entryTypes := v.StringSlice("entry_types")
	tags := v.StringSlice("tags")
	filePath := v.String("file_path", "")
	limit := v.Int("limit", 10)
	includeContent := v.Bool("include_content", true)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var conds []any
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}
	if sessionID != "" {
		conds = append(conds, Match("session_id", sessionID))
	}
	if namespace != "" {
		conds = append(conds, Match("namespace", namespace))
	}
	if filePath != "" {
		conds = append(conds, Match("file_path", filePath))
	}
	if len(entryTypes) > 0 {
		conds = append(conds, FilterShould(Matches("entry_type", entryTypes)...))
	}
	if len(tags) > 0 {
		conds = append(conds, MatchAny("tags", tags))
	}

	var filter map[string]any
	if len(conds) > 0 {
		filter = FilterMust(conds...)
	}

	cs.metrics.EmbeddingRequests.Add(1)
	vector, err := cs.embed.EmbedQuery(ctx, query)
	if err != nil {
		cs.metrics.EmbeddingErrors.Add(1)
		return mcp.ErrorResult(fmt.Errorf("embedding query: %w", err)), nil
	}

	searchStart := time.Now()
	results, err := cs.qdrant.Get(CollContext).Search(ctx, vector, filter, limit, includeContent)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("search: %w", err)), nil
	}
	cs.metrics.RecordSearchLatency(time.Since(searchStart).Microseconds())
	cs.metrics.RecallRequests.Add(1)

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"results": results,
		"count":   len(results),
	})
}

// --- Sharing ---

func (cs *ContextSvc) Share(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	entryIDs := v.RequiredStringSlice("entry_ids")
	targetAgents := v.RequiredStringSlice("target_agents")
	visibilityStr := v.String("visibility", string(VisibilityShared))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	visibility := Visibility(visibilityStr)

	updated := 0
	for _, id := range entryIDs {
		p, err := cs.qdrant.Get(CollContext).GetPoint(ctx, id, false)
		if err != nil || p.Payload == nil {
			continue
		}

		entry, err := PayloadToEntry(p.Payload)
		if err != nil || entry == nil {
			continue
		}
		entry.Visibility = visibility
		entry.SharedWith = uniqueStrings(append(entry.SharedWith, targetAgents...))

		payload := map[string]any{
			"visibility":  string(entry.Visibility),
			"shared_with": entry.SharedWith,
		}
		if err := cs.qdrant.Get(CollContext).SetPayload(ctx, []string{id}, payload, true); err == nil {
			updated++
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"updated": updated,
	})
}

func (cs *ContextSvc) QueryShared(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	requestingAgent := v.Required("requesting_agent_id")
	sourceAgentID := v.String("source_agent_id", "")
	entryTypes := v.StringSlice("entry_types")
	namespace := v.String("namespace", "")
	limit := v.Int("limit", 10)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	conds := []any{
		FilterShould(
			Match("visibility", string(VisibilityPublic)),
			FilterMust(
				Match("visibility", string(VisibilityShared)),
				MatchAny("shared_with", []string{requestingAgent}),
			),
		),
	}

	if sourceAgentID != "" {
		conds = append(conds, Match("agent_id", sourceAgentID))
	}
	if namespace != "" {
		conds = append(conds, Match("namespace", namespace))
	}
	if len(entryTypes) > 0 {
		conds = append(conds, FilterShould(Matches("entry_type", entryTypes)...))
	}

	filter := FilterMust(conds...)

	cs.metrics.EmbeddingRequests.Add(1)
	vector, err := cs.embed.EmbedQuery(ctx, query)
	if err != nil {
		cs.metrics.EmbeddingErrors.Add(1)
		return mcp.ErrorResult(fmt.Errorf("embedding query: %w", err)), nil
	}

	results, err := cs.qdrant.Get(CollContext).Search(ctx, vector, filter, limit, true)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("search: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"results": results,
		"count":   len(results),
	})
}
