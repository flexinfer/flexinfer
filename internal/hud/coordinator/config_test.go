package coordinator

import (
	"os"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.DefaultModel != "qwen3-8b" {
		t.Fatalf("expected default model qwen3-8b, got %s", cfg.DefaultModel)
	}
	if cfg.PollInterval != 30*time.Second {
		t.Fatalf("expected 30s poll interval, got %s", cfg.PollInterval)
	}
	if !cfg.EnableSummarizer {
		t.Fatal("expected summarizer enabled by default")
	}
	if cfg.MaxConcurrentLLM != 2 {
		t.Fatalf("expected max 2 concurrent LLM calls, got %d", cfg.MaxConcurrentLLM)
	}
	if cfg.Enabled() {
		t.Fatal("default config should be disabled (no URL)")
	}
}

func TestConfigFromEnv(t *testing.T) {
	// Set environment variables.
	os.Setenv("FLEXINFER_URL", "http://test:8080/")
	os.Setenv("FLEXINFER_API_KEY", "test-key")
	os.Setenv("COORDINATOR_MODEL", "llama3-70b")
	os.Setenv("COORDINATOR_ENABLE_PLANNER", "false")
	os.Setenv("COORDINATOR_POLL_INTERVAL", "15s")
	defer func() {
		os.Unsetenv("FLEXINFER_URL")
		os.Unsetenv("FLEXINFER_API_KEY")
		os.Unsetenv("COORDINATOR_MODEL")
		os.Unsetenv("COORDINATOR_ENABLE_PLANNER")
		os.Unsetenv("COORDINATOR_POLL_INTERVAL")
	}()

	cfg := ConfigFromEnv()

	if cfg.FlexInferURL != "http://test:8080" {
		t.Fatalf("expected URL without trailing slash, got %q", cfg.FlexInferURL)
	}
	if cfg.FlexInferKey != "test-key" {
		t.Fatalf("expected key test-key, got %q", cfg.FlexInferKey)
	}
	if cfg.DefaultModel != "llama3-70b" {
		t.Fatalf("expected model llama3-70b, got %s", cfg.DefaultModel)
	}
	if cfg.EnablePlanner {
		t.Fatal("expected planner disabled")
	}
	if cfg.PollInterval != 15*time.Second {
		t.Fatalf("expected 15s poll interval, got %s", cfg.PollInterval)
	}
	if !cfg.Enabled() {
		t.Fatal("config with URL should be enabled")
	}
}

func TestEnvBool(t *testing.T) {
	tests := []struct {
		val  string
		def  bool
		want bool
	}{
		{"", true, true},        // Unset, use default.
		{"", false, false},      // Unset, use default.
		{"true", false, true},   // Set true.
		{"false", true, false},  // Set false.
		{"1", false, true},      // Numeric true.
		{"0", true, false},      // Numeric false.
		{"invalid", true, true}, // Invalid, use default.
	}

	for _, tt := range tests {
		os.Setenv("TEST_BOOL", tt.val)
		got := envBool("TEST_BOOL", tt.def)
		os.Unsetenv("TEST_BOOL")

		if got != tt.want {
			t.Errorf("envBool(%q, %v) = %v, want %v", tt.val, tt.def, got, tt.want)
		}
	}
}
