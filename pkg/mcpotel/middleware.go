package mcpotel

import (
	"context"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TracedToolHandler wraps an MCP ToolHandler with OpenTelemetry tracing.
// It creates a span per tool invocation and extracts common agent context
// attributes (agent_id, session_id, namespace) from the tool arguments.
//
// When the tracer is a noop (no OTEL_EXPORTER_OTLP_ENDPOINT), the wrapper
// adds negligible overhead — a nil-check per call.
func TracedToolHandler(tracer trace.Tracer, toolName string, handler mcp.ToolHandler) mcp.ToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		ctx, span := tracer.Start(ctx, toolName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String(AttrToolName, toolName)),
		)
		defer span.End()

		// Extract common agent context attributes from args.
		setArgAttr(span, args, "agent_id", AttrAgentID)
		setArgAttr(span, args, "session_id", AttrSessionID)
		setArgAttr(span, args, "namespace", AttrNamespace)

		result, err := handler(ctx, args)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return result, err
		}

		if result != nil && result.IsError {
			span.SetStatus(codes.Error, "tool returned error")
		}

		return result, nil
	}
}

// setArgAttr extracts a string value from args and sets it as a span attribute.
func setArgAttr(span trace.Span, args map[string]any, key, attr string) {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			span.SetAttributes(attribute.String(attr, s))
		} else if s, ok := v.(fmt.Stringer); ok {
			span.SetAttributes(attribute.String(attr, s.String()))
		}
	}
}
