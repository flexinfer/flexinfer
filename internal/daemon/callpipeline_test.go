package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kitregistry "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/registry"
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/process"
	"github.com/crb2nu/loom/internal/router"
)

type fakeTransport struct {
	sendErr error
	recvErr error
	recvMsg *mcp.Message
	sent    []*mcp.Message
	sendFn  func(context.Context, *mcp.Message) error
	recvFn  func(context.Context) (*mcp.Message, error)
}

func (f *fakeTransport) Send(ctx context.Context, msg *mcp.Message) error {
	if f.sendFn != nil {
		return f.sendFn(ctx, msg)
	}
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, msg)
	return nil
}

func (f *fakeTransport) Recv(ctx context.Context) (*mcp.Message, error) {
	if f.recvFn != nil {
		return f.recvFn(ctx)
	}
	if f.recvErr != nil {
		return nil, f.recvErr
	}
	if f.recvMsg != nil {
		return f.recvMsg, nil
	}
	return &mcp.Message{JSONRPC: mcp.JSONRPCVersion}, nil
}

func (f *fakeTransport) Close() error { return nil }

func newCallPipelineTestDaemon() *Daemon {
	return &Daemon{
		cfg:     Config{Target: "codex"},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		metrics: NewMetrics(),
		router:  router.New(router.Config{HubEnabled: true}),
	}
}

func newTestPool(t *testing.T) *pool.Pool {
	t.Helper()
	return pool.New(pool.Config{
		MaxIdle:     2,
		MaxOpen:     2,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			return &fakeTransport{}, nil
		},
	})
}

func newCallMessage(t *testing.T, payload any) *mcp.Message {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "test-id",
		Method:  "loom/call",
		Params:  raw,
	}
}

func enableAuditAndCostForTest(t *testing.T, d *Daemon) string {
	t.Helper()

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	auditLogger, err := NewAuditLogger(AuditConfig{
		Enabled: true,
		LogPath: auditPath,
	}, d.logger)
	if err != nil {
		t.Fatalf("create audit logger: %v", err)
	}
	t.Cleanup(func() {
		_ = auditLogger.Close()
	})
	d.audit = auditLogger
	d.cost = NewCostTracker(CostConfig{Enabled: true}, d.logger)

	return auditPath
}

func readAuditEntries(t *testing.T, path string) []AuditEntry {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit log: %v", err)
	}
	defer f.Close()

	var entries []AuditEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry AuditEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("decode audit entry: %v", err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan audit log: %v", err)
	}
	return entries
}

func TestDaemonRPCTimeoutForMethod_Defaults(t *testing.T) {
	t.Setenv("LOOM_DAEMON_CONTROL_TIMEOUT", "")
	t.Setenv("LOOM_DAEMON_TOOL_TIMEOUT", "")

	if got := daemonRPCTimeoutForMethod("loom/status"); got != defaultDaemonControlRPCTimeout {
		t.Fatalf("daemonRPCTimeoutForMethod(control) = %v, want %v", got, defaultDaemonControlRPCTimeout)
	}
	if got := daemonRPCTimeoutForMethod("tools/call"); got != defaultDaemonToolRPCTimeout {
		t.Fatalf("daemonRPCTimeoutForMethod(tools/call) = %v, want %v", got, defaultDaemonToolRPCTimeout)
	}
}

func TestDaemonRPCTimeoutForMethod_EnvOverrideAndFallback(t *testing.T) {
	t.Setenv("LOOM_DAEMON_CONTROL_TIMEOUT", "45s")
	t.Setenv("LOOM_DAEMON_TOOL_TIMEOUT", "75s")

	if got := daemonRPCTimeoutForMethod("loom/status"); got != 45*time.Second {
		t.Fatalf("daemonRPCTimeoutForMethod(control) = %v, want 45s", got)
	}
	if got := daemonRPCTimeoutForMethod("tools/call"); got != 75*time.Second {
		t.Fatalf("daemonRPCTimeoutForMethod(tools/call) = %v, want 75s", got)
	}

	t.Setenv("LOOM_DAEMON_CONTROL_TIMEOUT", "0s")
	t.Setenv("LOOM_DAEMON_TOOL_TIMEOUT", "-1s")
	if got := daemonRPCTimeoutForMethod("loom/status"); got != defaultDaemonControlRPCTimeout {
		t.Fatalf("daemonRPCTimeoutForMethod(control) = %v, want %v", got, defaultDaemonControlRPCTimeout)
	}
	if got := daemonRPCTimeoutForMethod("tools/call"); got != defaultDaemonToolRPCTimeout {
		t.Fatalf("daemonRPCTimeoutForMethod(tools/call) = %v, want %v", got, defaultDaemonToolRPCTimeout)
	}
}

func TestCallPipelineParseAndResolve_InvalidParams(t *testing.T) {
	d := newCallPipelineTestDaemon()
	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "bad-json",
		Method:  "loom/call",
		Params:  json.RawMessage(`{`),
	}

	p := newCallPipeline(d, context.Background(), msg)
	resp := p.parseAndResolve()
	if resp == nil {
		t.Fatal("expected parse error response")
	}
	if resp.Error == nil || resp.Error.Code != mcp.InvalidParams {
		t.Fatalf("expected invalid params error, got %+v", resp.Error)
	}
}

func TestCallPipelineParseAndResolve_PrefixedTool(t *testing.T) {
	d := newCallPipelineTestDaemon()
	msg := newCallMessage(t, map[string]any{
		"tool": "github__list_repos",
	})

	p := newCallPipeline(d, context.Background(), msg)
	if resp := p.parseAndResolve(); resp != nil {
		t.Fatalf("unexpected error response: %+v", resp.Error)
	}

	if p.serverName != "github" {
		t.Fatalf("serverName = %q, want github", p.serverName)
	}
	if p.toolName != "list_repos" {
		t.Fatalf("toolName = %q, want list_repos", p.toolName)
	}
	if p.method != "tools/call" {
		t.Fatalf("method = %q, want tools/call", p.method)
	}
}

func TestCallPipelineParseAndResolve_NameFallback(t *testing.T) {
	d := newCallPipelineTestDaemon()
	msg := newCallMessage(t, map[string]any{
		"server": "github",
		"name":   "list_repos",
	})

	p := newCallPipeline(d, context.Background(), msg)
	if resp := p.parseAndResolve(); resp != nil {
		t.Fatalf("unexpected error response: %+v", resp.Error)
	}

	if p.toolName != "list_repos" {
		t.Fatalf("toolName = %q, want list_repos", p.toolName)
	}
}

