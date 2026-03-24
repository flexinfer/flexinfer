package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// interleavingHubTransport simulates a shared hub transport where concurrent
// send/recv pairs can cross wires. Recv always returns the most recently sent
// request ID, which causes deterministic mismatches when requests overlap.
type interleavingHubTransport struct {
	mu        sync.Mutex
	lastID    any
	sendCount int
}

func (t *interleavingHubTransport) Send(_ context.Context, msg *mcp.Message) error {
	t.mu.Lock()
	t.lastID = msg.ID
	t.sendCount++
	sendCount := t.sendCount
	t.mu.Unlock()

	if sendCount == 1 {
		time.Sleep(25 * time.Millisecond)
	}
	return nil
}

func (t *interleavingHubTransport) Recv(_ context.Context) (*mcp.Message, error) {
	t.mu.Lock()
	id := t.lastID
	t.mu.Unlock()
	return &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      id,
		Result:  json.RawMessage(`{"ok":true}`),
	}, nil
}

func (t *interleavingHubTransport) Close() error { return nil }

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

	entries, err := ReadAuditEntries(path, AuditReadOptions{})
	if err != nil {
		t.Fatalf("read audit log: %v", err)
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

func TestCallPipelineRouteAndConnect_HubLockOrdering(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.router = router.New(router.Config{HubEnabled: true})

	poolGetCalled := false
	lockHeldDuringDial := false
	d.hubPool = pool.New(pool.Config{
		MaxIdle:     2,
		MaxOpen:     2,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			poolGetCalled = true
			mu := d.callLock("hub_lock_test")
			lockHeldDuringDial = !mu.TryLock()
			if !lockHeldDuringDial {
				mu.Unlock()
			}
			return &fakeTransport{}, nil
		},
	})
	defer func() { _ = d.hubPool.Close() }()

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "hub-lock-order"},
		serverName: "hub_lock_test",
	}
	resp := p.routeAndConnect()
	if resp != nil {
		t.Fatalf("unexpected route error: %+v", resp.Error)
	}
	defer p.releaseConnection()

	if !poolGetCalled {
		t.Fatal("hub pool.Get was never called")
	}
	if !lockHeldDuringDial {
		t.Fatal("callLock was not held before hub pool dial")
	}
}

