package openairesponses

import (
	"fmt"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/env"
)

const (
	// FeatureGateEnvVar controls whether Responses orchestration is enabled.
	FeatureGateEnvVar = "LOOM_EXPERIMENTAL_OPENAI_RESPONSES"
	// APIKeyEnvVar optionally overrides the OpenAI API key used for Responses calls.
	APIKeyEnvVar = "LOOM_RESPONSES_API_KEY"
	// BaseURLEnvVar optionally overrides the OpenAI base URL for Responses calls.
	BaseURLEnvVar = "LOOM_RESPONSES_BASE_URL"
	// RequestTimeoutEnvVar configures timeout for upstream Responses calls.
	RequestTimeoutEnvVar = "LOOM_RESPONSES_REQUEST_TIMEOUT"
	// MaxLoopIterationsEnvVar limits tool-loop iterations per turn.
	MaxLoopIterationsEnvVar = "LOOM_RESPONSES_MAX_LOOP_ITERATIONS"
	// MaxRetriesEnvVar limits retries for upstream Responses calls.
	MaxRetriesEnvVar = "LOOM_RESPONSES_MAX_RETRIES"
	// TokenBudgetEnvVar sets the per-turn token budget (0 = unlimited).
	TokenBudgetEnvVar = "LOOM_RESPONSES_TOKEN_BUDGET"
	// CompactionStrategyEnvVar sets the compaction strategy: "none", "truncate", "summarize".
	CompactionStrategyEnvVar = "LOOM_RESPONSES_COMPACTION_STRATEGY"
)

const (
	DefaultBaseURL           = "https://api.openai.com/v1"
	DefaultRequestTimeout    = 90 * time.Second
	DefaultMaxLoopIterations = 12
	DefaultMaxRetries        = 2
)

// Config defines runtime settings for the Responses orchestration feature.
type Config struct {
	Enabled           bool
	RequestTimeout    time.Duration
	MaxLoopIterations int
	MaxRetries        int

	// TokenBudget is the estimated per-turn token limit. 0 = unlimited.
	TokenBudget int
	// Compaction controls how context is reduced when approaching token limits.
	Compaction CompactionStrategy
}

// LoadConfigFromEnv reads configuration from environment variables.
func LoadConfigFromEnv() Config {
	cfg := Config{
		Enabled:           env.Bool(FeatureGateEnvVar, false),
		RequestTimeout:    env.Duration(RequestTimeoutEnvVar, DefaultRequestTimeout),
		MaxLoopIterations: env.Int(MaxLoopIterationsEnvVar, DefaultMaxLoopIterations),
		MaxRetries:        env.IntWithZero(MaxRetriesEnvVar, DefaultMaxRetries),
		TokenBudget:       env.IntWithZero(TokenBudgetEnvVar, 0),
		Compaction:        CompactionStrategy(env.String(CompactionStrategyEnvVar, string(CompactTruncate))),
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	if cfg.MaxLoopIterations <= 0 {
		cfg.MaxLoopIterations = DefaultMaxLoopIterations
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = DefaultMaxRetries
	}
	return cfg
}

// Validate checks that configuration values are usable.
func (c Config) Validate() error {
	if c.RequestTimeout <= 0 {
		return fmt.Errorf("responses request timeout must be > 0")
	}
	if c.MaxLoopIterations <= 0 {
		return fmt.Errorf("responses max loop iterations must be > 0")
	}
	if c.MaxRetries < 0 {
		return fmt.Errorf("responses max retries must be >= 0")
	}
	return nil
}

// RequireEnabled returns an actionable error when the feature gate is disabled.
func (c Config) RequireEnabled() error {
	if c.Enabled {
		return nil
	}
	return fmt.Errorf("openai responses orchestration is disabled; set %s=1", FeatureGateEnvVar)
}

// APIKeyFromEnv returns the first configured API key for Responses calls.
func APIKeyFromEnv() string {
	return env.StringWithFallbacks(APIKeyEnvVar, "OPENAI_API_KEY")
}

// BaseURLFromEnv returns the configured Responses API base URL.
func BaseURLFromEnv() string {
	if v := env.String(BaseURLEnvVar, ""); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v := env.String("OPENAI_BASE_URL", ""); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return DefaultBaseURL
}
