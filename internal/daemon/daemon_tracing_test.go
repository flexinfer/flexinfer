package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/router"
	"github.com/crb2nu/loom/pkg/registry"
)

func setupDaemonTracer(t *testing.T) (*Daemon, *tracetest.SpanRecorder) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	})

	return &Daemon{tracer: tp.Tracer("loomd")}, recorder
}

func attrString(attrs []attribute.KeyValue, key string) string {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value.AsString()
		}
	}
	return ""
}

func attrBool(attrs []attribute.KeyValue, key string) (bool, bool) {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value.AsBool(), true
		}
	}
	return false, false
}

func TestHandleMessage_EmitsSpanForInitialize(t *testing.T) {
	d, recorder := setupDaemonTracer(t)

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      7,
		Method:  "initialize",
	}
	resp, err := d.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected initialize response")
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	span := spans[0]
	if span.Name() != "daemon.rpc.initialize" {
		t.Fatalf("span name = %q, want daemon.rpc.initialize", span.Name())
	}
	if got := attrString(span.Attributes(), "mcp.method"); got != "initialize" {
		t.Fatalf("mcp.method = %q, want initialize", got)
	}
	if got := attrString(span.Attributes(), "mcp.request_id"); got != "7" {
		t.Fatalf("mcp.request_id = %q, want 7", got)
	}
	if span.Status().Code != codes.Unset {
		t.Fatalf("span status = %s, want Unset", span.Status().Code)
	}
}

func TestHandleMessage_EmitsSpanForUnknownMethod(t *testing.T) {
	d, recorder := setupDaemonTracer(t)

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "abc",
		Method:  "unknown/method",
	}
	resp, err := d.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected method-not-found response")
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	span := spans[0]
	if span.Name() != "daemon.rpc.unknown/method" {
		t.Fatalf("span name = %q, want daemon.rpc.unknown/method", span.Name())
	}
	if got := attrString(span.Attributes(), "mcp.method"); got != "unknown/method" {
		t.Fatalf("mcp.method = %q, want unknown/method", got)
	}
	if hasResponse, ok := attrBool(span.Attributes(), "loom.has_response"); !ok || !hasResponse {
		t.Fatalf("loom.has_response = %v (present=%v), want true", hasResponse, ok)
	}
}

func TestComputeTracedServerCoverage(t *testing.T) {
	d := &Daemon{
		cfg: Config{Target: "dev"},
		registry: &registry.Registry{
			Servers: []*registry.Server{
				{
					Name: "mcp-gitlab",
					Common: &registry.TargetSpec{
						Command: "./bin/mcp-gitlab",
					},
				},
				{
					Name: "wrapped",
					Common: &registry.TargetSpec{
						Command: "go",
						Args:    []any{"run", "./cmd/mcp-custom"},
					},
				},
				{
					Name: "non-mcp",
					Common: &registry.TargetSpec{
						Command: "./bin/helper-service",
					},
				},
			},
		},
	}

	traced, total := d.computeTracedServerCoverage()
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if traced != 2 {
		t.Fatalf("traced = %d, want 2", traced)
	}
}

func TestHandleOTelStatus_IncludesRuntimeSurfaceCoverage(t *testing.T) {
	d := &Daemon{
		cfg: Config{Target: "dev"},
		registry: &registry.Registry{
			Servers: []*registry.Server{
				{
					Name: "mcp-gitlab",
					Common: &registry.TargetSpec{
						Command: "./bin/mcp-gitlab",
					},
				},
				{
					Name: "misc-service",
					Common: &registry.TargetSpec{
						Command: "./bin/misc-service",
					},
				},
			},
		},
	}

	msg := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: 1}
	resp, err := d.handleOTelStatus(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleOTelStatus error: %v", err)
	}
	if resp == nil || resp.Result == nil {
		t.Fatal("expected result payload")
	}

	payload := string(resp.Result)
	if !strings.Contains(payload, `"runtime_trace_surfaces"`) {
		t.Fatalf("expected runtime_trace_surfaces in payload: %s", payload)
	}
	if !strings.Contains(payload, `"runtime_trace_coverage":"100%"`) {
		t.Fatalf("expected runtime_trace_coverage in payload: %s", payload)
	}
	if !strings.Contains(payload, `"traced_servers":1`) || !strings.Contains(payload, `"total_servers":2`) {
		t.Fatalf("expected traced/total server counts in payload: %s", payload)
	}
}

