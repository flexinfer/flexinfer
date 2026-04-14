package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/internal/router"
)

func (p *callPipeline) parseAndResolve() *mcp.Message {
	p.stage = stageParse
	span := p.startStageSpan("daemon.pipeline.parse")
	defer span.End()

	if err := json.Unmarshal(p.msg.Params, &p.params); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return p.invalidParamsError(err.Error())
	}

	p.serverName = p.params.Server
	p.toolName = p.params.Tool
	if p.toolName == "" && p.params.Name != "" {
		p.toolName = p.params.Name
	}

	if p.serverName == "" && strings.Contains(p.toolName, "__") {
		parts := strings.SplitN(p.toolName, "__", 2)
		if len(parts) == 2 {
			p.serverName = parts[0]
			p.toolName = parts[1]
		}
	}

	p.method = p.params.Method
	if p.method == "" {
		p.method = "tools/call"
	}

	if p.serverName == "" && p.toolName != "" {
		if p.daemon.router == nil {
			return p.internalError(fmt.Errorf("daemon is starting"))
		}

		var args map[string]any
		if len(p.params.Arguments) > 0 {
			_ = json.Unmarshal(p.params.Arguments, &args)
		} else if len(p.params.Params) > 0 {
			_ = json.Unmarshal(p.params.Params, &args)
		}

		resolved, err := p.daemon.router.ResolveServer(p.daemon.cfg.Target, p.toolName, args)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return p.internalError(err)
		}
		if resolved == "" {
			errMsg := fmt.Sprintf("could not resolve server for tool: %s", p.toolName)
			span.SetStatus(codes.Error, errMsg)
			return p.invalidParamsError(errMsg)
		}
		p.serverName = resolved
	}

	if p.serverName == "" {
		errMsg := "missing server or tool for call"
		span.SetStatus(codes.Error, errMsg)
		return p.invalidParamsError(errMsg)
	}

	span.SetAttributes(
		attribute.String("mcp.tool", p.toolName),
		attribute.String("mcp.server", p.serverName),
	)

	p.auditStart = time.Now()
	return nil
}

func (p *callPipeline) authorize() *mcp.Message {
	p.stage = stageAuth
	span := p.startStageSpan("daemon.pipeline.authorize")
	defer span.End()

	if p.daemon.rbac == nil {
		span.SetAttributes(attribute.String("rbac.decision", "skipped"))
		return nil
	}
	decision := p.daemon.rbac.Check(p.params.AgentID, p.params.AgentType, p.serverName, p.toolName)
	p.daemon.logAccessDecision(decision)

	span.SetAttributes(
		attribute.String("mcp.agent_id", p.params.AgentID),
		attribute.Bool("rbac.allowed", decision.Allowed),
		attribute.String("rbac.role", decision.Role),
	)

	if decision.Allowed {
		return nil
	}
	span.SetStatus(codes.Error, decision.Reason)
	p.daemon.emitAudit(p.params, p.serverName, p.toolName, "", p.auditStart, "denied", decision.Reason, false, nil, p.stage, 0, 0, p.auditTimings())
	return p.rbacDeniedError(decision)
}

func (p *callPipeline) enforceRequestPolicy() *mcp.Message {
	p.stage = stagePolicy
	span := p.startStageSpan("daemon.pipeline.policy")
	defer span.End()

	if p.daemon.policy == nil {
		span.SetAttributes(attribute.String("policy.decision", "skipped"))
		return nil
	}

	decision := p.daemon.policy.CheckRequest(p.serverName, p.toolName, p.params)
	span.SetAttributes(attribute.Bool("policy.allowed", decision.Allowed))

	if decision.Allowed {
		return nil
	}

	span.SetAttributes(
		attribute.String("policy.reason_code", decision.ReasonCode),
		attribute.String("policy.rule_id", decision.RuleID),
	)
	span.SetStatus(codes.Error, decision.Reason)

	p.daemon.emitAudit(
		p.params,
		p.serverName,
		p.toolName,
		"",
		p.auditStart,
		"denied",
		decision.Reason,
		false,
		&decision,
		p.stage,
		0, 0,
		p.auditTimings(),
	)
	return p.policyDeniedError(decision)
}

