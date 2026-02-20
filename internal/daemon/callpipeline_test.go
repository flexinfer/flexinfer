package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
						ID:      "ok",
						Result:  json.RawMessage(`{"ok":true}`),
					},
				}, nil
			}
			// Subsequent dials: fully healthy (simulating server restart).
			return &fakeTransport{
				recvMsg: &mcp.Message{
					JSONRPC: mcp.JSONRPCVersion,
					ID:      "recovered",
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
					ID:      "ok",
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
					ID:      "ok",
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