func TestInitDaemonOTel_UsesRuntimeConfigAndEnvPrecedence(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://env-collector:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")

	sampleRate := 0.25
	tp, shutdown, state, err := initDaemonOTel(context.Background(), OTelConfig{
		Endpoint:    "http://file-collector:4318",
		Protocol:    "http",
		ServiceName: "loomd-custom",
		SampleRate:  &sampleRate,
	}, slog.Default())
	if err != nil {
		t.Fatalf("initDaemonOTel error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected shutdown func")
	}
	if state.Endpoint != "http://env-collector:4318" {
		t.Fatalf("endpoint = %q, want env override", state.Endpoint)
	}
	if !state.Configured {
		t.Fatal("expected configured runtime state")
	}
	if !state.Enabled {
		t.Fatal("expected enabled runtime state")
	}
	if state.Protocol != "grpc" {
		t.Fatalf("protocol = %q, want grpc", state.Protocol)
	}
	if state.ServiceName != "loomd-custom" {
		t.Fatalf("service name = %q, want loomd-custom", state.ServiceName)
	}
	if state.SampleRate != 0.25 {
		t.Fatalf("sample rate = %v, want 0.25", state.SampleRate)
	}
	_, isSDK := tp.(*sdktrace.TracerProvider)
	if !isSDK {
		t.Fatalf("provider type = %T, want *sdktrace.TracerProvider", tp)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
}

func TestHandleOTelStatus_IncludesRuntimeOTelState(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("MCP_LOG_FORMAT", "json")

	d := &Daemon{
		cfg: Config{Target: "dev"},
		otelRuntimeState: daemonOTelState{
			Configured:  true,
			Enabled:     true,
			Endpoint:    "http://collector:4318",
			Protocol:    "grpc",
			ServiceName: "loomd-custom",
			SampleRate:  0.5,
		},
		registry: &registry.Registry{
			Servers: []*registry.Server{
				{
					Name: "mcp-gitlab",
					Common: &registry.TargetSpec{
						Command: "./bin/mcp-gitlab",
					},
				},
			},
		},
	}

	msg := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: 2}
	resp, err := d.handleOTelStatus(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleOTelStatus error: %v", err)
	}
	if resp == nil || resp.Result == nil {
		t.Fatal("expected response payload")
	}

	var got struct {
		RuntimeConfigured bool    `json:"runtime_otlp_configured"`
		RuntimeEnabled    bool    `json:"runtime_otlp_enabled"`
		RuntimeEndpoint   string  `json:"runtime_otlp_endpoint"`
		RuntimeProtocol   string  `json:"runtime_otlp_protocol"`
		RuntimeService    string  `json:"runtime_otlp_service_name"`
		RuntimeSampleRate float64 `json:"runtime_otlp_sample_rate"`
		RuntimeError      string  `json:"runtime_otlp_error"`
		RuntimeCoverage   string  `json:"runtime_trace_coverage"`
	}
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if !got.RuntimeConfigured || !got.RuntimeEnabled {
		t.Fatalf("runtime OTel state = %+v, want configured+enabled", got)
	}
	if got.RuntimeEndpoint != "http://collector:4318" {
		t.Fatalf("runtime endpoint = %q, want collector URL", got.RuntimeEndpoint)
	}
	if got.RuntimeProtocol != "grpc" {
		t.Fatalf("runtime protocol = %q, want grpc", got.RuntimeProtocol)
	}
	if got.RuntimeService != "loomd-custom" {
		t.Fatalf("runtime service = %q, want loomd-custom", got.RuntimeService)
	}
	if got.RuntimeSampleRate != 0.5 {
		t.Fatalf("runtime sample rate = %v, want 0.5", got.RuntimeSampleRate)
	}
	if got.RuntimeError != "" {
		t.Fatalf("runtime error = %q, want empty", got.RuntimeError)
	}
	if got.RuntimeCoverage != "100%" {
		t.Fatalf("runtime coverage = %q, want 100%%", got.RuntimeCoverage)
	}
}

