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
	Allowed bool   `json:"allowed"`
	AgentID string `json:"agent_id"`
	Server  string `json:"server"`
	Tool    string `json:"tool"`
	Role    string `json:"role"`
	Reason  string `json:"reason"`
}

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
	qualifiedTool := server + "__" + tool

	// Global deny policy always wins, even for privileged roles.
	for _, pattern := range e.cfg.GlobalDeny {
		if matchesPattern(pattern, qualifiedTool) {
			return AccessDecision{
				Allowed: false,
				AgentID: agentID,
				Server:  server,
				Tool:    tool,
				Role:    "",
				Reason:  fmt.Sprintf("denied by global policy pattern %q", pattern),
			}
		}
	}

	roleName := e.resolveRole(agentID, agentType)
	if roleName == "" {
		allowed := strings.EqualFold(e.cfg.DefaultPolicy, "allow")
		if allowed {
			return e.allowWithRateLimit(agentID, agentType, server, tool, "", fmt.Sprintf("no binding matched; default_policy=%s", e.cfg.DefaultPolicy))
		}
		return AccessDecision{
			Allowed: false,
			AgentID: agentID,
			Server:  server,
			Tool:    tool,
			Role:    "",
			Reason:  fmt.Sprintf("no binding matched; default_policy=%s", e.cfg.DefaultPolicy),
		}
	}

	role, ok := e.cfg.Roles[roleName]
	if !ok {
		return AccessDecision{
			Allowed: false,
			AgentID: agentID,
			Server:  server,
			Tool:    tool,
			Role:    roleName,
			Reason:  fmt.Sprintf("role %q not defined", roleName),
		}
	}

	// Deny-wins: check deny patterns first.
	for _, pattern := range role.Deny {
		if matchesPattern(pattern, qualifiedTool) {
			return AccessDecision{
				Allowed: false,
				AgentID: agentID,
				Server:  server,
				Tool:    tool,
				Role:    roleName,
				Reason:  fmt.Sprintf("denied by pattern %q", pattern),
			}
		}
	}

	// Check allow patterns.
	for _, pattern := range role.Allow {
		if matchesPattern(pattern, qualifiedTool) {
			return e.allowWithRateLimit(agentID, agentType, server, tool, roleName, fmt.Sprintf("allowed by pattern %q", pattern))
		}
	}

	// No allow pattern matched.
	return AccessDecision{
		Allowed: false,
		AgentID: agentID,
		Server:  server,
		Tool:    tool,
		Role:    roleName,
		Reason:  "no allow pattern matched",
	}
}

func (e *RBACEnforcer) allowWithRateLimit(agentID, agentType, server, tool, role, allowReason string) AccessDecision {
	if denyReason, limited := e.checkRateLimit(agentID, agentType, server, tool); limited {
		return AccessDecision{
			Allowed: false,
			AgentID: agentID,
			Server:  server,
			Tool:    tool,
			Role:    role,
			Reason:  denyReason,
		}
	}
	return AccessDecision{
		Allowed: true,
		AgentID: agentID,
		Server:  server,
		Tool:    tool,
		Role:    role,
		Reason:  allowReason,
	}
}

func (e *RBACEnforcer) checkRateLimit(agentID, agentType, server, tool string) (string, bool) {
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
			return fmt.Sprintf("rate limit exceeded: rule[%d] max=%d/min", i, rule.RequestsPerMinute), true
		}
		counter.Count++
		e.counts[key] = counter
		e.mu.Unlock()
		return "", false
	}

	return "", false
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
func (e *RBACEnforcer) resolveRole(agentID, agentType string) string {
	var typeMatch, wildcardMatch string

	for _, b := range e.cfg.Bindings {
		// Exact agent_id match (highest priority).
		if b.AgentID != "" && b.AgentID != "*" && b.AgentID == agentID {
			return b.Role
		}
		// Agent type match.
		if b.AgentType != "" && b.AgentType == agentType && typeMatch == "" {
			typeMatch = b.Role
		}
		// Wildcard match (lowest priority).
		if b.AgentID == "*" && wildcardMatch == "" {
			wildcardMatch = b.Role
		}
	}

	if typeMatch != "" {
		return typeMatch
	}
	return wildcardMatch
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