func (p *callPipeline) tryCachedResponse() *mcp.Message {
	p.stage = stageCache
	span := p.startStageSpan("daemon.pipeline.cache")
	defer span.End()

	if p.daemon.respCache == nil || !p.daemon.respCache.IsCacheable(p.serverName, p.toolName) {
		span.SetAttributes(attribute.Bool("cache.hit", false))
		span.AddEvent("daemon.pipeline.cache.skipped")
		return nil
	}

	cacheParams := p.params.Params
	if len(cacheParams) == 0 {
		cacheParams = p.params.Arguments
	}
	p.cacheKey = p.daemon.respCache.Key(p.serverName, p.toolName, cacheParams)

	if cached, ok := p.daemon.respCache.Get(p.cacheKey); ok {
		span.SetAttributes(attribute.Bool("cache.hit", true))
		span.AddEvent("daemon.pipeline.cache.hit", trace.WithAttributes(
			attribute.String("mcp.server", p.serverName),
			attribute.String("mcp.tool", p.toolName),
		))
		p.daemon.metrics.RecordResponseCacheHit(p.serverName, p.toolName)
		if p.daemon.otelMetrics != nil {
			p.daemon.otelMetrics.RecordCacheOp(p.ctx, p.serverName, p.toolName, "hit")
		}
		p.daemon.logger.Debug("response cache hit", "server", p.serverName, "tool", p.toolName)
		p.daemon.emitAudit(p.params, p.serverName, p.toolName, "local", p.auditStart, "success", "", true, nil, p.stage, 0, 0, p.auditTimings())
		resp, _ := mcp.NewResponse(p.msg.ID, json.RawMessage(cached))
		return resp
	}

	span.SetAttributes(attribute.Bool("cache.hit", false))
	span.AddEvent("daemon.pipeline.cache.miss", trace.WithAttributes(
		attribute.String("mcp.server", p.serverName),
		attribute.String("mcp.tool", p.toolName),
	))
	p.daemon.metrics.RecordResponseCacheMiss(p.serverName, p.toolName)
	if p.daemon.otelMetrics != nil {
		p.daemon.otelMetrics.RecordCacheOp(p.ctx, p.serverName, p.toolName, "miss")
	}
	return nil
}