func TestStop_ShutsDownOtelTracerOnce(t *testing.T) {
	var calls atomic.Int32
	d := &Daemon{
		done: make(chan struct{}),
		otelShutdown: func(context.Context) error {
			calls.Add(1)
			return nil
		},
	}

	if err := d.Stop(); err != nil {
		t.Fatalf("first stop returned error: %v", err)
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("second stop returned error: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("shutdown calls = %d, want 1", got)
	}
}

func TestCallPipeline_RecordTransportSpanEvent(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
	})

	ctx, span := tp.Tracer("test").Start(context.Background(), "parent")
	p := &callPipeline{ctx: ctx}
	p.recordTransportSpanEvent("daemon.server.restart_triggered",
		attribute.String("server.name", "mcp-gitlab"),
		attribute.String("failure.stage", "recv"),
	)
	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	events := spans[0].Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Name != "daemon.server.restart_triggered" {
		t.Fatalf("event name = %q, want daemon.server.restart_triggered", events[0].Name)
	}
	if got := attrString(events[0].Attributes, "server.name"); got != "mcp-gitlab" {
		t.Fatalf("server.name event attr = %q, want mcp-gitlab", got)
	}
}

// setupTracedCallDaemon creates a Daemon with tracing enabled plus the minimal
// infrastructure needed for a tools/call pipeline (router, metrics, pool).
func setupTracedCallDaemon(t *testing.T, transport mcp.Transport) (*Daemon, *tracetest.SpanRecorder) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	})

	d := newCallPipelineTestDaemon()
	d.tracer = tp.Tracer("loomd")

	// Use a hub pool so the pipeline can route to hub without needing a local process manager.
	d.hubPool = pool.New(pool.Config{
		MaxIdle:     2,
		MaxOpen:     2,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			return transport, nil
		},
	})
	t.Cleanup(func() { _ = d.hubPool.Close() })

	return d, recorder
}

// findSpanByName returns the first ended span with the given name.
func findSpanByName(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// spanNames returns all span names from the slice.
func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, len(spans))
	for i, s := range spans {
		names[i] = s.Name()
	}
	return names
}

