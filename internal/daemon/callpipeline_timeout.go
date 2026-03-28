package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/crb2nu/loom/pkg/env"
)

// resolveToolCallTimeout determines the RPC timeout for a tools/call.
// Priority: explicit _timeout field > auto-derived from arguments > env/default.
func resolveToolCallTimeout(params callParams) time.Duration {
	method := params.Method
	if strings.TrimSpace(method) == "" {
		method = "tools/call"
	}

	// Non-tool methods use the standard timeout path.
	if strings.TrimSpace(method) != "tools/call" {
		return daemonRPCTimeoutForMethod(method)
	}

	base := daemonRPCTimeoutForMethod(method)

	// 1. Explicit _timeout field (highest priority).
	if hint := strings.TrimSpace(params.Timeout); hint != "" {
		if d, err := time.ParseDuration(hint); err == nil && d > 0 {
			return clampTimeout(d, base, maxDaemonToolRPCTimeout)
		}
		// Invalid _timeout: fall through to auto-derive.
	}

	// 2. Auto-derive from well-known argument fields.
	args := params.Arguments
	if len(args) == 0 {
		args = params.Params
	}
	if derived := deriveTimeoutFromArguments(args); derived > 0 {
		withBuffer := derived + autoDeriveDaemonTimeoutBuffer
		return clampTimeout(withBuffer, base, maxDaemonToolRPCTimeout)
	}

	// 3. Default (env or hardcoded 60s).
	return base
}

// deriveTimeoutFromArguments inspects well-known argument fields to infer a
// tool-level timeout. Returns 0 if no hint is found.
func deriveTimeoutFromArguments(args json.RawMessage) time.Duration {
	if len(args) == 0 {
		return 0
	}

	// Try to parse as tool call params (which nest arguments).
	var toolCall struct {
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(args, &toolCall); err == nil && len(toolCall.Arguments) > 0 {
		if d := extractTimeoutFromMap(toolCall.Arguments); d > 0 {
			return d
		}
	}

	// Try directly (smart-routing passes arguments at top level).
	return extractTimeoutFromMap(args)
}

func extractTimeoutFromMap(raw json.RawMessage) time.Duration {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0
	}

	// timeout_seconds (float64 -> seconds): mcp-gitlab, mcp-agent-context
	if v, ok := m["timeout_seconds"]; ok {
		if d := parseSecondsDuration(v); d > 0 {
			return d
		}
	}

	// timeoutSeconds (float64 -> seconds): mcp-k8s-ops, mcp-docker
	if v, ok := m["timeoutSeconds"]; ok {
		if d := parseSecondsDuration(v); d > 0 {
			return d
		}
	}

	// timeout (Go duration string): mcp-devbox
	if v, ok := m["timeout"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			if d, err := time.ParseDuration(s); err == nil && d > 0 {
				return d
			}
		}
		// Also try as numeric seconds (some tools use timeout: 60).
		if d := parseSecondsDuration(v); d > 0 {
			return d
		}
	}

	return 0
}

func parseSecondsDuration(raw json.RawMessage) time.Duration {
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil && f > 0 {
		return time.Duration(f * float64(time.Second))
	}
	return 0
}

func clampTimeout(d, min, max time.Duration) time.Duration {
	if d < min {
		return min
	}
	if d > max {
		return max
	}
	return d
}

func daemonRPCTimeoutForMethod(method string) time.Duration {
	if strings.TrimSpace(method) == "tools/call" {
		return normalizePositiveDuration(env.Duration("LOOM_DAEMON_TOOL_TIMEOUT", defaultDaemonToolRPCTimeout), defaultDaemonToolRPCTimeout)
	}
	return normalizePositiveDuration(env.Duration("LOOM_DAEMON_CONTROL_TIMEOUT", defaultDaemonControlRPCTimeout), defaultDaemonControlRPCTimeout)
}

func normalizePositiveDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func daemonRPCPhaseError(operation, phase string, timeout time.Duration, err error) error {
	op := strings.TrimSpace(operation)
	if op == "" {
		op = "daemon call"
	}
	if isRPCTimeout(err) {
		return fmt.Errorf("%s timeout during %s after %s (recoverable: daemon will reconnect upstream transport and retry on the next request): %w", op, phase, timeout, err)
	}
	return fmt.Errorf("%s failed during %s: %w", op, phase, err)
}

func isRPCTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "i/o timeout")
}

func shouldResetDaemonTransport(err error) bool {
	if err == nil {
		return false
	}
	if isRPCTimeout(err) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.ENOTCONN) {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "use of closed network connection") ||
		strings.Contains(lower, "unexpected eof") ||
		strings.Contains(lower, "transport closed")
}
