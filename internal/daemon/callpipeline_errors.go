package daemon

import (
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/internal/router"
)

// decompHintTokenThreshold is the estimated token count above which a
// decomposition hint is emitted, suggesting the agent use the
// recursive-context workflow for large responses.
const decompHintTokenThreshold = 8000

func (p *callPipeline) transportFailure(stage string, err error, start time.Time) *mcp.Message {
	if p.conn != nil {
		p.conn.Healthy = false
	}
	p.daemon.router.RecordFailure(p.serverName, p.target, err)
	p.daemon.metrics.RecordServerFailure(p.serverName, p.targetStr, stage)
	p.daemon.metrics.RecordRequest(p.serverName, p.method, "error", p.targetStr, time.Since(start))
	p.emitErrorAudit(p.targetStr, err.Error())

	if p.target == router.TargetLocal {
		switch stage {
		case "send":
			p.daemon.logger.Warn("local server send failed; restarting", "server", p.serverName, "error", err)
		default:
			p.daemon.logger.Warn("local server recv failed; restarting", "server", p.serverName, "error", err)
		}
		p.recordTransportSpanEvent("daemon.server.restart_triggered",
			attribute.String("server.name", p.serverName),
			attribute.String("failure.stage", stage),
			attribute.String("failure.error", err.Error()),
			attribute.String("target", p.targetStr),
		)

		p.daemon.pool.ClearServer(p.serverName)
		_ = p.daemon.procMgr.Stop(p.serverName)
		p.daemon.runningServers.Delete(p.serverName)
		if p.daemon.eventBus != nil {
			reason := "transport_recv_error"
			if stage == "send" {
				reason = "transport_send_error"
			}
			p.daemon.eventBus.Publish(EventProcessStop, map[string]any{
				"server": p.serverName,
				"reason": reason,
			})
		}
	} else if p.target == router.TargetHub && p.daemon.hubPool != nil {
		p.daemon.logger.Warn("hub transport failure; clearing pool",
			"server", p.serverName, "stage", stage, "error", err)
		p.recordTransportSpanEvent("daemon.proxy.hub_pool_cleared",
			attribute.String("server.name", p.serverName),
			attribute.String("failure.stage", stage),
			attribute.String("failure.error", err.Error()),
			attribute.String("target", p.targetStr),
		)
		p.daemon.hubPool.ClearServer(p.serverName)
		if p.daemon.hubClient != nil {
			p.daemon.hubClient.CloseConnection(p.serverName)
		}
	}

	return p.internalError(err)
}

func (p *callPipeline) recordTransportSpanEvent(name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(p.ctx)
	if !span.SpanContext().IsValid() {
		return
	}
	span.AddEvent(name, trace.WithAttributes(attrs...))
}

func (p *callPipeline) invalidParamsError(message string) *mcp.Message {
	return newErrorResponse(p.msg.ID, mcp.InvalidParams, message,
		newInvalidInputPipelineError(p.serverName, p.toolName, p.stage, message))
}

func (p *callPipeline) internalError(err error) *mcp.Message {
	return newErrorResponse(p.msg.ID, mcp.InternalError, err.Error(),
		newInternalPipelineError(p.serverName, p.toolName, p.stage, err))
}

func (p *callPipeline) internalErrorWithAudit(target string, err error) *mcp.Message {
	p.emitErrorAudit(target, err.Error())
	return newErrorResponse(p.msg.ID, mcp.InternalError, err.Error(),
		newInternalPipelineError(p.serverName, p.toolName, p.stage, err))
}

func (p *callPipeline) rbacDeniedError(decision AccessDecision) *mcp.Message {
	reason := fmt.Sprintf("access denied: agent %q with role %q cannot call %s__%s (%s)",
		decision.AgentID, decision.Role, decision.Server, decision.Tool, decision.Reason)
	return newErrorResponse(p.msg.ID, mcp.InvalidRequest, reason,
		newRBACDeniedPipelineError(p.serverName, p.toolName, decision))
}

func (p *callPipeline) policyDeniedError(decision GatewayPolicyDecision) *mcp.Message {
	return newErrorResponse(p.msg.ID, mcp.InvalidRequest,
		fmt.Sprintf("policy denied: %s (%s)", decision.ReasonCode, decision.Reason),
		newPolicyDeniedPipelineError(p.serverName, p.toolName, decision))
}

func (p *callPipeline) recordSuccessMetrics(duration time.Duration) {
	latencyMs := float64(duration.Milliseconds())
	p.daemon.router.RecordSuccess(p.serverName, p.target, latencyMs)
	p.daemon.metrics.RecordServerSuccess(p.serverName, p.targetStr)
	p.daemon.metrics.RecordRequest(p.serverName, p.method, "success", p.targetStr, duration)
}

func (p *callPipeline) markLocalActivity() {
	if p.target != router.TargetLocal || p.daemon.procMgr == nil {
		return
	}
	p.daemon.procMgr.MarkActivity(p.serverName)
}

func (p *callPipeline) cacheSuccessResponse(resp *mcp.Message) {
	if p.cacheKey == "" || resp == nil || resp.Error != nil || resp.Result == nil || p.daemon.respCache == nil {
		return
	}

	p.daemon.respCache.Set(p.cacheKey, resp.Result, p.serverName, p.toolName)
	stats := p.daemon.respCache.Stats()
	p.daemon.metrics.UpdateResponseCacheStats(stats.Entries, stats.SizeBytes)
	p.daemon.logger.Debug("response cached", "server", p.serverName, "tool", p.toolName)
}

func (p *callPipeline) emitResponseAudit(resp *mcp.Message) {
	status := "success"
	errMsg := ""
	if resp != nil && resp.Error != nil {
		status = "error"
		errMsg = resp.Error.Message
	}
	p.daemon.emitAudit(p.params, p.serverName, p.toolName, p.targetStr, p.auditStart, status, errMsg, false, nil, p.stage)
}

func (p *callPipeline) emitErrorAudit(target, errMsg string) {
	p.daemon.emitAudit(p.params, p.serverName, p.toolName, target, p.auditStart, "error", errMsg, false, nil, p.stage)
}

func (p *callPipeline) emitDecompHintIfLarge(resp *mcp.Message) {
	if resp == nil || resp.Result == nil || p.daemon.eventBus == nil {
		return
	}
	// Estimate tokens: ~4 bytes per token heuristic.
	estimatedTokens := (len(resp.Result) + 3) / 4
	if estimatedTokens < decompHintTokenThreshold {
		return
	}
	p.daemon.eventBus.Publish(EventDecompHint, map[string]any{
		"server":           p.serverName,
		"tool":             p.toolName,
		"response_bytes":   len(resp.Result),
		"estimated_tokens": estimatedTokens,
		"suggestion":       "Response exceeds 8K tokens. Consider using the recursive-context workflow for decomposed analysis.",
		"workflow":         "recursive-context",
	})
}