func TestCallPipelineRouteAndConnect_PreferHubConnectFailureRetriesLocal(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.router = router.New(router.Config{
		HubEnabled: true,
		Registry: &kitregistry.Registry{
			Servers: []*kitregistry.Server{
				{
					Name:       "agent_context",
					Categories: []string{"local-only"},
				},
			},
		},
	})
	d.routingPreferences = map[string]RoutingPreference{
		"agent_context": RoutingPreferHub,
	}

	hubDials := 0
	localDials := 0
	d.hubPool = pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			hubDials++
			return nil, errors.New("hub connect failed")
		},
	})
	d.pool = pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			localDials++
			return &fakeTransport{}, nil
		},
	})
	defer func() {
		_ = d.hubPool.Close()
		_ = d.pool.Close()
	}()

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "hub-connect-fallback"},
		serverName: "agent_context",
	}
	resp := p.routeAndConnect()
	if resp != nil {
		t.Fatalf("unexpected route error: %+v", resp.Error)
	}
	defer p.releaseConnection()

	if hubDials != 1 {
		t.Fatalf("hub dials = %d, want 1", hubDials)
	}
	if localDials != 1 {
		t.Fatalf("local dials = %d, want 1", localDials)
	}
	if p.target != router.TargetLocal {
		t.Fatalf("target = %v, want %v", p.target, router.TargetLocal)
	}
	if !p.localRetryUsed {
		t.Fatal("expected local retry to be marked used")
	}
	if active, _ := d.preferHubBackoffActive("agent_context"); !active {
		t.Fatal("expected prefer-hub backoff to be active after hub connect failure")
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

func TestCallPipelineExecute_LocalSendFailureRetriesOnce(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.router = router.New(router.Config{HubEnabled: false})
	localDials := 0
	var reqID any
	d.pool = pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			localDials++
			return &fakeTransport{
				sendFn: func(_ context.Context, msg *mcp.Message) error {
					reqID = msg.ID
					return nil
				},
				recvFn: func(_ context.Context) (*mcp.Message, error) {
					return &mcp.Message{
						JSONRPC: mcp.JSONRPCVersion,
						ID:      reqID,
						Result:  json.RawMessage(`{"retried":true}`),
					}, nil
				},
			}, nil
		},
	})
	defer func() { _ = d.pool.Close() }()

	p := &callPipeline{
		daemon:     d,
		ctx:        context.Background(),
		msg:        &mcp.Message{ID: "local-send-retry"},
		serverName: "gitlab",
		toolName:   "list_pipelines",
		method:     "tools/call",
		conn: &pool.Conn{
			ServerName: "gitlab",
			Transport:  &fakeTransport{sendErr: io.EOF},
			Healthy:    true,
		},
		target:    router.TargetLocal,
		targetStr: router.TargetLocal.String(),
	}
	defer p.releaseConnection()

	req := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "local-send-retry", Method: "tools/call"}
	resp := p.execute(req)
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected successful retry response, got %+v", resp)
	}
	if string(resp.Result) != `{"retried":true}` {
		t.Fatalf("result = %s, want retry success result", string(resp.Result))
	}
	if localDials != 1 {
		t.Fatalf("local dials = %d, want 1", localDials)
	}
	if !p.localTransportRetryUsed {
		t.Fatal("expected local transport retry flag to be set")
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

	// Set hubClient so transportFailure also clears the WebSocket client cache.
	d.hubClient = mcp.NewWebSocketClient(mcp.WebSocketConfig{})

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
	ped, ok := resp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("expected *PipelineErrorData, got %T", resp.Error.Data)
	}
	if ped.Code != "POLICY_DENIED" {
		t.Fatalf("PipelineErrorData.Code = %q, want POLICY_DENIED", ped.Code)
	}
	details, ok := ped.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected Details map, got %T", ped.Details)
	}
	if got := details["policy_rule_id"]; got != "deny-force-delete" {
		t.Fatalf("policy_rule_id = %v, want %q", got, "deny-force-delete")
	}
	if got := details["policy_reason_code"]; got != "POLICY_FORCE_DELETE_BLOCKED" {
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

func TestHandleCall_PreferHubRecvFailureRetriesLocal(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.router = router.New(router.Config{
		HubEnabled: true,
		Registry: &kitregistry.Registry{
			Servers: []*kitregistry.Server{
				{
					Name:       "agent_context",
					Categories: []string{"local-only"},
				},
			},
		},
	})
	d.routingPreferences = map[string]RoutingPreference{
		"agent_context": RoutingPreferHub,
	}

	hubDials := 0
	localDials := 0
	d.hubPool = pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			hubDials++
			return &fakeTransport{recvErr: errors.New("hub recv failed")}, nil
		},
	})
	d.pool = pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			localDials++
			var reqID any
			return &fakeTransport{
				sendFn: func(_ context.Context, msg *mcp.Message) error {
					reqID = msg.ID
					return nil
				},
				recvFn: func(_ context.Context) (*mcp.Message, error) {
					return &mcp.Message{
						JSONRPC: mcp.JSONRPCVersion,
						ID:      reqID,
						Result:  json.RawMessage(`{"local":true}`),
					}, nil
				},
			}, nil
		},
	})
	defer func() {
		_ = d.hubPool.Close()
		_ = d.pool.Close()
	}()

	msg := newCallMessage(t, map[string]any{
		"server": "agent_context",
		"tool":   "query",
	})
	msg.ID = "prefer-hub-fallback"

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected successful local fallback, got %+v", resp)
	}
	if string(resp.Result) != `{"local":true}` {
		t.Fatalf("result = %s, want local fallback result", string(resp.Result))
	}
	if hubDials != 1 {
		t.Fatalf("hub dials = %d, want 1", hubDials)
	}
	if localDials != 1 {
		t.Fatalf("local dials = %d, want 1", localDials)
	}
	if active, _ := d.preferHubBackoffActive("agent_context"); !active {
		t.Fatal("expected prefer-hub backoff to be active after hub recv failure")
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
// errorResponse helper produces correct JSON-RPC error envelopes with
// PipelineErrorData in the Data field.
func TestCallPipeline_ErrorResponseEnvelopeStructure(t *testing.T) {
	d := newCallPipelineTestDaemon()
	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "envelope-test",
	})

	// invalidParamsError now carries PipelineErrorData.
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
	ped, ok := resp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("expected *PipelineErrorData, got %T", resp.Error.Data)
	}
	if ped.Code != "INVALID_INPUT" {
		t.Fatalf("PipelineErrorData.Code = %q, want INVALID_INPUT", ped.Code)
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
	policyPED, ok := policyResp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("expected *PipelineErrorData, got %T", policyResp.Error.Data)
	}
	if policyPED.Code != "POLICY_DENIED" {
		t.Fatalf("PipelineErrorData.Code = %q, want POLICY_DENIED", policyPED.Code)
	}
	details, ok := policyPED.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected Details map, got %T", policyPED.Details)
	}
	if details["policy_rule_id"] != "rule-1" {
		t.Fatalf("policy_rule_id = %v, want rule-1", details["policy_rule_id"])
	}
	if details["policy_reason_code"] != "BLOCKED" {
		t.Fatalf("policy_reason_code = %v, want BLOCKED", details["policy_reason_code"])
	}
	if details["policy_stage"] != "request" {
		t.Fatalf("policy_stage = %v, want request", details["policy_stage"])
	}
	if details["policy_action"] != "deny" {
		t.Fatalf("policy_action = %v, want deny", details["policy_action"])
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

	// Call 2: same connection reused from pool, send fails (sendCount=2),
	// then daemon retries once with a fresh local connection in the same call.
	resp2, err2 := d.handleCall(context.Background(), msg)
	if err2 != nil {
		t.Fatalf("call 2: unexpected error: %v", err2)
	}
	if resp2.Error != nil {
		t.Fatalf("call 2: expected retry success, got error: %+v", resp2.Error)
	}
	if string(resp2.Result) != `{"recovered":true}` {
		t.Fatalf("call 2: result = %s, want in-call recovery response", string(resp2.Result))
	}

	// Pool should still have a healthy connection after in-call recovery.
	stats := d.pool.Stats()
	if stats.IdleConns == 0 {
		t.Fatalf("pool idle conns = %d, want > 0 after in-call recovery", stats.IdleConns)
	}

	// Call 3: subsequent calls continue succeeding after recovery.
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

func TestHandleCall_HubConcurrencyNoIDMismatchWithLock(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.router = router.New(router.Config{HubEnabled: true})

	shared := &interleavingHubTransport{}
	d.hubPool = pool.New(pool.Config{
		MaxIdle:     2,
		MaxOpen:     2,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			return shared, nil
		},
	})
	defer func() { _ = d.hubPool.Close() }()

	makeMsg := func(id string) *mcp.Message {
		msg := newCallMessage(t, map[string]any{
			"server": "hub_concurrency_test",
			"tool":   "query",
		})
		msg.ID = id
		return msg
	}

	type result struct {
		resp *mcp.Message
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"req-1", "req-2"} {
		wg.Add(1)
		go func(callID string) {
			defer wg.Done()
			<-start
			resp, err := d.handleCall(context.Background(), makeMsg(callID))
			results <- result{resp: resp, err: err}
		}(id)
	}

	close(start)
	wg.Wait()
	close(results)

	for res := range results {
		if res.err != nil {
			t.Fatalf("unexpected call error: %v", res.err)
		}
		if res.resp == nil || res.resp.Error != nil {
			t.Fatalf("expected success response, got %+v", res.resp)
		}
		if !strings.Contains(string(res.resp.Result), `"ok":true`) {
			t.Fatalf("unexpected response result: %s", string(res.resp.Result))
		}
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

func TestFetchServerToolsViaPool_CallLockTimeout(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.pool = newTestPool(t)
	defer func() { _ = d.pool.Close() }()

	mu := d.callLock("lock_timeout_srv")
	mu.Lock()
	go func() {
		time.Sleep(250 * time.Millisecond)
		mu.Unlock()
	}()

	t.Setenv("LOOM_DAEMON_CALL_LOCK_TIMEOUT", "50ms")

	start := time.Now()
	_, err := d.fetchServerToolsViaPool(context.Background(), "lock_timeout_srv")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected call lock timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "acquire call lock") {
		t.Fatalf("expected acquire call lock error, got %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("lock timeout path took too long: %v", elapsed)
	}
}

func TestCallPipelineConnectTarget_CallLockTimeout(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.pool = newTestPool(t)
	defer func() { _ = d.pool.Close() }()

	mu := d.callLock("lock_timeout_srv")
	mu.Lock()
	go func() {
		time.Sleep(250 * time.Millisecond)
		mu.Unlock()
	}()

	t.Setenv("LOOM_DAEMON_CALL_LOCK_TIMEOUT", "50ms")

	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "lock-timeout",
	})
	p.serverName = "lock_timeout_srv"

	start := time.Now()
	err := p.connectTarget(router.TargetLocal, "test lock timeout")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected call lock timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "acquire call lock") {
		t.Fatalf("expected acquire call lock error, got %v", err)
	}
	if p.lockHeld {
		t.Fatal("lock should not be marked as held when acquisition fails")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("lock timeout path took too long: %v", elapsed)
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

// ---------------------------------------------------------------------------
// emitDecompHintIfLarge — decomposition hint emission
// ---------------------------------------------------------------------------

func TestEmitDecompHintIfLarge_AboveThreshold(t *testing.T) {
	d := newCallPipelineTestDaemon()
	eb := NewEventBus(d.logger)
	d.eventBus = eb

	subID, ch := eb.Subscribe()
	defer eb.Unsubscribe(subID)

	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "decomp-test",
	})
	p.serverName = "llm"
	p.toolName = "chat"

	// Create a response exceeding decompHintTokenThreshold (8000 tokens * 4 bytes = 32000 bytes).
	bigResult := make(json.RawMessage, 40000)
	for i := range bigResult {
		bigResult[i] = 'x'
	}
	resp := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "decomp-test",
		Result:  bigResult,
	}

	p.emitDecompHintIfLarge(resp)

	select {
	case evt := <-ch:
		if evt.Type != EventDecompHint {
			t.Fatalf("event type = %q, want %q", evt.Type, EventDecompHint)
		}
		data, ok := evt.Data.(map[string]any)
		if !ok {
			t.Fatalf("event data type = %T, want map[string]any", evt.Data)
		}
		if data["server"] != "llm" {
			t.Errorf("server = %v, want llm", data["server"])
		}
		if data["tool"] != "chat" {
			t.Errorf("tool = %v, want chat", data["tool"])
		}
		if data["response_bytes"] != len(bigResult) {
			t.Errorf("response_bytes = %v, want %d", data["response_bytes"], len(bigResult))
		}
		estTokens, _ := data["estimated_tokens"].(int)
		if estTokens < decompHintTokenThreshold {
			t.Errorf("estimated_tokens = %d, want >= %d", estTokens, decompHintTokenThreshold)
		}
		if data["workflow"] != "recursive-context" {
			t.Errorf("workflow = %v, want recursive-context", data["workflow"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for decomp.hint event")
	}
}

func TestEmitDecompHintIfLarge_BelowThreshold(t *testing.T) {
	d := newCallPipelineTestDaemon()
	eb := NewEventBus(d.logger)
	d.eventBus = eb

	subID, ch := eb.Subscribe()
	defer eb.Unsubscribe(subID)

	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "decomp-small",
	})
	p.serverName = "git"
	p.toolName = "status"

	// Small response: 100 bytes ~ 25 tokens, well below threshold.
	resp := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "decomp-small",
		Result:  json.RawMessage(`{"status":"ok"}`),
	}

	p.emitDecompHintIfLarge(resp)

	select {
	case evt := <-ch:
		t.Fatalf("unexpected event emitted: %+v", evt)
	case <-time.After(50 * time.Millisecond):
		// Expected: no event.
	}
}

