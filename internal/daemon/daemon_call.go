package daemon

import (
	"context"
	"encoding/json"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
	Timeout   string          `json:"_timeout,omitempty"`   // RPC timeout hint (Go duration string)
}

func (d *Daemon) handleCall(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	d.activeRPCs.Add(1)
	defer d.activeRPCs.Add(-1)

	// Acquire daemon-wide concurrency semaphore (before per-server lock).
	if d.callSem != nil {
		select {
		case d.callSem <- struct{}{}:
			defer func() { <-d.callSem }()
		case <-ctx.Done():
			return mcp.NewErrorResponse(msg.ID, mcp.InternalError, "call concurrency limit reached"), nil
		}
	}

	pipeline := newCallPipeline(d, ctx, msg)

	if resp := pipeline.parseAndResolve(); resp != nil {
		return resp, nil
	}

	// Enrich the parent RPC span with resolved call metadata.
	// Use the original ctx (not pipeline.ctx) because pipeline stages update
	// p.ctx to carry their own child spans.
	if parentSpan := trace.SpanFromContext(ctx); parentSpan.SpanContext().IsValid() {
		attrs := []attribute.KeyValue{
			attribute.String("mcp.tool", pipeline.toolName),
			attribute.String("mcp.server", pipeline.serverName),
		}
		if pipeline.params.AgentID != "" {
			attrs = append(attrs, attribute.String("mcp.agent_id", pipeline.params.AgentID))
		}
		if pipeline.params.SessionID != "" {
			attrs = append(attrs, attribute.String("mcp.session_id", pipeline.params.SessionID))
		}
		parentSpan.SetAttributes(attrs...)
	}

	// Implicitly touch session lease on every call that carries a session_id.
	if pipeline.params.SessionID != "" && d.sessions != nil {
		d.sessions.Touch(pipeline.params.SessionID)
	}

	if resp := pipeline.validateInputSchema(); resp != nil {
		return resp, nil
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

	execResp := pipeline.execute(req)

	// Post-execute output scanning for PII/secrets.
	if scanned := pipeline.scanOutputForPII(execResp); scanned != nil {
		return scanned, nil
	}

	return execResp, nil
}

// emitAudit writes a structured audit entry and cost record if enabled.
func (d *Daemon) emitAudit(params callParams, server, tool, target string, start time.Time, status, errMsg string, cached bool, policy *GatewayPolicyDecision, pipelineStage string) {
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
			PipelineStage:    pipelineStage,
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

	// Record in ring buffer for HUD visibility.
	d.recordDenied(decision)
}

// deniedEntry stores a denied access decision with timestamp.
type deniedEntry struct {
	AgentID   string    `json:"agent_id"`
	Server    string    `json:"server"`
	Tool      string    `json:"tool"`
	Role      string    `json:"role,omitempty"`
	Reason    string    `json:"reason"`
	Timestamp time.Time `json:"timestamp"`
}

// recordDenied appends a denied decision to the ring buffer.
func (d *Daemon) recordDenied(decision AccessDecision) {
	d.deniedMu.Lock()
	defer d.deniedMu.Unlock()
	entry := deniedEntry{
		AgentID:   decision.AgentID,
		Server:    decision.Server,
		Tool:      decision.Tool,
		Role:      decision.Role,
		Reason:    decision.Reason,
		Timestamp: time.Now().UTC(),
	}
	if len(d.recentDenied) >= 50 {
		d.recentDenied = d.recentDenied[1:]
	}
	d.recentDenied = append(d.recentDenied, entry)
}

// recentDeniedSnapshot returns a copy of the denied calls ring buffer.
func (d *Daemon) recentDeniedSnapshot() []deniedEntry {
	d.deniedMu.RLock()
	defer d.deniedMu.RUnlock()
	cp := make([]deniedEntry, len(d.recentDenied))
	copy(cp, d.recentDenied)
	return cp
}
