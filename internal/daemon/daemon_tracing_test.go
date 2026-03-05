package daemon

import (
	"context"
	"strings"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

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
