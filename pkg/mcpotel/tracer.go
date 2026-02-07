// Package mcpotel provides shared OpenTelemetry initialization for MCP servers.
//
// Tracing is opt-in: if OTEL_EXPORTER_OTLP_ENDPOINT is not set, a noop
// TracerProvider is returned with zero overhead.
//
// For Langfuse integration, point the standard OTel env vars at the
// Langfuse OTLP endpoint:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT=https://cloud.langfuse.com/api/public/otel
//	OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic <base64(pk:sk)>"
package mcpotel

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// GenAI semantic conventions for Langfuse compatibility.
const (
	AttrToolName  = "gen_ai.tool.name"
	AttrAgentID   = "gen_ai.agent.id"
	AttrSessionID = "gen_ai.session.id"
	AttrNamespace = "gen_ai.namespace"
)

// ShutdownFunc gracefully flushes and shuts down the tracer provider.
type ShutdownFunc func(ctx context.Context) error

// InitTracer creates a TracerProvider based on environment configuration.
// If OTEL_EXPORTER_OTLP_ENDPOINT is unset, returns a noop provider with
// zero runtime overhead. The returned ShutdownFunc must be called on exit
// to flush pending spans.
func InitTracer(ctx context.Context, serviceName string, logger *slog.Logger) (trace.TracerProvider, ShutdownFunc, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		noopTP := noop.NewTracerProvider()
		return noopTP, func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return noop.NewTracerProvider(), func(context.Context) error { return nil }, err
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		return noop.NewTracerProvider(), func(context.Context) error { return nil }, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	logger.Info("OTel tracing enabled", "endpoint", endpoint, "service", serviceName)

	shutdown := func(ctx context.Context) error {
		return tp.Shutdown(ctx)
	}
	return tp, shutdown, nil
}

// Tracer returns a named tracer from the given provider.
// This is a convenience wrapper to reduce boilerplate in callers.
func Tracer(tp trace.TracerProvider, name string) trace.Tracer {
	return tp.Tracer(name)
}
