package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/registry"
)

func TestHandleOTelStatus_DerivesServerCoverageFromRegistry(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317")
	t.Setenv("MCP_LOG_FORMAT", "json")

	d := &Daemon{
		registry: &registry.Registry{
			Servers: []*registry.Server{
				{Name: "alpha"},
				{Name: "beta"},
				{Name: "gamma"},
			},
		},
	}

	msg, err := mcp.NewRequest(1, "loom/otel-status", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := d.handleOTelStatus(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleOTelStatus: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("expected success response, got %+v", resp)
	}

	var got struct {
		Endpoint        string `json:"otlp_endpoint"`
		OTLPConfigured  bool   `json:"otlp_configured"`
		LogFormat       string `json:"log_format"`
		JSONLogsEnabled bool   `json:"json_logs_enabled"`
		TracedServers   int    `json:"traced_servers"`
		TotalServers    int    `json:"total_servers"`
		TraceCoverage   string `json:"trace_coverage"`
	}
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if got.Endpoint != "http://localhost:4317" {
		t.Fatalf("otlp_endpoint = %q, want %q", got.Endpoint, "http://localhost:4317")
	}
	if !got.OTLPConfigured {
		t.Fatalf("otlp_configured = false, want true")
	}
	if got.LogFormat != "json" {
		t.Fatalf("log_format = %q, want json", got.LogFormat)
	}
	if !got.JSONLogsEnabled {
		t.Fatalf("json_logs_enabled = false, want true")
	}
	if got.TotalServers != 3 {
		t.Fatalf("total_servers = %d, want 3", got.TotalServers)
	}
	if got.TracedServers != got.TotalServers {
		t.Fatalf("traced_servers = %d, want %d", got.TracedServers, got.TotalServers)
	}
	if got.TraceCoverage != "100%" {
		t.Fatalf("trace_coverage = %q, want 100%%", got.TraceCoverage)
	}
}
