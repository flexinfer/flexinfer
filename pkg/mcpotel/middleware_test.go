package mcpotel

import (
	"context"
	"fmt"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestTracer returns a tracer backed by an in-memory span recorder.
func newTestTracer() (*tracetest.SpanRecorder, *sdktrace.TracerProvider) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	return sr, tp
}

func TestTracedToolHandler_Success(t *testing.T) {
	sr, tp := newTestTracer()
	tracer := tp.Tracer("test")

	inner := func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return mcp.TextResult("ok"), nil
	}

	handler := TracedToolHandler(tracer, "my_tool", inner)
	result, err := handler(context.Background(), map[string]any{"agent_id": "a1"})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	// Force flush
	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := sr.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "my_tool", spans[0].Name())
}

func TestTracedToolHandler_Error(t *testing.T) {
	sr, tp := newTestTracer()
	tracer := tp.Tracer("test")

	expectedErr := fmt.Errorf("something broke")
	inner := func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return nil, expectedErr
	}

	handler := TracedToolHandler(tracer, "fail_tool", inner)
	result, err := handler(context.Background(), map[string]any{})

	assert.ErrorIs(t, err, expectedErr)
	assert.Nil(t, result)

	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := sr.Ended()
	require.Len(t, spans, 1)

	// Span should record the error
	events := spans[0].Events()
	hasError := false
	for _, ev := range events {
		if ev.Name == "exception" {
			hasError = true
		}
	}
	assert.True(t, hasError, "expected span to record an error event")
}

func TestTracedToolHandler_ToolError(t *testing.T) {
	sr, tp := newTestTracer()
	tracer := tp.Tracer("test")

	inner := func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true}, nil
	}

	handler := TracedToolHandler(tracer, "tool_err", inner)
	result, err := handler(context.Background(), map[string]any{})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError)

	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := sr.Ended()
	require.Len(t, spans, 1)
	// Span status should be error
	assert.Equal(t, "Error", spans[0].Status().Code.String())
}

func TestTracedToolHandler_Attributes(t *testing.T) {
	sr, tp := newTestTracer()
	tracer := tp.Tracer("test")

	inner := func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return mcp.TextResult("ok"), nil
	}

	handler := TracedToolHandler(tracer, "attr_tool", inner)
	_, err := handler(context.Background(), map[string]any{
		"agent_id":   "agent-007",
		"session_id": "sess-123",
		"namespace":  "my/ns",
	})
	require.NoError(t, err)
	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := sr.Ended()
	require.Len(t, spans, 1)

	attrs := make(map[string]string)
	for _, a := range spans[0].Attributes() {
		attrs[string(a.Key)] = a.Value.AsString()
	}

	assert.Equal(t, "attr_tool", attrs[AttrToolName])
	assert.Equal(t, "agent-007", attrs[AttrAgentID])
	assert.Equal(t, "sess-123", attrs[AttrSessionID])
	assert.Equal(t, "my/ns", attrs[AttrNamespace])
}

func TestTracedToolHandler_MissingAttributes(t *testing.T) {
	sr, tp := newTestTracer()
	tracer := tp.Tracer("test")

	inner := func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return mcp.TextResult("ok"), nil
	}

	// Args with no context fields — should not panic
	handler := TracedToolHandler(tracer, "no_attrs", inner)
	_, err := handler(context.Background(), map[string]any{"query": "test"})
	require.NoError(t, err)
	require.NoError(t, tp.ForceFlush(context.Background()))

	spans := sr.Ended()
	require.Len(t, spans, 1)

	// Only tool name attribute should be present
	attrs := make(map[string]string)
	for _, a := range spans[0].Attributes() {
		attrs[string(a.Key)] = a.Value.AsString()
	}
	assert.Equal(t, "no_attrs", attrs[AttrToolName])
	assert.Empty(t, attrs[AttrAgentID])
	assert.Empty(t, attrs[AttrSessionID])
}

func TestTracedToolHandler_NilArgs(t *testing.T) {
	_, tp := newTestTracer()
	tracer := tp.Tracer("test")

	inner := func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return mcp.TextResult("ok"), nil
	}

	// nil args — should not panic
	handler := TracedToolHandler(tracer, "nil_args", inner)
	result, err := handler(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}
