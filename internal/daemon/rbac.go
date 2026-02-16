// Package daemon provides RBAC enforcement for tool access control.
package daemon

import (
	"fmt"
	"log/slog"
	"path"
	"strings"
)

// RBACConfig holds role-based access control configuration.
type RBACConfig struct {
	// Enabled activates RBAC enforcement. When false, all tool calls are allowed.
	Enabled bool `yaml:"enabled"`

	// DefaultPolicy is "allow" or "deny" when no binding matches an agent.
	DefaultPolicy string `yaml:"default_policy,omitempty"`

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
}

// NewRBACEnforcer creates an enforcer from config. Returns nil if RBAC is disabled.
func NewRBACEnforcer(cfg RBACConfig, logger *slog.Logger) *RBACEnforcer {
	if !cfg.Enabled {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &RBACEnforcer{cfg: cfg, logger: logger}
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

	roleName := e.resolveRole(agentID, agentType)
	if roleName == "" {
		allowed := strings.EqualFold(e.cfg.DefaultPolicy, "allow")
		return AccessDecision{
			Allowed: allowed,
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
			return AccessDecision{
				Allowed: true,
				AgentID: agentID,
				Server:  server,
				Tool:    tool,
				Role:    roleName,
				Reason:  fmt.Sprintf("allowed by pattern %q", pattern),
			}
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

// matchesPattern checks if toolName matches a glob pattern.
// Uses path.Match which supports *, ?, and [] wildcards.
func matchesPattern(pattern, toolName string) bool {
	matched, err := path.Match(pattern, toolName)
	if err != nil {
		return false
	}
	return matched
}
