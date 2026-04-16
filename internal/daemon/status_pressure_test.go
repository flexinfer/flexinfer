package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	kitregistry "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/registry"
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/process"
)

func TestHandleStatus_IncludesPoolPressureSnapshots(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.procMgr = process.NewManager(nil, "codex")
	d.registry = &kitregistry.Registry{Servers: []*kitregistry.Server{{Name: "local_srv"}, {Name: "hub_srv"}}}
	d.fileCfg.Resources.PoolMaxIdle = 1
	d.fileCfg.Resources.PoolMaxOpen = 4
	d.fileCfg.Resources.HubPoolMaxIdle = 2
	d.fileCfg.Resources.HubPoolMaxOpen = 2
	d.pool = newPressureTestPool(t, 4, 3, 1)
	d.hubPool = newPressureTestPool(t, 2, 2, 0)
	defer func() {
		_ = d.pool.Close()
		_ = d.hubPool.Close()
	}()

	msg := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "status-pressure", Method: "loom/status"}
	resp, err := d.handleStatus(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	var status struct {
		ActiveConns int `json:"activeConns"`
		IdleConns   int `json:"idleConns"`
		LocalPool   *struct {
			ActiveConns int     `json:"activeConns"`
			IdleConns   int     `json:"idleConns"`
			MaxIdle     int     `json:"maxIdle"`
			MaxOpen     int     `json:"maxOpen"`
			PressurePct float64 `json:"pressurePct"`
			AtCapacity  bool    `json:"atCapacity"`
		} `json:"localPool"`
		HubPool *struct {
			ActiveConns int     `json:"activeConns"`
			IdleConns   int     `json:"idleConns"`
			MaxIdle     int     `json:"maxIdle"`
			MaxOpen     int     `json:"maxOpen"`
			PressurePct float64 `json:"pressurePct"`
			AtCapacity  bool    `json:"atCapacity"`
		} `json:"hubPool"`
	}
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}

	if status.ActiveConns != 2 {
		t.Fatalf("activeConns = %d, want 2", status.ActiveConns)
	}
	if status.IdleConns != 1 {
		t.Fatalf("idleConns = %d, want 1", status.IdleConns)
	}
	if status.LocalPool == nil {
		t.Fatal("localPool missing from status")
	}
	if status.LocalPool.ActiveConns != 2 || status.LocalPool.IdleConns != 1 {
		t.Fatalf("localPool = %+v, want active=2 idle=1", status.LocalPool)
	}
	if status.LocalPool.MaxIdle != 1 || status.LocalPool.MaxOpen != 4 {
		t.Fatalf("localPool limits = %+v, want maxIdle=1 maxOpen=4", status.LocalPool)
	}
	if status.LocalPool.PressurePct < 49.9 || status.LocalPool.PressurePct > 50.1 {
		t.Fatalf("localPool pressurePct = %.2f, want 50.0", status.LocalPool.PressurePct)
	}
	if status.LocalPool.AtCapacity {
		t.Fatal("localPool should not be at capacity")
	}
	if status.HubPool == nil {
		t.Fatal("hubPool missing from status")
	}
	if status.HubPool.ActiveConns != 2 || status.HubPool.IdleConns != 0 {
		t.Fatalf("hubPool = %+v, want active=2 idle=0", status.HubPool)
	}
	if status.HubPool.MaxIdle != 2 || status.HubPool.MaxOpen != 2 {
		t.Fatalf("hubPool limits = %+v, want maxIdle=2 maxOpen=2", status.HubPool)
	}
	if status.HubPool.PressurePct != 100 {
		t.Fatalf("hubPool pressurePct = %.2f, want 100.0", status.HubPool.PressurePct)
	}
	if !status.HubPool.AtCapacity {
		t.Fatal("hubPool should be at capacity")
	}
}

