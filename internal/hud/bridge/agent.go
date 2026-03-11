// agent.go defines the AgentBridge struct, its constructor, internal helpers,
// and constants used across domain files.
//
// DTO types are in agent_dto.go.
//
// Domain files:
//   - agent_session.go    — Session lifecycle (Start/End/Get/List/Prune) + presence
//   - agent_task.go       — Task CRUD + dispatch
//   - agent_context.go    — Context inspect/stream/knowledge + budget helpers
//   - agent_graph.go      — Knowledge graph: entities, relations, annotations
//   - agent_ops.go        — Workflows, memory, handoffs, templates, coordination
//   - agent_contracts.go  — Shared HTTP request/response contracts
//   - agent_dto.go        — Shared DTO types
package bridge

import (
	"fmt"
	"strings"
	"time"
)

// AgentBridge wraps agent-context tool calls, routing them through the daemon's
// tools/call endpoint. Each method calls the appropriate agent_context__* tool
// and unmarshals the result into a clean Go struct.
type AgentBridge struct {
	client *DaemonClient
	cache  *Cache // session lookup cache (internal, always in-memory)
}

const defaultSessionListLimit = 1000

// NewAgentBridge creates an AgentBridge backed by the given DaemonClient.
func NewAgentBridge(client *DaemonClient) *AgentBridge {
	return &AgentBridge{client: client, cache: NewCache()}
}

const (
	contextInspectSystemPromptTokensDefault   = 768
	contextInspectResponseBudgetTokensDefault = 2048
)

// --- Internal helpers ---

func normalizeEntityInfo(e *EntityInfo) {
	if e == nil {
		return
	}
	if e.EntityType == "" {
		e.EntityType = e.Type
	}
	if e.Type == "" {
		e.Type = e.EntityType
	}
}

func normalizeRelationInfo(r *RelationInfo) {
	if r == nil {
		return
	}
	if r.RelationType == "" {
		r.RelationType = r.Type
	}
	if r.Type == "" {
		r.Type = r.RelationType
	}
}

func isUnknownToolErr(err error, toolName string) bool {
	if err == nil || strings.TrimSpace(toolName) == "" {
		return false
	}
	return strings.Contains(err.Error(), "unknown tool: "+toolName)
}

// callAgentTool invokes an agent_context tool and unmarshals the response
// into the provided target. It unwraps the MCP CallToolResult envelope and
// supports both JSON and TOON (Token-Optimized Object Notation) text payloads.
func (a *AgentBridge) callAgentTool(toolName string, args map[string]any, target any) error {
	raw, err := a.client.CallTool("agent_context__"+toolName, args)
	if err != nil {
		return fmt.Errorf("agent tool %s: %w", toolName, err)
	}

	if err := UnmarshalToolResult(raw, target); err != nil {
		return fmt.Errorf("unmarshal %s result: %w", toolName, err)
	}
	return nil
}

// callAgentToolTimeout is like callAgentTool but uses a per-call timeout
// override on the underlying DaemonClient RPC.
func (a *AgentBridge) callAgentToolTimeout(toolName string, args map[string]any, target any, timeout time.Duration) error {
	raw, err := a.client.CallToolWithTimeout("agent_context__"+toolName, args, timeout)
	if err != nil {
		return fmt.Errorf("agent tool %s: %w", toolName, err)
	}

	if err := UnmarshalToolResult(raw, target); err != nil {
		return fmt.Errorf("unmarshal %s result: %w", toolName, err)
	}
	return nil
}

// invalidateSessionCache removes the cached active-session entry for an agent.
func (a *AgentBridge) invalidateSessionCache(agentID string) {
	a.cache.Invalidate("active_session:" + agentID)
}