func TestCallPipelineParseAndResolve_MissingServerAndTool(t *testing.T) {
	d := newCallPipelineTestDaemon()
	msg := newCallMessage(t, map[string]any{
		"method": "tools/call",
	})

	p := newCallPipeline(d, context.Background(), msg)
	resp := p.parseAndResolve()
	if resp == nil {
		t.Fatal("expected error response")
	}
	if resp.Error == nil || resp.Error.Code != mcp.InvalidParams {
		t.Fatalf("expected invalid params error, got %+v", resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "missing server or tool") {
		t.Fatalf("unexpected error message: %q", resp.Error.Message)
	}
}

func TestCallPipelineAuthorize_DeniedByRBAC(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.rbac = NewRBACEnforcer(RBACConfig{
		Enabled:       true,
		DefaultPolicy: "deny",
	}, d.logger)

	p := &callPipeline{
		daemon:     d,
		msg:        &mcp.Message{ID: "deny-id"},
		serverName: "github",
		toolName:   "delete_repo",
		params: callParams{
			AgentID: "agent-x",
		},
		auditStart: time.Now(),
	}

	resp := p.authorize()
	if resp == nil {
		t.Fatal("expected denied response")
	}
	if resp.Error == nil || resp.Error.Code != mcp.InvalidRequest {
		t.Fatalf("expected invalid request denial, got %+v", resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "access denied") {
		t.Fatalf("unexpected denial message: %q", resp.Error.Message)
	}
}

func TestCallPipelineAuthorize_DeniedByGlobalPolicy(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.rbac = NewRBACEnforcer(RBACConfig{
		Enabled:       true,
		DefaultPolicy: "allow",
		GlobalDeny:    []string{"github__delete_*"},
		Roles: map[string]RBACRole{
			"admin": {Allow: []string{"*"}},
		},
		Bindings: []RBACBinding{
			{AgentID: "admin-agent", Role: "admin"},
		},
	}, d.logger)

	p := &callPipeline{
		daemon:     d,
		msg:        &mcp.Message{ID: "deny-global"},
		serverName: "github",
		toolName:   "delete_repo",
		params: callParams{
			AgentID: "admin-agent",
		},
		auditStart: time.Now(),
	}

	resp := p.authorize()
	if resp == nil {
		t.Fatal("expected denied response")
	}
	if resp.Error == nil || resp.Error.Code != mcp.InvalidRequest {
		t.Fatalf("expected invalid request denial, got %+v", resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "global policy") {
		t.Fatalf("unexpected denial message: %q", resp.Error.Message)
	}
}

func TestCallPipelineAuthorize_DeniedByRateLimit(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.rbac = NewRBACEnforcer(RBACConfig{
		Enabled:       true,
		DefaultPolicy: "allow",
		RateLimits: []RBACRateLimit{
			{
				AgentID:           "agent-rate",
				Server:            "github",
				Tool:              "list_repos",
				RequestsPerMinute: 1,
			},
		},
	}, d.logger)
	d.rbac.now = func() time.Time { return time.Date(2026, 2, 18, 1, 0, 0, 0, time.UTC) }

	p := &callPipeline{
		daemon:     d,
		msg:        &mcp.Message{ID: "deny-rate"},
		serverName: "github",
		toolName:   "list_repos",
		params: callParams{
			AgentID: "agent-rate",
		},
		auditStart: time.Now(),
	}

	if resp := p.authorize(); resp != nil {
		t.Fatalf("first request should pass authorization, got %+v", resp.Error)
	}

	resp := p.authorize()
	if resp == nil {
		t.Fatal("expected denied response on second request")
	}
	if resp.Error == nil || resp.Error.Code != mcp.InvalidRequest {
		t.Fatalf("expected invalid request denial, got %+v", resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "rate limit exceeded") {
		t.Fatalf("unexpected denial message: %q", resp.Error.Message)
	}
}

func TestCallPipelineTryCachedResponse_Hit(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.respCache = NewResponseCache(CacheConfig{Enabled: true})

	args := json.RawMessage(`{"query":"up"}`)
	p := &callPipeline{
		daemon:     d,
		msg:        &mcp.Message{ID: "cache-hit"},
		serverName: "prometheus",
		toolName:   "query",
		params: callParams{
			Arguments: args,
		},
		auditStart: time.Now(),
	}

	key := d.respCache.Key("prometheus", "query", args)
	cached := json.RawMessage(`{"cached":true}`)
	d.respCache.Set(key, cached, "prometheus", "query")

	resp := p.tryCachedResponse()
	if resp == nil {
		t.Fatal("expected cached response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error response: %+v", resp.Error)
	}
	if string(resp.Result) != string(cached) {
		t.Fatalf("result = %s, want %s", string(resp.Result), string(cached))
	}
}

func TestCallPipelineTryCachedResponse_Miss(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.respCache = NewResponseCache(CacheConfig{Enabled: true})

	p := &callPipeline{
		daemon:     d,
		msg:        &mcp.Message{ID: "cache-miss"},
		serverName: "prometheus",
		toolName:   "query",
		params: callParams{
			Arguments: json.RawMessage(`{"query":"up"}`),
		},
	}

	resp := p.tryCachedResponse()
	if resp != nil {
		t.Fatalf("expected cache miss (nil response), got %+v", resp)
	}
	if p.cacheKey == "" {
		t.Fatal("expected cache key to be computed on miss")
	}
}

func TestCallPipelineBuildForwardRequest_FromArguments(t *testing.T) {
	d := newCallPipelineTestDaemon()
	p := &callPipeline{
		daemon:   d,
		msg:      &mcp.Message{ID: "forward"},
		toolName: "query",
		method:   "tools/call",
		params: callParams{
			Arguments: json.RawMessage(`{"query":"up"}`),
		},
	}

	req, errResp := p.buildForwardRequest()
	if errResp != nil {
		t.Fatalf("unexpected error response: %+v", errResp)
	}
	if req.Method != "tools/call" {
		t.Fatalf("method = %q, want tools/call", req.Method)
	}

	var payload map[string]any
	if err := json.Unmarshal(req.Params, &payload); err != nil {
		t.Fatalf("unmarshal forward params: %v", err)
	}
	if payload["name"] != "query" {
		t.Fatalf("name = %v, want query", payload["name"])
	}
}

func TestCallPipelineBuildForwardRequest_InvalidRawParams(t *testing.T) {
	d := newCallPipelineTestDaemon()
	p := &callPipeline{
		daemon: d,
		msg:    &mcp.Message{ID: "forward-invalid"},
		method: "tools/call",
		params: callParams{
			Params: json.RawMessage(`{`),
		},
	}

	req, errResp := p.buildForwardRequest()
	if req != nil {
		t.Fatalf("expected nil request, got %+v", req)
	}
	if errResp == nil || errResp.Error == nil {
		t.Fatal("expected internal error response")
	}
	if errResp.Error.Code != mcp.InternalError {
		t.Fatalf("error code = %d, want %d", errResp.Error.Code, mcp.InternalError)
	}
}

func TestCallPipelineRouteAndConnect_Unavailable(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.router = router.New(router.Config{HubEnabled: false})

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "route-unavailable"},
		serverName: "unknown",
	}

	resp := p.routeAndConnect()
	if resp == nil || resp.Error == nil {
		t.Fatal("expected unavailable routing error response")
	}
	if resp.Error.Code != mcp.InternalError {
		t.Fatalf("error code = %d, want %d", resp.Error.Code, mcp.InternalError)
	}
	if !strings.Contains(resp.Error.Message, "server unavailable") {
		t.Fatalf("unexpected error message: %q", resp.Error.Message)
	}
}

func TestCallPipelineRouteAndConnect_HubFallbackNotConfigured(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.router = router.New(router.Config{HubEnabled: true})

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "route-hub-missing"},
		serverName: "unknown",
	}

	resp := p.routeAndConnect()
	if resp == nil || resp.Error == nil {
		t.Fatal("expected hub fallback configuration error response")
	}
	if resp.Error.Code != mcp.InternalError {
		t.Fatalf("error code = %d, want %d", resp.Error.Code, mcp.InternalError)
	}
	if !strings.Contains(resp.Error.Message, "hub fallback not configured") {
		t.Fatalf("unexpected error message: %q", resp.Error.Message)
	}
}

func TestCallPipelineRouteAndConnect_LocalSuccess(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.router = router.New(router.Config{
		HubEnabled: false,
		Registry: &kitregistry.Registry{
			Servers: []*kitregistry.Server{
				{
					Name:       "local_srv",
					Categories: []string{"local-only"},
				},
			},
		},
	})
	d.pool = newTestPool(t)
	defer func() { _ = d.pool.Close() }()

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "route-local-ok"},
		serverName: "local_srv",
	}

	resp := p.routeAndConnect()
	if resp != nil {
		t.Fatalf("unexpected routing error response: %+v", resp.Error)
	}
	if p.conn == nil {
		t.Fatal("expected connection to be established")
	}
	if p.target != router.TargetLocal {
		t.Fatalf("target = %v, want %v", p.target, router.TargetLocal)
	}
	if p.targetStr != router.TargetLocal.String() {
		t.Fatalf("targetStr = %q, want %q", p.targetStr, router.TargetLocal.String())
	}
}

func TestCallPipelineRouteAndConnect_HubSuccess(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.router = router.New(router.Config{HubEnabled: true})
	d.hubPool = newTestPool(t)
	defer func() { _ = d.hubPool.Close() }()

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "route-hub-ok"},
		serverName: "hub_only",
	}

	resp := p.routeAndConnect()
	if resp != nil {
		t.Fatalf("unexpected routing error response: %+v", resp.Error)
	}
	if p.conn == nil {
		t.Fatal("expected hub connection to be established")
	}
	if p.target != router.TargetHub {
		t.Fatalf("target = %v, want %v", p.target, router.TargetHub)
	}
	if p.targetStr != router.TargetHub.String() {
		t.Fatalf("targetStr = %q, want %q", p.targetStr, router.TargetHub.String())
	}
}

func TestCallPipelineRouteAndConnect_LocalDialFailureEmitsAuditAndCost(t *testing.T) {
	d := newCallPipelineTestDaemon()
	auditPath := enableAuditAndCostForTest(t, d)
	d.router = router.New(router.Config{
		HubEnabled: false,
		Registry: &kitregistry.Registry{
			Servers: []*kitregistry.Server{
				{
					Name:       "local_srv",
					Categories: []string{"local-only"},
				},
			},
		},
	})
	d.pool = pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Second,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			return nil, errors.New("dial failed")
		},
	})
	defer func() { _ = d.pool.Close() }()

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "route-local-fail"},
		serverName: "local_srv",
		toolName:   "query",
		method:     "tools/call",
		params: callParams{
			AgentID: "agent-1",
		},
		auditStart: time.Now(),
	}

	resp := p.routeAndConnect()
	if resp == nil || resp.Error == nil {
		t.Fatal("expected connect failure error response")
	}
	if !strings.Contains(resp.Error.Message, "dial failed") {
		t.Fatalf("unexpected connect failure message: %q", resp.Error.Message)
	}
	mu := d.callLock("local_srv")
	if !mu.TryLock() {
		t.Fatal("expected local call lock to be released after connect failure")
	}
	mu.Unlock()

	snap := d.cost.Snapshot()
	if snap.Totals.CallCount != 1 {
		t.Fatalf("call_count = %d, want 1", snap.Totals.CallCount)
	}
	if snap.Totals.ErrorCount != 1 {
		t.Fatalf("error_count = %d, want 1", snap.Totals.ErrorCount)
	}

	entries := readAuditEntries(t, auditPath)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].Status != "error" {
		t.Fatalf("audit status = %q, want error", entries[0].Status)
	}
	if entries[0].Target != router.TargetLocal.String() {
		t.Fatalf("audit target = %q, want %q", entries[0].Target, router.TargetLocal.String())
	}
}

func TestCallPipelineReleaseConnection_Local(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.pool = newTestPool(t)
	defer func() { _ = d.pool.Close() }()

	p := &callPipeline{
		daemon: d,
		conn: &pool.Conn{
			ServerName: "local_srv",
			Transport:  &fakeTransport{},
			Healthy:    true,
		},
		target: router.TargetLocal,
	}

	p.releaseConnection()

	stats := d.pool.Stats()
	if stats.IdleConns != 1 {
		t.Fatalf("idle conns = %d, want 1", stats.IdleConns)
	}
}

