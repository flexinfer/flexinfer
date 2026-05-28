package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/internal/routing"
)

// validConfig returns a Config with all fields set to valid defaults for testing.
func validConfig() Config {
	return Config{
		Namespace:                   "test-ns",
		MaxQueueSize:                100,
		QueueTimeout:                60 * time.Second,
		ColdStartTimeout:            60 * time.Second,
		BackoffMaxRetries:           3,
		BackoffInitialWait:          5 * time.Second,
		BackoffMaxWait:              30 * time.Second,
		RateLimitPerModel:           100,
		RateLimitBurst:              50,
		RateLimitGlobal:             1000,
		RateLimitGlobalBurst:        200,
		MaxTokensClampEnabled:       true,
		MaxTokensClampPromptReserve: defaultPromptReserveTokens,
	}
}

func TestConfigValidate_ValidDefaults(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
}

func TestConfigValidate_Errors(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr string
	}{
		{
			name:    "zero MaxQueueSize",
			modify:  func(c *Config) { c.MaxQueueSize = 0 },
			wantErr: "PROXY_MAX_QUEUE_SIZE must be > 0",
		},
		{
			name:    "zero QueueTimeout",
			modify:  func(c *Config) { c.QueueTimeout = 0 },
			wantErr: "PROXY_QUEUE_TIMEOUT must be > 0",
		},
		{
			name:    "zero ColdStartTimeout",
			modify:  func(c *Config) { c.ColdStartTimeout = 0 },
			wantErr: "PROXY_COLD_START_TIMEOUT must be > 0",
		},
		{
			name: "backoff initial > max",
			modify: func(c *Config) {
				c.BackoffEnabled = true
				c.BackoffInitialWait = 60 * time.Second
				c.BackoffMaxWait = 10 * time.Second
			},
			wantErr: "PROXY_BACKOFF_INITIAL_WAIT",
		},
		{
			name: "backoff zero retries",
			modify: func(c *Config) {
				c.BackoffEnabled = true
				c.BackoffMaxRetries = 0
			},
			wantErr: "PROXY_BACKOFF_MAX_RETRIES must be > 0",
		},
		{
			name: "rate limit zero per-model",
			modify: func(c *Config) {
				c.RateLimitEnabled = true
				c.RateLimitPerModel = 0
			},
			wantErr: "PROXY_RATE_LIMIT_PER_MODEL must be > 0",
		},
		{
			name: "rate limit zero burst",
			modify: func(c *Config) {
				c.RateLimitEnabled = true
				c.RateLimitBurst = 0
			},
			wantErr: "PROXY_RATE_LIMIT_BURST must be > 0",
		},
		{
			name: "auth enabled no token",
			modify: func(c *Config) {
				c.AuthEnabled = true
				c.AuthToken = ""
			},
			wantErr: "PROXY_AUTH_TOKEN must be set",
		},
		{
			name: "max tokens clamp zero reserve",
			modify: func(c *Config) {
				c.MaxTokensClampEnabled = true
				c.MaxTokensClampPromptReserve = 0
			},
			wantErr: "PROXY_MAX_TOKENS_CLAMP_PROMPT_RESERVE_TOKENS must be > 0",
		},
		{
			name: "label group routing unknown mode",
			modify: func(c *Config) {
				c.LabelGroupRouting = "least-loaded"
			},
			wantErr: "FLEXINFER_PROXY_LABEL_GROUP_ROUTING must be one of",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.modify(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestConfigValidate_MultipleErrors(t *testing.T) {
	cfg := Config{} // all zero values
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation errors, got nil")
	}
	msg := err.Error()
	// Should contain errors for MaxQueueSize, QueueTimeout, and ColdStartTimeout
	for _, want := range []string{"PROXY_MAX_QUEUE_SIZE", "PROXY_QUEUE_TIMEOUT", "PROXY_COLD_START_TIMEOUT"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error to contain %q, got %q", want, msg)
		}
	}
}

func TestConfigValidate_BackoffSkippedWhenDisabled(t *testing.T) {
	cfg := validConfig()
	cfg.BackoffEnabled = false
	cfg.BackoffInitialWait = 60 * time.Second
	cfg.BackoffMaxWait = 10 * time.Second // would be invalid if enabled
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error when backoff disabled, got: %v", err)
	}
}

func TestConfigValidate_RateLimitSkippedWhenDisabled(t *testing.T) {
	cfg := validConfig()
	cfg.RateLimitEnabled = false
	cfg.RateLimitPerModel = 0 // would be invalid if enabled
	cfg.RateLimitBurst = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error when rate limit disabled, got: %v", err)
	}
}