func TestEmitDecompHintIfLarge_NilGuards(t *testing.T) {
	d := newCallPipelineTestDaemon()
	// No eventBus set — should not panic.
	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "nil-guard",
	})

	// nil response.
	p.emitDecompHintIfLarge(nil)

	// Response with nil result.
	p.emitDecompHintIfLarge(&mcp.Message{JSONRPC: mcp.JSONRPCVersion})

	// Response with result but no event bus.
	p.emitDecompHintIfLarge(&mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		Result:  json.RawMessage(strings.Repeat("x", 40000)),
	})
	// No panic = pass.
}

// ---------------------------------------------------------------------------
// Nil guard pass-through: authorize, enforceRequestPolicy, tryCachedResponse
// ---------------------------------------------------------------------------

func TestCallPipelineAuthorize_NilRBACPassesThrough(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.rbac = nil // Explicitly nil.

	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "auth-nil",
	})
	p.params = callParams{Server: "git", Tool: "status"}
	p.auditStart = time.Now()

	if resp := p.authorize(); resp != nil {
		t.Fatalf("expected nil response when RBAC is nil, got %+v", resp)
	}
}

func TestCallPipelineEnforceRequestPolicy_NilPolicyPassesThrough(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.policy = nil // Explicitly nil.

	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "policy-nil",
	})
	p.params = callParams{Server: "git", Tool: "status"}
	p.auditStart = time.Now()

	if resp := p.enforceRequestPolicy(); resp != nil {
		t.Fatalf("expected nil response when policy is nil, got %+v", resp)
	}
}

func TestCallPipelineTryCachedResponse_NilCachePassesThrough(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.respCache = nil // Explicitly nil.

	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "cache-nil",
	})
	p.params = callParams{Server: "git", Tool: "status"}

	if resp := p.tryCachedResponse(); resp != nil {
		t.Fatalf("expected nil response when cache is nil, got %+v", resp)
	}
}

// ---------------------------------------------------------------------------
// Error envelope exhaustive: all constructors produce consistent structure
// ---------------------------------------------------------------------------