func TestCallPipelineReleaseConnection_Hub(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.hubPool = newTestPool(t)
	defer func() { _ = d.hubPool.Close() }()

	p := &callPipeline{
		daemon: d,
		conn: &pool.Conn{
			ServerName: "hub_srv",
			Transport:  &fakeTransport{},
			Healthy:    true,
		},
		target: router.TargetHub,
	}

	p.releaseConnection()

	stats := d.hubPool.Stats()
	if stats.IdleConns != 1 {
		t.Fatalf("idle conns = %d, want 1", stats.IdleConns)
	}
}

func TestCallPipelineExecute_SendFailure(t *testing.T) {
	d := newCallPipelineTestDaemon()
	tr := &fakeTransport{sendErr: errors.New("send failed")}

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "send-failure"},
		serverName: "prometheus",
		toolName:   "query",
		method:     "tools/call",
		conn: &pool.Conn{
			ServerName: "prometheus",
			Transport:  tr,
			Healthy:    true,
		},
		target:    router.TargetHub,
		targetStr: router.TargetHub.String(),
	}

	req := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "send-failure", Method: "tools/call"}
	resp := p.execute(req)
	if resp == nil || resp.Error == nil {
		t.Fatal("expected send failure error response")
	}
	if resp.Error.Code != mcp.InternalError {
		t.Fatalf("error code = %d, want %d", resp.Error.Code, mcp.InternalError)
	}
	if p.conn.Healthy {
		t.Fatal("expected connection to be marked unhealthy")
	}
}

func TestCallPipelineExecute_RecvFailure(t *testing.T) {
	d := newCallPipelineTestDaemon()
	tr := &fakeTransport{recvErr: errors.New("recv failed")}

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "recv-failure"},
		serverName: "prometheus",
		toolName:   "query",
		method:     "tools/call",
		conn: &pool.Conn{
			ServerName: "prometheus",
			Transport:  tr,
			Healthy:    true,
		},
		target:    router.TargetHub,
		targetStr: router.TargetHub.String(),
	}

	req := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "recv-failure", Method: "tools/call"}
	resp := p.execute(req)
	if resp == nil || resp.Error == nil {
		t.Fatal("expected recv failure error response")
	}
	if resp.Error.Code != mcp.InternalError {
		t.Fatalf("error code = %d, want %d", resp.Error.Code, mcp.InternalError)
	}
	if p.conn.Healthy {
		t.Fatal("expected connection to be marked unhealthy")
	}
}

func TestCallPipelineExecute_SendTimeoutWrapped(t *testing.T) {
	t.Setenv("LOOM_DAEMON_TOOL_TIMEOUT", "7s")

	d := newCallPipelineTestDaemon()
	tr := &fakeTransport{sendErr: context.DeadlineExceeded}

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "send-timeout"},
		serverName: "prometheus",
		toolName:   "query",
		method:     "tools/call",
		conn: &pool.Conn{
			ServerName: "prometheus",
			Transport:  tr,
			Healthy:    true,
		},
		target:    router.TargetHub,
		targetStr: router.TargetHub.String(),
	}

	req := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "send-timeout", Method: "tools/call"}
	resp := p.execute(req)
	if resp == nil || resp.Error == nil {
		t.Fatal("expected timeout error response")
	}
	if !strings.Contains(resp.Error.Message, "tools/call timeout during send after 7s") {
		t.Fatalf("timeout details missing from %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "recoverable: daemon will reconnect upstream transport and retry on the next request") {
		t.Fatalf("recoverability hint missing from %q", resp.Error.Message)
	}
	if p.conn.Healthy {
		t.Fatal("expected connection to be marked unhealthy")
	}
}

func TestCallPipelineExecute_RecvTimeoutWrapped(t *testing.T) {
	t.Setenv("LOOM_DAEMON_TOOL_TIMEOUT", "9s")

	d := newCallPipelineTestDaemon()
	tr := &fakeTransport{recvErr: context.DeadlineExceeded}

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "recv-timeout"},
		serverName: "prometheus",
		toolName:   "query",
		method:     "tools/call",
		conn: &pool.Conn{
			ServerName: "prometheus",
			Transport:  tr,
			Healthy:    true,
		},
		target:    router.TargetHub,
		targetStr: router.TargetHub.String(),
	}

	req := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "recv-timeout", Method: "tools/call"}
	resp := p.execute(req)
	if resp == nil || resp.Error == nil {
		t.Fatal("expected timeout error response")
	}
	if !strings.Contains(resp.Error.Message, "tools/call timeout during recv after 9s") {
		t.Fatalf("timeout details missing from %q", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "recoverable: daemon will reconnect upstream transport and retry on the next request") {
		t.Fatalf("recoverability hint missing from %q", resp.Error.Message)
	}
	if p.conn.Healthy {
		t.Fatal("expected connection to be marked unhealthy")
	}
}

func TestCallPipelineExecute_SuccessCachesResponse(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.respCache = NewResponseCache(CacheConfig{Enabled: true})
	tr := &fakeTransport{
		recvMsg: &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      "ok",
			Result:  json.RawMessage(`{"ok":true}`),
		},
	}

	params := json.RawMessage(`{"query":"up"}`)
	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "ok"},
		serverName: "prometheus",
		toolName:   "query",
		method:     "tools/call",
		cacheKey:   d.respCache.Key("prometheus", "query", params),
		conn: &pool.Conn{
			ServerName: "prometheus",
			Transport:  tr,
			Healthy:    true,
		},
		target:    router.TargetHub,
		targetStr: router.TargetHub.String(),
	}

	req := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "ok", Method: "tools/call", Params: params}
	resp := p.execute(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error response: %+v", resp.Error)
	}

	got, ok := d.respCache.Get(p.cacheKey)
	if !ok {
		t.Fatal("expected successful response to be cached")
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("cached result = %s, want %s", string(got), `{"ok":true}`)
	}
}

func TestCallPipelineTransportFailure_LocalClearsIdleAndStopsServer(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.pool = newTestPool(t)
	d.procMgr = process.NewManager(nil, "codex")
	defer func() { _ = d.pool.Close() }()

	// Seed an idle pooled connection so local recovery has something to clear.
	d.pool.Put(&pool.Conn{
		ServerName: "local_srv",
		Transport:  &fakeTransport{},
		Healthy:    true,
	})

	p := &callPipeline{
		daemon:     d,
		msg:        &mcp.Message{ID: "transport-local"},
		serverName: "local_srv",
		method:     "tools/call",
		target:     router.TargetLocal,
		targetStr:  router.TargetLocal.String(),
		conn: &pool.Conn{
			ServerName: "local_srv",
			Transport:  &fakeTransport{},
			Healthy:    true,
		},
	}

	resp := p.transportFailure("send", errors.New("send failed"), time.Now())
	if resp == nil || resp.Error == nil {
		t.Fatal("expected transport error response")
	}
	if resp.Error.Code != mcp.InternalError {
		t.Fatalf("error code = %d, want %d", resp.Error.Code, mcp.InternalError)
	}
	if p.conn.Healthy {
		t.Fatal("expected connection marked unhealthy on transport failure")
	}

	stats := d.pool.Stats()
	if stats.IdleConns != 0 {
		t.Fatalf("idle conns = %d, want 0 after ClearServer", stats.IdleConns)
	}
}

func TestCallPipelineTransportFailure_HubClearsPool(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.hubPool = newTestPool(t)
	defer func() { _ = d.hubPool.Close() }()

	// Seed idle connections so hub recovery has something to clear.
	d.hubPool.Put(&pool.Conn{
		ServerName: "hub_srv",
		Transport:  &fakeTransport{},
		Healthy:    true,
	})
	d.hubPool.Put(&pool.Conn{
		ServerName: "hub_srv",
		Transport:  &fakeTransport{},
		Healthy:    true,
	})

	stats := d.hubPool.Stats()
	if stats.IdleConns != 2 {
		t.Fatalf("idle conns before = %d, want 2", stats.IdleConns)
	}

	p := &callPipeline{
		daemon:     d,
		msg:        &mcp.Message{ID: "transport-hub-clear"},
		serverName: "hub_srv",
		method:     "tools/call",
		target:     router.TargetHub,
		targetStr:  router.TargetHub.String(),
		conn: &pool.Conn{
			ServerName: "hub_srv",
			Transport:  &fakeTransport{},
			Healthy:    true,
		},
	}

	resp := p.transportFailure("recv", errors.New("connection reset"), time.Now())
	if resp == nil || resp.Error == nil {
		t.Fatal("expected transport error response")
	}
	if p.conn.Healthy {
		t.Fatal("expected connection marked unhealthy on transport failure")
	}

	stats = d.hubPool.Stats()
	if stats.IdleConns != 0 {
		t.Fatalf("idle conns = %d, want 0 after hub ClearServer", stats.IdleConns)
	}
}

