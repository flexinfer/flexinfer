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
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// AgentBridge wraps agent-context tool calls, routing them through the daemon's
// tools/call endpoint. Each method calls the appropriate agent_context__* tool
// and unmarshals the result into a clean Go struct.
type AgentBridge struct {
	client *DaemonClient
	cache  *Cache       // session lookup cache (internal, always in-memory)
	tracer trace.Tracer // OTel tracer for bridge operations
}

const defaultSessionListLimit = 1000

// NewAgentBridge creates an AgentBridge backed by the given DaemonClient.
// The tracer defaults to a no-op; use SetTracer to enable OTel instrumentation.
func NewAgentBridge(client *DaemonClient) *AgentBridge {
	return &AgentBridge{
		client: client,
		cache:  NewCache(),
		tracer: noop.NewTracerProvider().Tracer(""),
	}
}

// SetTracer replaces the bridge's OTel tracer. Pass nil to revert to no-op.
func (a *AgentBridge) SetTracer(t trace.Tracer) {
	if t == nil {
		t = noop.NewTracerProvider().Tracer("")
	}
	a.tracer = t
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
// Each call produces an OTel span named "bridge.<toolName>".
func (a *AgentBridge) callAgentTool(toolName string, args map[string]any, target any) error {
	_, span := a.tracer.Start(context.Background(), "bridge."+toolName)
	defer span.End()

	raw, err := a.client.CallTool("agent_context__"+toolName, args)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("agent tool %s: %w", toolName, err)
	}

	if target == nil {
		if err := checkToolError(raw); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		span.SetStatus(codes.Ok, "")
		return nil
	}
	if err := UnmarshalToolResult(raw, target); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("unmarshal %s result: %w", toolName, err)
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// callAgentToolTimeout is like callAgentTool but uses a per-call timeout
// override on the underlying DaemonClient RPC.
// Each call produces an OTel span named "bridge.<toolName>".
func (a *AgentBridge) callAgentToolTimeout(toolName string, args map[string]any, target any, timeout time.Duration) error {
	_, span := a.tracer.Start(context.Background(), "bridge."+toolName)
	defer span.End()

	raw, err := a.client.CallToolWithTimeout("agent_context__"+toolName, args, timeout)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("agent tool %s: %w", toolName, err)
	}

	if target == nil {
		if err := checkToolError(raw); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		span.SetStatus(codes.Ok, "")
		return nil
	}
	if err := UnmarshalToolResult(raw, target); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("unmarshal %s result: %w", toolName, err)
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// invalidateSessionCache removes the cached active-session entry for an agent.
func (a *AgentBridge) invalidateSessionCache(agentID string) {
	a.cache.Invalidate("active_session:" + agentID)
}
