package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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
}

func (f *fakeTransport) Send(_ context.Context, msg *mcp.Message) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, msg)
	return nil
}

func (f *fakeTransport) Recv(_ context.Context) (*mcp.Message, error) {
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