func TestDebugConfigEndpoint(t *testing.T) {
	cfg := validConfig()
	cfg.AuthEnabled = true
	cfg.AuthToken = "super-secret-token"

	p := &Proxy{debugConfig: newDebugConfigView(cfg)}

	req := httptest.NewRequest(http.MethodGet, "/debug/config", nil)
	rec := httptest.NewRecorder()
	p.handleDebugConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result debugConfigView
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.Namespace != "test-ns" {
		t.Errorf("expected namespace=test-ns, got %s", result.Namespace)
	}
	if result.AuthToken != "***redacted***" {
		t.Errorf("expected auth token to be redacted, got %s", result.AuthToken)
	}
	if result.MaxQueueSize != 100 {
		t.Errorf("expected maxQueueSize=100, got %d", result.MaxQueueSize)
	}
	if !result.MaxTokensClampEnabled {
		t.Errorf("expected maxTokensClampEnabled=true")
	}
	if result.MaxTokensClampPromptReserve != defaultPromptReserveTokens {
		t.Errorf("expected maxTokensClampPromptReserve=%d, got %d", defaultPromptReserveTokens, result.MaxTokensClampPromptReserve)
	}
}

func TestDebugConfigEndpoint_EmptyToken(t *testing.T) {
	cfg := validConfig()
	cfg.AuthToken = ""

	p := &Proxy{debugConfig: newDebugConfigView(cfg)}

	req := httptest.NewRequest(http.MethodGet, "/debug/config", nil)
	rec := httptest.NewRecorder()
	p.handleDebugConfig(rec, req)

	var result debugConfigView
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.AuthToken != "" {
		t.Errorf("expected empty auth token, got %s", result.AuthToken)
	}
}

func TestConfigFromEnv_RoutingKeyStrictness(t *testing.T) {
	t.Setenv("PROXY_ROUTING_EXPLICIT_KEY_MAX_LENGTH", "64")
	t.Setenv("PROXY_ROUTING_SYSTEM_SEGMENT_MAX_LENGTH", "256")
	t.Setenv("PROXY_ROUTING_DOCUMENT_SEGMENT_MAX_LENGTH", "120")

	cfg := ConfigFromEnv(nil, "flexinfer-system")
	if cfg.RoutingExplicitCacheKeyMaxLength != 64 {
		t.Fatalf("explicit max length=%d want 64", cfg.RoutingExplicitCacheKeyMaxLength)
	}
	if cfg.RoutingSystemSegmentMaxLength != 256 {
		t.Fatalf("system max length=%d want 256", cfg.RoutingSystemSegmentMaxLength)
	}
	if cfg.RoutingDocSegmentMaxLength != 120 {
		t.Fatalf("document max length=%d want 120", cfg.RoutingDocSegmentMaxLength)
	}
}

func TestConfigFromEnv_MaxTokensClamp(t *testing.T) {
	t.Setenv("PROXY_MAX_TOKENS_CLAMP_ENABLED", "false")
	t.Setenv("PROXY_MAX_TOKENS_CLAMP_PROMPT_RESERVE_TOKENS", "256")

	cfg := ConfigFromEnv(nil, "flexinfer-system")
	if cfg.MaxTokensClampEnabled {
		t.Fatal("MaxTokensClampEnabled=true want false")
	}
	if cfg.MaxTokensClampPromptReserve != 256 {
		t.Fatalf("MaxTokensClampPromptReserve=%d want 256", cfg.MaxTokensClampPromptReserve)
	}
}

func TestNew_AppliesRoutingKeyStrictness(t *testing.T) {
	original := routing.CurrentPrefixKeyConfig()
	t.Cleanup(func() {
		routing.SetPrefixKeyConfig(original)
	})

	_ = New(Config{
		Namespace:                        "flexinfer-system",
		RoutingExplicitCacheKeyMaxLength: 48,
		RoutingSystemSegmentMaxLength:    128,
		RoutingDocSegmentMaxLength:       64,
	})

	got := routing.CurrentPrefixKeyConfig()
	if got.ExplicitCacheKeyMaxLength != 48 {
		t.Fatalf("explicit max length=%d want 48", got.ExplicitCacheKeyMaxLength)
	}
	if got.SystemSegmentMaxLength != 128 {
		t.Fatalf("system max length=%d want 128", got.SystemSegmentMaxLength)
	}
	if got.DocSegmentMaxLength != 64 {
		t.Fatalf("document max length=%d want 64", got.DocSegmentMaxLength)
	}
}
