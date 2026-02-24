package coordinator

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestValidate_DefaultConfigPasses(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid, got: %v", err)
	}
}

func TestValidate_ZeroMaxConcurrentLLM(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxConcurrentLLM = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for MaxConcurrentLLM=0")
	}
	if !strings.Contains(err.Error(), "MaxConcurrentLLM") {
		t.Errorf("error should mention MaxConcurrentLLM, got: %v", err)
	}
}

func TestValidate_CompressorRatioBounds(t *testing.T) {
	cfg := DefaultConfig()

	cfg.CompressorRatio = 0.0
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for CompressorRatio=0.0")
	}

	cfg.CompressorRatio = -0.1
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for CompressorRatio=-0.1")
	}

	cfg.CompressorRatio = 1.5
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for CompressorRatio=1.5")
	}

	cfg.CompressorRatio = 1.0
	if err := cfg.Validate(); err != nil {
		t.Errorf("CompressorRatio=1.0 should be valid, got: %v", err)
	}
}

func TestValidate_SubsystemTimeoutExceedsPollInterval(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PollInterval = 10 * time.Second
	cfg.SubsystemTimeout = 20 * time.Second
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when SubsystemTimeout > PollInterval")
	}
	if !strings.Contains(err.Error(), "SubsystemTimeout") {
		t.Errorf("error should mention SubsystemTimeout, got: %v", err)
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxConcurrentLLM = 0
	cfg.CircuitBreakerThreshold = 0
	cfg.SummarizerMaxTokens = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for multiple invalid fields")
	}
	// Should report all three issues.
	msg := err.Error()
	if !strings.Contains(msg, "MaxConcurrentLLM") {
		t.Error("missing MaxConcurrentLLM in error")
	}
	if !strings.Contains(msg, "CircuitBreakerThreshold") {
		t.Error("missing CircuitBreakerThreshold in error")
	}
	if !strings.Contains(msg, "SummarizerMaxTokens") {
		t.Error("missing SummarizerMaxTokens in error")
	}
}

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
	os.Setenv("COORDINATOR_DEFAULT_TIMEOUT", "40s")
	os.Setenv("COORDINATOR_SUBSYSTEM_TIMEOUT", "20s")
	os.Setenv("COORDINATOR_MAX_CONCURRENT_LLM", "4")
	defer func() {
		os.Unsetenv("FLEXINFER_URL")
		os.Unsetenv("FLEXINFER_API_KEY")
		os.Unsetenv("COORDINATOR_MODEL")
		os.Unsetenv("COORDINATOR_ENABLE_PLANNER")
		os.Unsetenv("COORDINATOR_POLL_INTERVAL")
		os.Unsetenv("COORDINATOR_DEFAULT_TIMEOUT")
		os.Unsetenv("COORDINATOR_SUBSYSTEM_TIMEOUT")
		os.Unsetenv("COORDINATOR_MAX_CONCURRENT_LLM")
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
	if cfg.DefaultTimeout != 40*time.Second {
		t.Fatalf("expected 40s default timeout, got %s", cfg.DefaultTimeout)
	}
	if cfg.SubsystemTimeout != 20*time.Second {
		t.Fatalf("expected 20s subsystem timeout, got %s", cfg.SubsystemTimeout)
	}
	if cfg.MaxConcurrentLLM != 4 {
		t.Fatalf("expected max concurrent LLM 4, got %d", cfg.MaxConcurrentLLM)
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

func TestEnvPositiveInt(t *testing.T) {
	tests := []struct {
		val  string
		def  int
		want int
	}{
		{"", 3, 3},
		{"7", 3, 7},
		{"0", 3, 3},
		{"-1", 3, 3},
		{"bad", 3, 3},
	}

	for _, tt := range tests {
		os.Setenv("TEST_INT", tt.val)
		got := envPositiveInt("TEST_INT", tt.def)
		os.Unsetenv("TEST_INT")
		if got != tt.want {
			t.Errorf("envPositiveInt(%q, %d) = %d, want %d", tt.val, tt.def, got, tt.want)
		}
	}
}

func TestEnvPositiveDuration(t *testing.T) {
	tests := []struct {
		val  string
		def  time.Duration
		want time.Duration
	}{
		{"", 10 * time.Second, 10 * time.Second},
		{"25s", 10 * time.Second, 25 * time.Second},
		{"0s", 10 * time.Second, 10 * time.Second},
		{"-1s", 10 * time.Second, 10 * time.Second},
		{"bad", 10 * time.Second, 10 * time.Second},
	}

	for _, tt := range tests {
		os.Setenv("TEST_DUR", tt.val)
		got := envPositiveDuration("TEST_DUR", tt.def)
		os.Unsetenv("TEST_DUR")
		if got != tt.want {
			t.Errorf("envPositiveDuration(%q, %s) = %s, want %s", tt.val, tt.def, got, tt.want)
		}
	}
}
