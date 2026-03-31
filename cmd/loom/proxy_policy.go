package main

import (
	"encoding/json"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/policy"
)

// proxyPolicyEngine is the package-level policy engine initialized at proxy
// startup. When no registry is available it falls back to the hard-coded
// default rules (kubectl edit / kubectl set env).
var proxyPolicyEngine *policy.Engine

// proxyFluxPolicyResponse inspects a tool call request and blocks commands
// that violate any registered guardrail policy before the daemon sees them.
// The function signature is kept for backward compatibility with existing
// call sites; it delegates to the registry-driven policy engine.
func proxyFluxPolicyResponse(msg *mcp.Message) (*mcp.Message, bool) {
	engine := proxyPolicyEngine
	if engine == nil {
		engine = policy.DefaultEngine()
	}
	return proxyPolicyCheck(engine, msg)
}

// proxyPolicyCheck evaluates a single MCP message against the given engine.
func proxyPolicyCheck(engine *policy.Engine, msg *mcp.Message) (*mcp.Message, bool) {
	if msg == nil {
		return nil, false
	}

	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return nil, false
	}

	if denial, blocked := engine.Check(params.Name, params.Arguments); blocked {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, denial), true
	}
	return nil, false
}
