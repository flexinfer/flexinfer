package openairesponses

import (
	"strings"
	"testing"
	"time"
)

func TestLoadConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv(FeatureGateEnvVar, "")
	t.Setenv(APIKeyEnvVar, "")
	t.Setenv(BaseURLEnvVar, "")
	t.Setenv(RequestTimeoutEnvVar, "")
	t.Setenv(MaxLoopIterationsEnvVar, "")
	t.Setenv(MaxRetriesEnvVar, "")

	cfg := LoadConfigFromEnv()
	if cfg.Enabled {
		t.Fatal("expected feature gate disabled by default")
	}
	if cfg.RequestTimeout != DefaultRequestTimeout {
		t.Fatalf("request timeout = %s, want %s", cfg.RequestTimeout, DefaultRequestTimeout)
	}
	if cfg.MaxLoopIterations != DefaultMaxLoopIterations {
		t.Fatalf("max loop iterations = %d, want %d", cfg.MaxLoopIterations, DefaultMaxLoopIterations)
	}
	if cfg.MaxRetries != DefaultMaxRetries {
		t.Fatalf("max retries = %d, want %d", cfg.MaxRetries, DefaultMaxRetries)
	}
	if got := BaseURLFromEnv(); got != DefaultBaseURL {
		t.Fatalf("base url = %q, want %q", got, DefaultBaseURL)
	}
}

func TestLoadConfigFromEnv_Overrides(t *testing.T) {
	t.Setenv(FeatureGateEnvVar, "true")
	t.Setenv(APIKeyEnvVar, "key-123")
	t.Setenv(BaseURLEnvVar, "https://example.test/v1")
	t.Setenv(RequestTimeoutEnvVar, "42s")
	t.Setenv(MaxLoopIterationsEnvVar, "25")
	t.Setenv(MaxRetriesEnvVar, "4")

	cfg := LoadConfigFromEnv()
	if !cfg.Enabled {
		t.Fatal("expected feature gate enabled")
	}
	if cfg.RequestTimeout != 42*time.Second {
		t.Fatalf("request timeout = %s, want 42s", cfg.RequestTimeout)
	}
	if cfg.MaxLoopIterations != 25 {
		t.Fatalf("max loop iterations = %d, want 25", cfg.MaxLoopIterations)
	}
	if cfg.MaxRetries != 4 {
		t.Fatalf("max retries = %d, want 4", cfg.MaxRetries)
	}
	if got := APIKeyFromEnv(); got != "key-123" {
		t.Fatalf("api key = %q, want key-123", got)
	}
	if got := BaseURLFromEnv(); got != "https://example.test/v1" {
		t.Fatalf("base url = %q, want override", got)
	}
}

func TestConfigRequireEnabled(t *testing.T) {
	err := (Config{}).RequireEnabled()
	if err == nil {
		t.Fatal("expected disabled gate error")
	}
	if !strings.Contains(err.Error(), FeatureGateEnvVar) {
		t.Fatalf("error %q should reference %s", err.Error(), FeatureGateEnvVar)
	}
	if err := (Config{Enabled: true}).RequireEnabled(); err != nil {
		t.Fatalf("require enabled returned error: %v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	if err := (Config{Enabled: true, RequestTimeout: time.Second, MaxLoopIterations: 1, MaxRetries: 0}).Validate(); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
	if err := (Config{RequestTimeout: 0, MaxLoopIterations: 1, MaxRetries: 0}).Validate(); err == nil {
		t.Fatal("expected timeout validation error")
	}
	if err := (Config{RequestTimeout: time.Second, MaxLoopIterations: 0, MaxRetries: 0}).Validate(); err == nil {
		t.Fatal("expected max loop validation error")
	}
	if err := (Config{RequestTimeout: time.Second, MaxLoopIterations: 1, MaxRetries: -1}).Validate(); err == nil {
		t.Fatal("expected max retries validation error")
	}
}
