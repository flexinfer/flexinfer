package proxy

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TestLoadOrCreateProxy_InjectsTraceContext proves the reverse proxy's Director
// propagates W3C trace context into the upstream request when a span is active,
// so the backend can continue the distributed trace started at the proxy edge.
func TestLoadOrCreateProxy_InjectsTraceContext(t *testing.T) {
	// Install the W3C propagator (as InitTracing does) and restore it after.
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("test").Start(context.Background(), "test")
	defer span.End()

	if !span.SpanContext().IsValid() {
		t.Fatal("expected a valid span context for the test")
	}

	p := &Proxy{}
	rp, ok := p.loadOrCreateProxy("http://backend.default.svc:8000")
	if !ok {
		t.Fatal("loadOrCreateProxy returned ok=false")
	}

	req := httptest.NewRequest("POST", "http://flexinfer/v1/chat/completions", nil).WithContext(ctx)
	rp.Director(req)

	got := req.Header.Get("traceparent")
	if got == "" {
		t.Fatal("expected traceparent header injected into upstream request, got none")
	}
	// The injected traceparent must carry the active span's trace ID.
	if want := span.SpanContext().TraceID().String(); want == "" || !strings.Contains(got, want) {
		t.Fatalf("traceparent %q does not carry active trace ID %q", got, want)
	}
}

// TestLoadOrCreateProxy_NoInjectionWithoutActiveSpan proves injection is a no-op
// when there is no active span (e.g. tracing disabled), preserving the prior
// forward-everything behavior with no spurious trace headers.
func TestLoadOrCreateProxy_NoInjectionWithoutActiveSpan(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

	p := &Proxy{}
	rp, ok := p.loadOrCreateProxy("http://backend.default.svc:8000")
	if !ok {
		t.Fatal("loadOrCreateProxy returned ok=false")
	}

	req := httptest.NewRequest("POST", "http://flexinfer/v1/chat/completions", nil)
	rp.Director(req)

	if got := req.Header.Get("traceparent"); got != "" {
		t.Fatalf("expected no traceparent without an active span, got %q", got)
	}
}
