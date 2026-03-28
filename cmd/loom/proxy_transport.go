// proxy_transport.go — wire-level RPC, stdio I/O, and transport error handling.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
)

// proxyTransportError wraps daemon transport send/recv errors so the main loop
// can deterministically identify and reset broken connections without relying
// on string-based error classification.
type proxyTransportError struct {
	err error
}

func (e *proxyTransportError) Error() string { return e.err.Error() }
func (e *proxyTransportError) Unwrap() error { return e.err }

type proxyDaemonErrorData struct {
	Code      string `json:"code"`
	Stage     string `json:"stage"`
	Retryable bool   `json:"retryable"`
}

func proxyDaemonRoundTrip(ctx context.Context, daemon mcp.Transport, req *mcp.Message, operation string) (*mcp.Message, error) {
	proxyDaemonRPCMu.Lock()
	defer proxyDaemonRPCMu.Unlock()

	if err := proxyRPCSend(ctx, daemon, req, operation); err != nil {
		return nil, err
	}
	return proxyRPCRecvSkipNotifications(ctx, daemon, operation)
}

// proxyRPCRecvSkipNotifications reads from the daemon transport, forwarding any
// interleaved notifications to the client stdout, until a response (message with
// ID or no Method) is received.
func proxyRPCRecvSkipNotifications(ctx context.Context, daemon mcp.Transport, operation string) (*mcp.Message, error) {
	timeout := proxyRPCTimeoutForOperation(operation)
	recvCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		resp, err := daemon.Recv(recvCtx)
		if err != nil {
			return nil, proxyRPCPhaseError(operation, "recv", timeout, err)
		}
		// A notification has no ID but has a Method.
		if resp.ID == nil && resp.Method != "" {
			proxyForwardNotification(ctx, resp)
			continue
		}
		if err := proxyRetryableDaemonResponse(resp); err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// proxyForwardNotification writes a daemon notification to the client stdio transport.
func proxyForwardNotification(ctx context.Context, notif *mcp.Message) {
	if proxyStdioTransport == nil {
		return
	}
	proxyStdioWriteMu.Lock()
	_ = proxyStdioTransport.Send(ctx, notif)
	proxyStdioWriteMu.Unlock()
}

// proxyDaemonRoundTripWithTimeout is like proxyDaemonRoundTrip but uses an
// extended timeout when the tool declares a long execution window. The RPC
// timeout is max(default, toolTimeout+buffer) capped at maxProxyToolRPCTimeout.
func proxyDaemonRoundTripWithTimeout(ctx context.Context, daemon mcp.Transport, req *mcp.Message, operation string, toolTimeout time.Duration) (*mcp.Message, error) {
	if toolTimeout <= 0 {
		return proxyDaemonRoundTrip(ctx, daemon, req, operation)
	}

	base := proxyRPCTimeoutForOperation(operation)
	extended := toolTimeout + autoProxyTimeoutBuffer
	timeout := extended
	if timeout < base {
		timeout = base
	}
	if timeout > maxProxyToolRPCTimeout {
		timeout = maxProxyToolRPCTimeout
	}

	proxyDaemonRPCMu.Lock()
	defer proxyDaemonRPCMu.Unlock()

	sendCtx, sendCancel := context.WithTimeout(ctx, timeout)
	err := daemon.Send(sendCtx, req)
	sendCancel()
	if err != nil {
		return nil, proxyRPCPhaseError(operation, "send", timeout, err)
	}

	recvCtx, recvCancel := context.WithTimeout(ctx, timeout)
	defer recvCancel()
	for {
		resp, recvErr := daemon.Recv(recvCtx)
		if recvErr != nil {
			return nil, proxyRPCPhaseError(operation, "recv", timeout, recvErr)
		}
		// Forward interleaved notifications to client.
		if resp.ID == nil && resp.Method != "" {
			proxyForwardNotification(ctx, resp)
			continue
		}
		if err := proxyRetryableDaemonResponse(resp); err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// proxyDeriveTimeoutFromArguments inspects well-known argument fields to infer
// a tool-level timeout from the tool call arguments. Returns 0 if no hint found.
func proxyDeriveTimeoutFromArguments(args json.RawMessage) time.Duration {
	if len(args) == 0 {
		return 0
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil {
		return 0
	}

	// timeout_seconds (float64 → seconds): mcp-gitlab, mcp-agent-context
	if v, ok := m["timeout_seconds"]; ok {
		if d := proxyParseSecondsDuration(v); d > 0 {
			return d
		}
	}

	// timeoutSeconds (float64 → seconds): mcp-k8s-ops, mcp-docker
	if v, ok := m["timeoutSeconds"]; ok {
		if d := proxyParseSecondsDuration(v); d > 0 {
			return d
		}
	}

	// timeout (Go duration string or numeric seconds): mcp-devbox
	if v, ok := m["timeout"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			if d, err := time.ParseDuration(s); err == nil && d > 0 {
				return d
			}
		}
		if d := proxyParseSecondsDuration(v); d > 0 {
			return d
		}
	}

	return 0
}

func proxyParseSecondsDuration(raw json.RawMessage) time.Duration {
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil && f > 0 {
		return time.Duration(f * float64(time.Second))
	}
	return 0
}

func proxyRPCSend(ctx context.Context, transport mcp.Transport, msg *mcp.Message, operation string) error {
	timeout := proxyRPCTimeoutForOperation(operation)
	sendCtx, cancel := context.WithTimeout(ctx, timeout)
	err := transport.Send(sendCtx, msg)
	cancel()
	if err != nil {
		return proxyRPCPhaseError(operation, "send", timeout, err)
	}
	return nil
}

func proxyRPCRecv(ctx context.Context, transport mcp.Transport, operation string) (*mcp.Message, error) {
	timeout := proxyRPCTimeoutForOperation(operation)
	recvCtx, cancel := context.WithTimeout(ctx, timeout)
	resp, err := transport.Recv(recvCtx)
	cancel()
	if err != nil {
		return nil, proxyRPCPhaseError(operation, "recv", timeout, err)
	}
	return resp, nil
}

func proxyRPCTimeoutForOperation(operation string) time.Duration {
	op := strings.TrimSpace(operation)
	switch op {
	case "initialize":
		return normalizePositiveDuration(env.Duration("LOOM_PROXY_INIT_TIMEOUT", defaultProxyInitRPCTimeout), defaultProxyInitRPCTimeout)
	case "tools/call":
		return normalizePositiveDuration(env.Duration("LOOM_PROXY_TOOL_TIMEOUT", defaultProxyToolRPCTimeout), defaultProxyToolRPCTimeout)
	default:
		return normalizePositiveDuration(env.Duration("LOOM_PROXY_CONTROL_TIMEOUT", defaultProxyControlRPCTimeout), defaultProxyControlRPCTimeout)
	}
}

func proxyRPCPhaseError(operation, phase string, timeout time.Duration, err error) error {
	op := strings.TrimSpace(operation)
	if op == "" {
		op = "daemon rpc"
	}
	var inner error
	if isRPCTimeout(err) {
		inner = fmt.Errorf("%s timeout during %s after %s (recoverable: proxy will reconnect and retry on the next request): %w", op, phase, timeout, err)
	} else {
		inner = fmt.Errorf("%s failed during %s: %w", op, phase, err)
	}
	return &proxyTransportError{err: inner}
}

func proxyRetryableDaemonResponse(resp *mcp.Message) error {
	if resp == nil || resp.Error == nil || resp.Error.Data == nil {
		return nil
	}

	meta, ok := proxyParseDaemonErrorData(resp.Error.Data)
	if !ok || !meta.Retryable {
		return nil
	}

	switch meta.Code {
	case "TRANSPORT_FAILURE", "TRANSPORT_CORRUPTION", "LOCK_TIMEOUT", "CONNECTION_ERROR":
		return &proxyTransportError{err: fmt.Errorf("daemon reported retryable %s: %s",
			proxyDaemonRetryableErrorLabel(meta),
			resp.Error.Message)}
	default:
		return nil
	}
}

func proxyDaemonRetryableErrorLabel(meta proxyDaemonErrorData) string {
	code := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(meta.Code), "_", " "))
	stage := strings.TrimSpace(meta.Stage)
	if stage == "" {
		return code
	}
	return fmt.Sprintf("%s during %s", code, stage)
}

func proxyParseDaemonErrorData(data any) (proxyDaemonErrorData, bool) {
	switch typed := data.(type) {
	case proxyDaemonErrorData:
		return typed, true
	case *proxyDaemonErrorData:
		if typed != nil {
			return *typed, true
		}
		return proxyDaemonErrorData{}, false
	case map[string]any:
		return proxyDaemonErrorData{
			Code:      proxyDaemonErrorString(typed["code"]),
			Stage:     proxyDaemonErrorString(typed["stage"]),
			Retryable: proxyDaemonErrorBool(typed["retryable"]),
		}, true
	case json.RawMessage:
		var meta proxyDaemonErrorData
		if err := json.Unmarshal(typed, &meta); err == nil {
			return meta, true
		}
	case []byte:
		var meta proxyDaemonErrorData
		if err := json.Unmarshal(typed, &meta); err == nil {
			return meta, true
		}
	default:
		raw, err := json.Marshal(data)
		if err == nil {
			var meta proxyDaemonErrorData
			if err := json.Unmarshal(raw, &meta); err == nil {
				return meta, true
			}
		}
	}

	return proxyDaemonErrorData{}, false
}

func proxyDaemonErrorString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func proxyDaemonErrorBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true")
	default:
		return false
	}
}

func shouldResetDaemonTransport(err error) bool {
	if err == nil {
		return false
	}
	// Primary path: structured error classification via errors.Is/errors.As.
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
	// Any network operation error indicates a broken transport.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	// Defense-in-depth: string fallbacks for non-standard error wrapping.
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "use of closed network connection") ||
		strings.Contains(lower, "unexpected eof")
}