func TestHandleStatus_IncludesHealthAndObservability(t *testing.T) {
	t.Setenv("MCP_LOG_FORMAT", "text")

	d := newCallPipelineTestDaemon()
	d.procMgr = process.NewManager(nil, "codex")
	d.registry = &kitregistry.Registry{Servers: []*kitregistry.Server{{Name: "alpha"}, {Name: "beta"}}}
	d.pool = newPressureTestPool(t, 2, 1, 1)
	d.hubPool = newPressureTestPool(t, 2, 0, 0)
	d.healthMonitor = &HealthMonitor{
		statuses: map[string]*ServerHealthStatus{
			"alpha": {
				Name:             "alpha",
				Healthy:          false,
				ConsecutiveFails: 3,
				TotalChecks:      8,
				TotalFailures:    3,
				AvgLatencyMs:     88.4,
				LastError:        "timeout",
				RestartCount:     2,
			},
			"beta": {
				Name:             "beta",
				Healthy:          true,
				ConsecutiveFails: 0,
				TotalChecks:      8,
				TotalFailures:    0,
				AvgLatencyMs:     11.2,
			},
		},
	}
	d.fileCfg.OTel.Endpoint = "http://localhost:4317"
	d.otelRuntimeState.Configured = true
	d.otelRuntimeState.Enabled = true
	d.otelRuntimeState.InitError = "collector unavailable"
	defer func() {
		_ = d.pool.Close()
		_ = d.hubPool.Close()
	}()

	msg := &mcp.Message{JSONRPC: mcp.JSONRPCVersion, ID: "status-health", Method: "loom/status"}
	resp, err := d.handleStatus(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	var status struct {
		DrainReady bool `json:"drainReady"`
		Health     struct {
			DegradedServers []string `json:"degraded_servers"`
			Servers         map[string]struct {
				Healthy      bool    `json:"healthy"`
				Ready        bool    `json:"ready"`
				RestartCount int     `json:"restart_count"`
				AvgLatencyMs float64 `json:"avg_latency_ms"`
				LastError    string  `json:"last_error"`
			} `json:"servers"`
		} `json:"health"`
		Observability struct {
			OTLPEndpoint string   `json:"otlp_endpoint"`
			LogFormat    string   `json:"log_format"`
			Warnings     []string `json:"warnings"`
		} `json:"observability"`
	}
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}

	if !status.DrainReady {
		t.Fatal("expected drainReady=true when no active RPCs")
	}
	if len(status.Health.DegradedServers) != 1 || status.Health.DegradedServers[0] != "alpha" {
		t.Fatalf("degraded servers = %#v, want [alpha]", status.Health.DegradedServers)
	}
	alpha := status.Health.Servers["alpha"]
	if alpha.RestartCount != 2 {
		t.Fatalf("alpha.restart_count = %d, want 2", alpha.RestartCount)
	}
	if alpha.Ready {
		t.Fatal("alpha.ready = true, want false")
	}
	if alpha.AvgLatencyMs < 88 || alpha.AvgLatencyMs > 89 {
		t.Fatalf("alpha.avg_latency_ms = %f, want ~88.4", alpha.AvgLatencyMs)
	}
	if alpha.LastError != "timeout" {
		t.Fatalf("alpha.last_error = %q, want timeout", alpha.LastError)
	}
	beta := status.Health.Servers["beta"]
	if !beta.Ready {
		t.Fatal("beta.ready = false, want true")
	}
	if status.Observability.OTLPEndpoint != "http://localhost:4317" {
		t.Fatalf("otlp_endpoint = %q, want configured endpoint", status.Observability.OTLPEndpoint)
	}
	if status.Observability.LogFormat != "text" {
		t.Fatalf("log_format = %q, want text", status.Observability.LogFormat)
	}
	if len(status.Observability.Warnings) == 0 {
		t.Fatal("expected observability warnings")
	}
}

func newPressureTestPool(t *testing.T, maxOpen, active, idle int) *pool.Pool {
	t.Helper()

	if idle > active {
		t.Fatalf("idle (%d) cannot exceed active (%d)", idle, active)
	}

	p := pool.New(pool.Config{
		MaxIdle:     maxOpen,
		MaxOpen:     maxOpen,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			return &fakeTransport{}, nil
		},
	})

	ctx := context.Background()
	conns := make([]*pool.Conn, 0, active)
	for i := 0; i < active; i++ {
		conn, err := p.Get(ctx, "server")
		if err != nil {
			t.Fatalf("seed connection %d: %v", i, err)
		}
		conns = append(conns, conn)
	}
	for i := 0; i < idle; i++ {
		p.Put(conns[i])
	}

	return p
}
