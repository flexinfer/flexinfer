package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	gosync "sync"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/router"
	"github.com/crb2nu/loom/pkg/env"
)

const (
	defaultDaemonControlRPCTimeout = 30 * time.Second
	defaultDaemonToolRPCTimeout    = 60 * time.Second
	maxDaemonToolRPCTimeout        = 15 * time.Minute
	autoDeriveDaemonTimeoutBuffer  = 60 * time.Second
)

// Pipeline stage constants for audit traceability.
const (
	stageParse      = "parse"
	stageAuth       = "authorize"
	stagePolicy     = "policy"
	stageCache      = "cache"
	stageRoute      = "route"
	stageBuild      = "build"
	stageExecute    = "execute"
	stageOutputScan = "output_scan"
)

// callPipeline executes the daemon tool-call flow in ordered stages.
type callPipeline struct {
	daemon *Daemon
	ctx    context.Context
	msg    *mcp.Message

	params     callParams
	serverName string
	toolName   string
	method     string
	cacheKey   string
	auditStart time.Time
	stage      string

	conn      *pool.Conn
	target    router.Target
	targetStr string
	callMu    *gosync.Mutex
	lockHeld  bool

	routingPreference       RoutingPreference
	preferHubRetryEligible  bool
	localRetryUsed          bool
	localTransportRetryUsed bool
}

func newCallPipeline(d *Daemon, ctx context.Context, msg *mcp.Message) *callPipeline {
	return &callPipeline{
		daemon: d,
		ctx:    ctx,
		msg:    msg,
	}
}

// startStageSpan begins a tracing span for the named pipeline stage.
// It safely handles a nil p.ctx (which occurs in unit tests that construct
// a callPipeline directly) and updates p.ctx so downstream stages nest.
func (p *callPipeline) startStageSpan(name string) trace.Span {
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var span trace.Span
	p.ctx, span = p.daemon.daemonTracer().Start(ctx, name)
	return span
}

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
	p.daemon.emitAudit(p.params, p.serverName, p.toolName, "", p.auditStart, "denied", decision.Reason, false, nil, p.stage)
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
		p.daemon.logger.Debug("response cache hit", "server", p.serverName, "tool", p.toolName)
		p.daemon.emitAudit(p.params, p.serverName, p.toolName, "local", p.auditStart, "success", "", true, nil, p.stage)
		resp, _ := mcp.NewResponse(p.msg.ID, json.RawMessage(cached))
		return resp
	}

	span.SetAttributes(attribute.Bool("cache.hit", false))
	span.AddEvent("daemon.pipeline.cache.miss", trace.WithAttributes(
		attribute.String("mcp.server", p.serverName),
		attribute.String("mcp.tool", p.toolName),
	))
	p.daemon.metrics.RecordResponseCacheMiss(p.serverName, p.toolName)
	return nil
}

func (p *callPipeline) routeAndConnect() *mcp.Message {
	p.stage = stageRoute
	span := p.startStageSpan("daemon.pipeline.route")
	defer span.End()

	decision, err := p.daemon.router.Route(p.ctx, p.serverName)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return p.internalErrorWithAudit("", err.Error())
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
		return p.internalErrorWithAudit(p.targetStr, err.Error())
	}

	return nil
}

func (p *callPipeline) releaseConnection() {
	if p.conn != nil {
		// If the context was cancelled (client disconnect), the upstream
		// connection may be in an indeterminate state (server still processing
		// the request). Mark it unhealthy and return it through the pool so the
		// pool decrements active connection accounting before closing it.
		cancelled := p.ctx != nil && p.ctx.Err() != nil
		if cancelled {
			p.conn.Healthy = false
			if p.target == router.TargetLocal {
				if p.daemon.pool != nil {
					p.daemon.pool.Put(p.conn)
				} else if p.conn.Transport != nil {
					_ = p.conn.Transport.Close()
				}
			} else {
				if p.daemon.hubPool != nil {
					p.daemon.hubPool.Put(p.conn)
				} else if p.conn.Transport != nil {
					_ = p.conn.Transport.Close()
				}
			}
		} else if p.target == router.TargetLocal {
			if p.daemon.pool != nil {
				p.daemon.pool.Put(p.conn)
			}
		} else {
			if p.daemon.hubPool != nil {
				p.daemon.hubPool.Put(p.conn)
			} else if p.conn.Transport != nil {
				_ = p.conn.Transport.Close()
			}
		}
		p.conn = nil
	}
	if p.lockHeld && p.callMu != nil {
		p.callMu.Unlock()
		p.lockHeld = false
	}
}