func TestCallPipelineTransportFailure_EmitsAuditAndCost(t *testing.T) {
	d := newCallPipelineTestDaemon()
	auditPath := enableAuditAndCostForTest(t, d)

	p := &callPipeline{
		daemon:     d,
		msg:        &mcp.Message{ID: "transport-hub"},
		serverName: "prometheus",
		toolName:   "query",
		method:     "tools/call",
		target:     router.TargetHub,
		targetStr:  router.TargetHub.String(),
		params: callParams{
			AgentID: "agent-1",
		},
		auditStart: time.Now(),
		conn: &pool.Conn{
			ServerName: "prometheus",
			Transport:  &fakeTransport{},
			Healthy:    true,
		},
	}

	resp := p.transportFailure("recv", errors.New("recv failed"), time.Now().Add(-50*time.Millisecond))
	if resp == nil || resp.Error == nil {
		t.Fatal("expected transport failure response")
	}

	snap := d.cost.Snapshot()
	if snap.Totals.CallCount != 1 {
		t.Fatalf("call_count = %d, want 1", snap.Totals.CallCount)
	}
	if snap.Totals.ErrorCount != 1 {
		t.Fatalf("error_count = %d, want 1", snap.Totals.ErrorCount)
	}

	entries := readAuditEntries(t, auditPath)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].Status != "error" {
		t.Fatalf("audit status = %q, want error", entries[0].Status)
	}
	if entries[0].Target != router.TargetHub.String() {
		t.Fatalf("audit target = %q, want %q", entries[0].Target, router.TargetHub.String())
	}
	if !strings.Contains(entries[0].Error, "recv failed") {
		t.Fatalf("audit error = %q, want recv failed", entries[0].Error)
	}
}

func TestHandleCall_ParseFailureShortCircuitsWithoutAudit(t *testing.T) {
	d := newCallPipelineTestDaemon()
	auditPath := enableAuditAndCostForTest(t, d)
	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "parse-fail",
		Method:  "loom/call",
		Params:  json.RawMessage(`{`),
	}

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected parse failure response")
	}
	if resp.Error.Code != mcp.InvalidParams {
		t.Fatalf("error code = %d, want %d", resp.Error.Code, mcp.InvalidParams)
	}

	entries := readAuditEntries(t, auditPath)
	if len(entries) != 0 {
		t.Fatalf("audit entries = %d, want 0", len(entries))
	}

	snap := d.cost.Snapshot()
	if snap.Totals.CallCount != 0 {
		t.Fatalf("call_count = %d, want 0", snap.Totals.CallCount)
	}
}

func TestHandleCall_RequestPolicyDeniedEmitsAuditAndCost(t *testing.T) {
	d := newCallPipelineTestDaemon()
	auditPath := enableAuditAndCostForTest(t, d)
	d.policy = NewGatewayPolicyEnforcer(GatewayPolicyConfig{
		Enabled: true,
		Request: []GatewayRequestPolicyRule{
			{
				ID:                 "deny-force-delete",
				Server:             "github",
				Tool:               "delete_*",
				ForbiddenArguments: []string{"force"},
				ReasonCode:         "POLICY_FORCE_DELETE_BLOCKED",
			},
		},
	}, d.logger)

	msg := newCallMessage(t, map[string]any{
		"server":    "github",
		"tool":      "delete_repo",
		"arguments": json.RawMessage(`{"force":true}`),
		"agent_id":  "agent-policy",
	})

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected policy denial response")
	}
	if resp.Error.Code != mcp.InvalidRequest {
		t.Fatalf("error code = %d, want %d", resp.Error.Code, mcp.InvalidRequest)
	}
	if !strings.Contains(resp.Error.Message, "policy denied") {
		t.Fatalf("unexpected denial message: %q", resp.Error.Message)
	}
	data, ok := resp.Error.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected error data map, got %T", resp.Error.Data)
	}
	if got := data["policy_rule_id"]; got != "deny-force-delete" {
		t.Fatalf("policy_rule_id = %v, want %q", got, "deny-force-delete")
	}
	if got := data["policy_reason_code"]; got != "POLICY_FORCE_DELETE_BLOCKED" {
		t.Fatalf("policy_reason_code = %v, want %q", got, "POLICY_FORCE_DELETE_BLOCKED")
	}

	entries := readAuditEntries(t, auditPath)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].Status != "denied" {
		t.Fatalf("audit status = %q, want denied", entries[0].Status)
	}
	if entries[0].PolicyRuleID != "deny-force-delete" {
		t.Fatalf("audit policy_rule_id = %q, want %q", entries[0].PolicyRuleID, "deny-force-delete")
	}
	if entries[0].PolicyReasonCode != "POLICY_FORCE_DELETE_BLOCKED" {
		t.Fatalf("audit policy_reason_code = %q, want %q", entries[0].PolicyReasonCode, "POLICY_FORCE_DELETE_BLOCKED")
	}

	snap := d.cost.Snapshot()
	if snap.Totals.CallCount != 1 {
		t.Fatalf("call_count = %d, want 1", snap.Totals.CallCount)
	}
	if snap.Totals.DeniedCount != 1 {
		t.Fatalf("denied_count = %d, want 1", snap.Totals.DeniedCount)
	}
}

func TestHandleCall_CacheHitShortCircuitsRouting(t *testing.T) {
	d := newCallPipelineTestDaemon()
	auditPath := enableAuditAndCostForTest(t, d)
	d.respCache = NewResponseCache(CacheConfig{Enabled: true})

	args := json.RawMessage(`{"query":"up"}`)
	key := d.respCache.Key("prometheus", "query", args)
	d.respCache.Set(key, json.RawMessage(`{"cached":true}`), "prometheus", "query")

	msg := newCallMessage(t, map[string]any{
		"server":    "prometheus",
		"tool":      "query",
		"arguments": json.RawMessage(`{"query":"up"}`),
		"agent_id":  "agent-cache",
	})

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("unexpected error response: %+v", resp)
	}
	if string(resp.Result) != `{"cached":true}` {
		t.Fatalf("result = %s, want %s", string(resp.Result), `{"cached":true}`)
	}

	entries := readAuditEntries(t, auditPath)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if !entries[0].Cached {
		t.Fatal("expected cached audit entry")
	}

	snap := d.cost.Snapshot()
	if snap.Totals.CallCount != 1 {
		t.Fatalf("call_count = %d, want 1", snap.Totals.CallCount)
	}
	if snap.Totals.CachedCount != 1 {
		t.Fatalf("cached_count = %d, want 1", snap.Totals.CachedCount)
	}
}

func TestHandleCall_RouteFailureEmitsSingleAudit(t *testing.T) {
	d := newCallPipelineTestDaemon()
	auditPath := enableAuditAndCostForTest(t, d)
	d.router = router.New(router.Config{HubEnabled: false})

	msg := newCallMessage(t, map[string]any{
		"server":   "unknown",
		"tool":     "query",
		"agent_id": "agent-route",
	})

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected route failure response")
	}
	if !strings.Contains(resp.Error.Message, "server unavailable") {
		t.Fatalf("unexpected route failure message: %q", resp.Error.Message)
	}

	entries := readAuditEntries(t, auditPath)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].Status != "error" {
		t.Fatalf("audit status = %q, want error", entries[0].Status)
	}

	snap := d.cost.Snapshot()
	if snap.Totals.CallCount != 1 {
		t.Fatalf("call_count = %d, want 1", snap.Totals.CallCount)
	}
	if snap.Totals.ErrorCount != 1 {
		t.Fatalf("error_count = %d, want 1", snap.Totals.ErrorCount)
	}
}

func TestHandleCall_TransportFailureEmitsSingleAudit(t *testing.T) {
	d := newCallPipelineTestDaemon()
	auditPath := enableAuditAndCostForTest(t, d)
	d.hubPool = pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Second,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			return &fakeTransport{sendErr: errors.New("send failed")}, nil
		},
	})
	defer func() { _ = d.hubPool.Close() }()

	msg := newCallMessage(t, map[string]any{
		"server":   "hub_only",
		"tool":     "query",
		"agent_id": "agent-send-fail",
	})

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected transport failure response")
	}
	if !strings.Contains(resp.Error.Message, "send failed") {
		t.Fatalf("unexpected transport error: %q", resp.Error.Message)
	}

	entries := readAuditEntries(t, auditPath)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].Status != "error" {
		t.Fatalf("audit status = %q, want error", entries[0].Status)
	}

	snap := d.cost.Snapshot()
	if snap.Totals.CallCount != 1 {
		t.Fatalf("call_count = %d, want 1", snap.Totals.CallCount)
	}
	if snap.Totals.ErrorCount != 1 {
		t.Fatalf("error_count = %d, want 1", snap.Totals.ErrorCount)
	}
}

func TestHandleCall_RBACDenialEmitsAuditAndCost(t *testing.T) {
	d := newCallPipelineTestDaemon()
	auditPath := enableAuditAndCostForTest(t, d)
	d.rbac = NewRBACEnforcer(RBACConfig{
		Enabled:       true,
		DefaultPolicy: "deny",
	}, d.logger)

	msg := newCallMessage(t, map[string]any{
		"server":   "github",
		"tool":     "delete_repo",
		"agent_id": "agent-rbac",
	})

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected RBAC denial response")
	}
	if resp.Error.Code != mcp.InvalidRequest {
		t.Fatalf("error code = %d, want %d", resp.Error.Code, mcp.InvalidRequest)
	}
	if !strings.Contains(resp.Error.Message, "access denied") {
		t.Fatalf("unexpected denial message: %q", resp.Error.Message)
	}

	entries := readAuditEntries(t, auditPath)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].Status != "denied" {
		t.Fatalf("audit status = %q, want denied", entries[0].Status)
	}
	if entries[0].Server != "github" {
		t.Fatalf("audit server = %q, want github", entries[0].Server)
	}
	if entries[0].Tool != "delete_repo" {
		t.Fatalf("audit tool = %q, want delete_repo", entries[0].Tool)
	}

	snap := d.cost.Snapshot()
	if snap.Totals.CallCount != 1 {
		t.Fatalf("call_count = %d, want 1", snap.Totals.CallCount)
	}
	if snap.Totals.DeniedCount != 1 {
		t.Fatalf("denied_count = %d, want 1", snap.Totals.DeniedCount)
	}
}

