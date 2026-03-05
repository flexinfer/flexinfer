// Package daemon provides RBAC enforcement for tool access control.
package daemon

import (
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"
)

// RBACConfig holds role-based access control configuration.
type RBACConfig struct {
	// Enabled activates RBAC enforcement. When false, all tool calls are allowed.
	Enabled bool `yaml:"enabled"`

	// DefaultPolicy is "allow" or "deny" when no binding matches an agent.
	DefaultPolicy string `yaml:"default_policy,omitempty"`

	// GlobalDeny is a list of deny patterns enforced before role resolution.
	// Use this for organization-wide policy blocks that apply to all agents.
	GlobalDeny []string `yaml:"global_deny,omitempty"`

	// RateLimits is an ordered list of per-agent/per-tool rate limit rules.
	// The first matching rule is enforced.
	RateLimits []RBACRateLimit `yaml:"rate_limits,omitempty"`

	// Roles maps role names to their permission definitions.
	Roles map[string]RBACRole `yaml:"roles,omitempty"`

	// Bindings maps agents to roles.
	Bindings []RBACBinding `yaml:"bindings,omitempty"`
}

// RBACRole defines allowed and denied tool patterns for a role.
type RBACRole struct {
	// Allow is a list of glob patterns for permitted tools (e.g., "*", "*__list_*").
	Allow []string `yaml:"allow,omitempty"`

	// Deny is a list of glob patterns for denied tools. Deny wins over allow.
	Deny []string `yaml:"deny,omitempty"`
}

// RBACRateLimit defines a tool-call rate limit rule.
type RBACRateLimit struct {
	// AgentID optionally scopes the rule to a specific agent (or "*" wildcard).
	AgentID string `yaml:"agent_id,omitempty"`

	// AgentType optionally scopes the rule to an agent platform type.
	AgentType string `yaml:"agent_type,omitempty"`

	// Server is an optional glob pattern matching server names.
	Server string `yaml:"server,omitempty"`

	// Tool is an optional glob pattern matching tool names.
	Tool string `yaml:"tool,omitempty"`

	// RequestsPerMinute sets the maximum calls allowed in each UTC minute window.
	RequestsPerMinute int `yaml:"requests_per_minute"`
}

// RBACBinding maps an agent identity to a role.
type RBACBinding struct {
	// AgentID matches by agent identifier (exact or "*" wildcard).
	AgentID string `yaml:"agent_id,omitempty"`

	// AgentType matches by agent type (e.g., "claude-code", "codex").
	AgentType string `yaml:"agent_type,omitempty"`

	// Role is the role name from the Roles map.
	Role string `yaml:"role"`
}

// AccessDecision records the outcome of an RBAC check.
type AccessDecision struct {
	Allowed        bool            `json:"allowed"`
	AgentID        string          `json:"agent_id"`
	Server         string          `json:"server"`
	Tool           string          `json:"tool"`
	Role           string          `json:"role"`
	Reason         string          `json:"reason"`
	ReasonCode     string          `json:"reason_code,omitempty"`
	DryRun         bool            `json:"dry_run,omitempty"`
	MatchedRule    string          `json:"matched_rule,omitempty"`
	MatchedBinding *RBACBindingRef `json:"matched_binding,omitempty"`
}

