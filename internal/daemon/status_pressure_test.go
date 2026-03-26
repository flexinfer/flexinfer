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