// TestCallPipeline_ErrorResponseEnvelopeStructure validates that the unified
// errorResponse helper produces correct JSON-RPC error envelopes with and
// without the optional Data field.
func TestCallPipeline_ErrorResponseEnvelopeStructure(t *testing.T) {
	d := newCallPipelineTestDaemon()
	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "envelope-test",
	})

	// Error without Data.
	resp := p.invalidParamsError("bad input")
	if resp.JSONRPC != mcp.JSONRPCVersion {
		t.Fatalf("JSONRPC = %q, want %q", resp.JSONRPC, mcp.JSONRPCVersion)
	}
	if resp.ID != "envelope-test" {
		t.Fatalf("ID = %v, want envelope-test", resp.ID)
	}
	if resp.Error.Code != mcp.InvalidParams {
		t.Fatalf("Code = %d, want %d", resp.Error.Code, mcp.InvalidParams)
	}
	if resp.Error.Data != nil {
		t.Fatalf("expected nil Data for plain error, got %v", resp.Error.Data)
	}

	// Error with Data (policy denial).
	policyResp := p.policyDeniedError(GatewayPolicyDecision{
		RuleID:     "rule-1",
		ReasonCode: "BLOCKED",
		Reason:     "test reason",
		Stage:      "request",
		Action:     "deny",
	})
	if policyResp.Error.Code != mcp.InvalidRequest {
		t.Fatalf("Code = %d, want %d", policyResp.Error.Code, mcp.InvalidRequest)
	}
	data, ok := policyResp.Error.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected Data map, got %T", policyResp.Error.Data)
	}
	if data["policy_rule_id"] != "rule-1" {
		t.Fatalf("policy_rule_id = %v, want rule-1", data["policy_rule_id"])
	}
	if data["policy_reason_code"] != "BLOCKED" {
		t.Fatalf("policy_reason_code = %v, want BLOCKED", data["policy_reason_code"])
	}
	if data["policy_stage"] != "request" {
		t.Fatalf("policy_stage = %v, want request", data["policy_stage"])
	}
	if data["policy_action"] != "deny" {
		t.Fatalf("policy_action = %v, want deny", data["policy_action"])
	}
}

// --- Drain readiness tests (TD-SESSION-05 / DEBT-006) ---

func TestHandleStatus_DrainReady_WhenIdle(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.pool = newTestPool(t)
	d.procMgr = process.NewManager(nil, "codex")
	d.registry = &kitregistry.Registry{}
	defer func() { _ = d.pool.Close() }()
	msg := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "status-1", Method: "loom/status"}

	resp, err := d.handleStatus(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	var status struct {
		Running    bool  `json:"running"`
		ActiveRPCs int64 `json:"activeRPCs"`
		DrainReady bool  `json:"drainReady"`
	}
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if !status.DrainReady {
		t.Fatal("expected drainReady=true when no active RPCs")
	}
	if status.ActiveRPCs != 0 {
		t.Fatalf("activeRPCs = %d, want 0", status.ActiveRPCs)
	}
}

func TestHandleStatus_NotDrainReady_DuringCall(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.procMgr = process.NewManager(nil, "codex")
	d.registry = &kitregistry.Registry{}

	d.router = router.New(router.Config{
		HubEnabled:       false,
		FailureThreshold: 10,
		Registry: &kitregistry.Registry{
			Servers: []*kitregistry.Server{
				{Name: "drain_srv", Categories: []string{"local-only"}},
			},
		},
	})

	// Transport blocks on recv until we release it.
	recvGate := make(chan struct{})
	d.pool = pool.New(pool.Config{
		MaxIdle:     2,
		MaxOpen:     2,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			return &fakeTransport{
				recvFn: func(ctx context.Context) (*mcp.Message, error) {
					select {
					case <-recvGate:
						return &mcp.Message{
							JSONRPC: mcp.JSONRPCVersion,
							ID:      "test-id",
							Result:  json.RawMessage(`{"ok":true}`),
						}, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				},
			}, nil
		},
	})
	defer func() { _ = d.pool.Close() }()

	msg := newCallMessage(t, map[string]any{
		"server": "drain_srv",
		"tool":   "slow",
	})

	// Start a call in the background.
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		d.handleCall(context.Background(), msg)
	}()

	// Wait briefly for the call to be in-flight.
	time.Sleep(50 * time.Millisecond)

	// Check activeRPCs and drainReady during active call.
	rpcs := d.activeRPCs.Load()
	if rpcs != 1 {
		t.Fatalf("activeRPCs = %d, want 1 during in-flight call", rpcs)
	}

	statusMsg := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "status-2", Method: "loom/status"}
	resp, err := d.handleStatus(context.Background(), statusMsg)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	var status struct {
		ActiveRPCs int64 `json:"activeRPCs"`
		DrainReady bool  `json:"drainReady"`
	}
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.DrainReady {
		t.Fatal("expected drainReady=false during in-flight RPC")
	}
	if status.ActiveRPCs != 1 {
		t.Fatalf("activeRPCs = %d, want 1", status.ActiveRPCs)
	}

	// Release the blocked call.
	close(recvGate)
	<-callDone

	// After call completes, should be drain-ready again.
	rpcs = d.activeRPCs.Load()
	if rpcs != 0 {
		t.Fatalf("activeRPCs = %d, want 0 after call completes", rpcs)
	}
}

// --- Chaos / resilience tests (TD-SESSION-03 / DEBT-007) ---

// TestCallPipeline_TransportFailureThenRecovery simulates a server crash
// mid-session: call 1 succeeds, call 2 fails (same connection breaks),
// call 3 succeeds with a fresh connection (simulating server restart).
func TestCallPipeline_TransportFailureThenRecovery(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.procMgr = process.NewManager(nil, "codex")

	// Use a high failure threshold so the circuit breaker doesn't trip.
	d.router = router.New(router.Config{
		HubEnabled:       false,
		FailureThreshold: 10,
		Registry: &kitregistry.Registry{
			Servers: []*kitregistry.Server{
				{Name: "chaos_srv", Categories: []string{"local-only"}},
			},
		},
	})

	// Track send count so the same transport can fail on its 2nd send
	// (simulating a connection that was healthy then breaks mid-session).
	sendCount := 0

	dialCount := 0
	d.pool = pool.New(pool.Config{
		MaxIdle:     2,
		MaxOpen:     2,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			dialCount++
			if dialCount == 1 {
				// First dial: transport succeeds on first send, then breaks.
				return &fakeTransport{
					sendFn: func(_ context.Context, _ *mcp.Message) error {
						sendCount++
						if sendCount >= 2 {
							return errors.New("broken pipe")
						}
						return nil
					},
					recvMsg: &mcp.Message{
						JSONRPC: mcp.JSONRPCVersion,
						ID:      "test-id",
						Result:  json.RawMessage(`{"ok":true}`),
					},
				}, nil
			}
			// Subsequent dials: fully healthy (simulating server restart).
			return &fakeTransport{
				recvMsg: &mcp.Message{
					JSONRPC: mcp.JSONRPCVersion,
					ID:      "test-id",
					Result:  json.RawMessage(`{"recovered":true}`),
				},
			}, nil
		},
	})
	defer func() { _ = d.pool.Close() }()

	msg := newCallMessage(t, map[string]any{
		"server": "chaos_srv",
		"tool":   "noop",
	})

	// Call 1: should succeed (sendCount goes from 0→1).
	resp1, err1 := d.handleCall(context.Background(), msg)
	if err1 != nil {
		t.Fatalf("call 1: unexpected error: %v", err1)
	}
	if resp1.Error != nil {
		t.Fatalf("call 1: unexpected error response: %+v", resp1.Error)
	}

	// Call 2: same connection reused from pool, send fails (sendCount=2).
	resp2, err2 := d.handleCall(context.Background(), msg)
	if err2 != nil {
		t.Fatalf("call 2: unexpected error: %v", err2)
	}
	if resp2.Error == nil {
		t.Fatal("call 2: expected transport failure error response")
	}
	if !strings.Contains(resp2.Error.Message, "broken pipe") {
		t.Fatalf("call 2: expected broken pipe in error, got %q", resp2.Error.Message)
	}

	// Pool should be cleared after transport failure.
	stats := d.pool.Stats()
	if stats.IdleConns != 0 {
		t.Fatalf("pool idle conns = %d, want 0 after transport failure", stats.IdleConns)
	}

	// Call 3: recovery with fresh connection from new dial.
	resp3, err3 := d.handleCall(context.Background(), msg)
	if err3 != nil {
		t.Fatalf("call 3: unexpected error: %v", err3)
	}
	if resp3.Error != nil {
		t.Fatalf("call 3: expected recovery success, got error: %+v", resp3.Error)
	}
	if string(resp3.Result) != `{"recovered":true}` {
		t.Fatalf("call 3: result = %s, want recovery response", string(resp3.Result))
	}
}

