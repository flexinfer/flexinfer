package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/openairesponses"
	"github.com/crb2nu/loom/pkg/weaver"
)

// handleOrchestraQuery handles loom/weaver/query requests.
func (d *Daemon) handleOrchestraQuery(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.weaver == nil {
		return newErrorResponse(msg.ID, mcp.InternalError,
			"weaver is not enabled", nil), nil
	}

	var params struct {
		Query     string   `json:"query"`
		Domains   []string `json:"domains,omitempty"`
		MaxTokens int      `json:"max_tokens,omitempty"`
		AgentID   string   `json:"agent_id,omitempty"`
		SessionID string   `json:"session_id,omitempty"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return newErrorResponse(msg.ID, mcp.InvalidParams,
			fmt.Sprintf("invalid params: %v", err), nil), nil
	}
	if params.Query == "" {
		return newErrorResponse(msg.ID, mcp.InvalidParams,
			"query is required", nil), nil
	}

	req := weaver.QueryRequest{
		Query:     params.Query,
		Domains:   params.Domains,
		MaxTokens: params.MaxTokens,
		Identity: openairesponses.ExecutionIdentity{
			AgentID:   params.AgentID,
			SessionID: params.SessionID,
		},
	}

	result, err := d.weaver.Query(ctx, req)
	if err != nil {
		return newErrorResponse(msg.ID, mcp.InternalError,
			fmt.Sprintf("weaver query failed: %v", err), nil), nil
	}

	return mcp.NewResponse(msg.ID, result)
}

// handleOrchestraGather handles loom/weaver/gather requests.
func (d *Daemon) handleOrchestraGather(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.weaver == nil {
		return newErrorResponse(msg.ID, mcp.InternalError,
			"weaver is not enabled", nil), nil
	}

	var params struct {
		Query     string   `json:"query"`
		Domains   []string `json:"domains"`
		AgentID   string   `json:"agent_id,omitempty"`
		SessionID string   `json:"session_id,omitempty"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return newErrorResponse(msg.ID, mcp.InvalidParams,
			fmt.Sprintf("invalid params: %v", err), nil), nil
	}
	if params.Query == "" {
		return newErrorResponse(msg.ID, mcp.InvalidParams,
			"query is required", nil), nil
	}
	if len(params.Domains) == 0 {
		return newErrorResponse(msg.ID, mcp.InvalidParams,
			"domains is required for gather", nil), nil
	}

	identity := openairesponses.ExecutionIdentity{
		AgentID:   params.AgentID,
		SessionID: params.SessionID,
	}

	result, err := d.weaver.Gather(ctx, params.Domains, params.Query, identity)
	if err != nil {
		return newErrorResponse(msg.ID, mcp.InternalError,
			fmt.Sprintf("weaver gather failed: %v", err), nil), nil
	}

	return mcp.NewResponse(msg.ID, result)
}

// handleOrchestraStatus handles loom/weaver/status requests.
func (d *Daemon) handleOrchestraStatus(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.weaver == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"enabled": false,
		})
	}
	return mcp.NewResponse(msg.ID, d.weaver.Status())
}

// handleOrchestraHistory handles loom/weaver/history requests.
func (d *Daemon) handleOrchestraHistory(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.weaver == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"entries": []any{},
		})
	}
	return mcp.NewResponse(msg.ID, map[string]any{
		"entries": d.weaver.History(),
	})
}

// handleWeaverMetrics handles loom/weaver/metrics requests.
func (d *Daemon) handleWeaverMetrics(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.weaver == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"total_queries": 0, "avg_latency_ms": 0,
			"error_rate": 0, "total_tokens": 0, "error_count": 0,
		})
	}
	summary := d.weaver.MetricsSummary()
	if summary == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"total_queries": 0, "avg_latency_ms": 0,
			"error_rate": 0, "total_tokens": 0, "error_count": 0,
		})
	}
	return mcp.NewResponse(msg.ID, summary)
}