// TestPipelineSpans_SuccessfulCall verifies that a successful tools/call RPC
// produces child spans for all 7 pipeline stages and that the parent span
// carries the enriched mcp.* attributes.
func TestPipelineSpans_SuccessfulCall(t *testing.T) {
	tr := &fakeTransport{
		recvMsg: &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      "trace-ok",
			Result:  json.RawMessage(`{"result":"ok"}`),
		},
	}
	d, recorder := setupTracedCallDaemon(t, tr)

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "trace-ok",
		Method:  "loom/call",
		Params: json.RawMessage(`{
			"server": "test-server",
			"tool": "test-tool",
			"agent_id": "agent-1",
			"session_id": "sess-42",
			"arguments": {"key": "value"}
		}`),
	}
	resp, err := d.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error response: %s", resp.Error.Message)
	}

	spans := recorder.Ended()
	names := spanNames(spans)

	// Expect: parse, authorize, policy, cache, route, build, execute + parent rpc span = 8
	expectedStageSpans := []string{
		"daemon.pipeline.parse",
		"daemon.pipeline.authorize",
		"daemon.pipeline.policy",
		"daemon.pipeline.cache",
		"daemon.pipeline.route",
		"daemon.pipeline.build",
		"daemon.pipeline.execute",
	}
	for _, expected := range expectedStageSpans {
		found := false
		for _, name := range names {
			if name == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing expected span %q; got spans: %v", expected, names)
		}
	}

	// Verify parent RPC span exists and carries enriched attributes.
	parentSpan := findSpanByName(spans, "daemon.rpc.loom/call")
	if parentSpan == nil {
		t.Fatal("missing parent daemon.rpc.loom/call span")
	}
	if got := attrString(parentSpan.Attributes(), "mcp.tool"); got != "test-tool" {
		t.Errorf("parent span mcp.tool = %q, want test-tool", got)
	}
	if got := attrString(parentSpan.Attributes(), "mcp.server"); got != "test-server" {
		t.Errorf("parent span mcp.server = %q, want test-server", got)
	}
	if got := attrString(parentSpan.Attributes(), "mcp.agent_id"); got != "agent-1" {
		t.Errorf("parent span mcp.agent_id = %q, want agent-1", got)
	}
	if got := attrString(parentSpan.Attributes(), "mcp.session_id"); got != "sess-42" {
		t.Errorf("parent span mcp.session_id = %q, want sess-42", got)
	}

	// Verify child spans are children of the parent span.
	parentSpanID := parentSpan.SpanContext().SpanID()
	for _, expected := range expectedStageSpans {
		child := findSpanByName(spans, expected)
		if child == nil {
			continue
		}
		if !child.Parent().SpanID().IsValid() {
			t.Errorf("span %q has no parent", expected)
		}
		// The first child (parse) should be a child of the parent.
		// Subsequent children are children of parse's updated context, which
		// still belongs to the same trace.
		if child.SpanContext().TraceID() != parentSpan.SpanContext().TraceID() {
			t.Errorf("span %q trace ID mismatch", expected)
		}
	}

	// Verify parse span attributes.
	parseSpan := findSpanByName(spans, "daemon.pipeline.parse")
	if parseSpan != nil {
		if got := attrString(parseSpan.Attributes(), "mcp.tool"); got != "test-tool" {
			t.Errorf("parse span mcp.tool = %q, want test-tool", got)
		}
		if got := attrString(parseSpan.Attributes(), "mcp.server"); got != "test-server" {
			t.Errorf("parse span mcp.server = %q, want test-server", got)
		}
	}

	// Verify execute span attributes.
	execSpan := findSpanByName(spans, "daemon.pipeline.execute")
	if execSpan != nil {
		if got := attrString(execSpan.Attributes(), "mcp.server"); got != "test-server" {
			t.Errorf("execute span mcp.server = %q, want test-server", got)
		}
		if got := attrString(execSpan.Attributes(), "mcp.tool"); got != "test-tool" {
			t.Errorf("execute span mcp.tool = %q, want test-tool", got)
		}
	}

	// Verify route span has routing decision event.
	routeSpan := findSpanByName(spans, "daemon.pipeline.route")
	if routeSpan != nil {
		if got := attrString(routeSpan.Attributes(), "routing.target"); got == "" {
			t.Error("route span missing routing.target attribute")
		}
		foundRouteEvent := false
		for _, ev := range routeSpan.Events() {
			if ev.Name == "daemon.pipeline.route.decision" {
				foundRouteEvent = true
				break
			}
		}
		if !foundRouteEvent {
			t.Error("route span missing daemon.pipeline.route.decision event")
		}
	}

	// Verify the first stage span is a direct child of the parent.
	if parseSpan != nil && parseSpan.Parent().SpanID() != parentSpanID {
		t.Errorf("parse span parent ID = %s, want %s (parent rpc span)", parseSpan.Parent().SpanID(), parentSpanID)
	}
}

