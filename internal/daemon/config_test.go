package daemon

import (
	"testing"
	"time"
)

func TestResourceConfig_GetPoolConfig_Defaults(t *testing.T) {
	cfg := ResourceConfig{}
	maxIdle, maxOpen, idleTimeout := cfg.GetPoolConfig()

	if maxIdle != 2 {
		t.Errorf("maxIdle = %d, want 2", maxIdle)
	}
	if maxOpen != 10 {
		t.Errorf("maxOpen = %d, want 10", maxOpen)
	}
	if idleTimeout != 5*time.Minute {
		t.Errorf("idleTimeout = %v, want 5m", idleTimeout)
	}
}

func TestResourceConfig_GetPoolConfig_Custom(t *testing.T) {
	cfg := ResourceConfig{
		PoolMaxIdle:            4,
		PoolMaxOpen:            20,
		PoolIdleTimeoutMinutes: 10,
	}
	maxIdle, maxOpen, idleTimeout := cfg.GetPoolConfig()

	if maxIdle != 4 {
		t.Errorf("maxIdle = %d, want 4", maxIdle)
	}
	if maxOpen != 20 {
		t.Errorf("maxOpen = %d, want 20", maxOpen)
	}
	if idleTimeout != 10*time.Minute {
		t.Errorf("idleTimeout = %v, want 10m", idleTimeout)
	}
}

func TestResourceConfig_GetHubPoolConfig_Defaults(t *testing.T) {
	cfg := ResourceConfig{}
	maxIdle, maxOpen, idleTimeout := cfg.GetHubPoolConfig()

	if maxIdle != 2 {
		t.Errorf("maxIdle = %d, want 2", maxIdle)
	}
	if maxOpen != 10 {
		t.Errorf("maxOpen = %d, want 10", maxOpen)
	}
	if idleTimeout != 5*time.Minute {
		t.Errorf("idleTimeout = %v, want 5m", idleTimeout)
	}
}

func TestResourceConfig_GetHubPoolConfig_Custom(t *testing.T) {
	cfg := ResourceConfig{
		HubPoolMaxIdle:            3,
		HubPoolMaxOpen:            15,
		HubPoolIdleTimeoutMinutes: 8,
	}
	maxIdle, maxOpen, idleTimeout := cfg.GetHubPoolConfig()

	if maxIdle != 3 {
		t.Errorf("maxIdle = %d, want 3", maxIdle)
	}
	if maxOpen != 15 {
		t.Errorf("maxOpen = %d, want 15", maxOpen)
	}
	if idleTimeout != 8*time.Minute {
		t.Errorf("idleTimeout = %v, want 8m", idleTimeout)
	}
}

func TestResourceConfig_GetRefreshConcurrency_Default(t *testing.T) {
	cfg := ResourceConfig{}
	if got := cfg.GetRefreshConcurrency(); got != 6 {
		t.Errorf("GetRefreshConcurrency() = %d, want 6", got)
	}
}

func TestResourceConfig_GetRefreshConcurrency_Custom(t *testing.T) {
	cfg := ResourceConfig{RefreshConcurrency: 12}
	if got := cfg.GetRefreshConcurrency(); got != 12 {
		t.Errorf("GetRefreshConcurrency() = %d, want 12", got)
	}
}

func TestHealthConfig_ToHealthMonitorConfig_Defaults(t *testing.T) {
	cfg := HealthConfig{}
	hmc := cfg.ToHealthMonitorConfig()

	defaults := DefaultHealthMonitorConfig()
	if hmc.CheckInterval != defaults.CheckInterval {
		t.Errorf("CheckInterval = %v, want %v", hmc.CheckInterval, defaults.CheckInterval)
	}
	if hmc.HealthyThreshold != defaults.HealthyThreshold {
		t.Errorf("HealthyThreshold = %d, want %d", hmc.HealthyThreshold, defaults.HealthyThreshold)
	}
	if hmc.UnhealthyThreshold != defaults.UnhealthyThreshold {
		t.Errorf("UnhealthyThreshold = %d, want %d", hmc.UnhealthyThreshold, defaults.UnhealthyThreshold)
	}
	if hmc.RestartThreshold != defaults.RestartThreshold {
		t.Errorf("RestartThreshold = %d, want %d", hmc.RestartThreshold, defaults.RestartThreshold)
	}
	if hmc.MaxRestarts != defaults.MaxRestarts {
		t.Errorf("MaxRestarts = %d, want %d", hmc.MaxRestarts, defaults.MaxRestarts)
	}
	if hmc.RestartCooldown != defaults.RestartCooldown {
		t.Errorf("RestartCooldown = %v, want %v", hmc.RestartCooldown, defaults.RestartCooldown)
	}
}

func TestHealthConfig_ToHealthMonitorConfig_Custom(t *testing.T) {
	cfg := HealthConfig{
		CheckIntervalSeconds:   60,
		HealthyThreshold:       5,
		UnhealthyThreshold:     10,
		RestartThreshold:       8,
		MaxRestarts:            5,
		RestartCooldownMinutes: 10,
	}
	hmc := cfg.ToHealthMonitorConfig()

	if hmc.CheckInterval != 60*time.Second {
		t.Errorf("CheckInterval = %v, want 60s", hmc.CheckInterval)
	}
	if hmc.HealthyThreshold != 5 {
		t.Errorf("HealthyThreshold = %d, want 5", hmc.HealthyThreshold)
	}
	if hmc.UnhealthyThreshold != 10 {
		t.Errorf("UnhealthyThreshold = %d, want 10", hmc.UnhealthyThreshold)
	}
	if hmc.RestartThreshold != 8 {
		t.Errorf("RestartThreshold = %d, want 8", hmc.RestartThreshold)
	}
	if hmc.MaxRestarts != 5 {
		t.Errorf("MaxRestarts = %d, want 5", hmc.MaxRestarts)
	}
	if hmc.RestartCooldown != 10*time.Minute {
		t.Errorf("RestartCooldown = %v, want 10m", hmc.RestartCooldown)
	}
}

func TestHealthConfig_ToHealthMonitorConfig_Nil(t *testing.T) {
	var cfg *HealthConfig
	hmc := cfg.ToHealthMonitorConfig()

	defaults := DefaultHealthMonitorConfig()
	if hmc.CheckInterval != defaults.CheckInterval {
		t.Errorf("nil config should return defaults, got CheckInterval = %v", hmc.CheckInterval)
	}
}
