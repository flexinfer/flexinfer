package daemon

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// DaemonOTelMetrics holds OTel metric instruments for the daemon.
// All instruments use the global meter provider; when OTel is not configured
// the provider is a noop and recording is free.
type DaemonOTelMetrics struct {
	ToolCallCount    metric.Int64Counter
	ToolCallDuration metric.Float64Histogram
	ToolCallReqBytes metric.Int64Counter
	ToolCallResBytes metric.Int64Counter
	RBACDenied       metric.Int64Counter
	CacheOperations  metric.Int64Counter
	ServerHealth     metric.Int64UpDownCounter
}

// NewDaemonOTelMetrics registers OTel metrics via the global meter provider.
func NewDaemonOTelMetrics() *DaemonOTelMetrics {
	meter := otel.Meter("loomd")

	toolCallCount, _ := meter.Int64Counter("loom.daemon.tool_call.count",
		metric.WithDescription("Total tool calls processed by the daemon"),
		metric.WithUnit("{call}"),
	)
	toolCallDuration, _ := meter.Float64Histogram("loom.daemon.tool_call.duration",
		metric.WithDescription("Tool call duration in milliseconds"),
		metric.WithUnit("ms"),
	)
	toolCallReqBytes, _ := meter.Int64Counter("loom.daemon.tool_call.request_bytes",
		metric.WithDescription("Total request bytes sent to MCP servers"),
		metric.WithUnit("By"),
	)
	toolCallResBytes, _ := meter.Int64Counter("loom.daemon.tool_call.response_bytes",
		metric.WithDescription("Total response bytes received from MCP servers"),
		metric.WithUnit("By"),
	)
	rbacDenied, _ := meter.Int64Counter("loom.daemon.rbac.denied",
		metric.WithDescription("Total RBAC-denied tool calls"),
		metric.WithUnit("{denial}"),
	)
	cacheOperations, _ := meter.Int64Counter("loom.daemon.cache.operations",
		metric.WithDescription("Cache hit/miss operations"),
		metric.WithUnit("{operation}"),
	)
	serverHealth, _ := meter.Int64UpDownCounter("loom.daemon.server.health",
		metric.WithDescription("Server health gauge (1=healthy, -1=unhealthy transition)"),
		metric.WithUnit("{server}"),
	)

	return &DaemonOTelMetrics{
		ToolCallCount:    toolCallCount,
		ToolCallDuration: toolCallDuration,
		ToolCallReqBytes: toolCallReqBytes,
		ToolCallResBytes: toolCallResBytes,
		RBACDenied:       rbacDenied,
		CacheOperations:  cacheOperations,
		ServerHealth:     serverHealth,
	}
}

// RecordToolCall records a tool call count and duration.
func (m *DaemonOTelMetrics) RecordToolCall(ctx context.Context, server, tool, agentID, status, target string, durationMs int64) {
	attrs := metric.WithAttributes(
		attribute.String("server", server),
		attribute.String("tool", tool),
		attribute.String("agent_id", agentID),
		attribute.String("status", status),
	)
	m.ToolCallCount.Add(ctx, 1, attrs)

	durationAttrs := metric.WithAttributes(
		attribute.String("server", server),
		attribute.String("tool", tool),
		attribute.String("target", target),
	)
	m.ToolCallDuration.Record(ctx, float64(durationMs), durationAttrs)
}

// RecordBytes records request and response byte counts.
func (m *DaemonOTelMetrics) RecordBytes(ctx context.Context, server, tool string, reqBytes, resBytes int64) {
	attrs := metric.WithAttributes(
		attribute.String("server", server),
		attribute.String("tool", tool),
	)
	if reqBytes > 0 {
		m.ToolCallReqBytes.Add(ctx, reqBytes, attrs)
	}
	if resBytes > 0 {
		m.ToolCallResBytes.Add(ctx, resBytes, attrs)
	}
}

// RecordRBACDenied records an RBAC denial.
func (m *DaemonOTelMetrics) RecordRBACDenied(ctx context.Context, agentID, server, tool string) {
	m.RBACDenied.Add(ctx, 1, metric.WithAttributes(
		attribute.String("agent_id", agentID),
		attribute.String("server", server),
		attribute.String("tool", tool),
	))
}

// RecordCacheOp records a cache hit or miss.
func (m *DaemonOTelMetrics) RecordCacheOp(ctx context.Context, server, tool, result string) {
	m.CacheOperations.Add(ctx, 1, metric.WithAttributes(
		attribute.String("server", server),
		attribute.String("tool", tool),
		attribute.String("result", result),
	))
}

// RecordServerHealthChange records a server health transition.
func (m *DaemonOTelMetrics) RecordServerHealthChange(ctx context.Context, server, target string, delta int64) {
	m.ServerHealth.Add(ctx, delta, metric.WithAttributes(
		attribute.String("server", server),
		attribute.String("target", target),
	))
}

// RecordToolCallFromAudit is a convenience that derives OTel metrics from audit parameters.
func (m *DaemonOTelMetrics) RecordToolCallFromAudit(server, tool, agentID, status, target string, durationMs, reqBytes, resBytes int64) {
	ctx := context.Background()
	m.RecordToolCall(ctx, server, tool, agentID, status, target, durationMs)
	m.RecordBytes(ctx, server, tool, reqBytes, resBytes)
}