// TestPipelineSpans_ParseFailure verifies that a parse error produces a span
// with error status and no downstream stage spans.
func TestPipelineSpans_ParseFailure(t *testing.T) {
	d, recorder := setupDaemonTracer(t)
	d.metrics = NewMetrics()
	d.logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "parse-fail",
		Method:  "loom/call",
		Params:  json.RawMessage(`{invalid json`),
	}
	resp, err := d.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected error response for invalid JSON")
	}

	spans := recorder.Ended()
	names := spanNames(spans)

	// Should have parent span + parse span (which errors out).
	parseSpan := findSpanByName(spans, "daemon.pipeline.parse")
	if parseSpan == nil {
		t.Fatalf("missing daemon.pipeline.parse span; got: %v", names)
	}
	if parseSpan.Status().Code != codes.Error {
		t.Errorf("parse span status = %s, want Error", parseSpan.Status().Code)
	}

	// No downstream stages should have been reached.
	for _, name := range []string{
		"daemon.pipeline.authorize",
		"daemon.pipeline.policy",
		"daemon.pipeline.cache",
		"daemon.pipeline.route",
		"daemon.pipeline.build",
		"daemon.pipeline.execute",
	} {
		if findSpanByName(spans, name) != nil {
			t.Errorf("unexpected span %q after parse failure", name)
		}
	}
}

// TestPipelineSpans_AuthorizeDenied verifies that an RBAC denial produces
// authorize span with error status and no downstream stage spans.
func TestPipelineSpans_AuthorizeDenied(t *testing.T) {
	d, recorder := setupDaemonTracer(t)
	d.metrics = NewMetrics()
	d.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	d.router = router.New(router.Config{HubEnabled: true})
	d.rbac = NewRBACEnforcer(RBACConfig{
		Enabled:       true,
		DefaultPolicy: "deny",
	}, d.logger)

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "rbac-deny",
		Method:  "loom/call",
		Params: json.RawMessage(`{
			"server": "protected-server",
			"tool": "admin-tool",
			"agent_id": "untrusted-agent"
		}`),
	}
	resp, err := d.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected RBAC denial response")
	}

	spans := recorder.Ended()

	// Authorize span should exist with error status.
	authSpan := findSpanByName(spans, "daemon.pipeline.authorize")
	if authSpan == nil {
		t.Fatalf("missing daemon.pipeline.authorize span; got: %v", spanNames(spans))
	}
	if authSpan.Status().Code != codes.Error {
		t.Errorf("authorize span status = %s, want Error", authSpan.Status().Code)
	}
	if got := attrString(authSpan.Attributes(), "mcp.agent_id"); got != "untrusted-agent" {
		t.Errorf("authorize span mcp.agent_id = %q, want untrusted-agent", got)
	}
	if allowed, ok := attrBool(authSpan.Attributes(), "rbac.allowed"); !ok || allowed {
		t.Errorf("authorize span rbac.allowed = %v (present=%v), want false", allowed, ok)
	}

	// No downstream stages after authorize should exist.
	for _, name := range []string{
		"daemon.pipeline.policy",
		"daemon.pipeline.cache",
		"daemon.pipeline.route",
		"daemon.pipeline.build",
		"daemon.pipeline.execute",
	} {
		if findSpanByName(spans, name) != nil {
			t.Errorf("unexpected span %q after authorize denial", name)
		}
	}
}