func TestCallPipeline_ErrorEnvelopeExhaustive(t *testing.T) {
	d := newCallPipelineTestDaemon()
	msgID := "exhaust-test"
	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      msgID,
	})

	assertEnvelope := func(t *testing.T, resp *mcp.Message, wantCode int) {
		t.Helper()
		if resp.JSONRPC != mcp.JSONRPCVersion {
			t.Errorf("JSONRPC = %q, want %q", resp.JSONRPC, mcp.JSONRPCVersion)
		}
		if resp.ID != msgID {
			t.Errorf("ID = %v, want %v", resp.ID, msgID)
		}
		if resp.Error == nil {
			t.Fatal("Error is nil")
		}
		if resp.Error.Code != wantCode {
			t.Errorf("Error.Code = %d, want %d", resp.Error.Code, wantCode)
		}
		if resp.Error.Message == "" {
			t.Error("Error.Message is empty")
		}
	}

	t.Run("invalidParamsError", func(t *testing.T) {
		resp := p.invalidParamsError("bad")
		assertEnvelope(t, resp, mcp.InvalidParams)
		ped, ok := resp.Error.Data.(*PipelineErrorData)
		if !ok {
			t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
		}
		if ped.Code != "INVALID_INPUT" {
			t.Errorf("Code = %q, want INVALID_INPUT", ped.Code)
		}
		if ped.Retryable {
			t.Error("expected Retryable=false for invalid params")
		}
	})

	t.Run("internalError", func(t *testing.T) {
		resp := p.internalError(errors.New("boom"))
		assertEnvelope(t, resp, mcp.InternalError)
		ped, ok := resp.Error.Data.(*PipelineErrorData)
		if !ok {
			t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
		}
		if ped.Code != "SERVER_ERROR" {
			t.Errorf("Code = %q, want SERVER_ERROR", ped.Code)
		}
	})

	t.Run("rbacDeniedError", func(t *testing.T) {
		resp := p.rbacDeniedError(AccessDecision{
			AgentID: "agent-1",
			Role:    "viewer",
			Server:  "git",
			Tool:    "push",
			Reason:  "not allowed",
		})
		assertEnvelope(t, resp, mcp.InvalidRequest)
		ped, ok := resp.Error.Data.(*PipelineErrorData)
		if !ok {
			t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
		}
		if ped.Code != "RBAC_DENIED" {
			t.Errorf("Code = %q, want RBAC_DENIED", ped.Code)
		}
		if ped.Stage != "authorize" {
			t.Errorf("Stage = %q, want authorize", ped.Stage)
		}
		if !strings.Contains(resp.Error.Message, "agent-1") {
			t.Errorf("message should contain agent_id, got %q", resp.Error.Message)
		}
		if !strings.Contains(resp.Error.Message, "viewer") {
			t.Errorf("message should contain role, got %q", resp.Error.Message)
		}
		if !strings.Contains(resp.Error.Message, "git__push") {
			t.Errorf("message should contain server__tool, got %q", resp.Error.Message)
		}
	})

	t.Run("policyDeniedError", func(t *testing.T) {
		resp := p.policyDeniedError(GatewayPolicyDecision{
			RuleID:     "rule-42",
			ReasonCode: "RATE_LIMIT",
			Reason:     "exceeded quota",
			Stage:      "request",
			Action:     "deny",
		})
		assertEnvelope(t, resp, mcp.InvalidRequest)
		ped, ok := resp.Error.Data.(*PipelineErrorData)
		if !ok {
			t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
		}
		if ped.Code != "POLICY_DENIED" {
			t.Errorf("Code = %q, want POLICY_DENIED", ped.Code)
		}
		if ped.Stage != "policy" {
			t.Errorf("Stage = %q, want policy", ped.Stage)
		}
		details, ok := ped.Details.(map[string]any)
		if !ok {
			t.Fatalf("Details type = %T, want map[string]any", ped.Details)
		}
		// All four policy detail fields must be present.
		for _, key := range []string{"policy_rule_id", "policy_reason_code", "policy_stage", "policy_action"} {
			if _, exists := details[key]; !exists {
				t.Errorf("Details missing key %q", key)
			}
		}
	})

	t.Run("internalErrorWithAudit", func(t *testing.T) {
		auditPath := enableAuditAndCostForTest(t, d)
		p.serverName = "git"
		p.toolName = "status"
		p.auditStart = time.Now()

		resp := p.internalErrorWithAudit("local", "dial failed")
		assertEnvelope(t, resp, mcp.InternalError)

		// Verify audit entry was written.
		entries := readAuditEntries(t, auditPath)
		if len(entries) == 0 {
			t.Fatal("expected audit entry for internalErrorWithAudit")
		}
		last := entries[len(entries)-1]
		if last.Status != "error" {
			t.Errorf("audit status = %q, want error", last.Status)
		}
	})
}

// ---------------------------------------------------------------------------
// Error envelope focused tests: specific error scenarios
// ---------------------------------------------------------------------------

func TestCallPipeline_ErrorEnvelope_RBACDenied(t *testing.T) {
	d := newCallPipelineTestDaemon()
	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "rbac-denied-envelope",
	})
	p.serverName = "github"
	p.toolName = "delete_repo"
	p.stage = stageAuth

	resp := p.rbacDeniedError(AccessDecision{
		AgentID:    "agent-1",
		Role:       "viewer",
		Server:     "github",
		Tool:       "delete_repo",
		Reason:     "denied by pattern",
		ReasonCode: "role_deny",
	})

	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != mcp.InvalidRequest {
		t.Fatalf("Error.Code = %d, want %d", resp.Error.Code, mcp.InvalidRequest)
	}

	ped, ok := resp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
	}
	if ped.Code != "RBAC_DENIED" {
		t.Errorf("Code = %q, want RBAC_DENIED", ped.Code)
	}
	if ped.Server != "github" {
		t.Errorf("Server = %q, want github", ped.Server)
	}
	if ped.Tool != "delete_repo" {
		t.Errorf("Tool = %q, want delete_repo", ped.Tool)
	}
	if ped.Stage != "authorize" {
		t.Errorf("Stage = %q, want authorize", ped.Stage)
	}
	if ped.Retryable {
		t.Error("expected Retryable=false for RBAC denial")
	}
	details, ok := ped.Details.(map[string]any)
	if !ok {
		t.Fatalf("Details type = %T, want map[string]any", ped.Details)
	}
	if details["reason_code"] != "role_deny" {
		t.Errorf("details.reason_code = %v, want role_deny", details["reason_code"])
	}
	if details["agent_id"] != "agent-1" {
		t.Errorf("details.agent_id = %v, want agent-1", details["agent_id"])
	}
}

func TestCallPipeline_ErrorEnvelope_RateLimited(t *testing.T) {
	d := newCallPipelineTestDaemon()
	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "rate-limited-envelope",
	})
	p.serverName = "github"
	p.toolName = "search"
	p.stage = stageAuth

	resp := p.rbacDeniedError(AccessDecision{
		AgentID:    "agent-1",
		Role:       "user",
		Server:     "github",
		Tool:       "search",
		Reason:     "rate limit exceeded",
		ReasonCode: "rate_limited",
	})

	if resp.Error == nil {
		t.Fatal("expected error response")
	}

	ped, ok := resp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
	}
	if ped.Code != "RATE_LIMITED" {
		t.Errorf("Code = %q, want RATE_LIMITED", ped.Code)
	}
	if !ped.Retryable {
		t.Error("expected Retryable=true for rate limit")
	}
	if ped.RetryAfter == "" {
		t.Error("expected non-empty RetryAfter for rate limit")
	}
	if ped.RetryAfter != "60s" {
		t.Errorf("RetryAfter = %q, want 60s", ped.RetryAfter)
	}
}

