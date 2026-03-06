package daemon

import (
	"strings"
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

// classifyTransportError returns a pipeline error code based on the nature of
// the transport failure.
func classifyTransportError(err error) (code string, retryable bool) {
	if isRPCTimeout(err) {
		return "TIMEOUT", true
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "transport corruption") ||
		strings.Contains(lower, "response id mismatch") {
		return "TRANSPORT_CORRUPTION", true
	}
	return "TRANSPORT_FAILURE", true
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
		return "CONNECTION_ERROR", true
	case stageExecute:
		return "TRANSPORT_FAILURE", true
	case stageBuild:
		return "SERVER_ERROR", false
	}
	return "SERVER_ERROR", false
}
