package weaver

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfigFromEnv_Defaults(t *testing.T) {
	// Clear any env vars that might be set.
	os.Unsetenv(EnvEnabled)
	os.Unsetenv(EnvRouterModel)
	os.Unsetenv(EnvSubagentModel)
	os.Unsetenv(EnvMaxIterations)
	os.Unsetenv(EnvTokenBudget)
	os.Unsetenv(EnvTimeout)
	os.Unsetenv(EnvMaxConcurrent)
	os.Unsetenv(EnvHTTPTimeout)

	cfg := LoadConfigFromEnv()

	if cfg.Enabled {
		t.Error("expected disabled by default")
	}
	if cfg.RouterModel != DefaultRouterModel {
		t.Errorf("expected router model %q, got %q", DefaultRouterModel, cfg.RouterModel)
	}
	if cfg.SubagentModel != DefaultSubagentModel {
		t.Errorf("expected subagent model %q, got %q", DefaultSubagentModel, cfg.SubagentModel)
	}
	if cfg.MaxIterations != DefaultMaxIterations {
		t.Errorf("expected max iterations %d, got %d", DefaultMaxIterations, cfg.MaxIterations)
	}
	if cfg.TokenBudget != DefaultTokenBudget {
		t.Errorf("expected token budget %d, got %d", DefaultTokenBudget, cfg.TokenBudget)
	}
	if cfg.Timeout != DefaultTimeout {
		t.Errorf("expected timeout %v, got %v", DefaultTimeout, cfg.Timeout)
	}
	if cfg.MaxConcurrent != DefaultMaxConcurrent {
		t.Errorf("expected max concurrent %d, got %d", DefaultMaxConcurrent, cfg.MaxConcurrent)
	}
	if cfg.HTTPTimeout != DefaultHTTPTimeout {
		t.Errorf("expected HTTP timeout %v, got %v", DefaultHTTPTimeout, cfg.HTTPTimeout)
	}
}

func TestLoadConfigFromEnv_Custom(t *testing.T) {
	t.Setenv(EnvEnabled, "1")
	t.Setenv(EnvRouterModel, "custom-router")
	t.Setenv(EnvSubagentModel, "custom-subagent")
	t.Setenv(EnvMaxIterations, "12")
	t.Setenv(EnvTokenBudget, "8192")
	t.Setenv(EnvTimeout, "60s")
	t.Setenv(EnvMaxConcurrent, "8")
	t.Setenv(EnvHTTPTimeout, "120s")

	cfg := LoadConfigFromEnv()

	if !cfg.Enabled {
		t.Error("expected enabled")
	}
	if cfg.RouterModel != "custom-router" {
		t.Errorf("expected custom-router, got %q", cfg.RouterModel)
	}
	if cfg.SubagentModel != "custom-subagent" {
		t.Errorf("expected custom-subagent, got %q", cfg.SubagentModel)
	}
	if cfg.MaxIterations != 12 {
		t.Errorf("expected 12, got %d", cfg.MaxIterations)
	}
	if cfg.TokenBudget != 8192 {
		t.Errorf("expected 8192, got %d", cfg.TokenBudget)
	}
	if cfg.MaxConcurrent != 8 {
		t.Errorf("expected 8, got %d", cfg.MaxConcurrent)
	}
	if cfg.HTTPTimeout != 120*time.Second {
		t.Errorf("expected 120s, got %v", cfg.HTTPTimeout)
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid",
			cfg: Config{
				RouterModel:   "model",
				SubagentModel: "model",
				MaxIterations: 8,
				Timeout:       DefaultTimeout,
				MaxConcurrent: 4,
			},
		},
		{
			name: "missing router model",
			cfg: Config{
				SubagentModel: "model",
				MaxIterations: 8,
				Timeout:       DefaultTimeout,
				MaxConcurrent: 4,
			},
			wantErr: true,
		},
		{
			name: "missing subagent model",
			cfg: Config{
				RouterModel:   "model",
				MaxIterations: 8,
				Timeout:       DefaultTimeout,
				MaxConcurrent: 4,
			},
			wantErr: true,
		},
		{
			name: "zero max iterations",
			cfg: Config{
				RouterModel:   "model",
				SubagentModel: "model",
				MaxIterations: 0,
				Timeout:       DefaultTimeout,
				MaxConcurrent: 4,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_RequireEnabled(t *testing.T) {
	cfg := Config{Enabled: false}
	if err := cfg.RequireEnabled(); err == nil {
		t.Error("expected error when disabled")
	}

	cfg.Enabled = true
	if err := cfg.RequireEnabled(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadConfigFromEnv_ModelBehaviors(t *testing.T) {
	os.Unsetenv(EnvEnabled)
	cfg := LoadConfigFromEnv()

	if cfg.ModelBehaviors == nil {
		t.Fatal("expected non-nil ModelBehaviors")
	}
	b, ok := cfg.ModelBehaviors["qwen3"]
	if !ok {
		t.Fatal("expected qwen3 behavior")
	}
	if b.UserMessagePrefix != "/no_think\n" {
		t.Errorf("expected /no_think prefix, got %q", b.UserMessagePrefix)
	}
}

func TestFindModelBehavior(t *testing.T) {
	behaviors := map[string]ModelBehavior{
		"qwen3": {UserMessagePrefix: "/no_think\n"},
	}

	tests := []struct {
		model     string
		wantMatch bool
		wantPfx   string
	}{
		{"qwen3.5-9b", true, "/no_think\n"},
		{"Qwen3-8B", true, "/no_think\n"},
		{"QWEN3-large", true, "/no_think\n"},
		{"gemma-4-turboquant", false, ""},
		{"llama-3.2", false, ""},
		{"", false, ""},
	}

	for _, tt := range tests {
		b, ok := FindModelBehavior(behaviors, tt.model)
		if ok != tt.wantMatch {
			t.Errorf("FindModelBehavior(%q): got match=%v, want %v", tt.model, ok, tt.wantMatch)
		}
		if b.UserMessagePrefix != tt.wantPfx {
			t.Errorf("FindModelBehavior(%q): got prefix=%q, want %q", tt.model, b.UserMessagePrefix, tt.wantPfx)
		}
	}
}

func TestModelBehavior_Gemma4_NoPrefix(t *testing.T) {
	behaviors := DefaultModelBehaviors()
	b, ok := FindModelBehavior(behaviors, "gemma-4-turboquant")
	if ok {
		t.Errorf("expected no behavior match for gemma-4, got %+v", b)
	}
	if b.UserMessagePrefix != "" {
		t.Errorf("expected empty prefix for gemma-4, got %q", b.UserMessagePrefix)
	}
}