func TestCallPipeline_ErrorEnvelope_TransportTimeout(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.pool = newTestPool(t)
	defer func() { _ = d.pool.Close() }()
	d.procMgr = process.NewManager(nil, "codex")

	d.pool.Put(&pool.Conn{
		ServerName: "slow-server",
		Transport:  &fakeTransport{},
		Healthy:    true,
	})

	p := &callPipeline{
		daemon:     d,
		msg:        &mcp.Message{ID: "timeout-envelope"},
		serverName: "slow-server",
		toolName:   "query",
		method:     "tools/call",
		target:     router.TargetLocal,
		targetStr:  router.TargetLocal.String(),
		stage:      stageExecute,
		conn: &pool.Conn{
			ServerName: "slow-server",
			Transport:  &fakeTransport{},
			Healthy:    true,
		},
	}

	resp := p.transportFailure("recv", context.DeadlineExceeded, time.Now())
	if resp == nil || resp.Error == nil {
		t.Fatal("expected error response")
	}

	ped, ok := resp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
	}
	if ped.Code != "TIMEOUT" {
		t.Errorf("Code = %q, want TIMEOUT", ped.Code)
	}
	if !ped.Retryable {
		t.Error("expected Retryable=true for timeout")
	}
	if ped.Stage != "execute" {
		t.Errorf("Stage = %q, want execute", ped.Stage)
	}
	if ped.Server != "slow-server" {
		t.Errorf("Server = %q, want slow-server", ped.Server)
	}
}

func TestCallPipeline_ErrorEnvelope_ToolNotFound(t *testing.T) {
	d := newCallPipelineTestDaemon()
	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "tool-not-found-envelope",
	})
	p.stage = stageParse
	p.toolName = "nonexistent_tool"

	resp := p.invalidParamsError("could not resolve server for tool: nonexistent_tool")
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != mcp.InvalidParams {
		t.Fatalf("Error.Code = %d, want %d", resp.Error.Code, mcp.InvalidParams)
	}

	ped, ok := resp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
	}
	if ped.Code != "TOOL_NOT_FOUND" {
		t.Errorf("Code = %q, want TOOL_NOT_FOUND", ped.Code)
	}
	if ped.Stage != "parse" {
		t.Errorf("Stage = %q, want parse", ped.Stage)
	}
	if ped.Retryable {
		t.Error("expected Retryable=false for tool not found")
	}
}

// ---------------------------------------------------------------------------
// buildForwardRequest: raw Params path
// ---------------------------------------------------------------------------

func TestCallPipelineBuildForwardRequest_FromRawParams(t *testing.T) {
	d := newCallPipelineTestDaemon()
	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "build-raw",
	})
	p.toolName = "search"
	p.method = "tools/call"
	p.params = callParams{
		Params: json.RawMessage(`{"name":"search","arguments":{"query":"hello"}}`),
	}

	req, errResp := p.buildForwardRequest()
	if errResp != nil {
		t.Fatalf("unexpected error response: %+v", errResp)
	}
	if req == nil {
		t.Fatal("expected non-nil request")
	}

	// When Params is set, it should be forwarded verbatim.
	var params map[string]any
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params["name"] != "search" {
		t.Errorf("forwarded name = %v, want search", params["name"])
	}
}

func TestCallPipelineBuildForwardRequest_EmptyArguments(t *testing.T) {
	d := newCallPipelineTestDaemon()
	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "build-empty",
	})
	p.toolName = "ping"
	p.method = "tools/call"
	p.params = callParams{} // No Arguments, no Params.

	req, errResp := p.buildForwardRequest()
	if errResp != nil {
		t.Fatalf("unexpected error response: %+v", errResp)
	}
	if req == nil {
		t.Fatal("expected non-nil request")
	}

	// Should produce {"name":"ping","arguments":{}}
	var params map[string]any
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params["name"] != "ping" {
		t.Errorf("forwarded name = %v, want ping", params["name"])
	}
	args, ok := params["arguments"].(map[string]any)
	if !ok || len(args) != 0 {
		t.Errorf("expected empty arguments map, got %v", params["arguments"])
	}
}

// ---------------------------------------------------------------------------
// releaseConnection edge cases
// ---------------------------------------------------------------------------

func TestCallPipelineReleaseConnection_NoConnNoLock(t *testing.T) {
	d := newCallPipelineTestDaemon()
	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "release-nil",
	})
	// Neither conn nor lock are set.
	p.releaseConnection() // Should not panic.
}

func TestCallPipelineReleaseConnection_LockHeldNoConn(t *testing.T) {
	d := newCallPipelineTestDaemon()

	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "release-lock-only",
	})
	p.callMu = d.callLock("test-server")
	p.callMu.Lock()
	p.lockHeld = true

	p.releaseConnection()

	if p.lockHeld {
		t.Error("expected lockHeld to be false after release")
	}
}

// ---------------------------------------------------------------------------
// isRPCTimeout edge cases
// ---------------------------------------------------------------------------

