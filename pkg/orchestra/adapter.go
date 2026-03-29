package orchestra

import (
	"context"

	"github.com/crb2nu/loom/pkg/openairesponses"
)

// ToolInfo describes a tool available in the daemon's tool cache.
type ToolInfo struct {
	Name        string
	Description string
	InputSchema map[string]any
	Server      string
}

// ToolLister provides tool metadata from the daemon's tool cache.
type ToolLister interface {
	ListTools() ([]ToolInfo, error)
}

// SubAgentAdapter implements openairesponses.ToolAdapter, scoped to a single
// SubAgent's tool list. It filters the daemon's full tool inventory down to
// just the tools this subagent is allowed to use.
type SubAgentAdapter struct {
	agent  SubAgent
	lister ToolLister
}

// NewSubAgentAdapter creates an adapter for the given subagent.
func NewSubAgentAdapter(agent SubAgent, lister ToolLister) *SubAgentAdapter {
	return &SubAgentAdapter{agent: agent, lister: lister}
}

// BuildTools returns only the tools belonging to this subagent's domain.
func (a *SubAgentAdapter) BuildTools(_ context.Context, _ openairesponses.ExecutionIdentity) ([]openairesponses.ToolDefinition, error) {
	allTools, err := a.lister.ListTools()
	if err != nil {
		return nil, err
	}

	allowed := make(map[string]bool, len(a.agent.Tools))
	for _, t := range a.agent.Tools {
		allowed[t] = true
	}

	var defs []openairesponses.ToolDefinition
	for _, t := range allTools {
		if allowed[t.Name] {
			defs = append(defs, openairesponses.ToolDefinition{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
				Server:      t.Server,
				Tool:        t.Name,
			})
		}
	}
	return defs, nil
}

// ResolveCall passes through the tool call unchanged since tools are
// already namespaced with server prefix.
func (a *SubAgentAdapter) ResolveCall(_ context.Context, call openairesponses.ToolCall) (openairesponses.ToolCall, error) {
	return call, nil
}