// TestPipelineSpans_CacheHitSkipsDownstream verifies that a cache hit produces
// a cache span with hit=true event and no route/build/execute spans.
func TestPipelineSpans_CacheHitSkipsDownstream(t *testing.T) {
	d, recorder := setupDaemonTracer(t)
	d.metrics = NewMetrics()
	d.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	d.router = router.New(router.Config{HubEnabled: true})
	d.respCache = NewResponseCache(CacheConfig{Enabled: true})

	args := json.RawMessage(`{"query":"up"}`)
	key := d.respCache.Key("prometheus", "query", args)
	d.respCache.Set(key, json.RawMessage(`{"cached":true}`), "prometheus", "query")

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "cache-hit",
		Method:  "loom/call",
		Params: json.RawMessage(`{
			"server": "prometheus",
			"tool": "query",
			"arguments": {"query":"up"}
		}`),
	}
	resp, err := d.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatal("expected successful cached response")
	}

	spans := recorder.Ended()

	cacheSpan := findSpanByName(spans, "daemon.pipeline.cache")
	if cacheSpan == nil {
		t.Fatalf("missing daemon.pipeline.cache span; got: %v", spanNames(spans))
	}

	// Verify cache.hit attribute.
	if hit, ok := attrBool(cacheSpan.Attributes(), "cache.hit"); !ok || !hit {
		t.Errorf("cache span cache.hit = %v (present=%v), want true", hit, ok)
	}

	// Verify cache hit event.
	foundHitEvent := false
	for _, ev := range cacheSpan.Events() {
		if ev.Name == "daemon.pipeline.cache.hit" {
			foundHitEvent = true
			break
		}
	}
	if !foundHitEvent {
		t.Error("cache span missing daemon.pipeline.cache.hit event")
	}

	// No downstream stages after cache hit.
	for _, name := range []string{
		"daemon.pipeline.route",
		"daemon.pipeline.build",
		"daemon.pipeline.execute",
	} {
		if findSpanByName(spans, name) != nil {
			t.Errorf("unexpected span %q after cache hit", name)
		}
	}
}

// TestPipelineSpans_CacheMissEvent verifies that a cache miss produces
// the cache span with hit=false and a miss event.
func TestPipelineSpans_CacheMissEvent(t *testing.T) {
	tr := &fakeTransport{
		recvMsg: &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      "cache-miss",
			Result:  json.RawMessage(`{"ok":true}`),
		},
	}
	d, recorder := setupTracedCallDaemon(t, tr)
	d.respCache = NewResponseCache(CacheConfig{Enabled: true})

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "cache-miss",
		Method:  "loom/call",
		Params: json.RawMessage(`{
			"server": "prometheus",
			"tool": "query",
			"arguments": {"query":"up"}
		}`),
	}
	resp, err := d.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatal("expected successful response")
	}

	spans := recorder.Ended()
	cacheSpan := findSpanByName(spans, "daemon.pipeline.cache")
	if cacheSpan == nil {
		t.Fatalf("missing daemon.pipeline.cache span; got: %v", spanNames(spans))
	}

	if hit, ok := attrBool(cacheSpan.Attributes(), "cache.hit"); !ok || hit {
		t.Errorf("cache span cache.hit = %v (present=%v), want false", hit, ok)
	}

	foundMissEvent := false
	for _, ev := range cacheSpan.Events() {
		if ev.Name == "daemon.pipeline.cache.miss" {
			foundMissEvent = true
			break
		}
	}
	if !foundMissEvent {
		t.Error("cache span missing daemon.pipeline.cache.miss event")
	}
}

// TestPipelineSpans_RouteFailure verifies that a routing failure produces
// the route span with error status.
func TestPipelineSpans_RouteFailure(t *testing.T) {
	d, recorder := setupDaemonTracer(t)
	d.metrics = NewMetrics()
	d.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	d.router = router.New(router.Config{HubEnabled: false})

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "route-fail",
		Method:  "loom/call",
		Params: json.RawMessage(`{
			"server": "unknown-server",
			"tool": "some-tool"
		}`),
	}
	resp, err := d.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected route failure response")
	}
	if !strings.Contains(resp.Error.Message, "server unavailable") {
		t.Fatalf("unexpected error message: %q", resp.Error.Message)
	}

	spans := recorder.Ended()
	routeSpan := findSpanByName(spans, "daemon.pipeline.route")
	if routeSpan == nil {
		t.Fatalf("missing daemon.pipeline.route span; got: %v", spanNames(spans))
	}
	if routeSpan.Status().Code != codes.Error {
		t.Errorf("route span status = %s, want Error", routeSpan.Status().Code)
	}

	// No downstream stages.
	for _, name := range []string{"daemon.pipeline.build", "daemon.pipeline.execute"} {
		if findSpanByName(spans, name) != nil {
			t.Errorf("unexpected span %q after route failure", name)
		}
	}
}

