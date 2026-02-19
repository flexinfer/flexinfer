package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	gosync "sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/router"
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

	conn      *pool.Conn
	target    router.Target
	targetStr string
	callMu    *gosync.Mutex
	lockHeld  bool
}

func newCallPipeline(d *Daemon, ctx context.Context, msg *mcp.Message) *callPipeline {
	return &callPipeline{
		daemon: d,
		ctx:    ctx,
		msg:    msg,
	}
}

func (p *callPipeline) parseAndResolve() *mcp.Message {
	if err := json.Unmarshal(p.msg.Params, &p.params); err != nil {
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
			return p.internalError(err)
		}
		if resolved == "" {
			return p.invalidParamsError(fmt.Sprintf("could not resolve server for tool: %s", p.toolName))
		}
		p.serverName = resolved
	}

	if p.serverName == "" {
		return p.invalidParamsError("missing server or tool for call")
	}

	p.auditStart = time.Now()
	return nil
}

func (p *callPipeline) authorize() *mcp.Message {
	if p.daemon.rbac == nil {
		return nil
	}
	decision := p.daemon.rbac.Check(p.params.AgentID, p.params.AgentType, p.serverName, p.toolName)
	p.daemon.logAccessDecision(decision)
	if decision.Allowed {
		return nil
	}
	p.daemon.emitAudit(p.params, p.serverName, p.toolName, "", p.auditStart, "denied", decision.Reason, false)
	return p.daemon.rbacDeniedResponse(p.msg.ID, decision)
}

func (p *callPipeline) tryCachedResponse() *mcp.Message {
	if p.daemon.respCache == nil || !p.daemon.respCache.IsCacheable(p.serverName, p.toolName) {
		return nil
	}

	cacheParams := p.params.Params
	if len(cacheParams) == 0 {
		cacheParams = p.params.Arguments
	}
	p.cacheKey = p.daemon.respCache.Key(p.serverName, p.toolName, cacheParams)

	if cached, ok := p.daemon.respCache.Get(p.cacheKey); ok {
		p.daemon.metrics.RecordResponseCacheHit(p.serverName, p.toolName)
		p.daemon.logger.Debug("response cache hit", "server", p.serverName, "tool", p.toolName)
		p.daemon.emitAudit(p.params, p.serverName, p.toolName, "local", p.auditStart, "success", "", true)
		resp, _ := mcp.NewResponse(p.msg.ID, json.RawMessage(cached))
		return resp
	}

	p.daemon.metrics.RecordResponseCacheMiss(p.serverName, p.toolName)
	return nil
}

func (p *callPipeline) routeAndConnect() *mcp.Message {
	decision, err := p.daemon.router.Route(p.ctx, p.serverName)
	if err != nil {
		return p.internalErrorWithAudit("", err.Error())
	}

	if pref, ok := p.daemon.routingPreferences[p.serverName]; ok && pref != RoutingHealthBased {
		hasHub := p.daemon.hubPool != nil
		newTarget, overridden := applyRoutingPreference(pref, decision.Target, hasHub)
		if overridden {
			p.daemon.logger.Debug("routing preference override",
				"server", p.serverName,
				"preference", pref,
				"original", decision.Target,
				"overridden_to", newTarget)
			decision.Target = newTarget
		}
	}

	p.daemon.logger.Debug("routing decision", "server", p.serverName, "target", decision.Target, "reason", decision.Reason)

	switch decision.Target {
	case router.TargetLocal:
		p.target = router.TargetLocal
		p.targetStr = p.target.String()
		p.callMu = p.daemon.callLock(p.serverName)
		lockStart := time.Now()
		p.callMu.Lock()
		p.lockHeld = true
		lockWait := time.Since(lockStart)
		if lockWait > 100*time.Millisecond {
			p.daemon.metrics.CallLockWaitTotal.WithLabelValues(p.serverName).Inc()
			p.daemon.logger.Debug("call lock contention", "server", p.serverName, "wait_ms", lockWait.Milliseconds())
		}
		// Mark server active before dialing so idle reaper won't classify this call as idle.
		if p.daemon.procMgr != nil {
			p.daemon.procMgr.MarkActivity(p.serverName)
		}
		p.conn, err = p.daemon.pool.Get(p.ctx, p.serverName)
	case router.TargetHub:
		p.target = router.TargetHub
		p.targetStr = p.target.String()
		if p.daemon.hubPool == nil {
			return p.internalErrorWithAudit(p.targetStr, "hub fallback not configured")
		}
		p.conn, err = p.daemon.hubPool.Get(p.ctx, p.serverName)
	case router.TargetUnavailable:
		p.target = router.TargetUnavailable
		p.targetStr = p.target.String()
		errMsg := fmt.Sprintf("server unavailable: %s", decision.Reason)
		return p.internalErrorWithAudit(p.targetStr, errMsg)
	}

	if err != nil {
		if p.lockHeld && p.callMu != nil {
			p.callMu.Unlock()
			p.lockHeld = false
		}
		p.daemon.router.RecordFailure(p.serverName, p.target, err)
		p.daemon.metrics.RecordServerFailure(p.serverName, p.targetStr, "connect")
		return p.internalErrorWithAudit(p.targetStr, err.Error())
	}

	return nil
}

func (p *callPipeline) releaseConnection() {
	if p.conn != nil {
		if p.target == router.TargetLocal {
			p.daemon.pool.Put(p.conn)
		} else {
			p.daemon.hubPool.Put(p.conn)
		}
	}
	if p.lockHeld && p.callMu != nil {
		p.callMu.Unlock()
		p.lockHeld = false
	}
}

func (p *callPipeline) buildForwardRequest() (*mcp.Message, *mcp.Message) {
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
		return nil, p.internalError(err)
	}
	return req, nil
}

func (p *callPipeline) execute(req *mcp.Message) *mcp.Message {
	start := time.Now()

	p.daemon.metrics.RecordRequestStart(p.serverName)
	defer p.daemon.metrics.RecordRequestEnd(p.serverName)

	if err := p.conn.Transport.Send(p.ctx, req); err != nil {
		return p.transportFailure("send", err, start)
	}

	resp, err := p.conn.Transport.Recv(p.ctx)
	if err != nil {
		return p.transportFailure("recv", err, start)
	}

	duration := time.Since(start)
	p.recordSuccessMetrics(duration)
	p.markLocalActivity()
	p.cacheSuccessResponse(resp)
	p.emitResponseAudit(resp)
	return resp
}

func (p *callPipeline) transportFailure(stage string, err error, start time.Time) *mcp.Message {
	p.conn.Healthy = false
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
	}

	return p.internalError(err)
}

func (p *callPipeline) invalidParamsError(message string) *mcp.Message {
	return mcp.NewErrorResponse(p.msg.ID, mcp.InvalidParams, message)
}

func (p *callPipeline) internalError(err error) *mcp.Message {
	return p.internalErrorMessage(err.Error())
}

func (p *callPipeline) internalErrorMessage(message string) *mcp.Message {
	return mcp.NewErrorResponse(p.msg.ID, mcp.InternalError, message)
}

func (p *callPipeline) internalErrorWithAudit(target, message string) *mcp.Message {
	p.emitErrorAudit(target, message)
	return p.internalErrorMessage(message)
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
	p.daemon.emitAudit(p.params, p.serverName, p.toolName, p.targetStr, p.auditStart, status, errMsg, false)
}

func (p *callPipeline) emitErrorAudit(target, errMsg string) {
	p.daemon.emitAudit(p.params, p.serverName, p.toolName, target, p.auditStart, "error", errMsg, false)
}