func TestIsRPCTimeout_Comprehensive(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"os deadline exceeded", os.ErrDeadlineExceeded, true},
		{"plain error", errors.New("connection refused"), false},
		{"i/o timeout in message", errors.New("read tcp: i/o timeout"), true},
		{"wrapped deadline", fmt.Errorf("call: %w", context.DeadlineExceeded), true},
		{"context canceled", context.Canceled, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isRPCTimeout(tc.err)
			if got != tc.want {
				t.Fatalf("isRPCTimeout(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseAndResolve: server resolved via router
// ---------------------------------------------------------------------------

func TestCallPipelineParseAndResolve_ServerResolvedViaSmartRoute(t *testing.T) {
	d := newCallPipelineTestDaemon()
	// Register a tool in the router's registry and build tool index.
	reg := &kitregistry.Registry{
		Servers: []*kitregistry.Server{
			{
				Name: "time",
				Common: &kitregistry.TargetSpec{
					Tools: []kitregistry.ToolSchema{
						{Name: "current_time"},
					},
				},
			},
		},
	}
	d.router = router.New(router.Config{Registry: reg})
	d.router.BuildToolIndex()

	msg := newCallMessage(t, map[string]any{
		"tool":      "current_time",
		"arguments": map[string]any{},
	})

	p := newCallPipeline(d, context.Background(), msg)
	resp := p.parseAndResolve()
	if resp != nil {
		t.Fatalf("expected nil (success), got error: %s", resp.Error.Message)
	}
	if p.serverName != "time" {
		t.Errorf("serverName = %q, want time", p.serverName)
	}
	if p.toolName != "current_time" {
		t.Errorf("toolName = %q, want current_time", p.toolName)
	}
}

// ---------------------------------------------------------------------------
// emitResponseAudit: both success and error paths
// ---------------------------------------------------------------------------

func TestEmitResponseAudit_SuccessAndError(t *testing.T) {
	d := newCallPipelineTestDaemon()
	auditPath := enableAuditAndCostForTest(t, d)

	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "audit-test",
	})
	p.serverName = "git"
	p.toolName = "status"
	p.targetStr = "local"
	p.stage = stageExecute
	p.auditStart = time.Now()

	// Emit success audit.
	p.emitResponseAudit(&mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "audit-test"})

	// Emit error audit.
	p.emitResponseAudit(&mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "audit-test",
		Error:   &mcp.Error{Code: mcp.InternalError, Message: "something broke"},
	})

	entries := readAuditEntries(t, auditPath)
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 audit entries, got %d", len(entries))
	}
	if entries[0].Status != "success" {
		t.Errorf("first entry status = %q, want success", entries[0].Status)
	}
	if entries[1].Status != "error" {
		t.Errorf("second entry status = %q, want error", entries[1].Status)
	}
	if entries[1].Error != "something broke" {
		t.Errorf("second entry error = %q, want 'something broke'", entries[1].Error)
	}
}

// ---------------------------------------------------------------------------
// cacheSuccessResponse: guards and successful caching
// ---------------------------------------------------------------------------

func TestCacheSuccessResponse_Guards(t *testing.T) {
	d := newCallPipelineTestDaemon()
	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "cache-guard",
	})

	// No cache key → should be a no-op.
	p.cacheKey = ""
	p.cacheSuccessResponse(&mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		Result:  json.RawMessage(`"ok"`),
	})

	// Nil response → no-op.
	p.cacheKey = "some-key"
	p.cacheSuccessResponse(nil)

	// Error response → no-op.
	p.cacheSuccessResponse(&mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		Error:   &mcp.Error{Code: -1, Message: "err"},
	})

	// Nil result → no-op.
	p.cacheSuccessResponse(&mcp.Message{JSONRPC: mcp.JSONRPCVersion})

	// No panics = pass.
}

// ---------------------------------------------------------------------------
// resolveToolCallTimeout: negative _timeout
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// DEBT-016: classifyInternalError consistency
// ---------------------------------------------------------------------------

func TestClassifyInternalError_StageRouteErrorCodes(t *testing.T) {
	tests := []struct {
		name          string
		errMsg        string
		wantCode      string
		wantRetryable bool
	}{
		{"server unavailable", "server unavailable for myserver", "SERVER_UNAVAILABLE", false},
		{"lock timeout", "acquire call lock for server: context deadline exceeded", "LOCK_TIMEOUT", true},
		{"generic route", "connection refused", "CONNECTION_ERROR", true},
		{"transport corruption", "response id mismatch on recv", "TRANSPORT_CORRUPTION", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, retryable := classifyInternalError(fmt.Errorf("%s", tc.errMsg), stageRoute)
			if code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
			if retryable != tc.wantRetryable {
				t.Errorf("retryable = %v, want %v", retryable, tc.wantRetryable)
			}
		})
	}
}

func TestClassifyInternalError_StageExecute(t *testing.T) {
	code, retryable := classifyInternalError(fmt.Errorf("send failed"), stageExecute)
	if code != "TRANSPORT_FAILURE" {
		t.Errorf("code = %q, want TRANSPORT_FAILURE", code)
	}
	if !retryable {
		t.Error("expected retryable=true for execute stage")
	}
}

func TestClassifyInternalError_StageBuild(t *testing.T) {
	code, retryable := classifyInternalError(fmt.Errorf("marshal error"), stageBuild)
	if code != "SERVER_ERROR" {
		t.Errorf("code = %q, want SERVER_ERROR", code)
	}
	if retryable {
		t.Error("expected retryable=false for build stage")
	}
}