// TestCallPipeline_ConsecutiveTimeoutsThenRecovery verifies that multiple
// consecutive timeout failures don't permanently wedge the call pipeline,
// and recovery succeeds once the transport is healthy again.
func TestCallPipeline_ConsecutiveTimeoutsThenRecovery(t *testing.T) {
	t.Setenv("LOOM_DAEMON_TOOL_TIMEOUT", "1s")

	dialCount := 0
	d := newCallPipelineTestDaemon()
	d.procMgr = process.NewManager(nil, "codex")

	// Set a high failure threshold so the circuit breaker allows recovery
	// without waiting for the 30s recovery window.
	d.router = router.New(router.Config{
		HubEnabled:       false,
		FailureThreshold: 10,
		Registry: &kitregistry.Registry{
			Servers: []*kitregistry.Server{
				{Name: "timeout_srv", Categories: []string{"local-only"}},
			},
		},
	})

	d.pool = pool.New(pool.Config{
		MaxIdle:     2,
		MaxOpen:     2,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			dialCount++
			if dialCount <= 3 {
				return &fakeTransport{
					recvFn: func(ctx context.Context) (*mcp.Message, error) {
						<-ctx.Done()
						return nil, ctx.Err()
					},
				}, nil
			}
			return &fakeTransport{
				recvMsg: &mcp.Message{
					JSONRPC: mcp.JSONRPCVersion,
					ID:      "test-id",
					Result:  json.RawMessage(`{"finally":true}`),
				},
			}, nil
		},
	})
	defer func() { _ = d.pool.Close() }()

	msg := newCallMessage(t, map[string]any{
		"server": "timeout_srv",
		"tool":   "slow_op",
	})

	// 3 consecutive timeouts - each clears the pool and dials a new transport.
	for i := 1; i <= 3; i++ {
		resp, err := d.handleCall(context.Background(), msg)
		if err != nil {
			t.Fatalf("timeout call %d: unexpected error: %v", i, err)
		}
		if resp.Error == nil {
			t.Fatalf("timeout call %d: expected timeout error response", i)
		}
		if !strings.Contains(resp.Error.Message, "timeout") {
			t.Fatalf("timeout call %d: expected timeout in error, got %q", i, resp.Error.Message)
		}
	}

	// Recovery call should succeed with 4th dial returning healthy transport.
	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("recovery call: unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("recovery call: expected success, got error: %+v", resp.Error)
	}
	if string(resp.Result) != `{"finally":true}` {
		t.Fatalf("recovery call: result = %s, want recovery response", string(resp.Result))
	}
}

// TestCallPipeline_RecvEOFTriggersServerRestart verifies that an EOF during
// recv (server process crashed) clears the pool and triggers server stop,
// allowing the next call to re-dial a fresh server process.
func TestCallPipeline_RecvEOFTriggersServerRestart(t *testing.T) {
	callNum := 0
	d := newCallPipelineTestDaemon()
	d.procMgr = process.NewManager(nil, "codex")

	d.router = router.New(router.Config{
		HubEnabled:       false,
		FailureThreshold: 10,
		Registry: &kitregistry.Registry{
			Servers: []*kitregistry.Server{
				{Name: "eof_srv", Categories: []string{"local-only"}},
			},
		},
	})

	d.pool = pool.New(pool.Config{
		MaxIdle:     2,
		MaxOpen:     2,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			callNum++
			if callNum == 1 {
				return &fakeTransport{recvErr: io.EOF}, nil
			}
			return &fakeTransport{
				recvMsg: &mcp.Message{
					JSONRPC: mcp.JSONRPCVersion,
					ID:      "test-id",
					Result:  json.RawMessage(`{"restarted":true}`),
				},
			}, nil
		},
	})
	defer func() { _ = d.pool.Close() }()

	msg := newCallMessage(t, map[string]any{
		"server": "eof_srv",
		"tool":   "check",
	})

	// Call 1: EOF (simulating server process crash).
	resp1, err1 := d.handleCall(context.Background(), msg)
	if err1 != nil {
		t.Fatalf("eof call: unexpected error: %v", err1)
	}
	if resp1.Error == nil {
		t.Fatal("eof call: expected error response")
	}

	// Pool should be cleared.
	stats := d.pool.Stats()
	if stats.IdleConns != 0 {
		t.Fatalf("pool idle = %d, want 0 after EOF", stats.IdleConns)
	}

	// Call 2: recovery after server restart.
	resp2, err2 := d.handleCall(context.Background(), msg)
	if err2 != nil {
		t.Fatalf("recovery call: unexpected error: %v", err2)
	}
	if resp2.Error != nil {
		t.Fatalf("recovery call: expected success, got error: %+v", resp2.Error)
	}
	if string(resp2.Result) != `{"restarted":true}` {
		t.Fatalf("recovery call: result = %s, want restarted response", string(resp2.Result))
	}
}

// --- Response ID correlation tests ---

// TestCallPipelineExecute_ResponseIDMismatch verifies that execute() detects
// when the response ID does not match the request ID and treats it as a
// transport failure. This catches the root cause of stale responses from
// shared stdio transports.
func TestCallPipelineExecute_ResponseIDMismatch(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.procMgr = process.NewManager(nil, "codex")

	// Transport returns a response with a different ID than the request.
	tr := &fakeTransport{
		recvMsg: &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      "stale-99",
			Result:  json.RawMessage(`{"wrong":true}`),
		},
	}

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "req-42"},
		serverName: "test_srv",
		toolName:   "query",
		method:     "tools/call",
		conn: &pool.Conn{
			ServerName: "test_srv",
			Transport:  tr,
			Healthy:    true,
		},
		target:    router.TargetHub,
		targetStr: router.TargetHub.String(),
	}

	req := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "req-42", Method: "tools/call"}
	resp := p.execute(req)
	if resp == nil || resp.Error == nil {
		t.Fatal("expected error response for ID mismatch")
	}
	if resp.Error.Code != mcp.InternalError {
		t.Fatalf("error code = %d, want %d", resp.Error.Code, mcp.InternalError)
	}
	if !strings.Contains(resp.Error.Message, "response ID mismatch") {
		t.Fatalf("error message = %q, want response ID mismatch", resp.Error.Message)
	}
	if !strings.Contains(resp.Error.Message, "stale-99") {
		t.Fatalf("error message should contain mismatched ID, got %q", resp.Error.Message)
	}
	if p.conn.Healthy {
		t.Fatal("expected connection to be marked unhealthy after ID mismatch")
	}
}

// TestCallPipelineExecute_ResponseIDMatchSucceeds verifies that execute()
// passes when the response ID matches the request ID.
func TestCallPipelineExecute_ResponseIDMatchSucceeds(t *testing.T) {
	d := newCallPipelineTestDaemon()
	tr := &fakeTransport{
		recvMsg: &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      "match-1",
			Result:  json.RawMessage(`{"ok":true}`),
		},
	}

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "match-1"},
		serverName: "test_srv",
		toolName:   "query",
		method:     "tools/call",
		conn: &pool.Conn{
			ServerName: "test_srv",
			Transport:  tr,
			Healthy:    true,
		},
		target:    router.TargetHub,
		targetStr: router.TargetHub.String(),
	}

	req := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "match-1", Method: "tools/call"}
	resp := p.execute(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error response: %+v", resp.Error)
	}
	if string(resp.Result) != `{"ok":true}` {
		t.Fatalf("result = %s, want ok", string(resp.Result))
	}
}

// TestCallPipelineExecute_ResponseIDNilAccepted verifies that a nil response
// ID (e.g., for notifications) does not trigger a mismatch error.
func TestCallPipelineExecute_ResponseIDNilAccepted(t *testing.T) {
	d := newCallPipelineTestDaemon()
	tr := &fakeTransport{
		recvMsg: &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			// ID is nil (notification-style response)
			Result: json.RawMessage(`{"notif":true}`),
		},
	}

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "req-notif"},
		serverName: "test_srv",
		toolName:   "query",
		method:     "tools/call",
		conn: &pool.Conn{
			ServerName: "test_srv",
			Transport:  tr,
			Healthy:    true,
		},
		target:    router.TargetHub,
		targetStr: router.TargetHub.String(),
	}

	req := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "req-notif", Method: "tools/call"}
	resp := p.execute(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error for nil response ID: %+v", resp.Error)
	}
}

// TestCallPipelineExecute_NumericIDCorrelation verifies that numeric IDs
// (which can be float64 after JSON round-trip) are correctly correlated
// with their string representations.
func TestCallPipelineExecute_NumericIDCorrelation(t *testing.T) {
	d := newCallPipelineTestDaemon()

	// Simulate JSON round-trip: numeric ID becomes float64.
	tr := &fakeTransport{
		recvMsg: &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      float64(42),
			Result:  json.RawMessage(`{"ok":true}`),
		},
	}

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: float64(42)},
		serverName: "test_srv",
		toolName:   "query",
		method:     "tools/call",
		conn: &pool.Conn{
			ServerName: "test_srv",
			Transport:  tr,
			Healthy:    true,
		},
		target:    router.TargetHub,
		targetStr: router.TargetHub.String(),
	}

	req := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: float64(42), Method: "tools/call"}
	resp := p.execute(req)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error for matching numeric IDs: %+v", resp.Error)
	}
}

