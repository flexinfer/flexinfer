package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/openairesponses"
	"github.com/crb2nu/loom/pkg/orchestra"
)

// handleOrchestraQuery handles loom/orchestra/query requests.
func (d *Daemon) handleOrchestraQuery(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.orchestra == nil {
		return newErrorResponse(msg.ID, mcp.InternalError,
			"orchestra is not enabled", nil), nil
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

	req := orchestra.QueryRequest{
		Query:     params.Query,
		Domains:   params.Domains,
		MaxTokens: params.MaxTokens,
		Identity: openairesponses.ExecutionIdentity{
			AgentID:   params.AgentID,
			SessionID: params.SessionID,
		},
	}

	result, err := d.orchestra.Query(ctx, req)
	if err != nil {
		return newErrorResponse(msg.ID, mcp.InternalError,
			fmt.Sprintf("orchestra query failed: %v", err), nil), nil
	}

	return mcp.NewResponse(msg.ID, result)
}

// handleOrchestraGather handles loom/orchestra/gather requests.
func (d *Daemon) handleOrchestraGather(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.orchestra == nil {
		return newErrorResponse(msg.ID, mcp.InternalError,
			"orchestra is not enabled", nil), nil
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

	result, err := d.orchestra.Gather(ctx, params.Domains, params.Query, identity)
	if err != nil {
		return newErrorResponse(msg.ID, mcp.InternalError,
			fmt.Sprintf("orchestra gather failed: %v", err), nil), nil
	}

	return mcp.NewResponse(msg.ID, result)
}

// handleOrchestraStatus handles loom/orchestra/status requests.
func (d *Daemon) handleOrchestraStatus(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.orchestra == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"enabled": false,
		})
	}
	return mcp.NewResponse(msg.ID, d.orchestra.Status())
}
