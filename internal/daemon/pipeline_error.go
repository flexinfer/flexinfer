package daemon

import (
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// PipelineErrorData provides machine-readable metadata in call pipeline errors.
// It is placed in the Data field of mcp.Error responses so that clients can
// programmatically distinguish error types without parsing the human-readable
// message string.
type PipelineErrorData struct {
	Code       string `json:"code"`                  // e.g., "INVALID_INPUT", "RBAC_DENIED", "RATE_LIMITED", "TIMEOUT"
	Server     string `json:"server,omitempty"`      // originating server name
	Tool       string `json:"tool,omitempty"`        // originating tool name
	Stage      string `json:"stage"`                 // "parse", "authorize", "policy", "route", "build", "execute"
	Retryable  bool   `json:"retryable"`             // whether the caller may retry
	RetryAfter string `json:"retry_after,omitempty"` // for rate limits (e.g., "60s")
	Details    any    `json:"details,omitempty"`     // stage-specific data
}

// newPipelineError constructs a PipelineErrorData with consistent field
// population. All call pipeline error paths should use this helper (or its
// wrappers) so that new fields are added in one place.
func newPipelineError(code, server, tool, stage string, retryable bool) *PipelineErrorData {
	return &PipelineErrorData{
		Code:      code,
		Server:    server,
		Tool:      tool,
		Stage:     stage,
		Retryable: retryable,
	}
}

func newErrorResponse(id any, rpcCode int, message string, data any) *mcp.Message {
	return &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      id,
		Error: &mcp.Error{
			Code:    rpcCode,
			Message: message,
			Data:    data,
		},
	}
}

func newInvalidInputPipelineError(server, tool, stage, message string) *PipelineErrorData {
	code := "INVALID_INPUT"
	if strings.Contains(message, "could not resolve server for tool") {
		code = "TOOL_NOT_FOUND"
	} else if strings.Contains(message, "missing server") {
		code = "SERVER_NOT_FOUND"
	}
	return newPipelineError(code, server, tool, stage, false)
}

func newInternalPipelineError(server, tool, stage string, err error) *PipelineErrorData {
	code, retryable := classifyInternalError(err, stage)
	return newPipelineError(code, server, tool, stage, retryable)
}

func newGatePipelineError(code string) *PipelineErrorData {
	return newPipelineError(code, "", "", stageGate, true)
}

func newRBACDeniedPipelineError(server, tool string, decision AccessDecision) *PipelineErrorData {
	code := "RBAC_DENIED"
	retryable := false
	retryAfter := ""
	if decision.ReasonCode == "rate_limited" {
		code = "RATE_LIMITED"
		retryable = true
		retryAfter = "60s"
	}

	ped := newPipelineError(code, server, tool, stageAuth, retryable)
	ped.RetryAfter = retryAfter
	ped.Details = map[string]any{
		"reason_code": decision.ReasonCode,
		"agent_id":    decision.AgentID,
		"role":        decision.Role,
	}
	return ped
}

func newPolicyDeniedPipelineError(server, tool string, decision GatewayPolicyDecision) *PipelineErrorData {
	ped := newPipelineError("POLICY_DENIED", server, tool, stagePolicy, false)
	ped.Details = map[string]any{
		"policy_rule_id":     decision.RuleID,
		"policy_reason_code": decision.ReasonCode,
		"policy_stage":       decision.Stage,
		"policy_action":      decision.Action,
	}
	return ped
}

// classifyInternalError returns a pipeline error code based on the error and
// current pipeline stage.
func classifyInternalError(err error, stage string) (code string, retryable bool) {
	if isRPCTimeout(err) {
		return "TIMEOUT", true
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "response id mismatch") || strings.Contains(lower, "transport corruption") {
		return "TRANSPORT_CORRUPTION", true
	}
	if shouldResetDaemonTransport(err) {
		return "TRANSPORT_FAILURE", true
	}
	switch stage {
	case stageRoute:
		if strings.Contains(lower, "server unavailable") {
			return "SERVER_UNAVAILABLE", false
		}
		if strings.Contains(lower, "lock") {
			return "LOCK_TIMEOUT", true
		}
		return "CONNECTION_ERROR", true
	case stageExecute:
		return "TRANSPORT_FAILURE", true
	case stageBuild:
		return "SERVER_ERROR", false
	}
	return "SERVER_ERROR", false
}
