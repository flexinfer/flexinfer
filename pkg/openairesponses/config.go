package openairesponses

import (
	"fmt"
	"time"

	"github.com/crb2nu/loom/pkg/env"
)

const (
	// FeatureGateEnvVar controls whether Responses orchestration is enabled.
	FeatureGateEnvVar = "LOOM_EXPERIMENTAL_OPENAI_RESPONSES"
	// RequestTimeoutEnvVar configures timeout for upstream Responses calls.
	RequestTimeoutEnvVar = "LOOM_RESPONSES_REQUEST_TIMEOUT"
	// MaxLoopIterationsEnvVar limits tool-loop iterations per turn.
	MaxLoopIterationsEnvVar = "LOOM_RESPONSES_MAX_LOOP_ITERATIONS"
)

const (
	DefaultRequestTimeout    = 90 * time.Second
	DefaultMaxLoopIterations = 12
)

// Config defines runtime settings for the Responses orchestration feature.
type Config struct {
	Enabled           bool
	RequestTimeout    time.Duration
	MaxLoopIterations int
}

// LoadConfigFromEnv reads configuration from environment variables.
func LoadConfigFromEnv() Config {
	cfg := Config{
		Enabled:           env.Bool(FeatureGateEnvVar, false),
		RequestTimeout:    env.Duration(RequestTimeoutEnvVar, DefaultRequestTimeout),
		MaxLoopIterations: env.Int(MaxLoopIterationsEnvVar, DefaultMaxLoopIterations),
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	if cfg.MaxLoopIterations <= 0 {
		cfg.MaxLoopIterations = DefaultMaxLoopIterations
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
	return nil
}

// RequireEnabled returns an actionable error when the feature gate is disabled.
func (c Config) RequireEnabled() error {
	if c.Enabled {
		return nil
	}
	return fmt.Errorf("openai responses orchestration is disabled; set %s=1", FeatureGateEnvVar)
}
