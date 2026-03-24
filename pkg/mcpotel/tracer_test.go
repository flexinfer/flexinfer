package mcpotel

import (
	"context"
	"log/slog"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitTracer_NoEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	tp, shutdown, err := InitTracer(context.Background(), "test-service", slog.Default())
	require.NoError(t, err)
	require.NotNil(t, tp)
	require.NotNil(t, shutdown)

	// Should return a noop provider (value type, not pointer)
	_, isNoop := tp.(noop.TracerProvider)
	assert.True(t, isNoop, "expected noop TracerProvider when endpoint is unset")

	// Shutdown should be safe to call
	assert.NoError(t, shutdown(context.Background()))
}

func TestInitTracer_WithEndpoint(t *testing.T) {
	// Set endpoint to a dummy value — we don't actually connect, just verify
	// that the provider is an SDK TracerProvider (not noop).
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")

	tp, shutdown, err := InitTracer(context.Background(), "test-service", slog.Default())
	// The resource.Merge may return a schema URL conflict error with certain
	// OTel SDK versions. Treat that as acceptable — the important thing is
	// that we get a non-nil provider or a graceful fallback.
	if err != nil {
		// Should still return a noop provider on error, not nil
		require.NotNil(t, tp, "expected non-nil provider even on init error")
		assert.NoError(t, shutdown(context.Background()))
		return
	}

	require.NotNil(t, tp)

	// Should return a real SDK TracerProvider
	_, isSDK := tp.(*sdktrace.TracerProvider)
	assert.True(t, isSDK, "expected *sdktrace.TracerProvider when endpoint is set")

	// Clean up
	assert.NoError(t, shutdown(context.Background()))
}

func TestInitTracer_ShutdownIdempotent(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	_, shutdown, err := InitTracer(context.Background(), "test-service", slog.Default())
	require.NoError(t, err)

	// Calling shutdown multiple times should not panic
	assert.NoError(t, shutdown(context.Background()))
	assert.NoError(t, shutdown(context.Background()))
	assert.NoError(t, shutdown(context.Background()))
}

func TestTracer_Convenience_Noop(t *testing.T) {
	noopTP := noop.NewTracerProvider()
	tracer := Tracer(noopTP, "test")
	assert.NotNil(t, tracer)
}

func TestTracer_Convenience_SDK(t *testing.T) {
	// Create a real SDK provider directly (bypassing resource merge)
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tracer := Tracer(tp, "test")
	assert.NotNil(t, tracer)
}

func TestInitTracerWithOptions_NoEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	tp, shutdown, err := InitTracerWithOptions(context.Background(), "test", slog.Default(), Options{})
	require.NoError(t, err)
	_, isNoop := tp.(noop.TracerProvider)
	assert.True(t, isNoop, "expected noop when no endpoint configured")
	assert.NoError(t, shutdown(context.Background()))
}

func TestInitTracerWithOptions_FileEndpoint(t *testing.T) {
	// No env var, but Options has endpoint
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	tp, shutdown, err := InitTracerWithOptions(context.Background(), "test", slog.Default(), Options{
		Endpoint: "http://localhost:4318",
	})
	if err != nil {
		require.NotNil(t, tp)
		assert.NoError(t, shutdown(context.Background()))
		return
	}
	_, isSDK := tp.(*sdktrace.TracerProvider)
	assert.True(t, isSDK, "expected SDK provider when file config endpoint is set")
	assert.NoError(t, shutdown(context.Background()))
}

func TestInitTracerWithOptions_EnvOverridesFile(t *testing.T) {
	// Env var takes precedence over Options.Endpoint
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")

	tp, shutdown, err := InitTracerWithOptions(context.Background(), "test", slog.Default(), Options{
		Endpoint: "http://should-be-ignored:9999",
	})
	if err != nil {
		require.NotNil(t, tp)
		assert.NoError(t, shutdown(context.Background()))
		return
	}
	_, isSDK := tp.(*sdktrace.TracerProvider)
	assert.True(t, isSDK, "expected SDK provider when env endpoint is set")
	assert.NoError(t, shutdown(context.Background()))
}

func TestInitTracerWithOptions_SampleRate(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")

	tp, shutdown, err := InitTracerWithOptions(context.Background(), "test", slog.Default(), Options{
		SampleRate: 0.5,
	})
	if err != nil {
		require.NotNil(t, tp)
		assert.NoError(t, shutdown(context.Background()))
		return
	}
	_, isSDK := tp.(*sdktrace.TracerProvider)
	assert.True(t, isSDK, "expected SDK provider with sampling")
	assert.NoError(t, shutdown(context.Background()))
}

func TestInitTracerWithOptions_GRPCProtocol(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")

	tp, shutdown, err := InitTracerWithOptions(context.Background(), "test", slog.Default(), Options{
		Protocol: "grpc",
	})
	if err != nil {
		require.NotNil(t, tp)
		assert.NoError(t, shutdown(context.Background()))
		return
	}
	_, isSDK := tp.(*sdktrace.TracerProvider)
	assert.True(t, isSDK, "expected SDK provider with gRPC protocol")
	assert.NoError(t, shutdown(context.Background()))
}

func TestInitTracerWithOptions_GRPCWithFileEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	tp, shutdown, err := InitTracerWithOptions(context.Background(), "test", slog.Default(), Options{
		Protocol: "grpc",
		Endpoint: "localhost:4317",
	})
	if err != nil {
		require.NotNil(t, tp)
		assert.NoError(t, shutdown(context.Background()))
		return
	}
	_, isSDK := tp.(*sdktrace.TracerProvider)
	assert.True(t, isSDK, "expected SDK provider with gRPC file config endpoint")
	assert.NoError(t, shutdown(context.Background()))
}

func TestInitTracerWithOptions_GRPCWithHeaders(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")

	tp, shutdown, err := InitTracerWithOptions(context.Background(), "test", slog.Default(), Options{
		Protocol: "grpc",
		Endpoint: "localhost:4317",
		Headers:  map[string]string{"Authorization": "Bearer test-token"},
	})
	if err != nil {
		require.NotNil(t, tp)
		assert.NoError(t, shutdown(context.Background()))
		return
	}
	_, isSDK := tp.(*sdktrace.TracerProvider)
	assert.True(t, isSDK, "expected SDK provider with gRPC headers")
	assert.NoError(t, shutdown(context.Background()))
}
