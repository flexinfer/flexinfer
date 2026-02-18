package observability

import (
	"context"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// InitTracing initializes global OpenTelemetry tracing when FLEXINFER_OTEL_ENABLED=true.
// The exporter is OTLP/HTTP and follows standard OTEL_EXPORTER_OTLP_* env vars.
func InitTracing(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	if !envBool("FLEXINFER_OTEL_ENABLED", false) {
		return func(context.Context) error { return nil }, nil
	}

	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}

	resAttrs := []attribute.KeyValue{
		attribute.String("service.name", serviceName),
	}
	if ns := strings.TrimSpace(os.Getenv("FLEXINFER_OTEL_SERVICE_NAMESPACE")); ns != "" {
		resAttrs = append(resAttrs, attribute.String("service.namespace", ns))
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(resource.NewWithAttributes("", resAttrs...)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	return tp.Shutdown, nil
}

func envBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