func (p *callPipeline) buildForwardRequest() (*mcp.Message, *mcp.Message) {
	p.stage = stageBuild
	span := p.startStageSpan("daemon.pipeline.build")
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
			_ = json.Unmarshal(p.params.Arguments, &args)
			callParams["arguments"] = args
		} else {
			callParams["arguments"] = map[string]any{}
		}
		forwardParams, _ = json.Marshal(callParams)
	}

	req, err := mcp.NewRequest(p.msg.ID, p.method, forwardParams)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, p.internalError(err)
	}
	return req, nil
}

func (p *callPipeline) execute(req *mcp.Message) *mcp.Message {
	p.stage = stageExecute
	span := p.startStageSpan("daemon.pipeline.execute")
	defer span.End()

	span.SetAttributes(
		attribute.String("mcp.server", p.serverName),
		attribute.String("mcp.tool", p.toolName),
	)

	start := time.Now()
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
	sendErr := p.conn.Transport.Send(sendCtx, req)
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
	resp, recvErr := p.conn.Transport.Recv(recvCtx)
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
	p.emitResponseAudit(resp)
	p.emitDecompHintIfLarge(resp)
	return resp
}

func (p *callPipeline) retryLocalAfterLocalSendFailure(err error, req *mcp.Message, start time.Time) (*mcp.Message, bool) {
	if p.localTransportRetryUsed || p.target != router.TargetLocal || p.daemon == nil || p.daemon.pool == nil {
		return nil, false
	}
	if !shouldResetDaemonTransport(err) {
		return nil, false
	}

	p.localTransportRetryUsed = true
	if p.conn != nil {
		p.conn.Healthy = false
	}
	p.daemon.router.RecordFailure(p.serverName, p.target, err)
	p.daemon.metrics.RecordServerFailure(p.serverName, p.targetStr, "send")
	p.daemon.logger.Warn("local transport send failed; reconnecting and retrying once",
		"server", p.serverName, "error", err)

	p.daemon.pool.ClearServer(p.serverName)
	if p.daemon.procMgr != nil {
		_ = p.daemon.procMgr.Stop(p.serverName)
	}
	p.daemon.runningServers.Delete(p.serverName)
	if p.daemon.eventBus != nil {
		p.daemon.eventBus.Publish(EventProcessStop, map[string]any{
			"server": p.serverName,
			"reason": "transport_send_retry",
		})
	}

	p.releaseConnection()
	if connectErr := p.connectTarget(router.TargetLocal, "local transport send retry"); connectErr != nil {
		combined := fmt.Errorf("local send failed: %v; local retry failed: %w", err, connectErr)
		p.daemon.metrics.RecordRequest(p.serverName, p.method, "error", p.targetStr, time.Since(start))
		return p.internalErrorWithAudit(p.targetStr, combined.Error()), true
	}

	return p.execute(req), true
}