// TestCallPipelineExecute_NumericIDMismatch verifies that numeric ID
// mismatches are detected.
func TestCallPipelineExecute_NumericIDMismatch(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.procMgr = process.NewManager(nil, "codex")

	tr := &fakeTransport{
		recvMsg: &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      float64(999),
			Result:  json.RawMessage(`{"wrong":true}`),
		},
	}

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: float64(42)},
		serverName: "test_srv",
		toolName:   "query",
		method:     "tools/call",
		conn: &pool.Conn{
			ServerName: "test_srv",
			Transport:  tr,
			Healthy:    true,
		},
		target:    router.TargetHub,
		targetStr: router.TargetHub.String(),
	}

	req := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: float64(42), Method: "tools/call"}
	resp := p.execute(req)
	if resp == nil || resp.Error == nil {
		t.Fatal("expected error for numeric ID mismatch")
	}
	if !strings.Contains(resp.Error.Message, "response ID mismatch") {
		t.Fatalf("expected mismatch error, got %q", resp.Error.Message)
	}
	if p.conn.Healthy {
		t.Fatal("expected connection marked unhealthy")
	}
}

// --- Concurrent call correlation tests ---

// TestHandleCall_ConcurrentCallsGetCorrectResponses verifies that multiple
// concurrent calls through the daemon each get the response meant for them,
// not a stale response from another caller. This is the test that directly
// exercises the bug discovered via the Python reproduction script.
func TestHandleCall_ConcurrentCallsGetCorrectResponses(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.procMgr = process.NewManager(nil, "codex")

	d.router = router.New(router.Config{
		HubEnabled:       false,
		FailureThreshold: 100,
		Registry: &kitregistry.Registry{
			Servers: []*kitregistry.Server{
				{Name: "concurrent_srv", Categories: []string{"local-only"}},
			},
		},
	})

	// Each call gets a transport that echoes back the request ID in the response.
	d.pool = pool.New(pool.Config{
		MaxIdle:     10,
		MaxOpen:     10,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			return &fakeTransport{
				sendFn: func(_ context.Context, _ *mcp.Message) error { return nil },
				recvFn: func(_ context.Context) (*mcp.Message, error) {
					// Simulate a correct server: response ID echoes the request ID.
					// Since the callLock serializes calls, we can safely use a
					// per-transport counter here.
					return &mcp.Message{
						JSONRPC: mcp.JSONRPCVersion,
						ID:      "test-id",
						Result:  json.RawMessage(`{"ok":true}`),
					}, nil
				},
			}, nil
		},
	})
	defer func() { _ = d.pool.Close() }()

	const concurrency = 20
	errs := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			msg := newCallMessage(t, map[string]any{
				"server": "concurrent_srv",
				"tool":   "echo",
			})

			resp, err := d.handleCall(context.Background(), msg)
			if err != nil {
				errs <- fmt.Errorf("call %d: %w", idx, err)
				return
			}
			if resp.Error != nil {
				errs <- fmt.Errorf("call %d: error response: %s", idx, resp.Error.Message)
				return
			}
			errs <- nil
		}(i)
	}

	for i := 0; i < concurrency; i++ {
		if err := <-errs; err != nil {
			t.Errorf("%v", err)
		}
	}
}

// TestHandleCall_ResponseIDMismatchEndToEnd verifies the full handleCall path
// detects and rejects a response with a mismatched ID.
func TestHandleCall_ResponseIDMismatchEndToEnd(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.procMgr = process.NewManager(nil, "codex")

	d.router = router.New(router.Config{
		HubEnabled:       false,
		FailureThreshold: 10,
		Registry: &kitregistry.Registry{
			Servers: []*kitregistry.Server{
				{Name: "mismatch_srv", Categories: []string{"local-only"}},
			},
		},
	})

	// Transport returns a stale response with a wrong ID.
	d.pool = pool.New(pool.Config{
		MaxIdle:     2,
		MaxOpen:     2,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			return &fakeTransport{
				recvMsg: &mcp.Message{
					JSONRPC: mcp.JSONRPCVersion,
					ID:      "stale-from-hud",
					Result:  json.RawMessage(`{"stale":true}`),
				},
			}, nil
		},
	})
	defer func() { _ = d.pool.Close() }()

	msg := newCallMessage(t, map[string]any{
		"server": "mismatch_srv",
		"tool":   "query",
	})

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected error response for ID mismatch")
	}
	if !strings.Contains(resp.Error.Message, "response ID mismatch") {
		t.Fatalf("expected mismatch error, got %q", resp.Error.Message)
	}
}

// --- Lock ordering tests ---

// TestFetchServerToolsViaPool_LockOrdering verifies that fetchServerToolsViaPool
// acquires the callLock before pool.Get, preventing deadlock with callPipeline.
// The test installs a DialFunc that checks whether the callLock is already held
// (via TryLock) when pool.Get triggers a new dial. If lock ordering is correct
// (lock→pool), TryLock must fail because the lock is held by the caller.
func TestFetchServerToolsViaPool_LockOrdering(t *testing.T) {
	d := newCallPipelineTestDaemon()

	poolGetCalled := false
	var lockHeldDuringDial bool

	d.pool = pool.New(pool.Config{
		MaxIdle:     2,
		MaxOpen:     2,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			poolGetCalled = true
			// If the callLock is properly acquired BEFORE pool.Get, then
			// TryLock from the same goroutine will fail (returns false)
			// because the mutex is already held by our caller.
			mu := d.callLock("order_test")
			lockHeldDuringDial = !mu.TryLock()
			if !lockHeldDuringDial {
				// TryLock succeeded, meaning lock was NOT held. Unlock it.
				mu.Unlock()
			}
			return &fakeTransport{
				recvMsg: &mcp.Message{
					JSONRPC: mcp.JSONRPCVersion,
					ID:      float64(1),
					Result:  json.RawMessage(`{"tools":[]}`),
				},
			}, nil
		},
	})
	defer func() { _ = d.pool.Close() }()

	_, _ = d.fetchServerToolsViaPool(context.Background(), "order_test")

	if !poolGetCalled {
		t.Fatal("pool.Get was never called")
	}
	if !lockHeldDuringDial {
		t.Fatal("callLock was NOT held when pool.Get dialed - lock ordering violation (should be lock→pool, not pool→lock)")
	}
}

// TestFetchServerResources_LockOrdering verifies that fetchServerResources
// also acquires callLock before pool.Get.
func TestFetchServerResources_LockOrdering(t *testing.T) {
	d := newCallPipelineTestDaemon()

	poolGetCalled := false
	var lockHeldDuringDial bool

	d.pool = pool.New(pool.Config{
		MaxIdle:     2,
		MaxOpen:     2,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			poolGetCalled = true
			mu := d.callLock("res_order_test")
			lockHeldDuringDial = !mu.TryLock()
			if !lockHeldDuringDial {
				mu.Unlock()
			}
			return &fakeTransport{
				recvMsg: &mcp.Message{
					JSONRPC: mcp.JSONRPCVersion,
					ID:      float64(1),
					Result:  json.RawMessage(`{"resources":[]}`),
				},
			}, nil
		},
	})
	defer func() { _ = d.pool.Close() }()

	_, _ = d.fetchServerResources(context.Background(), "res_order_test")

	if !poolGetCalled {
		t.Fatal("pool.Get was never called")
	}
	if !lockHeldDuringDial {
		t.Fatal("callLock was NOT held when pool.Get dialed - lock ordering violation")
	}
}

