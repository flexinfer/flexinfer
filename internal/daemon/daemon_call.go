package daemon

import (
	"context"
	"encoding/json"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

type callParams struct {
	Server    string          `json:"server,omitempty"`
	Tool      string          `json:"tool,omitempty"` // For smart routing without prefix
	Name      string          `json:"name,omitempty"` // MCP standard tools/call format
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`  // For smart routing
	AgentID   string          `json:"agent_id,omitempty"`   // Agent identity for RBAC
	AgentType string          `json:"agent_type,omitempty"` // Agent type for RBAC
	SessionID string          `json:"session_id,omitempty"` // Proxy session lease ID
}

func (d *Daemon) handleCall(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	d.activeRPCs.Add(1)
	defer d.activeRPCs.Add(-1)

	pipeline := newCallPipeline(d, ctx, msg)

	if resp := pipeline.parseAndResolve(); resp != nil {
		return resp, nil
	}

	// Implicitly touch session lease on every call that carries a session_id.
	if pipeline.params.SessionID != "" && d.sessions != nil {
		d.sessions.Touch(pipeline.params.SessionID)
	}

	if resp := pipeline.authorize(); resp != nil {
		return resp, nil
	}

	if resp := pipeline.enforceRequestPolicy(); resp != nil {
		return resp, nil
	}

	if resp := pipeline.tryCachedResponse(); resp != nil {
		return resp, nil
	}

	if resp := pipeline.routeAndConnect(); resp != nil {
		return resp, nil
	}
	defer pipeline.releaseConnection()

	req, resp := pipeline.buildForwardRequest()
	if resp != nil {
		return resp, nil
	}

	return pipeline.execute(req), nil
}

// emitAudit writes a structured audit entry and cost record if enabled.
func (d *Daemon) emitAudit(params callParams, server, tool, target string, start time.Time, status, errMsg string, cached bool, policy *GatewayPolicyDecision) {
	durationMs := time.Since(start).Milliseconds()

	policyRuleID := ""
	policyReasonCode := ""
	if policy != nil {
		policyRuleID = policy.RuleID
		policyReasonCode = policy.ReasonCode
	}

	if d.audit != nil {
		d.audit.Log(AuditEntry{
			Timestamp:        start.UTC(),
			AgentID:          params.AgentID,
			AgentType:        params.AgentType,
			Server:           server,
			Tool:             tool,
			DurationMs:       durationMs,
			Status:           status,
			Error:            errMsg,
			Target:           target,
			Cached:           cached,
			PolicyRuleID:     policyRuleID,
			PolicyReasonCode: policyReasonCode,
		})
	}

	if d.cost != nil {
		costStatus := status
		if cached {
			costStatus = "cached"
		}
		d.cost.Record(UsageRecord{
			AgentID:    params.AgentID,
			AgentType:  params.AgentType,
			Server:     server,
			Tool:       tool,
			DurationMs: durationMs,
			Status:     costStatus,
		})
	}
}

// logAccessDecision logs an RBAC access decision and publishes deny events.
func (d *Daemon) logAccessDecision(decision AccessDecision) {
	if decision.Allowed {
		d.logger.Debug("rbac allowed",
			"agent_id", decision.AgentID,
			"server", decision.Server,
			"tool", decision.Tool,
			"role", decision.Role,
			"reason", decision.Reason,
		)
		return
	}
	d.logger.Warn("rbac denied",
		"agent_id", decision.AgentID,
		"server", decision.Server,
		"tool", decision.Tool,
		"role", decision.Role,
		"reason", decision.Reason,
	)
	if d.eventBus != nil {
		d.eventBus.Publish(EventAccessDenied, decision)
	}
	d.metrics.RBACDenied.Inc()
}
