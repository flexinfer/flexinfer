package daemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/registry"
)

func TestDefaultTunnelManagerConfig(t *testing.T) {
	cfg := DefaultTunnelManagerConfig()

	if cfg.ConnectTimeout != 30*time.Second {
		t.Errorf("expected ConnectTimeout 30s, got %v", cfg.ConnectTimeout)
	}
	if cfg.ReconnectInterval != 5*time.Second {
		t.Errorf("expected ReconnectInterval 5s, got %v", cfg.ReconnectInterval)
	}
	if cfg.MaxReconnectInterval != 5*time.Minute {
		t.Errorf("expected MaxReconnectInterval 5m, got %v", cfg.MaxReconnectInterval)
	}
	if cfg.MaxReconnectAttempts != 0 {
		t.Errorf("expected MaxReconnectAttempts 0, got %v", cfg.MaxReconnectAttempts)
	}
}

func TestNewTunnelManager(t *testing.T) {
	cfg := DefaultTunnelManagerConfig()
	tm := NewTunnelManager(cfg, nil)

	if tm == nil {
		t.Fatal("expected TunnelManager, got nil")
	}
	if tm.TunnelCount() != 0 {
		t.Errorf("expected 0 tunnels, got %d", tm.TunnelCount())
	}
}

func TestTunnelManager_StartStop(t *testing.T) {
	cfg := DefaultTunnelManagerConfig()
	tm := NewTunnelManager(cfg, nil)

	ctx := context.Background()
	tm.Start(ctx)

	if tm.ctx == nil {
		t.Error("expected context to be set")
	}

	tm.Stop()
}

func TestTunnelManager_AddTunnel_NilSpec(t *testing.T) {
	cfg := DefaultTunnelManagerConfig()
	tm := NewTunnelManager(cfg, nil)
	ctx := context.Background()
	tm.Start(ctx)
	defer tm.Stop()

	err := tm.AddTunnel("test-server", nil, 16443, "localhost:6443")
	if err == nil {
		t.Error("expected error for nil SSH spec")
	}
}

func TestTunnelManager_AddTunnel_EmptyHost(t *testing.T) {
	cfg := DefaultTunnelManagerConfig()
	tm := NewTunnelManager(cfg, nil)
	ctx := context.Background()
	tm.Start(ctx)
	defer tm.Stop()

	sshSpec := &registry.SSHSpec{
		Host: "",
	}

	err := tm.AddTunnel("test-server", sshSpec, 16443, "localhost:6443")
	if err == nil {
		t.Error("expected error for empty SSH host")
	}
}

func TestTunnelManager_AddTunnel_Duplicate(t *testing.T) {
	cfg := DefaultTunnelManagerConfig()
	tm := NewTunnelManager(cfg, nil)
	ctx := context.Background()
	tm.Start(ctx)
	defer tm.Stop()

	sshSpec := &registry.SSHSpec{
		Host: "example.com:22",
		User: "testuser",
	}

	// First add should succeed (starts connection attempt in background)
	err := tm.AddTunnel("test-server", sshSpec, 16443, "localhost:6443")
	if err != nil {
		t.Fatalf("first add failed: %v", err)
	}

	// Second add should fail (duplicate)
	err = tm.AddTunnel("test-server", sshSpec, 16444, "localhost:6443")
	if err == nil {
		t.Error("expected error for duplicate tunnel")
	}

	if tm.TunnelCount() != 1 {
		t.Errorf("expected 1 tunnel, got %d", tm.TunnelCount())
	}
}

func TestTunnelManager_RemoveTunnel_NotFound(t *testing.T) {
	cfg := DefaultTunnelManagerConfig()
	tm := NewTunnelManager(cfg, nil)
	ctx := context.Background()
	tm.Start(ctx)
	defer tm.Stop()

	err := tm.RemoveTunnel("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent tunnel")
	}
}

