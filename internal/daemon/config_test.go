package daemon

import (
	"strings"
	"testing"
	"time"
)

func TestResourceConfig_GetPoolConfig_Defaults(t *testing.T) {
	cfg := ResourceConfig{}
	maxIdle, maxOpen, idleTimeout, waitTimeout := cfg.GetPoolConfig()

	if maxIdle != 2 {
		t.Errorf("maxIdle = %d, want 2", maxIdle)
	}
	if maxOpen != 25 {
		t.Errorf("maxOpen = %d, want 25", maxOpen)
	}
	if idleTimeout != 5*time.Minute {
		t.Errorf("idleTimeout = %v, want 5m", idleTimeout)
	}
	if waitTimeout != 5*time.Second {
		t.Errorf("waitTimeout = %v, want 5s", waitTimeout)
	}
}

func TestResourceConfig_GetPoolConfig_Custom(t *testing.T) {
	cfg := ResourceConfig{
		PoolMaxIdle:            4,
		PoolMaxOpen:            20,
		PoolIdleTimeoutMinutes: 10,
		PoolWaitTimeout:        "10s",
	}
	maxIdle, maxOpen, idleTimeout, waitTimeout := cfg.GetPoolConfig()

	if maxIdle != 4 {
		t.Errorf("maxIdle = %d, want 4", maxIdle)
	}
	if maxOpen != 20 {
		t.Errorf("maxOpen = %d, want 20", maxOpen)
	}
	if idleTimeout != 10*time.Minute {
		t.Errorf("idleTimeout = %v, want 10m", idleTimeout)
	}
	if waitTimeout != 10*time.Second {
		t.Errorf("waitTimeout = %v, want 10s", waitTimeout)
	}
}

func TestResourceConfig_GetHubPoolConfig_Defaults(t *testing.T) {
	cfg := ResourceConfig{}
	maxIdle, maxOpen, idleTimeout, waitTimeout := cfg.GetHubPoolConfig()

	if maxIdle != 2 {
		t.Errorf("maxIdle = %d, want 2", maxIdle)
	}
	if maxOpen != 25 {
		t.Errorf("maxOpen = %d, want 25", maxOpen)
	}
	if idleTimeout != 5*time.Minute {
		t.Errorf("idleTimeout = %v, want 5m", idleTimeout)
	}
	if waitTimeout != 5*time.Second {
		t.Errorf("waitTimeout = %v, want 5s", waitTimeout)
	}
}