func (p *callPipeline) connectTarget(target router.Target, reason string) error {
	p.target = target
	p.targetStr = target.String()
	p.conn = nil

	if target == router.TargetUnavailable {
		return fmt.Errorf("server unavailable: %s", reason)
	}
	if target != router.TargetLocal && target != router.TargetHub {
		return fmt.Errorf("unsupported routing target: %s", target)
	}

	var (
		err      error
		lockWait time.Duration
	)
	p.callMu, lockWait, err = p.daemon.acquireCallLock(p.ctx, p.serverName)
	if err != nil {
		p.daemon.router.RecordFailure(p.serverName, p.target, err)
		p.daemon.metrics.RecordServerFailure(p.serverName, p.targetStr, "call_lock")
		return err
	}
	p.lockHeld = true
	if lockWait > 100*time.Millisecond {
		p.daemon.metrics.CallLockWaitTotal.WithLabelValues(p.serverName).Inc()
		p.daemon.logger.Debug("call lock contention", "server", p.serverName, "wait_ms", lockWait.Milliseconds())
	}

	switch target {
	case router.TargetLocal:
		// Mark server active before dialing so idle reaper won't classify this call as idle.
		if p.daemon.procMgr != nil {
			p.daemon.procMgr.MarkActivity(p.serverName)
		}
		if p.daemon.pool == nil {
			err = fmt.Errorf("local pool not configured")
		} else {
			p.conn, err = p.daemon.pool.Get(p.ctx, p.serverName)
			if err == nil {
				p.conn = p.discardIfStale(p.conn, p.daemon.pool)
				if p.conn == nil {
					err = fmt.Errorf("stale connection discarded and fresh dial failed for %s", p.serverName)
				}
			}
		}
	case router.TargetHub:
		if p.daemon.hubPool == nil {
			err = fmt.Errorf("hub fallback not configured")
		} else {
			p.conn, err = p.daemon.hubPool.Get(p.ctx, p.serverName)
			if err == nil {
				p.conn = p.discardIfStale(p.conn, p.daemon.hubPool)
				if p.conn == nil {
					err = fmt.Errorf("stale connection discarded and fresh dial failed for %s", p.serverName)
				}
			}
		}
	}

	if err != nil {
		if p.lockHeld && p.callMu != nil {
			p.callMu.Unlock()
			p.lockHeld = false
		}
		p.daemon.router.RecordFailure(p.serverName, p.target, err)
		p.daemon.metrics.RecordServerFailure(p.serverName, p.targetStr, "connect")
		return err
	}

	return nil
}

// discardIfStale checks whether a pooled connection has been idle longer than
// the configured stale threshold. If so, it marks the connection unhealthy,
// returns it to the pool (which closes it), and dials a fresh connection.
// Returns the original connection if not stale or if the threshold is disabled.
func (p *callPipeline) discardIfStale(conn *pool.Conn, pl *pool.Pool) *pool.Conn {
	threshold := p.daemon.poolStaleThreshold()
	if threshold <= 0 || conn == nil {
		return conn
	}
	idle := time.Since(conn.LastUsed)
	if idle <= threshold {
		return conn
	}

	p.daemon.logger.Debug("discarding stale pool connection",
		"server", p.serverName,
		"idle", idle.Round(time.Second),
		"threshold", threshold)

	conn.Healthy = false
	pl.Put(conn)

	fresh, err := pl.Get(p.ctx, p.serverName)
	if err != nil {
		p.daemon.logger.Warn("failed to dial fresh connection after discarding stale",
			"server", p.serverName, "error", err)
		return nil
	}
	return fresh
}

func (p *callPipeline) shouldRetryLocalAfterHubFailure() bool {
	return p.preferHubRetryEligible &&
		!p.localRetryUsed &&
		p.target == router.TargetHub &&
		p.daemon != nil &&
		p.daemon.pool != nil
}

func (p *callPipeline) retryLocalAfterHubFailure(stage string, err error, req *mcp.Message, start time.Time) (*mcp.Message, bool) {
	if !p.shouldRetryLocalAfterHubFailure() {
		return nil, false
	}

	p.localRetryUsed = true
	if p.conn != nil {
		p.conn.Healthy = false
	}
	p.daemon.router.RecordFailure(p.serverName, p.target, err)
	p.daemon.metrics.RecordServerFailure(p.serverName, p.targetStr, stage)
	if p.daemon.hubPool != nil {
		p.daemon.hubPool.ClearServer(p.serverName)
	}
	if p.daemon.hubClient != nil {
		p.daemon.hubClient.CloseConnection(p.serverName)
	}

	until := p.daemon.setPreferHubBackoff(p.serverName, preferHubBackoffDuration)
	p.daemon.logger.Warn("prefer-hub override transport failed; retrying local",
		"server", p.serverName,
		"stage", stage,
		"error", err,
		"backoff_until", until)
	p.recordTransportSpanEvent("daemon.proxy.local_retry_after_hub_failure",
		attribute.String("server.name", p.serverName),
		attribute.String("failure.stage", stage),
		attribute.String("failure.error", err.Error()),
		attribute.String("routing.from", router.TargetHub.String()),
		attribute.String("routing.to", router.TargetLocal.String()),
		attribute.String("retry.backoff_until", until.Format(time.RFC3339Nano)),
	)

	p.releaseConnection()

	if connectErr := p.connectTarget(router.TargetLocal, "prefer-hub fallback after hub transport failure"); connectErr != nil {
		combined := fmt.Errorf("hub %s failed: %v; local retry failed: %w", stage, err, connectErr)
		p.daemon.metrics.RecordRequest(p.serverName, p.method, "error", p.targetStr, time.Since(start))
		return p.internalErrorWithAudit(p.targetStr, combined.Error()), true
	}

	return p.execute(req), true
}

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