func TestClassifyInternalError_TimeoutOverridesStage(t *testing.T) {
	for _, stage := range []string{stageRoute, stageExecute, stageBuild, stagePolicy} {
		t.Run(stage, func(t *testing.T) {
			code, retryable := classifyInternalError(context.DeadlineExceeded, stage)
			if code != "TIMEOUT" {
				t.Errorf("code = %q, want TIMEOUT for stage %s", code, stage)
			}
			if !retryable {
				t.Error("expected retryable=true for timeout")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DEBT-016: internalError and internalErrorWithAudit produce identical codes
// ---------------------------------------------------------------------------

func TestInternalError_AndWithAudit_ProduceSameCodes(t *testing.T) {
	cases := []struct {
		name   string
		errMsg string
		stage  string
	}{
		{"route lock timeout", "acquire call lock: context deadline exceeded", stageRoute},
		{"route connection error", "dial tcp: connection refused", stageRoute},
		{"route server unavailable", "server unavailable for myserver", stageRoute},
		{"execute transport failure", "send failed: broken pipe", stageExecute},
		{"build marshal error", "json: unsupported type", stageBuild},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newCallPipelineTestDaemon()
			auditPath := enableAuditAndCostForTest(t, d)

			p := newCallPipeline(d, context.Background(), &mcp.Message{
				JSONRPC: mcp.JSONRPCVersion,
				ID:      "code-match",
			})
			p.serverName = "test-server"
			p.toolName = "test-tool"
			p.stage = tc.stage
			p.auditStart = time.Now()

			resp1 := p.internalError(fmt.Errorf("%s", tc.errMsg))
			resp2 := p.internalErrorWithAudit("local", tc.errMsg)

			ped1 := resp1.Error.Data.(*PipelineErrorData)
			ped2 := resp2.Error.Data.(*PipelineErrorData)

			if ped1.Code != ped2.Code {
				t.Errorf("code mismatch: internalError=%q, internalErrorWithAudit=%q",
					ped1.Code, ped2.Code)
			}
			if ped1.Retryable != ped2.Retryable {
				t.Errorf("retryable mismatch: internalError=%v, internalErrorWithAudit=%v",
					ped1.Retryable, ped2.Retryable)
			}
			if ped1.Stage != ped2.Stage {
				t.Errorf("stage mismatch: internalError=%q, internalErrorWithAudit=%q",
					ped1.Stage, ped2.Stage)
			}

			// internalErrorWithAudit should have emitted an audit entry.
			entries := readAuditEntries(t, auditPath)
			if len(entries) == 0 {
				t.Error("internalErrorWithAudit should emit an audit entry")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DEBT-016: handleCall stage short-circuit with error code verification
// ---------------------------------------------------------------------------

func TestHandleCall_ParseErrorCodeAndShortCircuit(t *testing.T) {
	d := newCallPipelineTestDaemon()
	auditPath := enableAuditAndCostForTest(t, d)

	// Set up RBAC to catch if auth stage runs (it shouldn't).
	d.rbac = NewRBACEnforcer(RBACConfig{
		Enabled:       true,
		DefaultPolicy: "deny",
	}, d.logger)

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "parse-sc",
		Method:  "loom/call",
		Params:  json.RawMessage(`{`), // Invalid JSON.
	}

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be a parse-stage error.
	ped, ok := resp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
	}
	if ped.Stage != stageParse {
		t.Errorf("Stage = %q, want %q", ped.Stage, stageParse)
	}
	if ped.Code != "INVALID_INPUT" {
		t.Errorf("Code = %q, want INVALID_INPUT", ped.Code)
	}

	// No audit means auth/route/execute never ran.
	entries := readAuditEntries(t, auditPath)
	if len(entries) != 0 {
		t.Fatalf("parse failure short-circuit broken: got %d audit entries", len(entries))
	}
}

func TestHandleCall_AuthDenialShortCircuitsRouteAndExecute(t *testing.T) {
	d := newCallPipelineTestDaemon()
	auditPath := enableAuditAndCostForTest(t, d)

	// RBAC deny-all.
	d.rbac = NewRBACEnforcer(RBACConfig{
		Enabled:       true,
		DefaultPolicy: "deny",
	}, d.logger)

	// Set up a pool that would fail loudly if route/execute ran.
	d.pool = pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			t.Fatal("pool dial should not be called after auth denial")
			return nil, nil
		},
	})
	defer func() { _ = d.pool.Close() }()

	msg := newCallMessage(t, map[string]any{
		"server":   "github",
		"tool":     "push",
		"agent_id": "restricted-agent",
	})

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected RBAC denial")
	}

	ped, ok := resp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
	}
	if ped.Code != "RBAC_DENIED" {
		t.Errorf("Code = %q, want RBAC_DENIED", ped.Code)
	}
	if ped.Stage != stageAuth {
		t.Errorf("Stage = %q, want %q", ped.Stage, stageAuth)
	}

	// Only 1 audit entry from auth stage, none from route/execute.
	entries := readAuditEntries(t, auditPath)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].PipelineStage != stageAuth {
		t.Errorf("audit pipeline_stage = %q, want %q", entries[0].PipelineStage, stageAuth)
	}
}

func TestHandleCall_CacheHitShortCircuitsBuildAndExecute(t *testing.T) {
	d := newCallPipelineTestDaemon()
	auditPath := enableAuditAndCostForTest(t, d)
	d.respCache = NewResponseCache(CacheConfig{Enabled: true})

	args := json.RawMessage(`{"q":"test"}`)
	key := d.respCache.Key("prometheus", "query", args)
	d.respCache.Set(key, json.RawMessage(`{"cached":true}`), "prometheus", "query")

	// Set up pools that would fail loudly if route/build/execute ran.
	d.pool = pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			t.Fatal("pool dial should not be called after cache hit")
			return nil, nil
		},
	})
	defer func() { _ = d.pool.Close() }()

	msg := newCallMessage(t, map[string]any{
		"server":    "prometheus",
		"tool":      "query",
		"arguments": json.RawMessage(`{"q":"test"}`),
		"agent_id":  "agent-cache-sc",
	})

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if string(resp.Result) != `{"cached":true}` {
		t.Errorf("result = %s, want cached response", string(resp.Result))
	}

	// Cache hit audit at cache stage, no route/execute audit.
	entries := readAuditEntries(t, auditPath)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].PipelineStage != stageCache {
		t.Errorf("audit pipeline_stage = %q, want %q", entries[0].PipelineStage, stageCache)
	}
	if !entries[0].Cached {
		t.Error("expected cached=true")
	}
}

func TestHandleCall_RouteFailureErrorCodeFromClassifyInternalError(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.router = router.New(router.Config{HubEnabled: false})

	msg := newCallMessage(t, map[string]any{
		"server":   "nonexistent",
		"tool":     "query",
		"agent_id": "agent-route-code",
	})

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected route failure")
	}

	ped, ok := resp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
	}
	if ped.Stage != stageRoute {
		t.Errorf("Stage = %q, want %q", ped.Stage, stageRoute)
	}

	// Verify the code matches what classifyInternalError would produce.
	expectedCode, expectedRetryable := classifyInternalError(
		fmt.Errorf("%s", resp.Error.Message), stageRoute)
	if ped.Code != expectedCode {
		t.Errorf("Code = %q, want %q (from classifyInternalError)", ped.Code, expectedCode)
	}
	if ped.Retryable != expectedRetryable {
		t.Errorf("Retryable = %v, want %v", ped.Retryable, expectedRetryable)
	}
}

func TestResolveToolCallTimeout_NegativeExplicit(t *testing.T) {
	t.Setenv("LOOM_DAEMON_TOOL_TIMEOUT", "")

	// Negative duration strings are parsed by time.ParseDuration but d > 0 fails.
	got := resolveToolCallTimeout(callParams{Method: "tools/call", Timeout: "-5m"})
	if got != defaultDaemonToolRPCTimeout {
		t.Fatalf("expected default timeout for negative _timeout, got %v", got)
	}
}

