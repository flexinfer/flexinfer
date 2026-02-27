package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	gosync "sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

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
	stageParse   = "parse"
	stageAuth    = "authorize"
	stagePolicy  = "policy"
	stageCache   = "cache"
	stageRoute   = "route"
	stageBuild   = "build"
	stageExecute = "execute"
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
}

func newCallPipeline(d *Daemon, ctx context.Context, msg *mcp.Message) *callPipeline {
	return &callPipeline{
		daemon: d,
		ctx:    ctx,
		msg:    msg,
	}
}

func (p *callPipeline) parseAndResolve() *mcp.Message {
	p.stage = stageParse
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
	p.stage = stageAuth
	if p.daemon.rbac == nil {
		return nil
	}
	decision := p.daemon.rbac.Check(p.params.AgentID, p.params.AgentType, p.serverName, p.toolName)
	p.daemon.logAccessDecision(decision)
	if decision.Allowed {
		return nil
	}
	p.daemon.emitAudit(p.params, p.serverName, p.toolName, "", p.auditStart, "denied", decision.Reason, false, nil, p.stage)
	return p.rbacDeniedError(decision)
}

func (p *callPipeline) enforceRequestPolicy() *mcp.Message {
	p.stage = stagePolicy
	if p.daemon.policy == nil {
		return nil
	}

	decision := p.daemon.policy.CheckRequest(p.serverName, p.toolName, p.params)
	if decision.Allowed {
		return nil
	}

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
		p.daemon.emitAudit(p.params, p.serverName, p.toolName, "local", p.auditStart, "success", "", true, nil, p.stage)
		resp, _ := mcp.NewResponse(p.msg.ID, json.RawMessage(cached))
		return resp
	}

	p.daemon.metrics.RecordResponseCacheMiss(p.serverName, p.toolName)
	return nil
}

func (p *callPipeline) routeAndConnect() *mcp.Message {
	p.stage = stageRoute
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
	p.stage = stageBuild
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
	p.stage = stageExecute
	start := time.Now()
	callTimeout := resolveToolCallTimeout(p.params)

	p.daemon.metrics.RecordRequestStart(p.serverName)
	defer p.daemon.metrics.RecordRequestEnd(p.serverName)

	sendCtx, sendCancel := context.WithTimeout(p.ctx, callTimeout)
	sendErr := p.conn.Transport.Send(sendCtx, req)
	sendCancel()
	if sendErr != nil {
		return p.transportFailure("send", daemonRPCPhaseError(p.method, "send", callTimeout, sendErr), start)
	}

	recvCtx, recvCancel := context.WithTimeout(p.ctx, callTimeout)
	resp, recvErr := p.conn.Transport.Recv(recvCtx)
	recvCancel()
	if recvErr != nil {
		return p.transportFailure("recv", daemonRPCPhaseError(p.method, "recv", callTimeout, recvErr), start)
	}

	// Validate response ID matches request ID. A mismatch indicates transport
	// corruption—typically a stale response from an interleaved request on a
	// shared stdio transport. Treat as a transport failure so the connection
	// is recycled and the caller gets a clear error instead of wrong data.
	if resp.ID != nil && req.ID != nil && fmt.Sprint(resp.ID) != fmt.Sprint(req.ID) {
		err := fmt.Errorf("response ID mismatch: sent %v, got %v (possible transport corruption)", req.ID, resp.ID)
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
	} else if p.target == router.TargetHub && p.daemon.hubPool != nil {
		p.daemon.logger.Warn("hub transport failure; clearing pool",
			"server", p.serverName, "stage", stage, "error", err)
		p.daemon.hubPool.ClearServer(p.serverName)
		if p.daemon.hubClient != nil {
			p.daemon.hubClient.CloseConnection(p.serverName)
		}
	}

	return p.internalError(err)
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
	return p.errorResponse(mcp.InvalidParams, message, nil)
}

func (p *callPipeline) internalError(err error) *mcp.Message {
	return p.errorResponse(mcp.InternalError, err.Error(), nil)
}

func (p *callPipeline) internalErrorWithAudit(target, message string) *mcp.Message {
	p.emitErrorAudit(target, message)
	return p.errorResponse(mcp.InternalError, message, nil)
}

func (p *callPipeline) rbacDeniedError(decision AccessDecision) *mcp.Message {
	reason := fmt.Sprintf("access denied: agent %q with role %q cannot call %s__%s (%s)",
		decision.AgentID, decision.Role, decision.Server, decision.Tool, decision.Reason)
	return p.errorResponse(mcp.InvalidRequest, reason, nil)
}

func (p *callPipeline) policyDeniedError(decision GatewayPolicyDecision) *mcp.Message {
	return p.errorResponse(mcp.InvalidRequest,
		fmt.Sprintf("policy denied: %s (%s)", decision.ReasonCode, decision.Reason),
		map[string]any{
			"policy_rule_id":     decision.RuleID,
			"policy_reason_code": decision.ReasonCode,
			"policy_stage":       decision.Stage,
			"policy_action":      decision.Action,
		})
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
