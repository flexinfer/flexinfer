package agentcontext

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// HandleTaskDispatch implements the `agent_task_dispatch` MCP tool.
//
// v1 contract: accepts `capability_needed` (optional []string) directly from
// args to avoid coupling to a not-yet-exported task-get helper. A later slice
// will add task_id resolution. Loads the capability seed YAML + active
// presence, calls ChooseAgent, returns `{ok, agent_id, reason}`.
//
// v1 deliberately does NOT actually spawn — it returns the dispatch decision
// only, per .loom/87 §5.F6.
func (s *Service) HandleTaskDispatch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	capabilityNeeded := v.StringSlice("capability_needed")
	scope := v.String("scope", "session")
	_ = scope // retained for forward-compat; honored in task_id path later

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	s.metrics.FleetDispatchRequests.Add(1)

	capMap, err := LoadAgentCapabilities("")
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("load capabilities: %w", err)), nil
	}

	presenceResult, err := s.presence.List(ctx, map[string]any{})
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("list presence: %w", err)), nil
	}

	candidates := buildDispatchCandidates(presenceResult, capMap)

	task := Task{CapabilityNeeded: capabilityNeeded}
	agentID, reason := ChooseAgent(task, candidates)

	if reason != "chosen" {
		s.metrics.FleetDispatchMismatches.Add(1)
	}

	return mcp.JSONResult(map[string]any{
		"ok":                    true,
		"agent_id":              agentID,
		"reason":                reason,
		"candidates_considered": len(candidates),
	})
}

// buildDispatchCandidates extracts agent ids from the presence list result
// and joins them with the capability seed. Presence entries without a matching
// capability entry get an empty capability list — they survive only when the
// task has no requirement.
func buildDispatchCandidates(presenceResult *mcp.CallToolResult, capMap CapabilityMap) []AgentCandidate {
	if presenceResult == nil || len(presenceResult.Content) == 0 {
		return nil
	}
	raw := presenceResult.Content[0].Text
	if raw == "" {
		return nil
	}
	var parsed struct {
		Agents []struct {
			AgentID string `json:"agent_id"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}
	seen := make(map[string]bool, len(parsed.Agents))
	out := make([]AgentCandidate, 0, len(parsed.Agents))
	for _, a := range parsed.Agents {
		if a.AgentID == "" || seen[a.AgentID] {
			continue
		}
		seen[a.AgentID] = true
		out = append(out, AgentCandidate{
			AgentID:      a.AgentID,
			Capabilities: capMap[a.AgentID],
			Load:         0, // Presence load metric TBD; stub zero for v1.
		})
	}
	return out
}