func (p *callPipeline) routeAndConnect() *mcp.Message {
	p.stage = stageRoute
	span := p.startStageSpan("daemon.pipeline.route")
	start := time.Now()
	defer func() { p.routeDurationMs = time.Since(start).Milliseconds() }()
	defer span.End()

	if p.daemon.router == nil {
		err := fmt.Errorf("daemon is starting")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return p.internalError(err)
	}

	decision, err := p.daemon.router.Route(p.ctx, p.serverName)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return p.internalErrorWithAudit("", err)
	}

	p.routingPreference = RoutingHealthBased
	p.preferHubRetryEligible = false

	// Hub delegation: when the server is in the delegate list and the hub is
	// healthy, override the routing decision to TargetHub so the daemon
	// relays the call to the in-cluster service instead of spawning a local
	// subprocess. This is the key "thin daemon" optimisation.
	if p.daemon.hubDelegateEligible(p.serverName) {
		decision.Target = router.TargetHub
		p.daemon.logger.Debug("hub delegation active", "server", p.serverName)
		span.AddEvent("daemon.pipeline.route.hub_delegate", trace.WithAttributes(
			attribute.String("mcp.server", p.serverName),
		))
	}

	if pref, ok := p.daemon.routingPreferences[p.serverName]; ok && pref != RoutingHealthBased {
		p.routingPreference = pref
		hasHub := p.daemon.hubPool != nil
		allowPreferHub := true
		if pref == RoutingPreferHub {
			if active, until := p.daemon.preferHubBackoffActive(p.serverName); active {
				allowPreferHub = false
				p.daemon.logger.Debug("prefer-hub override suppressed by backoff",
					"server", p.serverName,
					"backoff_until", until)
			}
		}

		originalTarget := decision.Target
		newTarget, overridden := applyRoutingPreferenceWithOptions(pref, originalTarget, hasHub, allowPreferHub)
		if overridden {
			p.daemon.logger.Debug("routing preference override",
				"server", p.serverName,
				"preference", pref,
				"original", originalTarget,
				"overridden_to", newTarget)
		}
		if pref == RoutingPreferHub && allowPreferHub && originalTarget == router.TargetLocal && newTarget == router.TargetHub {
			p.preferHubRetryEligible = true
		}
		decision.Target = newTarget
	}

	span.SetAttributes(
		attribute.String("routing.target", decision.Target.String()),
		attribute.String("mcp.server", p.serverName),
	)
	span.AddEvent("daemon.pipeline.route.decision", trace.WithAttributes(
		attribute.String("routing.target", decision.Target.String()),
		attribute.String("routing.reason", decision.Reason),
		attribute.String("mcp.server", p.serverName),
	))

	p.daemon.logger.Debug("routing decision", "server", p.serverName, "target", decision.Target, "reason", decision.Reason)

	if err := p.connectTarget(decision.Target, decision.Reason); err != nil {
		if p.preferHubRetryEligible && !p.localRetryUsed && decision.Target == router.TargetHub && p.daemon.pool != nil {
			p.localRetryUsed = true
			until := p.daemon.setPreferHubBackoff(p.serverName, preferHubBackoffDuration)
			p.daemon.logger.Warn("prefer-hub override connect failed; retrying local",
				"server", p.serverName,
				"error", err,
				"backoff_until", until)

			hubErr := err
			if localErr := p.connectTarget(router.TargetLocal, "prefer-hub fallback after hub connect failure"); localErr == nil {
				return nil
			} else {
				err = fmt.Errorf("hub connect failed: %v; local retry failed: %w", hubErr, localErr)
			}
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return p.internalErrorWithAudit(p.targetStr, err)
	}

	return nil
}

func (p *callPipeline) buildForwardRequest() (*mcp.Message, *mcp.Message) {
	p.stage = stageBuild
	span := p.startStageSpan("daemon.pipeline.build")
	start := time.Now()
	defer func() { p.buildDurationMs = time.Since(start).Milliseconds() }()
	defer span.End()

	var forwardParams json.RawMessage
	if len(p.params.Params) > 0 {
		forwardParams = p.params.Params
	} else {
		callParams := map[string]any{
			"name": p.toolName,
		}
		if len(p.params.Arguments) > 0 {
			var args map[string]any
			if err := json.Unmarshal(p.params.Arguments, &args); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return nil, p.internalErrorWithAudit(p.targetStr, fmt.Errorf("decode forward request arguments: %w", err))
			}
			callParams["arguments"] = args
		} else {
			callParams["arguments"] = map[string]any{}
		}
		var err error
		forwardParams, err = json.Marshal(callParams)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, p.internalErrorWithAudit(p.targetStr, fmt.Errorf("encode forward request params: %w", err))
		}
	}

	req, err := mcp.NewRequest(p.msg.ID, p.method, forwardParams)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, p.internalErrorWithAudit(p.targetStr, err)
	}
	return req, nil
}

