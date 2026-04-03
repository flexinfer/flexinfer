package daemon

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func setupTestMeterProvider(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetMeterProvider(prev)
	})
	return reader
}

func collectMetricNames(t *testing.T, reader *sdkmetric.ManualReader) map[string]bool {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	names := make(map[string]bool)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
		}
	}
	return names
}

func findMetric(t *testing.T, reader *sdkmetric.ManualReader, name string) *metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for i := range sm.Metrics {
			if sm.Metrics[i].Name == name {
				return &sm.Metrics[i]
			}
		}
	}
	return nil
}

func TestDaemonOTelMetrics_InstrumentsEmitData(t *testing.T) {
	reader := setupTestMeterProvider(t)
	m := NewDaemonOTelMetrics()
	ctx := context.Background()

	m.RecordToolCall(ctx, "mcp-gitlab", "search_repos", "claude-code", "success", "local", 150)
	m.RecordBytes(ctx, "mcp-gitlab", "search_repos", 512, 2048)
	m.RecordRBACDenied(ctx, "untrusted", "admin-server", "delete_all")
	m.RecordCacheOp(ctx, "prometheus", "query", "hit")
	m.RecordCacheOp(ctx, "prometheus", "query", "miss")
	m.RecordServerHealthChange(ctx, "mcp-gitlab", "local", 1)

	names := collectMetricNames(t, reader)

	expected := []string{
		"loom.daemon.tool_call.count",
		"loom.daemon.tool_call.duration",
		"loom.daemon.tool_call.request_bytes",
		"loom.daemon.tool_call.response_bytes",
		"loom.daemon.rbac.denied",
		"loom.daemon.cache.operations",
		"loom.daemon.server.health",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing metric %q; got: %v", name, names)
		}
	}
}

func TestDaemonOTelMetrics_NoopSafe(t *testing.T) {
	// With no SDK provider configured, calls should not panic.
	m := NewDaemonOTelMetrics()
	ctx := context.Background()

	m.RecordToolCall(ctx, "s", "t", "a", "success", "local", 10)
	m.RecordBytes(ctx, "s", "t", 100, 200)
	m.RecordRBACDenied(ctx, "a", "s", "t")
	m.RecordCacheOp(ctx, "s", "t", "hit")
	m.RecordServerHealthChange(ctx, "s", "local", 1)
}

func TestDaemonOTelMetrics_ToolCallAttributes(t *testing.T) {
	reader := setupTestMeterProvider(t)
	m := NewDaemonOTelMetrics()
	ctx := context.Background()

	m.RecordToolCall(ctx, "mcp-test", "do_thing", "agent-1", "error", "hub", 42)

	met := findMetric(t, reader, "loom.daemon.tool_call.count")
	if met == nil {
		t.Fatal("loom.daemon.tool_call.count metric not found")
	}
	sum, ok := met.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("unexpected data type for count: %T", met.Data)
	}
	if len(sum.DataPoints) != 1 {
		t.Fatalf("expected 1 data point, got %d", len(sum.DataPoints))
	}
	dp := sum.DataPoints[0]
	if dp.Value != 1 {
		t.Fatalf("expected count=1, got %d", dp.Value)
	}
	attrMap := make(map[string]string)
	for _, kv := range dp.Attributes.ToSlice() {
		attrMap[string(kv.Key)] = kv.Value.AsString()
	}
	if attrMap["server"] != "mcp-test" {
		t.Errorf("server attr = %q, want mcp-test", attrMap["server"])
	}
	if attrMap["status"] != "error" {
		t.Errorf("status attr = %q, want error", attrMap["status"])
	}
	if attrMap["agent_id"] != "agent-1" {
		t.Errorf("agent_id attr = %q, want agent-1", attrMap["agent_id"])
	}
}

func TestDaemonOTelMetrics_CacheOperationLabels(t *testing.T) {
	reader := setupTestMeterProvider(t)
	m := NewDaemonOTelMetrics()
	ctx := context.Background()

	m.RecordCacheOp(ctx, "prometheus", "query", "hit")
	m.RecordCacheOp(ctx, "prometheus", "query", "miss")
	m.RecordCacheOp(ctx, "prometheus", "query", "hit")

	met := findMetric(t, reader, "loom.daemon.cache.operations")
	if met == nil {
		t.Fatal("loom.daemon.cache.operations metric not found")
	}
	sum, ok := met.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("unexpected data type: %T", met.Data)
	}
	if len(sum.DataPoints) != 2 {
		t.Fatalf("expected 2 data points (hit+miss), got %d", len(sum.DataPoints))
	}
	for _, dp := range sum.DataPoints {
		result := ""
		for _, kv := range dp.Attributes.ToSlice() {
			if kv.Key == attribute.Key("result") {
				result = kv.Value.AsString()
			}
		}
		switch result {
		case "hit":
			if dp.Value != 2 {
				t.Errorf("hit count = %d, want 2", dp.Value)
			}
		case "miss":
			if dp.Value != 1 {
				t.Errorf("miss count = %d, want 1", dp.Value)
			}
		default:
			t.Errorf("unexpected result label: %q", result)
		}
	}
}

func TestDaemonOTelMetrics_RecordToolCallFromAudit(t *testing.T) {
	reader := setupTestMeterProvider(t)
	m := NewDaemonOTelMetrics()

	m.RecordToolCallFromAudit("mcp-gitlab", "search", "agent-1", "success", "local", 42, 100, 200)

	names := collectMetricNames(t, reader)
	if !names["loom.daemon.tool_call.count"] {
		t.Error("missing tool_call.count after RecordToolCallFromAudit")
	}
	if !names["loom.daemon.tool_call.request_bytes"] {
		t.Error("missing tool_call.request_bytes after RecordToolCallFromAudit")
	}
	if !names["loom.daemon.tool_call.response_bytes"] {
		t.Error("missing tool_call.response_bytes after RecordToolCallFromAudit")
	}
}