// errorResponse builds a JSON-RPC error message for the current pipeline call.
// All pipeline error constructors funnel through this method so that envelope
// structure (JSONRPC version, ID, optional Data) is defined in one place.
func (p *callPipeline) errorResponse(code int, message string, data any) *mcp.Message {
	return &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      p.msg.ID,
		Error: &mcp.Error{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
}

func (p *callPipeline) invalidParamsError(message string) *mcp.Message {
	code := "INVALID_INPUT"
	if strings.Contains(message, "could not resolve server for tool") {
		code = "TOOL_NOT_FOUND"
	} else if strings.Contains(message, "missing server") {
		code = "SERVER_NOT_FOUND"
	}
	return p.errorResponse(mcp.InvalidParams, message,
		newPipelineError(code, p.serverName, p.toolName, p.stage, false))
}

func (p *callPipeline) internalError(err error) *mcp.Message {
	code, retryable := classifyInternalError(err, p.stage)
	return p.errorResponse(mcp.InternalError, err.Error(),
		newPipelineError(code, p.serverName, p.toolName, p.stage, retryable))
}

func (p *callPipeline) internalErrorWithAudit(target, message string) *mcp.Message {
	p.emitErrorAudit(target, message)
	code, retryable := classifyInternalError(fmt.Errorf("%s", message), p.stage)
	return p.errorResponse(mcp.InternalError, message,
		newPipelineError(code, p.serverName, p.toolName, p.stage, retryable))
}

func (p *callPipeline) rbacDeniedError(decision AccessDecision) *mcp.Message {
	reason := fmt.Sprintf("access denied: agent %q with role %q cannot call %s__%s (%s)",
		decision.AgentID, decision.Role, decision.Server, decision.Tool, decision.Reason)
	code := "RBAC_DENIED"
	retryable := false
	retryAfter := ""
	if decision.ReasonCode == "rate_limited" {
		code = "RATE_LIMITED"
		retryable = true
		retryAfter = "60s"
	}
	ped := newPipelineError(code, p.serverName, p.toolName, stageAuth, retryable)
	ped.RetryAfter = retryAfter
	ped.Details = map[string]any{
		"reason_code": decision.ReasonCode,
		"agent_id":    decision.AgentID,
		"role":        decision.Role,
	}
	return p.errorResponse(mcp.InvalidRequest, reason, ped)
}

func (p *callPipeline) policyDeniedError(decision GatewayPolicyDecision) *mcp.Message {
	ped := newPipelineError("POLICY_DENIED", p.serverName, p.toolName, stagePolicy, false)
	ped.Details = map[string]any{
		"policy_rule_id":     decision.RuleID,
		"policy_reason_code": decision.ReasonCode,
		"policy_stage":       decision.Stage,
		"policy_action":      decision.Action,
	}
	return p.errorResponse(mcp.InvalidRequest,
		fmt.Sprintf("policy denied: %s (%s)", decision.ReasonCode, decision.Reason), ped)
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

// decompHintTokenThreshold is the estimated token count above which a
// decomposition hint is emitted, suggesting the agent use the
// recursive-context workflow for large responses.
const decompHintTokenThreshold = 8000

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

// resolveToolCallTimeout determines the RPC timeout for a tools/call.
// Priority: explicit _timeout field > auto-derived from arguments > env/default.
func resolveToolCallTimeout(params callParams) time.Duration {
	method := params.Method
	if strings.TrimSpace(method) == "" {
		method = "tools/call"
	}

	// Non-tool methods use the standard timeout path.
	if strings.TrimSpace(method) != "tools/call" {
		return daemonRPCTimeoutForMethod(method)
	}

	base := daemonRPCTimeoutForMethod(method)

	// 1. Explicit _timeout field (highest priority).
	if hint := strings.TrimSpace(params.Timeout); hint != "" {
		if d, err := time.ParseDuration(hint); err == nil && d > 0 {
			return clampTimeout(d, base, maxDaemonToolRPCTimeout)
		}
		// Invalid _timeout: fall through to auto-derive.
	}

	// 2. Auto-derive from well-known argument fields.
	args := params.Arguments
	if len(args) == 0 {
		args = params.Params
	}
	if derived := deriveTimeoutFromArguments(args); derived > 0 {
		withBuffer := derived + autoDeriveDaemonTimeoutBuffer
		return clampTimeout(withBuffer, base, maxDaemonToolRPCTimeout)
	}

	// 3. Default (env or hardcoded 60s).
	return base
}

// deriveTimeoutFromArguments inspects well-known argument fields to infer a
// tool-level timeout. Returns 0 if no hint is found.
func deriveTimeoutFromArguments(args json.RawMessage) time.Duration {
	if len(args) == 0 {
		return 0
	}

	// Try to parse as tool call params (which nest arguments).
	var toolCall struct {
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(args, &toolCall); err == nil && len(toolCall.Arguments) > 0 {
		if d := extractTimeoutFromMap(toolCall.Arguments); d > 0 {
			return d
		}
	}

	// Try directly (smart-routing passes arguments at top level).
	return extractTimeoutFromMap(args)
}

func extractTimeoutFromMap(raw json.RawMessage) time.Duration {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0
	}

	// timeout_seconds (float64 → seconds): mcp-gitlab, mcp-agent-context
	if v, ok := m["timeout_seconds"]; ok {
		if d := parseSecondsDuration(v); d > 0 {
			return d
		}
	}

	// timeoutSeconds (float64 → seconds): mcp-k8s-ops, mcp-docker
	if v, ok := m["timeoutSeconds"]; ok {
		if d := parseSecondsDuration(v); d > 0 {
			return d
		}
	}

	// timeout (Go duration string): mcp-devbox
	if v, ok := m["timeout"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			if d, err := time.ParseDuration(s); err == nil && d > 0 {
				return d
			}
		}
		// Also try as numeric seconds (some tools use timeout: 60).
		if d := parseSecondsDuration(v); d > 0 {
			return d
		}
	}

	return 0
}

func parseSecondsDuration(raw json.RawMessage) time.Duration {
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil && f > 0 {
		return time.Duration(f * float64(time.Second))
	}
	return 0
}

func clampTimeout(d, min, max time.Duration) time.Duration {
	if d < min {
		return min
	}
	if d > max {
		return max
	}
	return d
}

func daemonRPCTimeoutForMethod(method string) time.Duration {
	if strings.TrimSpace(method) == "tools/call" {
		return normalizePositiveDuration(env.Duration("LOOM_DAEMON_TOOL_TIMEOUT", defaultDaemonToolRPCTimeout), defaultDaemonToolRPCTimeout)
	}
	return normalizePositiveDuration(env.Duration("LOOM_DAEMON_CONTROL_TIMEOUT", defaultDaemonControlRPCTimeout), defaultDaemonControlRPCTimeout)
}

func normalizePositiveDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func daemonRPCPhaseError(operation, phase string, timeout time.Duration, err error) error {
	op := strings.TrimSpace(operation)
	if op == "" {
		op = "daemon call"
	}
	if isRPCTimeout(err) {
		return fmt.Errorf("%s timeout during %s after %s (recoverable: daemon will reconnect upstream transport and retry on the next request): %w", op, phase, timeout, err)
	}
	return fmt.Errorf("%s failed during %s: %w", op, phase, err)
}

func isRPCTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "i/o timeout")
}

func shouldResetDaemonTransport(err error) bool {
	if err == nil {
		return false
	}
	if isRPCTimeout(err) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.ENOTCONN) {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "use of closed network connection") ||
		strings.Contains(lower, "unexpected eof") ||
		strings.Contains(lower, "transport closed")
}
