// Package orchestra provides a local-model MCP tool orchestration system.
// A single orchestra__query call replaces 5-10 sequential tool calls by
// using FlexInfer models to classify, dispatch, and synthesize results.
package orchestra

import (
	"fmt"
	"time"

	"github.com/crb2nu/loom/pkg/env"
)

const (
	EnvEnabled       = "ORCHESTRA_ENABLED"
	EnvRouterModel   = "ORCHESTRA_ROUTER_MODEL"
	EnvSubagentModel = "ORCHESTRA_SUBAGENT_MODEL"
	EnvMaxIterations = "ORCHESTRA_MAX_ITERATIONS"
	EnvTokenBudget   = "ORCHESTRA_TOKEN_BUDGET"
	EnvTimeout       = "ORCHESTRA_TIMEOUT"
	EnvMaxConcurrent = "ORCHESTRA_MAX_CONCURRENT"

	DefaultRouterModel   = "qwen3.5-9b"
	DefaultSubagentModel = "qwen3.5-9b"
	DefaultMaxIterations = 8
	DefaultTokenBudget   = 4096
	DefaultTimeout       = 30 * time.Second
	DefaultMaxConcurrent = 4
)

// Config holds orchestra configuration loaded from environment variables.
type Config struct {
	Enabled       bool
	RouterModel   string
	SubagentModel string
	MaxIterations int
	TokenBudget   int
	Timeout       time.Duration
	MaxConcurrent int
}

// LoadConfigFromEnv reads orchestra configuration from environment variables.
func LoadConfigFromEnv() Config {
	cfg := Config{
		Enabled:       env.Bool(EnvEnabled, false),
		RouterModel:   env.String(EnvRouterModel, DefaultRouterModel),
		SubagentModel: env.String(EnvSubagentModel, DefaultSubagentModel),
		MaxIterations: env.Int(EnvMaxIterations, DefaultMaxIterations),
		TokenBudget:   env.Int(EnvTokenBudget, DefaultTokenBudget),
		Timeout:       env.Duration(EnvTimeout, DefaultTimeout),
		MaxConcurrent: env.Int(EnvMaxConcurrent, DefaultMaxConcurrent),
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = DefaultMaxIterations
	}
	if cfg.TokenBudget <= 0 {
		cfg.TokenBudget = DefaultTokenBudget
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = DefaultMaxConcurrent
	}
	return cfg
}

// Validate checks that configuration values are usable.
func (c Config) Validate() error {
	if c.RouterModel == "" {
		return fmt.Errorf("orchestra router model is required")
	}
	if c.SubagentModel == "" {
		return fmt.Errorf("orchestra subagent model is required")
	}
	if c.MaxIterations <= 0 {
		return fmt.Errorf("orchestra max iterations must be > 0")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("orchestra timeout must be > 0")
	}
	if c.MaxConcurrent <= 0 {
		return fmt.Errorf("orchestra max concurrent must be > 0")
	}
	return nil
}

// RequireEnabled returns an error when orchestra is disabled.
func (c Config) RequireEnabled() error {
	if c.Enabled {
		return nil
	}
	return fmt.Errorf("orchestra is disabled; set %s=1", EnvEnabled)
}