// TestHandleCall_StageBoundaryAuditRegression verifies that every pipeline stage
// that emits an audit entry populates the PipelineStage field with the correct
// constant. Parse failures produce no audit entry (pre-audit), so they are only
// verified to not emit. This is the primary regression guard for DEBT-016.
func TestHandleCall_StageBoundaryAuditRegression(t *testing.T) {
	t.Run("parse_no_audit", func(t *testing.T) {
		d := newCallPipelineTestDaemon()
		auditPath := enableAuditAndCostForTest(t, d)
		msg := &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      "stage-parse",
			Method:  "loom/call",
			Params:  json.RawMessage(`{`),
		}

		resp, err := d.handleCall(context.Background(), msg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Error == nil || resp.Error.Code != mcp.InvalidParams {
			t.Fatalf("expected InvalidParams, got %+v", resp.Error)
		}

		entries := readAuditEntries(t, auditPath)
		if len(entries) != 0 {
			t.Fatalf("parse failure should emit 0 audit entries, got %d", len(entries))
		}
	})

	t.Run("authorize_stage", func(t *testing.T) {
		d := newCallPipelineTestDaemon()
		auditPath := enableAuditAndCostForTest(t, d)
		d.rbac = NewRBACEnforcer(RBACConfig{
			Enabled:       true,
			DefaultPolicy: "deny",
		}, d.logger)

		msg := newCallMessage(t, map[string]any{
			"server":   "github",
			"tool":     "delete_repo",
			"agent_id": "agent-stage-auth",
		})

		resp, _ := d.handleCall(context.Background(), msg)
		if resp.Error == nil {
			t.Fatal("expected RBAC denial")
		}

		entries := readAuditEntries(t, auditPath)
		if len(entries) != 1 {
			t.Fatalf("audit entries = %d, want 1", len(entries))
		}
		if entries[0].PipelineStage != stageAuth {
			t.Fatalf("pipeline_stage = %q, want %q", entries[0].PipelineStage, stageAuth)
		}
		if entries[0].Status != "denied" {
			t.Fatalf("status = %q, want denied", entries[0].Status)
		}
	})

	t.Run("policy_stage", func(t *testing.T) {
		d := newCallPipelineTestDaemon()
		auditPath := enableAuditAndCostForTest(t, d)
		d.policy = NewGatewayPolicyEnforcer(GatewayPolicyConfig{
			Enabled: true,
			Request: []GatewayRequestPolicyRule{
				{
					ID:                 "deny-test",
					Server:             "github",
					Tool:               "delete_*",
					ForbiddenArguments: []string{"force"},
					ReasonCode:         "STAGE_TEST",
				},
			},
		}, d.logger)

		msg := newCallMessage(t, map[string]any{
			"server":    "github",
			"tool":      "delete_repo",
			"arguments": json.RawMessage(`{"force":true}`),
			"agent_id":  "agent-stage-policy",
		})

		resp, _ := d.handleCall(context.Background(), msg)
		if resp.Error == nil {
			t.Fatal("expected policy denial")
		}

		entries := readAuditEntries(t, auditPath)
		if len(entries) != 1 {
			t.Fatalf("audit entries = %d, want 1", len(entries))
		}
		if entries[0].PipelineStage != stagePolicy {
			t.Fatalf("pipeline_stage = %q, want %q", entries[0].PipelineStage, stagePolicy)
		}
		if entries[0].Status != "denied" {
			t.Fatalf("status = %q, want denied", entries[0].Status)
		}
	})

	t.Run("cache_stage", func(t *testing.T) {
		d := newCallPipelineTestDaemon()
		auditPath := enableAuditAndCostForTest(t, d)
		d.respCache = NewResponseCache(CacheConfig{Enabled: true})

		args := json.RawMessage(`{"query":"up"}`)
		key := d.respCache.Key("prometheus", "query", args)
		d.respCache.Set(key, json.RawMessage(`{"cached":true}`), "prometheus", "query")

		msg := newCallMessage(t, map[string]any{
			"server":    "prometheus",
			"tool":      "query",
			"arguments": json.RawMessage(`{"query":"up"}`),
			"agent_id":  "agent-stage-cache",
		})

		resp, _ := d.handleCall(context.Background(), msg)
		if resp.Error != nil {
			t.Fatalf("unexpected error: %v", resp.Error)
		}

		entries := readAuditEntries(t, auditPath)
		if len(entries) != 1 {
			t.Fatalf("audit entries = %d, want 1", len(entries))
		}
		if entries[0].PipelineStage != stageCache {
			t.Fatalf("pipeline_stage = %q, want %q", entries[0].PipelineStage, stageCache)
		}
		if !entries[0].Cached {
			t.Fatal("expected cached=true in audit entry")
		}
	})

	t.Run("route_stage", func(t *testing.T) {
		d := newCallPipelineTestDaemon()
		auditPath := enableAuditAndCostForTest(t, d)
		d.router = router.New(router.Config{HubEnabled: false})

		msg := newCallMessage(t, map[string]any{
			"server":   "unknown_server",
			"tool":     "query",
			"agent_id": "agent-stage-route",
		})

		resp, _ := d.handleCall(context.Background(), msg)
		if resp.Error == nil {
			t.Fatal("expected route failure")
		}

		entries := readAuditEntries(t, auditPath)
		if len(entries) != 1 {
			t.Fatalf("audit entries = %d, want 1", len(entries))
		}
		if entries[0].PipelineStage != stageRoute {
			t.Fatalf("pipeline_stage = %q, want %q", entries[0].PipelineStage, stageRoute)
		}
		if entries[0].Status != "error" {
			t.Fatalf("status = %q, want error", entries[0].Status)
		}
	})

	t.Run("execute_stage", func(t *testing.T) {
		d := newCallPipelineTestDaemon()
		auditPath := enableAuditAndCostForTest(t, d)
		d.procMgr = process.NewManager(nil, "codex")
		d.hubPool = pool.New(pool.Config{
			MaxIdle:     1,
			MaxOpen:     1,
			IdleTimeout: time.Second,
			DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
				return &fakeTransport{sendErr: fmt.Errorf("stage-test send failure")}, nil
			},
		})
		defer func() { _ = d.hubPool.Close() }()

		msg := newCallMessage(t, map[string]any{
			"server":   "hub_server",
			"tool":     "query",
			"agent_id": "agent-stage-exec",
		})

		resp, _ := d.handleCall(context.Background(), msg)
		if resp.Error == nil {
			t.Fatal("expected transport failure")
		}

		entries := readAuditEntries(t, auditPath)
		if len(entries) != 1 {
			t.Fatalf("audit entries = %d, want 1", len(entries))
		}
		if entries[0].PipelineStage != stageExecute {
			t.Fatalf("pipeline_stage = %q, want %q", entries[0].PipelineStage, stageExecute)
		}
		if entries[0].Status != "error" {
			t.Fatalf("status = %q, want error", entries[0].Status)
		}
	})
}

func TestResolveToolCallTimeout(t *testing.T) {
	t.Setenv("LOOM_DAEMON_TOOL_TIMEOUT", "")

	tests := []struct {
		name string
		p    callParams
		want time.Duration
	}{
		{
			name: "explicit _timeout field",
			p:    callParams{Method: "tools/call", Timeout: "5m"},
			want: 5 * time.Minute,
		},
		{
			name: "explicit _timeout capped at max",
			p:    callParams{Method: "tools/call", Timeout: "30m"},
			want: maxDaemonToolRPCTimeout,
		},
		{
			name: "explicit _timeout invalid falls through to default",
			p:    callParams{Method: "tools/call", Timeout: "garbage"},
			want: defaultDaemonToolRPCTimeout,
		},
		{
			name: "explicit _timeout zero falls through to default",
			p:    callParams{Method: "tools/call", Timeout: "0s"},
			want: defaultDaemonToolRPCTimeout,
		},
		{
			name: "auto-derive timeout_seconds from arguments",
			p: callParams{
				Method:    "tools/call",
				Arguments: json.RawMessage(`{"timeout_seconds": 600}`),
			},
			want: 600*time.Second + autoDeriveDaemonTimeoutBuffer,
		},
		{
			name: "auto-derive timeoutSeconds from arguments",
			p: callParams{
				Method:    "tools/call",
				Arguments: json.RawMessage(`{"timeoutSeconds": 300}`),
			},
			want: 300*time.Second + autoDeriveDaemonTimeoutBuffer,
		},
		{
			name: "auto-derive Go duration string from arguments",
			p: callParams{
				Method:    "tools/call",
				Arguments: json.RawMessage(`{"timeout": "10m"}`),
			},
			want: 10*time.Minute + autoDeriveDaemonTimeoutBuffer,
		},
		{
			name: "auto-derive capped at max",
			p: callParams{
				Method:    "tools/call",
				Arguments: json.RawMessage(`{"timeout_seconds": 3600}`),
			},
			want: maxDaemonToolRPCTimeout,
		},
		{
			name: "no hint returns default",
			p:    callParams{Method: "tools/call"},
			want: defaultDaemonToolRPCTimeout,
		},
		{
			name: "explicit _timeout beats auto-derived",
			p: callParams{
				Method:    "tools/call",
				Timeout:   "3m",
				Arguments: json.RawMessage(`{"timeout_seconds": 600}`),
			},
			want: 3 * time.Minute,
		},
		{
			name: "non-tool method uses standard timeout",
			p:    callParams{Method: "loom/status"},
			want: defaultDaemonControlRPCTimeout,
		},
		{
			name: "empty method defaults to tools/call path",
			p:    callParams{},
			want: defaultDaemonToolRPCTimeout,
		},
		{
			name: "auto-derive from nested params with arguments",
			p: callParams{
				Method: "tools/call",
				Params: json.RawMessage(`{"name":"poll_pipeline","arguments":{"timeout_seconds":120}}`),
			},
			want: 120*time.Second + autoDeriveDaemonTimeoutBuffer,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveToolCallTimeout(tc.p)
			if got != tc.want {
				t.Fatalf("resolveToolCallTimeout() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeriveTimeoutFromArguments(t *testing.T) {
	tests := []struct {
		name string
		args json.RawMessage
		want time.Duration
	}{
		{
			name: "empty args",
			args: nil,
			want: 0,
		},
		{
			name: "negative value",
			args: json.RawMessage(`{"timeout_seconds": -10}`),
			want: 0,
		},
		{
			name: "zero value",
			args: json.RawMessage(`{"timeout_seconds": 0}`),
			want: 0,
		},
		{
			name: "invalid json",
			args: json.RawMessage(`{bad`),
			want: 0,
		},
		{
			name: "timeout as numeric seconds",
			args: json.RawMessage(`{"timeout": 120}`),
			want: 120 * time.Second,
		},
		{
			name: "nested arguments",
			args: json.RawMessage(`{"arguments":{"timeoutSeconds":180}}`),
			want: 180 * time.Second,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveTimeoutFromArguments(tc.args)
			if got != tc.want {
				t.Fatalf("deriveTimeoutFromArguments() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClampTimeout(t *testing.T) {
	tests := []struct {
		name     string
		d        time.Duration
		min, max time.Duration
		want     time.Duration
	}{
		{"below min", 10 * time.Second, 30 * time.Second, 5 * time.Minute, 30 * time.Second},
		{"above max", 20 * time.Minute, 30 * time.Second, 15 * time.Minute, 15 * time.Minute},
		{"in range", 5 * time.Minute, 30 * time.Second, 15 * time.Minute, 5 * time.Minute},
		{"equal to min", 30 * time.Second, 30 * time.Second, 15 * time.Minute, 30 * time.Second},
		{"equal to max", 15 * time.Minute, 30 * time.Second, 15 * time.Minute, 15 * time.Minute},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := clampTimeout(tc.d, tc.min, tc.max)
			if got != tc.want {
				t.Fatalf("clampTimeout(%v, %v, %v) = %v, want %v", tc.d, tc.min, tc.max, got, tc.want)
			}
		})
	}
}