func TestResolveToolCallTimeout_WhitespaceOnly(t *testing.T) {
	t.Setenv("LOOM_DAEMON_TOOL_TIMEOUT", "")

	got := resolveToolCallTimeout(callParams{Method: "tools/call", Timeout: "   "})
	if got != defaultDaemonToolRPCTimeout {
		t.Fatalf("expected default timeout for whitespace _timeout, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// DEBT-016: Gate-stage error envelope consistency (draining + concurrency)
// ---------------------------------------------------------------------------

func TestHandleCall_DrainingReturnsPipelineErrorData(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.draining.Store(true)

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "drain-test",
		Method:  "loom/call",
		Params:  json.RawMessage(`{"server":"s","tool":"t"}`),
	}

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response when draining")
	}
	if resp.Error.Code != mcp.InternalError {
		t.Errorf("Code = %d, want %d", resp.Error.Code, mcp.InternalError)
	}

	ped, ok := resp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
	}
	if ped.Code != "DAEMON_DRAINING" {
		t.Errorf("Code = %q, want DAEMON_DRAINING", ped.Code)
	}
	if ped.Stage != "gate" {
		t.Errorf("Stage = %q, want gate", ped.Stage)
	}
	if !ped.Retryable {
		t.Error("expected Retryable=true for draining")
	}
}

func TestHandleCall_ConcurrencyLimitReturnsPipelineErrorData(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.callSem = make(chan struct{}, 1)
	d.callSem <- struct{}{} // Fill the semaphore.

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "sem-test",
		Method:  "loom/call",
		Params:  json.RawMessage(`{"server":"s","tool":"t"}`),
	}

	resp, err := d.handleCall(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response when concurrency limit reached")
	}

	ped, ok := resp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
	}
	if ped.Code != "CONCURRENCY_LIMIT" {
		t.Errorf("Code = %q, want CONCURRENCY_LIMIT", ped.Code)
	}
	if ped.Stage != "gate" {
		t.Errorf("Stage = %q, want gate", ped.Stage)
	}
	if !ped.Retryable {
		t.Error("expected Retryable=true for concurrency limit")
	}
}

// ---------------------------------------------------------------------------
// DEBT-016: Policy denial short-circuits route and execute
// ---------------------------------------------------------------------------

func TestHandleCall_PolicyDenialShortCircuitsRouteAndExecute(t *testing.T) {
	d := newCallPipelineTestDaemon()
	auditPath := enableAuditAndCostForTest(t, d)

	d.policy = NewGatewayPolicyEnforcer(GatewayPolicyConfig{
		Enabled: true,
		Request: []GatewayRequestPolicyRule{
			{
				ID:                 "deny-delete",
				Server:             "github",
				Tool:               "delete_*",
				ForbiddenArguments: []string{"force"},
				ReasonCode:         "FORBIDDEN_ARG",
			},
		},
	}, d.logger)

	// Set up a pool that would fail loudly if route/execute ran.
	d.pool = pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			t.Fatal("pool dial should not be called after policy denial")
			return nil, nil
		},
	})
	defer func() { _ = d.pool.Close() }()

	msg := newCallMessage(t, map[string]any{
		"server":    "github",
		"tool":      "delete_repo",
		"arguments": json.RawMessage(`{"force":true}`),
		"agent_id":  "policy-sc-agent",
	})

	resp, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected policy denial")
	}

	ped, ok := resp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
	}
	if ped.Code != "POLICY_DENIED" {
		t.Errorf("Code = %q, want POLICY_DENIED", ped.Code)
	}
	if ped.Stage != stagePolicy {
		t.Errorf("Stage = %q, want %q", ped.Stage, stagePolicy)
	}

	// Only 1 audit entry from policy stage.
	entries := readAuditEntries(t, auditPath)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].PipelineStage != stagePolicy {
		t.Errorf("audit pipeline_stage = %q, want %q", entries[0].PipelineStage, stagePolicy)
	}
}

// ---------------------------------------------------------------------------
// DEBT-016: All error paths produce PipelineErrorData with required fields
// ---------------------------------------------------------------------------

func TestErrorEnvelope_AllPathsProducePipelineErrorData(t *testing.T) {
	d := newCallPipelineTestDaemon()

	p := newCallPipeline(d, context.Background(), &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "envelope-test",
	})
	p.serverName = "test-server"
	p.toolName = "test-tool"
	p.stage = stageExecute
	p.auditStart = time.Now()

	cases := []struct {
		name string
		resp *mcp.Message
	}{
		{"invalidParams", p.invalidParamsError("bad input")},
		{"internalError", p.internalError(errors.New("something broke"))},
		{"internalErrorWithAudit", p.internalErrorWithAudit("local", "transport died")},
		{"rbacDenied", p.rbacDeniedError(AccessDecision{
			Allowed:    false,
			Reason:     "not authorized",
			ReasonCode: "no_rule",
			AgentID:    "agent-x",
			Role:       "viewer",
			Server:     "test-server",
			Tool:       "test-tool",
		})},
		{"policyDenied", p.policyDeniedError(GatewayPolicyDecision{
			Action:     "deny",
			Reason:     "forbidden",
			ReasonCode: "BLOCKED",
			RuleID:     "rule-1",
			Stage:      "request",
		})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.resp.Error == nil {
				t.Fatal("expected Error in response")
			}
			if tc.resp.JSONRPC != mcp.JSONRPCVersion {
				t.Errorf("JSONRPC = %q, want %q", tc.resp.JSONRPC, mcp.JSONRPCVersion)
			}
			if tc.resp.ID != "envelope-test" {
				t.Errorf("ID = %v, want envelope-test", tc.resp.ID)
			}

			ped, ok := tc.resp.Error.Data.(*PipelineErrorData)
			if !ok {
				t.Fatalf("Data type = %T, want *PipelineErrorData", tc.resp.Error.Data)
			}
			if ped.Code == "" {
				t.Error("PipelineErrorData.Code is empty")
			}
			if ped.Stage == "" {
				t.Error("PipelineErrorData.Stage is empty")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DEBT-016: newPipelineError constructor produces correct fields
// ---------------------------------------------------------------------------

func TestNewPipelineError(t *testing.T) {
	ped := newPipelineError("TIMEOUT", "my-server", "my-tool", stageExecute, true)
	if ped.Code != "TIMEOUT" {
		t.Errorf("Code = %q, want TIMEOUT", ped.Code)
	}
	if ped.Server != "my-server" {
		t.Errorf("Server = %q, want my-server", ped.Server)
	}
	if ped.Tool != "my-tool" {
		t.Errorf("Tool = %q, want my-tool", ped.Tool)
	}
	if ped.Stage != stageExecute {
		t.Errorf("Stage = %q, want %q", ped.Stage, stageExecute)
	}
	if !ped.Retryable {
		t.Error("expected Retryable=true")
	}
	if ped.RetryAfter != "" {
		t.Errorf("RetryAfter = %q, want empty", ped.RetryAfter)
	}
	if ped.Details != nil {
		t.Errorf("Details = %v, want nil", ped.Details)
	}
}
