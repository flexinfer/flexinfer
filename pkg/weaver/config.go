// Package weaver provides a local-model MCP tool orchestration system.
// A single weaver__query call replaces 5-10 sequential tool calls by
// using FlexInfer models to classify, dispatch, and synthesize results.
package weaver

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/aimodels"
	"github.com/crb2nu/loom/pkg/env"
	"gopkg.in/yaml.v3"
)

const (
	EnvEnabled       = "WEAVER_ENABLED"
	EnvRouterModel   = "WEAVER_ROUTER_MODEL"
	EnvSubagentModel = "WEAVER_SUBAGENT_MODEL"
	EnvMaxIterations = "WEAVER_MAX_ITERATIONS"
	EnvTokenBudget   = "WEAVER_TOKEN_BUDGET"
	EnvTimeout       = "WEAVER_TIMEOUT"
	EnvMaxConcurrent = "WEAVER_MAX_CONCURRENT"
	EnvHTTPTimeout   = "WEAVER_HTTP_TIMEOUT"

	// Deprecated: Use WEAVER_* equivalents.
	EnvMaxIterationsDeprecated = "ORCHESTRA_MAX_ITERATIONS"
	EnvTokenBudgetDeprecated   = "ORCHESTRA_TOKEN_BUDGET"
	EnvTimeoutDeprecated       = "ORCHESTRA_TIMEOUT"
	EnvMaxConcurrentDeprecated = "ORCHESTRA_MAX_CONCURRENT"

	DefaultMaxIterations = 8
	DefaultTokenBudget   = 4096
	DefaultTimeout       = 30 * time.Second
	DefaultMaxConcurrent = 4
	DefaultHTTPTimeout   = 60 * time.Second
)

// DefaultRouterModel and DefaultSubagentModel resolve through pkg/aimodels
// so the canonical model names live in one place. They retain string
// values to keep API-compat with callers that read them directly.
//
// Operators override at runtime via WEAVER_ROUTER_MODEL /
// WEAVER_SUBAGENT_MODEL env vars or ~/.config/loom/aimodel-roles.yaml.
var (
	DefaultRouterModel   = aimodels.DefaultResolver().ResolveOrDefault(aimodels.RoleWeaverRouter, "qwen3-1p7b-tools-radeonvii")
	DefaultSubagentModel = aimodels.DefaultResolver().ResolveOrDefault(aimodels.RoleWeaverSubagent, "qwen3-8b")
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

// envIntWithFallback reads newKey first; if unset, falls back to oldKey
// (logging a deprecation warning). Returns defaultVal when neither is set.
func envIntWithFallback(newKey, oldKey string, defaultVal int) int {
	if v := env.Int(newKey, 0); v != 0 {
		return v
	}
	if v := env.Int(oldKey, 0); v != 0 {
		slog.Warn("deprecated env var, use new name", "old", oldKey, "new", newKey)
		return v
	}
	return defaultVal
}

// envDurationWithFallback reads newKey first; if unset, falls back to oldKey
// (logging a deprecation warning). Returns defaultVal when neither is set.
func envDurationWithFallback(newKey, oldKey string, defaultVal time.Duration) time.Duration {
	if v := env.Duration(newKey, 0); v != 0 {
		return v
	}
	if v := env.Duration(oldKey, 0); v != 0 {
		slog.Warn("deprecated env var, use new name", "old", oldKey, "new", newKey)
		return v
	}
	return defaultVal
}

// LoadConfigFromEnv reads weaver configuration from environment variables.
func LoadConfigFromEnv() Config {
	cfg := Config{
		Enabled:        env.Bool(EnvEnabled, false),
		RouterModel:    env.String(EnvRouterModel, DefaultRouterModel),
		SubagentModel:  env.String(EnvSubagentModel, DefaultSubagentModel),
		MaxIterations:  envIntWithFallback(EnvMaxIterations, EnvMaxIterationsDeprecated, DefaultMaxIterations),
		TokenBudget:    envIntWithFallback(EnvTokenBudget, EnvTokenBudgetDeprecated, DefaultTokenBudget),
		Timeout:        envDurationWithFallback(EnvTimeout, EnvTimeoutDeprecated, DefaultTimeout),
		MaxConcurrent:  envIntWithFallback(EnvMaxConcurrent, EnvMaxConcurrentDeprecated, DefaultMaxConcurrent),
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

// behaviorsFile is the YAML schema for weaver-behaviors.yaml.
type behaviorsFile struct {
	Behaviors []struct {
		Prefix            string `yaml:"prefix"`
		UserMessagePrefix string `yaml:"user_message_prefix"`
	} `yaml:"behaviors"`
}

// DefaultBehaviorsPath returns the default path for the behaviors YAML file.
func DefaultBehaviorsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "loom", "weaver-behaviors.yaml")
}

// LoadBehaviorsFromFile reads model behaviors from a YAML file.
// Returns nil, nil if the file does not exist (missing file is not an error).
// Returns nil, error if the file exists but cannot be parsed.
func LoadBehaviorsFromFile(path string) (map[string]ModelBehavior, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read behaviors file: %w", err)
	}

	var bf behaviorsFile
	if err := yaml.Unmarshal(data, &bf); err != nil {
		return nil, fmt.Errorf("parse behaviors YAML: %w", err)
	}

	if len(bf.Behaviors) == 0 {
		return nil, nil
	}

	result := make(map[string]ModelBehavior, len(bf.Behaviors))
	for _, b := range bf.Behaviors {
		if b.Prefix == "" {
			continue
		}
		result[b.Prefix] = ModelBehavior{
			UserMessagePrefix: b.UserMessagePrefix,
		}
	}
	return result, nil
}

// F10 auto-compose configuration (append-only).
const (
	// EnvAutoComposeEnabled gates the weaver auto-compose feature. When true,
	// unmatched compound queries will fall back to a deterministic keyword-based
	// domain selection + parallel dispatch + synthesis pipeline.
	EnvAutoComposeEnabled = "WEAVER_AUTO_COMPOSE_ENABLED"
	// EnvAutoComposeMaxDomains caps how many domains auto-compose may pick.
	EnvAutoComposeMaxDomains = "WEAVER_AUTO_COMPOSE_MAX_DOMAINS"

	// DefaultAutoComposeMaxDomains is the default cap when unset.
	DefaultAutoComposeMaxDomains = 3
)

// AutoComposeEnabled returns true when WEAVER_AUTO_COMPOSE_ENABLED is set.
// Default is false.
func AutoComposeEnabled() bool {
	return env.Bool(EnvAutoComposeEnabled, false)
}

// AutoComposeMaxDomains returns the configured cap for auto-compose domain
// selection. Default is DefaultAutoComposeMaxDomains.
func AutoComposeMaxDomains() int {
	n := env.Int(EnvAutoComposeMaxDomains, DefaultAutoComposeMaxDomains)
	if n <= 0 {
		return DefaultAutoComposeMaxDomains
	}
	return n
}
