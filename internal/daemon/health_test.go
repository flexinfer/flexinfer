package daemon

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestDefaultHealthMonitorConfig(t *testing.T) {
	cfg := DefaultHealthMonitorConfig()

	if cfg.CheckInterval != 30*time.Second {
		t.Errorf("CheckInterval = %v, want 30s", cfg.CheckInterval)
	}
	if cfg.DeepProbeInterval != 5*time.Minute {
		t.Errorf("DeepProbeInterval = %v, want 5m", cfg.DeepProbeInterval)
	}
	if cfg.HealthyThreshold != 2 {
		t.Errorf("HealthyThreshold = %d, want 2", cfg.HealthyThreshold)
	}
	if cfg.UnhealthyThreshold != 3 {
		t.Errorf("UnhealthyThreshold = %d, want 3", cfg.UnhealthyThreshold)
	}
	if cfg.RestartThreshold != 3 {
		t.Errorf("RestartThreshold = %d, want 3", cfg.RestartThreshold)
	}
	if cfg.MaxRestarts != 3 {
		t.Errorf("MaxRestarts = %d, want 3", cfg.MaxRestarts)
	}
	if cfg.RestartCooldown != 5*time.Minute {
		t.Errorf("RestartCooldown = %v, want 5m", cfg.RestartCooldown)
	}
}

func TestServerHealthStatus_Fields(t *testing.T) {
	now := time.Now()
	status := ServerHealthStatus{
		Name:              "test-server",
		Healthy:           true,
		LastCheck:         now,
		LastHealthy:       now,
		ConsecutiveFails:  0,
		TotalChecks:       10,
		TotalFailures:     2,
		AvgLatencyMs:      50.5,
		LastError:         "",
		RestartCount:      1,
		LastRestart:       now.Add(-time.Hour),
		AutoRestartFailed: false,
	}

	if status.Name != "test-server" {
		t.Error("Name not set correctly")
	}
	if !status.Healthy {
		t.Error("Healthy should be true")
	}
	if status.TotalChecks != 10 {
		t.Errorf("TotalChecks = %d, want 10", status.TotalChecks)
	}
	if status.TotalFailures != 2 {
		t.Errorf("TotalFailures = %d, want 2", status.TotalFailures)
	}
	if status.AvgLatencyMs != 50.5 {
		t.Errorf("AvgLatencyMs = %f, want 50.5", status.AvgLatencyMs)
	}
}

func TestHealthMonitor_GetStatus_NotFound(t *testing.T) {
	// Create a minimal health monitor without a full daemon
	h := &HealthMonitor{
		statuses: make(map[string]*ServerHealthStatus),
	}

	status := h.GetStatus("nonexistent")
	if status != nil {
		t.Error("GetStatus should return nil for nonexistent server")
	}
}

func TestHealthMonitor_GetStatus_Found(t *testing.T) {
	h := &HealthMonitor{
		statuses: make(map[string]*ServerHealthStatus),
	}

	// Add a status
	h.statuses["test-server"] = &ServerHealthStatus{
		Name:    "test-server",
		Healthy: true,
	}

	status := h.GetStatus("test-server")
	if status == nil {
		t.Fatal("GetStatus should return status for existing server")
	}
	if status.Name != "test-server" {
		t.Errorf("Name = %s, want test-server", status.Name)
	}
	if !status.Healthy {
		t.Error("Healthy should be true")
	}
}

func TestHealthMonitor_GetStatus_ReturnsCopy(t *testing.T) {
	h := &HealthMonitor{
		statuses: make(map[string]*ServerHealthStatus),
	}

	original := &ServerHealthStatus{
		Name:    "test-server",
		Healthy: true,
	}
	h.statuses["test-server"] = original

	// Get status and modify the copy
	status := h.GetStatus("test-server")
	status.Healthy = false

	// Original should be unchanged
	if !original.Healthy {
		t.Error("GetStatus should return a copy, not the original")
	}
}

func TestHealthMonitor_GetAllStatuses_Empty(t *testing.T) {
	h := &HealthMonitor{
		statuses: make(map[string]*ServerHealthStatus),
	}

	statuses := h.GetAllStatuses()
	if len(statuses) != 0 {
		t.Errorf("GetAllStatuses should return empty map, got %d", len(statuses))
	}
}

func TestHealthMonitor_GetAllStatuses_Multiple(t *testing.T) {
	h := &HealthMonitor{
		statuses: make(map[string]*ServerHealthStatus),
	}

	h.statuses["server1"] = &ServerHealthStatus{Name: "server1", Healthy: true}
	h.statuses["server2"] = &ServerHealthStatus{Name: "server2", Healthy: false}
	h.statuses["server3"] = &ServerHealthStatus{Name: "server3", Healthy: true}

	statuses := h.GetAllStatuses()
	if len(statuses) != 3 {
		t.Errorf("GetAllStatuses returned %d statuses, want 3", len(statuses))
	}

	// Verify all servers are present
	for _, name := range []string{"server1", "server2", "server3"} {
		if _, ok := statuses[name]; !ok {
			t.Errorf("GetAllStatuses missing %s", name)
		}
	}
}

