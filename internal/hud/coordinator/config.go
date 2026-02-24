// Package coordinator provides LLM-powered intelligence for agent context
// operations. It connects to FlexInfer (an OpenAI-compatible proxy) to
// summarize sessions, compress memory, triage entries, extract knowledge
// graph entities, and plan workflows from natural language.
//
// The coordinator is entirely optional — if FLEXINFER_URL is empty, the
// HUD works exactly as before.
package coordinator

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all coordinator configuration.
type Config struct {
	// FlexInfer connection.
	FlexInferURL string // Base URL (e.g., "http://flexinfer-proxy:8080"). Empty = disabled.
	FlexInferKey string // Optional API key for FlexInfer.

	// Model selection.
	DefaultModel  string // Default model for all tasks (e.g., "qwen3-8b").
	FallbackModel string // Fallback if default is unavailable.
	PlannerModel  string // Override for planning (may need larger model).

	// Feature toggles — each subsystem can be independently disabled.
	EnableSummarizer bool
	EnableCompressor bool
	EnableTriager    bool
	EnableExtractor  bool
	EnablePlanner    bool

	// Timing.
	PollInterval time.Duration // Background sweep interval.

	// Subsystem tuning.
	SummarizerMaxTokens int     // Max tokens for summary output.
	CompressorRatio     float64 // Target compression ratio (0.0–1.0).
	TriagerBatchSize    int     // Entries per triage batch.
	ExtractorBatchSize  int     // Entries per extraction batch.

	// Per-cycle safety caps — limit work per poll to avoid storming the backend.
	MaxSweepSessions int // Max sessions to summarize per poll cycle.
	MaxCompressItems int // Max items to compress per poll cycle.

	// Circuit breaker.
	CircuitBreakerThreshold int           // Consecutive failures to open.
	CircuitBreakerReset     time.Duration // Time before half-open retry.

	// Concurrency.
	MaxConcurrentLLM int           // Max parallel LLM calls.
	DefaultTimeout   time.Duration // Default per-call timeout.
	SubsystemTimeout time.Duration // Per-subsystem timeout within a poll cycle.
	PlannerTimeout   time.Duration // Planner gets longer timeout (API only).
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		DefaultModel:  "qwen3-8b",
		FallbackModel: "",
		PlannerModel:  "",

		EnableSummarizer: true,
		EnableCompressor: true,
		EnableTriager:    true,
		EnableExtractor:  true,
		EnablePlanner:    true,

		PollInterval: 30 * time.Second,

		SummarizerMaxTokens: 500,
		CompressorRatio:     0.3,
		TriagerBatchSize:    10,
		ExtractorBatchSize:  5,

		MaxSweepSessions: 2,
		MaxCompressItems: 3,

		CircuitBreakerThreshold: 3,
		CircuitBreakerReset:     30 * time.Second,

		MaxConcurrentLLM: 2,
		DefaultTimeout:   30 * time.Second,
		SubsystemTimeout: 15 * time.Second,
		PlannerTimeout:   45 * time.Second,
	}
}

// ConfigFromEnv reads coordinator configuration from environment variables,
// falling back to defaults for any unset variable.
func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	if v := os.Getenv("FLEXINFER_URL"); v != "" {
		cfg.FlexInferURL = strings.TrimRight(v, "/")
	}
	if v := os.Getenv("FLEXINFER_API_KEY"); v != "" {
		cfg.FlexInferKey = v
	}
	if v := os.Getenv("COORDINATOR_MODEL"); v != "" {
		cfg.DefaultModel = v
	}
	if v := os.Getenv("COORDINATOR_FALLBACK_MODEL"); v != "" {
		cfg.FallbackModel = v
	}
	if v := os.Getenv("COORDINATOR_PLANNER_MODEL"); v != "" {
		cfg.PlannerModel = v
	}

	cfg.EnableSummarizer = envBool("COORDINATOR_ENABLE_SUMMARIZER", cfg.EnableSummarizer)
	cfg.EnableCompressor = envBool("COORDINATOR_ENABLE_COMPRESSOR", cfg.EnableCompressor)
	cfg.EnableTriager = envBool("COORDINATOR_ENABLE_TRIAGER", cfg.EnableTriager)
	cfg.EnableExtractor = envBool("COORDINATOR_ENABLE_EXTRACTOR", cfg.EnableExtractor)
	cfg.EnablePlanner = envBool("COORDINATOR_ENABLE_PLANNER", cfg.EnablePlanner)

	if v := os.Getenv("COORDINATOR_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.PollInterval = d
		}
	}
	cfg.MaxConcurrentLLM = envPositiveInt("COORDINATOR_MAX_CONCURRENT_LLM", cfg.MaxConcurrentLLM)
	cfg.CircuitBreakerThreshold = envPositiveInt("COORDINATOR_CIRCUIT_BREAKER_THRESHOLD", cfg.CircuitBreakerThreshold)
	cfg.DefaultTimeout = envPositiveDuration("COORDINATOR_DEFAULT_TIMEOUT", cfg.DefaultTimeout)
	cfg.SubsystemTimeout = envPositiveDuration("COORDINATOR_SUBSYSTEM_TIMEOUT", cfg.SubsystemTimeout)
	cfg.PlannerTimeout = envPositiveDuration("COORDINATOR_PLANNER_TIMEOUT", cfg.PlannerTimeout)
	cfg.CircuitBreakerReset = envPositiveDuration("COORDINATOR_CIRCUIT_BREAKER_RESET", cfg.CircuitBreakerReset)

	return cfg
}

// Enabled reports whether the coordinator should be started.
func (c Config) Enabled() bool {
	return c.FlexInferURL != ""
}

// Validate checks the config for obviously invalid values that would
// cause panics or broken behavior at runtime. Returns nil if valid.
func (c Config) Validate() error {
	var errs []string
	if c.MaxConcurrentLLM < 1 {
		errs = append(errs, "MaxConcurrentLLM must be >= 1")
	}
	if c.CircuitBreakerThreshold < 1 {
		errs = append(errs, "CircuitBreakerThreshold must be >= 1")
	}
	if c.CompressorRatio <= 0 || c.CompressorRatio > 1.0 {
		errs = append(errs, "CompressorRatio must be in (0.0, 1.0]")
	}
	if c.SubsystemTimeout <= 0 {
		errs = append(errs, "SubsystemTimeout must be > 0")
	}
	if c.PollInterval > 0 && c.SubsystemTimeout > c.PollInterval {
		errs = append(errs, "SubsystemTimeout must be <= PollInterval")
	}
	if c.SummarizerMaxTokens < 1 {
		errs = append(errs, "SummarizerMaxTokens must be >= 1")
	}
	if c.TriagerBatchSize < 1 {
		errs = append(errs, "TriagerBatchSize must be >= 1")
	}
	if c.ExtractorBatchSize < 1 {
		errs = append(errs, "ExtractorBatchSize must be >= 1")
	}
	if c.MaxSweepSessions < 1 {
		errs = append(errs, "MaxSweepSessions must be >= 1")
	}
	if c.MaxCompressItems < 1 {
		errs = append(errs, "MaxCompressItems must be >= 1")
	}
	if len(errs) > 0 {
		return fmt.Errorf("coordinator config: %s", strings.Join(errs, "; "))
	}
	return nil
}

// envBool reads a boolean environment variable, returning def if unset.
func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// envPositiveInt reads a positive integer environment variable, returning def
// when unset, non-numeric, or non-positive.
func envPositiveInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// envPositiveDuration reads a positive duration environment variable,
// returning def when unset, invalid, or non-positive.
func envPositiveDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}
