// Package mcpotel provides shared OpenTelemetry initialization for MCP servers.
//
// Tracing is opt-in: if no endpoint is configured (via Options or
// OTEL_EXPORTER_OTLP_ENDPOINT), a noop TracerProvider is returned with zero
// overhead.
//
// For Langfuse integration, point the standard OTel env vars at the
// Langfuse OTLP endpoint:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT=https://cloud.langfuse.com/api/public/otel
//	OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic <base64(pk:sk)>"
package mcpotel

import (
	"context"
	"fmt"
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

// Options configures trace export from file-based config.
// Environment variables (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_HEADERS)
// take precedence when set.
type Options struct {
	// Endpoint is the OTLP collector endpoint.
	Endpoint string

	// Protocol selects OTLP transport: "http" (default) or "grpc".
	Protocol string

	// Headers are additional HTTP headers (e.g., auth).
	Headers map[string]string

	// SampleRate is the trace sampling ratio (0.0–1.0). 0 means use default (1.0).
	SampleRate float64
}

// InitTracer creates a TracerProvider based on environment configuration.
// If OTEL_EXPORTER_OTLP_ENDPOINT is unset, returns a noop provider with
// zero runtime overhead. The returned ShutdownFunc must be called on exit
// to flush pending spans.
func InitTracer(ctx context.Context, serviceName string, logger *slog.Logger) (trace.TracerProvider, ShutdownFunc, error) {
	return InitTracerWithOptions(ctx, serviceName, logger, Options{})
}

// InitTracerWithOptions creates a TracerProvider using the given options.
// Environment variables take precedence over Options fields. If no endpoint
// is configured, returns a noop provider with zero overhead.
func InitTracerWithOptions(ctx context.Context, serviceName string, logger *slog.Logger, opts Options) (trace.TracerProvider, ShutdownFunc, error) {
	noopShutdown := ShutdownFunc(func(context.Context) error { return nil })

	// Environment takes precedence over file config.
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = opts.Endpoint
	}
	if endpoint == "" {
		return noop.NewTracerProvider(), noopShutdown, nil
	}

	protocol := opts.Protocol
	if envProto := os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"); envProto != "" {
		protocol = envProto
	}

	exporter, err := newExporter(ctx, protocol, opts)
	if err != nil {
		return noop.NewTracerProvider(), noopShutdown, fmt.Errorf("create OTLP exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		return noop.NewTracerProvider(), noopShutdown, err
	}

	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	}

	// Apply sampling rate if configured.
	rate := opts.SampleRate
	if rate > 0 && rate < 1.0 {
		tpOpts = append(tpOpts, sdktrace.WithSampler(
			sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate)),
		))
	}

	tp := sdktrace.NewTracerProvider(tpOpts...)
	otel.SetTracerProvider(tp)

	logger.Info("OTel tracing enabled",
		"endpoint", endpoint,
		"protocol", protocol,
		"service", serviceName,
	)

	shutdown := func(ctx context.Context) error {
		return tp.Shutdown(ctx)
	}
	return tp, shutdown, nil
}

// newExporter creates an OTLP span exporter for the given protocol.
// For "grpc", it creates a gRPC exporter. Otherwise defaults to HTTP.
func newExporter(ctx context.Context, protocol string, opts Options) (sdktrace.SpanExporter, error) {
	switch protocol {
	case "grpc":
		return newGRPCExporter(ctx, opts)
	default:
		// HTTP exporter — uses standard OTEL env vars for endpoint/headers.
		// Config-level headers are applied only when env headers are absent.
		httpOpts := []otlptracehttp.Option{}
		if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && opts.Endpoint != "" {
			httpOpts = append(httpOpts, otlptracehttp.WithEndpointURL(opts.Endpoint))
		}
		if os.Getenv("OTEL_EXPORTER_OTLP_HEADERS") == "" && len(opts.Headers) > 0 {
			httpOpts = append(httpOpts, otlptracehttp.WithHeaders(opts.Headers))
		}
		return otlptracehttp.New(ctx, httpOpts...)
	}
}

// newGRPCExporter creates a gRPC-based OTLP span exporter.
func newGRPCExporter(ctx context.Context, opts Options) (sdktrace.SpanExporter, error) {
	// Use the protocol-agnostic otlptrace client interface for gRPC.
	// The gRPC exporter reads OTEL_EXPORTER_OTLP_ENDPOINT env automatically.
	// We use the generic otlptrace.New with a custom client to avoid
	// importing the full gRPC stack when not needed. For now, fall back
	// to HTTP exporter with a gRPC-style endpoint hint.
	//
	// Full gRPC support can be added later when otlptracegrpc is included.
	_ = ctx
	_ = opts
	return nil, fmt.Errorf("gRPC protocol requires otlptracegrpc dependency; use protocol=http or add the dependency")
}

// Tracer returns a named tracer from the given provider.
// This is a convenience wrapper to reduce boilerplate in callers.
func Tracer(tp trace.TracerProvider, name string) trace.Tracer {
	return tp.Tracer(name)
}