func TestHealthMonitor_GetAllStatuses_ReturnsCopies(t *testing.T) {
	h := &HealthMonitor{
		statuses: make(map[string]*ServerHealthStatus),
	}

	original := &ServerHealthStatus{Name: "server1", Healthy: true}
	h.statuses["server1"] = original

	statuses := h.GetAllStatuses()
	statuses["server1"].Healthy = false

	// Original should be unchanged
	if !original.Healthy {
		t.Error("GetAllStatuses should return copies")
	}
}

func TestHealthMonitor_ResetRestartCount(t *testing.T) {
	h := &HealthMonitor{
		statuses: make(map[string]*ServerHealthStatus),
	}

	h.statuses["test-server"] = &ServerHealthStatus{
		Name:              "test-server",
		RestartCount:      5,
		AutoRestartFailed: true,
	}

	h.ResetRestartCount("test-server")

	status := h.statuses["test-server"]
	if status.RestartCount != 0 {
		t.Errorf("RestartCount = %d, want 0", status.RestartCount)
	}
	if status.AutoRestartFailed {
		t.Error("AutoRestartFailed should be false after reset")
	}
}

func TestHealthMonitor_ResetRestartCount_NotFound(t *testing.T) {
	h := &HealthMonitor{
		statuses: make(map[string]*ServerHealthStatus),
	}

	// Should not panic for nonexistent server
	h.ResetRestartCount("nonexistent")
}

func TestHealthMonitorConfig_Fields(t *testing.T) {
	cfg := HealthMonitorConfig{
		CheckInterval:      10 * time.Second,
		DeepProbeInterval:  3 * time.Minute,
		HealthyThreshold:   5,
		UnhealthyThreshold: 10,
		RestartThreshold:   15,
		MaxRestarts:        20,
		RestartCooldown:    time.Hour,
	}

	if cfg.CheckInterval != 10*time.Second {
		t.Error("CheckInterval not set correctly")
	}
	if cfg.DeepProbeInterval != 3*time.Minute {
		t.Error("DeepProbeInterval not set correctly")
	}
	if cfg.HealthyThreshold != 5 {
		t.Error("HealthyThreshold not set correctly")
	}
	if cfg.UnhealthyThreshold != 10 {
		t.Error("UnhealthyThreshold not set correctly")
	}
	if cfg.RestartThreshold != 15 {
		t.Error("RestartThreshold not set correctly")
	}
	if cfg.MaxRestarts != 20 {
		t.Error("MaxRestarts not set correctly")
	}
	if cfg.RestartCooldown != time.Hour {
		t.Error("RestartCooldown not set correctly")
	}
}

// --- Pool-based probe tests (TD-PERF-01 / DEBT-005) ---

func TestNeedsDeepProbe_NilStatus(t *testing.T) {
	h := &HealthMonitor{deepProbeInterval: 5 * time.Minute}
	if !h.needsDeepProbe(nil) {
		t.Fatal("nil status should always require deep probe")
	}
}

func TestNeedsDeepProbe_ZeroLastDeepProbe(t *testing.T) {
	h := &HealthMonitor{deepProbeInterval: 5 * time.Minute}
	status := &ServerHealthStatus{Name: "s"}
	if !h.needsDeepProbe(status) {
		t.Fatal("zero LastDeepProbe should require deep probe")
	}
}

func TestNeedsDeepProbe_IntervalNotElapsed(t *testing.T) {
	h := &HealthMonitor{deepProbeInterval: 5 * time.Minute}
	status := &ServerHealthStatus{
		Name:          "s",
		LastDeepProbe: time.Now().Add(-2 * time.Minute),
	}
	if h.needsDeepProbe(status) {
		t.Fatal("deep probe should not be needed within interval")
	}
}

func TestNeedsDeepProbe_IntervalElapsed(t *testing.T) {
	h := &HealthMonitor{deepProbeInterval: 5 * time.Minute}
	status := &ServerHealthStatus{
		Name:          "s",
		LastDeepProbe: time.Now().Add(-6 * time.Minute),
	}
	if !h.needsDeepProbe(status) {
		t.Fatal("deep probe should be needed after interval elapsed")
	}
}

func TestNeedsDeepProbe_ZeroInterval_AlwaysDeep(t *testing.T) {
	h := &HealthMonitor{deepProbeInterval: 0}
	status := &ServerHealthStatus{
		Name:          "s",
		LastDeepProbe: time.Now(),
	}
	if !h.needsDeepProbe(status) {
		t.Fatal("zero deepProbeInterval should always require deep probe")
	}
}

func TestNewHealthMonitor_DeepProbeIntervalWired(t *testing.T) {
	cfg := HealthMonitorConfig{
		CheckInterval:     30 * time.Second,
		DeepProbeInterval: 10 * time.Minute,
	}
	d := &Daemon{
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		metrics: NewMetrics(),
	}
	h := NewHealthMonitor(d, cfg)
	if h.deepProbeInterval != 10*time.Minute {
		t.Fatalf("deepProbeInterval = %v, want 10m", h.deepProbeInterval)
	}
}