// TestPipelineSpans_ExecuteTransportFailure verifies that a transport send
// failure in execute produces the execute span with error status.
func TestPipelineSpans_ExecuteTransportFailure(t *testing.T) {
	tr := &fakeTransport{
		sendErr: fmt.Errorf("connection reset"),
	}
	d, recorder := setupTracedCallDaemon(t, tr)

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "exec-fail",
		Method:  "loom/call",
		Params: json.RawMessage(`{
			"server": "failing-server",
			"tool": "broken-tool"
		}`),
	}
	resp, err := d.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected execute failure response")
	}

	spans := recorder.Ended()
	execSpan := findSpanByName(spans, "daemon.pipeline.execute")
	if execSpan == nil {
		t.Fatalf("missing daemon.pipeline.execute span; got: %v", spanNames(spans))
	}
	if execSpan.Status().Code != codes.Error {
		t.Errorf("execute span status = %s, want Error", execSpan.Status().Code)
	}
	if got := attrString(execSpan.Attributes(), "mcp.server"); got != "failing-server" {
		t.Errorf("execute span mcp.server = %q, want failing-server", got)
	}
}

// TestPipelineSpans_NilRBACAndPolicy verifies that when RBAC and policy are nil,
// the authorize and policy spans still exist with "skipped" attributes.
func TestPipelineSpans_NilRBACAndPolicy(t *testing.T) {
	tr := &fakeTransport{
		recvMsg: &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      "nil-rbac",
			Result:  json.RawMessage(`{"ok":true}`),
		},
	}
	d, recorder := setupTracedCallDaemon(t, tr)
	// Ensure rbac and policy are nil (default from newCallPipelineTestDaemon).

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "nil-rbac",
		Method:  "loom/call",
		Params: json.RawMessage(`{
			"server": "test-server",
			"tool": "test-tool"
		}`),
	}
	resp, err := d.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatal("expected successful response")
	}

	spans := recorder.Ended()

	authSpan := findSpanByName(spans, "daemon.pipeline.authorize")
	if authSpan == nil {
		t.Fatalf("missing daemon.pipeline.authorize span; got: %v", spanNames(spans))
	}
	if got := attrString(authSpan.Attributes(), "rbac.decision"); got != "skipped" {
		t.Errorf("authorize span rbac.decision = %q, want skipped", got)
	}

	policySpan := findSpanByName(spans, "daemon.pipeline.policy")
	if policySpan == nil {
		t.Fatalf("missing daemon.pipeline.policy span; got: %v", spanNames(spans))
	}
	if got := attrString(policySpan.Attributes(), "policy.decision"); got != "skipped" {
		t.Errorf("policy span policy.decision = %q, want skipped", got)
	}
}

// TestPipelineSpans_ParentSpanNoAgentIDWhenEmpty verifies that mcp.agent_id
// is not set on the parent span when agent_id is empty.
func TestPipelineSpans_ParentSpanNoAgentIDWhenEmpty(t *testing.T) {
	tr := &fakeTransport{
		recvMsg: &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      "no-agent",
			Result:  json.RawMessage(`{"ok":true}`),
		},
	}
	d, recorder := setupTracedCallDaemon(t, tr)

	msg := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      "no-agent",
		Method:  "loom/call",
		Params: json.RawMessage(`{
			"server": "test-server",
			"tool": "test-tool"
		}`),
	}
	resp, err := d.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatal("expected successful response")
	}

	spans := recorder.Ended()
	parentSpan := findSpanByName(spans, "daemon.rpc.loom/call")
	if parentSpan == nil {
		t.Fatal("missing parent span")
	}
	// agent_id should NOT be set when empty.
	if got := attrString(parentSpan.Attributes(), "mcp.agent_id"); got != "" {
		t.Errorf("parent span mcp.agent_id = %q, want empty (not set)", got)
	}
}