func (p *callPipeline) execute(req *mcp.Message) *mcp.Message {
	p.stage = stageExecute
	span := p.startStageSpan("daemon.pipeline.execute")
	start := time.Now()
	defer func() { p.executeDurationMs = time.Since(start).Milliseconds() }()
	defer span.End()

	span.SetAttributes(
		attribute.String("mcp.server", p.serverName),
		attribute.String("mcp.tool", p.toolName),
	)

	callTimeout := resolveToolCallTimeout(p.params)

	// Per-server timeout override from config (routing.timeouts).
	if p.daemon != nil && p.daemon.fileCfg.Routing.Timeouts != nil {
		if serverTimeout, ok := p.daemon.fileCfg.Routing.Timeouts[p.serverName]; ok {
			if d, err := time.ParseDuration(serverTimeout); err == nil && d > callTimeout {
				callTimeout = d
			}
		}
	}

	p.daemon.metrics.RecordRequestStart(p.serverName)
	defer p.daemon.metrics.RecordRequestEnd(p.serverName)

	sendCtx, sendCancel := context.WithTimeout(p.ctx, callTimeout)
	sendStart := time.Now()
	sendErr := p.conn.Transport.Send(sendCtx, req)
	p.sendDurationMs = time.Since(sendStart).Milliseconds()
	sendCancel()
	if sendErr != nil {
		err := daemonRPCPhaseError(p.method, "send", callTimeout, sendErr)
		if resp, retried := p.retryLocalAfterLocalSendFailure(err, req, start); retried {
			span.AddEvent("daemon.pipeline.execute.retry", trace.WithAttributes(
				attribute.String("retry.type", "local_after_local_send_failure"),
			))
			return resp
		}
		if resp, retried := p.retryLocalAfterHubFailure("send", err, req, start); retried {
			span.AddEvent("daemon.pipeline.execute.retry", trace.WithAttributes(
				attribute.String("retry.type", "local_after_hub_failure"),
				attribute.String("retry.phase", "send"),
			))
			return resp
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return p.transportFailure("send", err, start)
	}

	recvCtx, recvCancel := context.WithTimeout(p.ctx, callTimeout)
	recvStart := time.Now()
	resp, recvErr := p.conn.Transport.Recv(recvCtx)
	p.recvDurationMs = time.Since(recvStart).Milliseconds()
	recvCancel()
	if recvErr != nil {
		err := daemonRPCPhaseError(p.method, "recv", callTimeout, recvErr)
		if retryResp, retried := p.retryLocalAfterHubFailure("recv", err, req, start); retried {
			span.AddEvent("daemon.pipeline.execute.retry", trace.WithAttributes(
				attribute.String("retry.type", "local_after_hub_failure"),
				attribute.String("retry.phase", "recv"),
			))
			return retryResp
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return p.transportFailure("recv", err, start)
	}

	// Validate response ID matches request ID. A mismatch indicates transport
	// corruption--typically a stale response from an interleaved request on a
	// shared stdio transport. Treat as a transport failure so the connection
	// is recycled and the caller gets a clear error instead of wrong data.
	if resp.ID != nil && req.ID != nil && fmt.Sprint(resp.ID) != fmt.Sprint(req.ID) {
		err := fmt.Errorf("response ID mismatch: sent %v, got %v (possible transport corruption)", req.ID, resp.ID)
		if retryResp, retried := p.retryLocalAfterHubFailure("recv", err, req, start); retried {
			span.AddEvent("daemon.pipeline.execute.retry", trace.WithAttributes(
				attribute.String("retry.type", "local_after_hub_failure"),
				attribute.String("retry.phase", "recv"),
			))
			return retryResp
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return p.transportFailure("recv", err, start)
	}

	duration := time.Since(start)
	p.recordSuccessMetrics(duration)
	p.markLocalActivity()
	p.cacheSuccessResponse(resp)

	// Compute byte sizes for cost tracking and OTel metrics.
	if req != nil && req.Params != nil {
		p.reqBytes = int64(len(req.Params))
	}
	if resp != nil && resp.Result != nil {
		p.resBytes = int64(len(resp.Result))
	}

	p.emitResponseAudit(resp)
	p.emitDecompHintIfLarge(resp)
	return resp
}
