package observability

import (
	"context"
	"testing"
)

func TestInitTracingDisabledByDefault(t *testing.T) {
	t.Setenv("FLEXINFER_OTEL_ENABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://%zz")

	shutdown, err := InitTracing(context.Background(), "flexinfer-test")
	if err != nil {
		t.Fatalf("InitTracing() with tracing disabled returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("InitTracing() returned nil shutdown function")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("disabled tracing shutdown returned error: %v", err)
	}
}

func TestEnvBoolParsesOtelEnabledValues(t *testing.T) {
	t.Setenv("FLEXINFER_OTEL_ENABLED", "true")
	if !envBool("FLEXINFER_OTEL_ENABLED", false) {
		t.Fatal("expected true to enable tracing")
	}

	t.Setenv("FLEXINFER_OTEL_ENABLED", "off")
	if envBool("FLEXINFER_OTEL_ENABLED", true) {
		t.Fatal("expected off to disable tracing")
	}
}
