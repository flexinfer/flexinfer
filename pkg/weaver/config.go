// Package weaver provides a local-model MCP tool orchestration system.
// A single weaver__query call replaces 5-10 sequential tool calls by
// using FlexInfer models to classify, dispatch, and synthesize results.
package weaver

import (
	"fmt"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/env"
)

const (
	EnvEnabled       = "WEAVER_ENABLED"
	EnvRouterModel   = "WEAVER_ROUTER_MODEL"
	EnvSubagentModel = "WEAVER_SUBAGENT_MODEL"
	EnvMaxIterations = "ORCHESTRA_MAX_ITERATIONS"
	EnvTokenBudget   = "ORCHESTRA_TOKEN_BUDGET"
	EnvTimeout       = "ORCHESTRA_TIMEOUT"
	EnvMaxConcurrent = "ORCHESTRA_MAX_CONCURRENT"
	EnvHTTPTimeout   = "WEAVER_HTTP_TIMEOUT"

	DefaultRouterModel   = "gemma-4-turboquant"
	DefaultSubagentModel = "gemma-4-turboquant"
	DefaultMaxIterations = 8
	DefaultTokenBudget   = 4096
	DefaultTimeout       = 30 * time.Second
	DefaultMaxConcurrent = 4
	DefaultHTTPTimeout   = 60 * time.Second
)

// ModelBehavior holds model-specific adjustments applied before LLM calls.
type ModelBehavior struct {
	// UserMessagePrefix is prepended to the first user message (e.g. "/no_think\n" for Qwen3).
	UserMessagePrefix string
}

// DefaultModelBehaviors returns the built-in model behavior map.
// Keys are model name prefixes matched case-insensitively.
func DefaultModelBehaviors() map[string]ModelBehavior {
	return map[string]ModelBehavior{
		"qwen3": {UserMessagePrefix: "/no_think\n"},
	}
}

// FindModelBehavior returns the ModelBehavior for the given model name by
// checking for a case-insensitive prefix match against the behavior map keys.
// Returns the zero value and false if no match is found.
func FindModelBehavior(behaviors map[string]ModelBehavior, model string) (ModelBehavior, bool) {
	lower := strings.ToLower(model)
	for prefix, b := range behaviors {
		if strings.HasPrefix(lower, strings.ToLower(prefix)) {
			return b, true
		}
	}
	return ModelBehavior{}, false
}

// Config holds weaver configuration loaded from environment variables.
type Config struct {
	Enabled        bool
	RouterModel    string
	SubagentModel  string
	MaxIterations  int
	TokenBudget    int
	Timeout        time.Duration
	MaxConcurrent  int
	HTTPTimeout    time.Duration
	ModelBehaviors map[string]ModelBehavior
}

// LoadConfigFromEnv reads weaver configuration from environment variables.
func LoadConfigFromEnv() Config {
	cfg := Config{
		Enabled:        env.Bool(EnvEnabled, false),
		RouterModel:    env.String(EnvRouterModel, DefaultRouterModel),
		SubagentModel:  env.String(EnvSubagentModel, DefaultSubagentModel),
		MaxIterations:  env.Int(EnvMaxIterations, DefaultMaxIterations),
		TokenBudget:    env.Int(EnvTokenBudget, DefaultTokenBudget),
		Timeout:        env.Duration(EnvTimeout, DefaultTimeout),
		MaxConcurrent:  env.Int(EnvMaxConcurrent, DefaultMaxConcurrent),
		HTTPTimeout:    env.Duration(EnvHTTPTimeout, DefaultHTTPTimeout),
		ModelBehaviors: DefaultModelBehaviors(),
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
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = DefaultHTTPTimeout
	}
	return cfg
}

// Validate checks that configuration values are usable.
func (c Config) Validate() error {
	if c.RouterModel == "" {
		return fmt.Errorf("weaver router model is required")
	}
	if c.SubagentModel == "" {
		return fmt.Errorf("weaver subagent model is required")
	}
	if c.MaxIterations <= 0 {
		return fmt.Errorf("weaver max iterations must be > 0")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("weaver timeout must be > 0")
	}
	if c.MaxConcurrent <= 0 {
		return fmt.Errorf("weaver max concurrent must be > 0")
	}
	return nil
}

// RequireEnabled returns an error when weaver is disabled.
func (c Config) RequireEnabled() error {
	if c.Enabled {
		return nil
	}
	return fmt.Errorf("weaver is disabled; set %s=1", EnvEnabled)
}
