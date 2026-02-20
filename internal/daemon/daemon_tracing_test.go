package daemon

import (
	"context"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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