func TestTunnelManager_GetStatus_NotFound(t *testing.T) {
	cfg := DefaultTunnelManagerConfig()
	tm := NewTunnelManager(cfg, nil)
	ctx := context.Background()
	tm.Start(ctx)
	defer tm.Stop()

	_, err := tm.GetStatus("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent tunnel")
	}
}

func TestTunnelManager_GetAllStatuses_Empty(t *testing.T) {
	cfg := DefaultTunnelManagerConfig()
	tm := NewTunnelManager(cfg, nil)
	ctx := context.Background()
	tm.Start(ctx)
	defer tm.Stop()

	statuses := tm.GetAllStatuses()
	if len(statuses) != 0 {
		t.Errorf("expected empty statuses, got %d", len(statuses))
	}
}

func TestTunnelManager_GetLocalAddr_NotFound(t *testing.T) {
	cfg := DefaultTunnelManagerConfig()
	tm := NewTunnelManager(cfg, nil)
	ctx := context.Background()
	tm.Start(ctx)
	defer tm.Stop()

	_, err := tm.GetLocalAddr("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent tunnel")
	}
}

func TestTunnelManager_ConnectedCount_Empty(t *testing.T) {
	cfg := DefaultTunnelManagerConfig()
	tm := NewTunnelManager(cfg, nil)
	ctx := context.Background()
	tm.Start(ctx)
	defer tm.Stop()

	count := tm.ConnectedCount()
	if count != 0 {
		t.Errorf("expected 0 connected, got %d", count)
	}
}

func TestTunnelState_Values(t *testing.T) {
	states := []TunnelState{
		TunnelStateDisconnected,
		TunnelStateConnecting,
		TunnelStateConnected,
		TunnelStateReconnecting,
		TunnelStateFailed,
	}

	for _, state := range states {
		if state == "" {
			t.Error("tunnel state should not be empty")
		}
	}
}

func TestManagedTunnel_GetStatus(t *testing.T) {
	mt := &managedTunnel{
		serverName: "test",
		localAddr:  "127.0.0.1:16443",
		status: TunnelStatus{
			ServerName:  "test",
			State:       TunnelStateConnected,
			LocalAddr:   "127.0.0.1:16443",
			ConnectedAt: time.Now().Add(-5 * time.Minute),
		},
	}

	status := mt.getStatus()
	if status.State != TunnelStateConnected {
		t.Errorf("expected Connected state, got %s", status.State)
	}
	if status.Uptime < 4*time.Minute {
		t.Errorf("expected uptime > 4m, got %v", status.Uptime)
	}
}

func TestManagedTunnel_SetState(t *testing.T) {
	mt := &managedTunnel{
		serverName: "test",
		status: TunnelStatus{
			ServerName: "test",
			State:      TunnelStateDisconnected,
		},
	}

	mt.setState(TunnelStateConnecting, nil)
	if mt.status.State != TunnelStateConnecting {
		t.Errorf("expected Connecting, got %s", mt.status.State)
	}

	testErr := fmt.Errorf("connection refused")
	mt.setState(TunnelStateFailed, testErr)
	if mt.status.LastError != "connection refused" {
		t.Errorf("expected error message, got %s", mt.status.LastError)
	}

	mt.setState(TunnelStateConnected, nil)
	if mt.status.State != TunnelStateConnected {
		t.Errorf("expected Connected, got %s", mt.status.State)
	}
	if mt.status.LastError != "" {
		t.Errorf("expected empty error on connect, got %s", mt.status.LastError)
	}
	if mt.status.ConnectedAt.IsZero() {
		t.Error("expected ConnectedAt to be set")
	}
}

func TestManagedTunnel_IncrementReconnect(t *testing.T) {
	mt := &managedTunnel{
		serverName: "test",
		status: TunnelStatus{
			ReconnectCount: 0,
		},
	}

	mt.incrementReconnect()
	if mt.status.ReconnectCount != 1 {
		t.Errorf("expected 1, got %d", mt.status.ReconnectCount)
	}

	mt.incrementReconnect()
	if mt.status.ReconnectCount != 2 {
		t.Errorf("expected 2, got %d", mt.status.ReconnectCount)
	}
}
