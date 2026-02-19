package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

const (
	policyStageRequest = "request"
	policyActionDeny   = "deny"

	defaultGatewayRequestReasonCode = "GATEWAY_POLICY_REQUEST_DENY"
)

// GatewayPolicyConfig controls request/response policy hooks in the call pipeline.
type GatewayPolicyConfig struct {
	// Enabled activates gateway policy checks.
	Enabled bool `yaml:"enabled"`

	// Request contains pre-forward request rules.
	Request []GatewayRequestPolicyRule `yaml:"request,omitempty"`
}

// GatewayRequestPolicyRule describes one pre-forward policy rule.
type GatewayRequestPolicyRule struct {
	// ID is a stable identifier written to audits and error envelopes.
	ID string `yaml:"id,omitempty"`

	// Server is an optional glob for server names (e.g., "github", "k8s_*").
	Server string `yaml:"server,omitempty"`

	// Tool is an optional glob for tool names (e.g., "delete_*").
	Tool string `yaml:"tool,omitempty"`

	// RequiredArguments must all be present in tool arguments.
	RequiredArguments []string `yaml:"required_arguments,omitempty"`

	// ForbiddenArguments deny requests when any key is present in arguments.
	ForbiddenArguments []string `yaml:"forbidden_arguments,omitempty"`

	// ContainsAny denies when request payload JSON contains any substring.
	ContainsAny []string `yaml:"contains_any,omitempty"`

	// ReasonCode is a stable machine-readable deny reason.
	ReasonCode string `yaml:"reason_code,omitempty"`
}

// GatewayPolicyDecision captures allow/deny outcome for policy checks.
type GatewayPolicyDecision struct {
	Allowed    bool
	Stage      string
	Action     string
	RuleID     string
	ReasonCode string
	Reason     string
}

// DefaultGatewayPolicyConfig returns disabled policy defaults.
func DefaultGatewayPolicyConfig() GatewayPolicyConfig {
	return GatewayPolicyConfig{
		Enabled: false,
	}
}

// GatewayPolicyEnforcer evaluates policy rules for request hooks.
type GatewayPolicyEnforcer struct {
	cfg    GatewayPolicyConfig
	logger *slog.Logger
}

// NewGatewayPolicyEnforcer creates an enforcer, or nil when policy is disabled.
func NewGatewayPolicyEnforcer(cfg GatewayPolicyConfig, logger *slog.Logger) *GatewayPolicyEnforcer {
	if !cfg.Enabled {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &GatewayPolicyEnforcer{
		cfg:    cfg,
		logger: logger,
	}
}

// CheckRequest evaluates pre-forward request rules.
func (e *GatewayPolicyEnforcer) CheckRequest(server, tool string, params callParams) GatewayPolicyDecision {
	for i, rule := range e.cfg.Request {
		if !matchesOptionalPattern(rule.Server, server) {
			continue
		}
		if !matchesOptionalPattern(rule.Tool, tool) {
			continue
		}
		decision, denied := evaluateGatewayRequestRule(rule, i, params)
		if denied {
			e.logger.Warn("gateway policy denied request",
				"server", server,
				"tool", tool,
				"rule_id", decision.RuleID,
				"reason_code", decision.ReasonCode,
				"reason", decision.Reason,
			)
			return decision
		}
	}

	return GatewayPolicyDecision{
		Allowed: true,
		Stage:   policyStageRequest,
		Action:  policyActionDeny,
	}
}

func evaluateGatewayRequestRule(rule GatewayRequestPolicyRule, idx int, params callParams) (GatewayPolicyDecision, bool) {
	ruleID := normalizeGatewayRuleID(rule.ID, idx)
	reasonCode := normalizeGatewayReasonCode(rule.ReasonCode)
	args := extractGatewayRequestArguments(params)

	if len(rule.RequiredArguments) > 0 {
		missing := findMissingKeys(args, rule.RequiredArguments)
		if len(missing) > 0 {
			return GatewayPolicyDecision{
				Allowed:    false,
				Stage:      policyStageRequest,
				Action:     policyActionDeny,
				RuleID:     ruleID,
				ReasonCode: reasonCode,
				Reason:     fmt.Sprintf("missing required arguments: %s", strings.Join(missing, ", ")),
			}, true
		}
	}

	if len(rule.ForbiddenArguments) > 0 {
		present := findPresentKeys(args, rule.ForbiddenArguments)
		if len(present) > 0 {
			return GatewayPolicyDecision{
				Allowed:    false,
				Stage:      policyStageRequest,
				Action:     policyActionDeny,
				RuleID:     ruleID,
				ReasonCode: reasonCode,
				Reason:     fmt.Sprintf("forbidden arguments present: %s", strings.Join(present, ", ")),
			}, true
		}
	}

	if pattern, ok := payloadContainsAny(gatewayRequestPayload(params), rule.ContainsAny); ok {
		return GatewayPolicyDecision{
			Allowed:    false,
			Stage:      policyStageRequest,
			Action:     policyActionDeny,
			RuleID:     ruleID,
			ReasonCode: reasonCode,
			Reason:     fmt.Sprintf("request payload matched blocked pattern %q", pattern),
		}, true
	}

	return GatewayPolicyDecision{Allowed: true, Stage: policyStageRequest, Action: policyActionDeny}, false
}

func normalizeGatewayRuleID(id string, idx int) string {
	if trimmed := strings.TrimSpace(id); trimmed != "" {
		return trimmed
	}
	return fmt.Sprintf("request-rule-%d", idx+1)
}

func normalizeGatewayReasonCode(reasonCode string) string {
	if trimmed := strings.TrimSpace(reasonCode); trimmed != "" {
		return trimmed
	}
	return defaultGatewayRequestReasonCode
}

func extractGatewayRequestArguments(params callParams) map[string]any {
	if len(params.Arguments) > 0 {
		args := make(map[string]any)
		if err := json.Unmarshal(params.Arguments, &args); err == nil {
			return args
		}
	}

	if len(params.Params) == 0 {
		return map[string]any{}
	}

	var payload map[string]any
	if err := json.Unmarshal(params.Params, &payload); err != nil {
		return map[string]any{}
	}

	if nested, ok := payload["arguments"].(map[string]any); ok {
		return nested
	}

	return payload
}

func findMissingKeys(args map[string]any, required []string) []string {
	missing := make([]string, 0, len(required))
	for _, key := range required {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := args[key]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing
}

func findPresentKeys(args map[string]any, forbidden []string) []string {
	present := make([]string, 0, len(forbidden))
	for _, key := range forbidden {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := args[key]; ok {
			present = append(present, key)
		}
	}
	sort.Strings(present)
	return present
}

func gatewayRequestPayload(params callParams) string {
	if len(params.Params) > 0 {
		return string(params.Params)
	}
	if len(params.Arguments) > 0 {
		return string(params.Arguments)
	}
	return ""
}

func payloadContainsAny(payload string, patterns []string) (string, bool) {
	if payload == "" || len(patterns) == 0 {
		return "", false
	}
	payloadLower := strings.ToLower(payload)
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(payloadLower, strings.ToLower(p)) {
			return p, true
		}
	}
	return "", false
}