func TestResourceConfig_GetHubPoolConfig_Custom(t *testing.T) {
	cfg := ResourceConfig{
		HubPoolMaxIdle:            3,
		HubPoolMaxOpen:            15,
		HubPoolIdleTimeoutMinutes: 8,
	}
	maxIdle, maxOpen, idleTimeout, waitTimeout := cfg.GetHubPoolConfig()

	if maxIdle != 3 {
		t.Errorf("maxIdle = %d, want 3", maxIdle)
	}
	if maxOpen != 15 {
		t.Errorf("maxOpen = %d, want 15", maxOpen)
	}
	if idleTimeout != 8*time.Minute {
		t.Errorf("idleTimeout = %v, want 8m", idleTimeout)
	}
	if waitTimeout != 5*time.Second {
		t.Errorf("waitTimeout = %v, want 5s", waitTimeout)
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
	if hmc.DeepProbeInterval != defaults.DeepProbeInterval {
		t.Errorf("DeepProbeInterval = %v, want %v", hmc.DeepProbeInterval, defaults.DeepProbeInterval)
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
		CheckIntervalSeconds:     60,
		DeepProbeIntervalMinutes: 10,
		HealthyThreshold:         5,
		UnhealthyThreshold:       10,
		RestartThreshold:         8,
		MaxRestarts:              5,
		RestartCooldownMinutes:   10,
	}
	hmc := cfg.ToHealthMonitorConfig()

	if hmc.CheckInterval != 60*time.Second {
		t.Errorf("CheckInterval = %v, want 60s", hmc.CheckInterval)
	}
	if hmc.DeepProbeInterval != 10*time.Minute {
		t.Errorf("DeepProbeInterval = %v, want 10m", hmc.DeepProbeInterval)
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

func TestHealthConfig_ToHealthMonitorConfig_NegativeDeepProbeDisables(t *testing.T) {
	cfg := HealthConfig{DeepProbeIntervalMinutes: -1}
	hmc := cfg.ToHealthMonitorConfig()
	if hmc.DeepProbeInterval != 0 {
		t.Errorf("DeepProbeInterval = %v, want 0 (always deep probe)", hmc.DeepProbeInterval)
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

// --- Config validation tests ---

func TestParseAndValidateConfig_ValidConfig(t *testing.T) {
	yaml := []byte(`
hub:
  url: "wss://example.com/ws"
  enabled: true
debug: false
`)
	cfg, warnings, err := parseAndValidateConfig(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	if cfg.Hub.URL != "wss://example.com/ws" {
		t.Errorf("Hub.URL = %q, want %q", cfg.Hub.URL, "wss://example.com/ws")
	}
}

func TestParseAndValidateConfig_UnknownKeyWarning(t *testing.T) {
	yaml := []byte(`
hub:
  url: "wss://example.com/ws"
  hub_fallbackk: true
debug: false
`)
	cfg, warnings, err := parseAndValidateConfig(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected a warning for unknown key, got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "unknown key") && strings.Contains(w, "hub_fallbackk") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning mentioning 'hub_fallbackk', got %v", warnings)
	}
	// Config should still parse successfully with known fields.
	if cfg.Hub.URL != "wss://example.com/ws" {
		t.Errorf("Hub.URL = %q after lenient parse, want %q", cfg.Hub.URL, "wss://example.com/ws")
	}
}

func TestParseAndValidateConfig_TopLevelUnknownKey(t *testing.T) {
	yaml := []byte(`
hub:
  url: "wss://example.com/ws"
bogus_top_level: 42
`)
	_, warnings, err := parseAndValidateConfig(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected a warning for top-level unknown key, got none")
	}
}

func TestParseAndValidateConfig_InvalidAuthType(t *testing.T) {
	yaml := []byte(`
http:
  auth:
    type: "kerberos"
`)
	cfg, warnings, err := parseAndValidateConfig(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTP.Auth.Type != "kerberos" {
		t.Errorf("HTTP.Auth.Type = %q, want %q", cfg.HTTP.Auth.Type, "kerberos")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "unknown http.auth.type") && strings.Contains(w, "kerberos") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about invalid auth type 'kerberos', got %v", warnings)
	}
}

func TestParseAndValidateConfig_ValidAuthTypes(t *testing.T) {
	for _, authType := range []string{"", "token", "oidc", "mtls", "oauth2"} {
		yamlData := []byte(`
http:
  auth:
    type: "` + authType + `"
`)
		_, warnings, err := parseAndValidateConfig(yamlData)
		if err != nil {
			t.Fatalf("auth type %q: unexpected error: %v", authType, err)
		}
		for _, w := range warnings {
			if strings.Contains(w, "http.auth.type") {
				t.Errorf("auth type %q should not produce a warning, got %q", authType, w)
			}
		}
	}
}

func TestParseAndValidateConfig_SyntaxError(t *testing.T) {
	yaml := []byte(`
hub:
  url: [invalid yaml
`)
	_, _, err := parseAndValidateConfig(yaml)
	if err == nil {
		t.Fatal("expected a hard error for invalid YAML syntax, got nil")
	}
}

func TestParseAndValidateConfig_DefaultsApplied(t *testing.T) {
	yaml := []byte(`
hub:
  enabled: true
`)
	cfg, _, err := parseAndValidateConfig(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Hub.URL != "wss://mcp.flexinfer.ai/ws" {
		t.Errorf("Hub.URL default = %q, want %q", cfg.Hub.URL, "wss://mcp.flexinfer.ai/ws")
	}
	if cfg.Hub.Profile != "codex" {
		t.Errorf("Hub.Profile default = %q, want %q", cfg.Hub.Profile, "codex")
	}
}

func TestGetPoolStaleThreshold_Default(t *testing.T) {
	cfg := ResourceConfig{}
	if got := cfg.GetPoolStaleThreshold(); got != 2*time.Minute {
		t.Errorf("GetPoolStaleThreshold() = %v, want 2m", got)
	}
}

func TestGetPoolStaleThreshold_Explicit(t *testing.T) {
	cfg := ResourceConfig{PoolStaleThresholdSeconds: 60}
	if got := cfg.GetPoolStaleThreshold(); got != 60*time.Second {
		t.Errorf("GetPoolStaleThreshold() = %v, want 60s", got)
	}
}

func TestGetPoolStaleThreshold_Disabled(t *testing.T) {
	cfg := ResourceConfig{PoolStaleThresholdSeconds: -1}
	if got := cfg.GetPoolStaleThreshold(); got != 0 {
		t.Errorf("GetPoolStaleThreshold() = %v, want 0 (disabled)", got)
	}
}

func TestGetConfigPath_ReturnsValidPath(t *testing.T) {
	// getConfigPath should return a non-empty path even if HOME is not set.
	// We don't manipulate env vars here to avoid test pollution; we just
	// verify the function doesn't panic and returns a path ending correctly.
	path := getConfigPath()
	if !strings.HasSuffix(path, ".config/loom/config.yaml") {
		t.Errorf("getConfigPath() = %q, expected suffix .config/loom/config.yaml", path)
	}
}