// RBACBindingRef records which binding matched during evaluation.
type RBACBindingRef struct {
	AgentID   string `json:"agent_id,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	Role      string `json:"role"`
}

// RBACEvaluationMode controls whether an RBAC check should enforce side effects.
type RBACEvaluationMode string

const (
	// RBACEvaluationModeEnforce applies normal enforcement behavior.
	RBACEvaluationModeEnforce RBACEvaluationMode = "enforce"
	// RBACEvaluationModeDryRun evaluates policy without mutating limiter state.
	RBACEvaluationModeDryRun RBACEvaluationMode = "dry-run"
)

// RBACEnforcer evaluates tool access based on agent identity and role bindings.
type RBACEnforcer struct {
	cfg    RBACConfig
	logger *slog.Logger
	mu     sync.Mutex
	counts map[string]rateLimitCounter
	now    func() time.Time
}

type rateLimitCounter struct {
	WindowStart time.Time
	Count       int
}

// NewRBACEnforcer creates an enforcer from config. Returns nil if RBAC is disabled.
func NewRBACEnforcer(cfg RBACConfig, logger *slog.Logger) *RBACEnforcer {
	if !cfg.Enabled {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &RBACEnforcer{
		cfg:    cfg,
		logger: logger,
		counts: make(map[string]rateLimitCounter),
		now:    time.Now,
	}
}

// DefaultRBACConfig returns a disabled RBAC configuration.
func DefaultRBACConfig() RBACConfig {
	return RBACConfig{
		Enabled:       false,
		DefaultPolicy: "allow",
	}
}

// Check evaluates whether the given agent may call the specified tool.
func (e *RBACEnforcer) Check(agentID, agentType, server, tool string) AccessDecision {
	return e.CheckWithMode(agentID, agentType, server, tool, RBACEvaluationModeEnforce)
}

// Simulate evaluates policy without mutating rate-limit counters.
func (e *RBACEnforcer) Simulate(agentID, agentType, server, tool string) AccessDecision {
	return e.evaluate(agentID, agentType, server, tool, true)
}

// CheckWithMode evaluates RBAC for the given tool call in the specified mode.
func (e *RBACEnforcer) CheckWithMode(agentID, agentType, server, tool string, mode RBACEvaluationMode) AccessDecision {
	return e.evaluate(agentID, agentType, server, tool, mode == RBACEvaluationModeDryRun)
}

func (e *RBACEnforcer) evaluate(agentID, agentType, server, tool string, dryRun bool) AccessDecision {
	qualifiedTool := server + "__" + tool

	// Global deny policy always wins, even for privileged roles.
	for _, pattern := range e.cfg.GlobalDeny {
		if matchesPattern(pattern, qualifiedTool) {
			return AccessDecision{
				Allowed:     false,
				AgentID:     agentID,
				Server:      server,
				Tool:        tool,
				Role:        "",
				Reason:      fmt.Sprintf("denied by global policy pattern %q", pattern),
				ReasonCode:  "global_deny",
				DryRun:      dryRun,
				MatchedRule: pattern,
			}
		}
	}

	roleName, binding := e.resolveRole(agentID, agentType)
	var bindingRef *RBACBindingRef
	if binding != nil {
		bindingRef = &RBACBindingRef{
			AgentID:   binding.AgentID,
			AgentType: binding.AgentType,
			Role:      binding.Role,
		}
	}
	if roleName == "" {
		allowed := strings.EqualFold(e.cfg.DefaultPolicy, "allow")
		if allowed {
			return e.allowWithRateLimit(
				agentID,
				agentType,
				server,
				tool,
				"",
				dryRun,
				fmt.Sprintf("no binding matched; default_policy=%s", e.cfg.DefaultPolicy),
				"default_allow",
				fmt.Sprintf("default_policy=%s", e.cfg.DefaultPolicy),
				nil,
			)
		}
		return AccessDecision{
			Allowed:     false,
			AgentID:     agentID,
			Server:      server,
			Tool:        tool,
			Role:        "",
			Reason:      fmt.Sprintf("no binding matched; default_policy=%s", e.cfg.DefaultPolicy),
			ReasonCode:  "default_deny",
			DryRun:      dryRun,
			MatchedRule: fmt.Sprintf("default_policy=%s", e.cfg.DefaultPolicy),
		}
	}

	role, ok := e.cfg.Roles[roleName]
	if !ok {
		return AccessDecision{
			Allowed:        false,
			AgentID:        agentID,
			Server:         server,
			Tool:           tool,
			Role:           roleName,
			Reason:         fmt.Sprintf("role %q not defined", roleName),
			ReasonCode:     "role_not_defined",
			DryRun:         dryRun,
			MatchedBinding: bindingRef,
		}
	}

	// Deny-wins: check deny patterns first.
	for _, pattern := range role.Deny {
		if matchesPattern(pattern, qualifiedTool) {
			return AccessDecision{
				Allowed:        false,
				AgentID:        agentID,
				Server:         server,
				Tool:           tool,
				Role:           roleName,
				Reason:         fmt.Sprintf("denied by pattern %q", pattern),
				ReasonCode:     "role_deny",
				DryRun:         dryRun,
				MatchedRule:    pattern,
				MatchedBinding: bindingRef,
			}
		}
	}

	// Check allow patterns.
	for _, pattern := range role.Allow {
		if matchesPattern(pattern, qualifiedTool) {
			return e.allowWithRateLimit(
				agentID,
				agentType,
				server,
				tool,
				roleName,
				dryRun,
				fmt.Sprintf("allowed by pattern %q", pattern),
				"role_allow",
				pattern,
				bindingRef,
			)
		}
	}

	// No allow pattern matched.
	return AccessDecision{
		Allowed:        false,
		AgentID:        agentID,
		Server:         server,
		Tool:           tool,
		Role:           roleName,
		Reason:         "no allow pattern matched",
		ReasonCode:     "no_allow_match",
		DryRun:         dryRun,
		MatchedBinding: bindingRef,
	}
}

func (e *RBACEnforcer) allowWithRateLimit(
	agentID, agentType, server, tool, role string,
	dryRun bool,
	allowReason, allowReasonCode, matchedRule string,
	matchedBinding *RBACBindingRef,
) AccessDecision {
	if denyReason, limited, rule := e.checkRateLimit(agentID, agentType, server, tool, dryRun); limited {
		return AccessDecision{
			Allowed:        false,
			AgentID:        agentID,
			Server:         server,
			Tool:           tool,
			Role:           role,
			Reason:         denyReason,
			ReasonCode:     "rate_limited",
			DryRun:         dryRun,
			MatchedRule:    rule,
			MatchedBinding: matchedBinding,
		}
	}
	return AccessDecision{
		Allowed:        true,
		AgentID:        agentID,
		Server:         server,
		Tool:           tool,
		Role:           role,
		Reason:         allowReason,
		ReasonCode:     allowReasonCode,
		DryRun:         dryRun,
		MatchedRule:    matchedRule,
		MatchedBinding: matchedBinding,
	}
}

func (e *RBACEnforcer) checkRateLimit(agentID, agentType, server, tool string, dryRun bool) (string, bool, string) {
	for i, rule := range e.cfg.RateLimits {
		if rule.RequestsPerMinute <= 0 || !matchesRateRule(rule, agentID, agentType, server, tool) {
			continue
		}

		key := rateLimitKey(i, agentID, agentType, server, tool)
		window := e.now().UTC().Truncate(time.Minute)

		e.mu.Lock()
		counter := e.counts[key]
		if !counter.WindowStart.Equal(window) {
			counter = rateLimitCounter{WindowStart: window}
		}
		if counter.Count >= rule.RequestsPerMinute {
			e.counts[key] = counter
			e.mu.Unlock()
			return fmt.Sprintf("rate limit exceeded: rule[%d] max=%d/min", i, rule.RequestsPerMinute), true, fmt.Sprintf("rate_limits[%d]", i)
		}
		if dryRun {
			e.mu.Unlock()
			return "", false, fmt.Sprintf("rate_limits[%d]", i)
		}
		counter.Count++
		e.counts[key] = counter
		e.mu.Unlock()
		return "", false, fmt.Sprintf("rate_limits[%d]", i)
	}

	return "", false, ""
}

func matchesRateRule(rule RBACRateLimit, agentID, agentType, server, tool string) bool {
	if rule.AgentID != "" && rule.AgentID != "*" && rule.AgentID != agentID {
		return false
	}
	if rule.AgentType != "" && rule.AgentType != agentType {
		return false
	}
	if !matchesOptionalPattern(rule.Server, server) {
		return false
	}
	if !matchesOptionalPattern(rule.Tool, tool) {
		return false
	}
	return true
}

func matchesOptionalPattern(pattern, value string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	return matchesPattern(pattern, value)
}

func rateLimitKey(ruleIndex int, agentID, agentType, server, tool string) string {
	agentKey := "anonymous"
	if agentID != "" {
		agentKey = "id:" + agentID
	} else if agentType != "" {
		agentKey = "type:" + agentType
	}
	return fmt.Sprintf("%d|%s|%s|%s", ruleIndex, agentKey, server, tool)
}

// resolveRole finds the best matching role for an agent.
// Priority: exact agent_id > agent_type > wildcard "*".
func (e *RBACEnforcer) resolveRole(agentID, agentType string) (string, *RBACBinding) {
	var typeMatch, wildcardMatch *RBACBinding

	for i := range e.cfg.Bindings {
		b := e.cfg.Bindings[i]
		// Exact agent_id match (highest priority).
		if b.AgentID != "" && b.AgentID != "*" && b.AgentID == agentID {
			binding := b
			return binding.Role, &binding
		}
		// Agent type match.
		if b.AgentType != "" && b.AgentType == agentType && typeMatch == nil {
			binding := b
			typeMatch = &binding
		}
		// Wildcard match (lowest priority).
		if b.AgentID == "*" && wildcardMatch == nil {
			binding := b
			wildcardMatch = &binding
		}
	}

	if typeMatch != nil {
		return typeMatch.Role, typeMatch
	}
	if wildcardMatch != nil {
		return wildcardMatch.Role, wildcardMatch
	}
	return "", nil
}

// Config returns a copy of the RBAC configuration.
func (e *RBACEnforcer) Config() RBACConfig {
	return e.cfg
}

// matchesPattern checks if toolName matches a glob pattern.
// Uses path.Match which supports *, ?, and [] wildcards.
func matchesPattern(pattern, toolName string) bool {
	matched, err := path.Match(pattern, toolName)
	if err != nil {
		return false
	}
	return matched
}
