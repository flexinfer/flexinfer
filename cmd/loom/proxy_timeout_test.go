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
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

type stubTransport struct {
	sendErr error
	recvErr error
	recvMsg *mcp.Message
}

func (s *stubTransport) Send(context.Context, *mcp.Message) error {
	return s.sendErr
}

func (s *stubTransport) Recv(context.Context) (*mcp.Message, error) {
	if s.recvErr != nil {
		return nil, s.recvErr
	}
	if s.recvMsg == nil {
		return &mcp.Message{JSONRPC: "2.0"}, nil
	}
	return s.recvMsg, nil
}

func (s *stubTransport) Close() error {
	return nil
}

func TestProxyRPCTimeoutForOperation_Defaults(t *testing.T) {
	t.Setenv("LOOM_PROXY_INIT_TIMEOUT", "")
	t.Setenv("LOOM_PROXY_CONTROL_TIMEOUT", "")
	t.Setenv("LOOM_PROXY_TOOL_TIMEOUT", "")

	if got := proxyRPCTimeoutForOperation("initialize"); got != defaultProxyInitRPCTimeout {
		t.Fatalf("proxyRPCTimeoutForOperation(initialize) = %v, want %v", got, defaultProxyInitRPCTimeout)
	}
	if got := proxyRPCTimeoutForOperation("tools/call"); got != defaultProxyToolRPCTimeout {
		t.Fatalf("proxyRPCTimeoutForOperation(tools/call) = %v, want %v", got, defaultProxyToolRPCTimeout)
	}
	if got := proxyRPCTimeoutForOperation("resources/list"); got != defaultProxyControlRPCTimeout {
		t.Fatalf("proxyRPCTimeoutForOperation(control) = %v, want %v", got, defaultProxyControlRPCTimeout)
	}
}

func TestProxyRPCTimeoutForOperation_EnvOverrideAndFallback(t *testing.T) {
	t.Setenv("LOOM_PROXY_INIT_TIMEOUT", "15s")
	t.Setenv("LOOM_PROXY_CONTROL_TIMEOUT", "45s")
	t.Setenv("LOOM_PROXY_TOOL_TIMEOUT", "90s")

	if got := proxyRPCTimeoutForOperation("initialize"); got != 15*time.Second {
		t.Fatalf("proxyRPCTimeoutForOperation(initialize) = %v, want 15s", got)
	}
	if got := proxyRPCTimeoutForOperation("tools/call"); got != 90*time.Second {
		t.Fatalf("proxyRPCTimeoutForOperation(tools/call) = %v, want 90s", got)
	}
	if got := proxyRPCTimeoutForOperation("resources/list"); got != 45*time.Second {
		t.Fatalf("proxyRPCTimeoutForOperation(control) = %v, want 45s", got)
	}

	t.Setenv("LOOM_PROXY_INIT_TIMEOUT", "0s")
	if got := proxyRPCTimeoutForOperation("initialize"); got != defaultProxyInitRPCTimeout {
		t.Fatalf("proxyRPCTimeoutForOperation(initialize) = %v, want %v", got, defaultProxyInitRPCTimeout)
	}
}

func TestProxyRPCPhaseError_TimeoutIncludesRecoverability(t *testing.T) {
	err := proxyRPCPhaseError("tools/call", "recv", 42*time.Second, context.DeadlineExceeded)
	msg := err.Error()

	if !strings.Contains(msg, "tools/call timeout during recv after 42s") {
		t.Fatalf("missing timeout phase details in %q", msg)
	}
	if !strings.Contains(msg, "recoverable: proxy will reconnect and retry on the next request") {
		t.Fatalf("missing recoverability hint in %q", msg)
	}
}

func TestProxyRPCPhaseError_ReturnsTransportError(t *testing.T) {
	err := proxyRPCPhaseError("tools/call", "send", 30*time.Second, io.EOF)
	var transportErr *proxyTransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("expected proxyTransportError, got %T: %v", err, err)
	}
	// The underlying EOF should be reachable via Unwrap chain.
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF in error chain, got %v", err)
	}
}

func TestProxyRPCPhaseError_TimeoutReturnsTransportError(t *testing.T) {
	err := proxyRPCPhaseError("loom/status", "recv", 10*time.Second, context.DeadlineExceeded)
	var transportErr *proxyTransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("expected proxyTransportError for timeout, got %T: %v", err, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded in chain, got %v", err)
	}
}

func TestProxyTransportError_NotMatchedByNonTransportErrors(t *testing.T) {
	// JSON parse errors should NOT match proxyTransportError.
	err := &json.SyntaxError{}
	var transportErr *proxyTransportError
	if errors.As(err, &transportErr) {
		t.Fatal("json.SyntaxError should not match proxyTransportError")
	}

	// Generic errors should NOT match.
	generic := errors.New("permission denied")
	if errors.As(generic, &transportErr) {
		t.Fatal("generic error should not match proxyTransportError")
	}
}

func TestShouldResetDaemonTransport(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: true},
		{name: "eof", err: io.EOF, want: true},
		{name: "unexpected eof structured", err: io.ErrUnexpectedEOF, want: true},
		{name: "wrapped unexpected eof", err: fmt.Errorf("read: %w", io.ErrUnexpectedEOF), want: true},
		{name: "net.ErrClosed", err: net.ErrClosed, want: true},
		{name: "wrapped epipe", err: fmt.Errorf("wrapped: %w", syscall.EPIPE), want: true},
		{name: "wrapped econnreset", err: fmt.Errorf("wrapped: %w", syscall.ECONNRESET), want: true},
		{name: "wrapped econnaborted", err: fmt.Errorf("wrapped: %w", syscall.ECONNABORTED), want: true},
		{name: "wrapped enotconn", err: fmt.Errorf("wrapped: %w", syscall.ENOTCONN), want: true},
		{name: "net.OpError", err: &net.OpError{Op: "read", Err: errors.New("connection refused")}, want: true},
		{name: "wrapped net.OpError", err: fmt.Errorf("transport: %w", &net.OpError{Op: "write", Err: syscall.EPIPE}), want: true},
		// String fallbacks (defense-in-depth for non-standard wrapping).
		{name: "broken pipe text", err: errors.New("write unix /tmp/loom.sock: broken pipe"), want: true},
		{name: "closed network text", err: errors.New("use of closed network connection"), want: true},
		{name: "unexpected eof text", err: errors.New("stream read failed: unexpected EOF"), want: true},
		{name: "connection reset text", err: errors.New("connection reset by peer"), want: true},
		// Non-transport errors should NOT trigger reset.
		{name: "generic", err: errors.New("permission denied"), want: false},
		{name: "json syntax", err: &json.SyntaxError{}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldResetDaemonTransport(tc.err); got != tc.want {
				t.Fatalf("shouldResetDaemonTransport(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestProxyRPCSendTimeoutErrorIncludesRecoverabilityHint(t *testing.T) {
	req, _ := mcp.NewRequest(1, "tools/call", map[string]any{"name": "noop"})
	err := proxyRPCSend(context.Background(), &stubTransport{sendErr: context.DeadlineExceeded}, req, "tools/call")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "recoverable") {
		t.Fatalf("expected recoverable hint in error, got %q", err.Error())
	}
	// The error should be a proxyTransportError.
	var transportErr *proxyTransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("expected proxyTransportError, got %T", err)
	}
}

func TestProxyDaemonRoundTripRecvFailureReturnsTransportError(t *testing.T) {
	req, _ := mcp.NewRequest(1, "tools/list", nil)
	_, err := proxyDaemonRoundTrip(context.Background(), &stubTransport{recvErr: io.EOF}, req, "tools/list")
	if err == nil {
		t.Fatal("expected recv error")
	}
	var transportErr *proxyTransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("expected proxyTransportError for recv EOF, got %T: %v", err, err)
	}
}

func TestProxyDaemonRoundTripSendFailureReturnsTransportError(t *testing.T) {
	req, _ := mcp.NewRequest(1, "loom/status", nil)
	_, err := proxyDaemonRoundTrip(context.Background(), &stubTransport{sendErr: syscall.EPIPE}, req, "loom/status")
	if err == nil {
		t.Fatal("expected send error")
	}
	var transportErr *proxyTransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("expected proxyTransportError for send EPIPE, got %T: %v", err, err)
	}
}

func TestProxyDaemonRoundTrip_RetryableDaemonTransportFailureBecomesTransportError(t *testing.T) {
	req, _ := mcp.NewRequest(1, "tools/call", map[string]any{"name": "noop"})
	_, err := proxyDaemonRoundTrip(context.Background(), &stubTransport{
		recvMsg: &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      json.RawMessage(`1`),
			Error: &mcp.Error{
				Code:    mcp.InternalError,
				Message: "transport closed",
				Data: map[string]any{
					"code":      "TRANSPORT_FAILURE",
					"stage":     "execute",
					"retryable": true,
				},
			},
		},
	}, req, "tools/call")
	if err == nil {
		t.Fatal("expected retryable daemon transport failure to surface as error")
	}
	var transportErr *proxyTransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("expected proxyTransportError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "daemon reported retryable transport failure") {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}

func TestProxyDaemonRoundTrip_RetryableTimeoutDaemonErrorPassesThrough(t *testing.T) {
	req, _ := mcp.NewRequest(1, "tools/call", map[string]any{"name": "noop"})
	resp, err := proxyDaemonRoundTrip(context.Background(), &stubTransport{
		recvMsg: &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      json.RawMessage(`1`),
			Error: &mcp.Error{
				Code:    mcp.InternalError,
				Message: "timed out",
				Data: map[string]any{
					"code":      "TIMEOUT",
					"stage":     "execute",
					"retryable": true,
				},
			},
		},
	}, req, "tools/call")
	if err != nil {
		t.Fatalf("expected timeout response to pass through, got error: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected daemon error response")
	}
}

func TestProxyDeriveTimeoutFromArguments(t *testing.T) {
	tests := []struct {
		name string
		args json.RawMessage
		want time.Duration
	}{
		{
			name: "timeout_seconds integer",
			args: json.RawMessage(`{"timeout_seconds": 600}`),
			want: 600 * time.Second,
		},
		{
			name: "timeoutSeconds integer",
			args: json.RawMessage(`{"timeoutSeconds": 300}`),
			want: 300 * time.Second,
		},
		{
			name: "Go duration string",
			args: json.RawMessage(`{"timeout": "10m"}`),
			want: 10 * time.Minute,
		},
		{
			name: "timeout as numeric seconds",
			args: json.RawMessage(`{"timeout": 120}`),
			want: 120 * time.Second,
		},
		{
			name: "no hint returns zero",
			args: json.RawMessage(`{"name": "some_tool"}`),
			want: 0,
		},
		{
			name: "empty args",
			args: nil,
			want: 0,
		},
		{
			name: "negative value",
			args: json.RawMessage(`{"timeout_seconds": -5}`),
			want: 0,
		},
		{
			name: "zero value",
			args: json.RawMessage(`{"timeout_seconds": 0}`),
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := proxyDeriveTimeoutFromArguments(tc.args)
			if got != tc.want {
				t.Fatalf("proxyDeriveTimeoutFromArguments() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProxyDaemonRoundTripWithTimeout_ZeroFallsBack(t *testing.T) {
	t.Setenv("LOOM_PROXY_TOOL_TIMEOUT", "")

	req, _ := mcp.NewRequest(1, "tools/call", map[string]any{"name": "noop"})
	resp, err := proxyDaemonRoundTripWithTimeout(
		context.Background(),
		&stubTransport{},
		req,
		"tools/call",
		0, // zero timeout → fallback to proxyDaemonRoundTrip
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response")
	}
}

func TestProxyDaemonRoundTripWithTimeout_SendFailure(t *testing.T) {
	req, _ := mcp.NewRequest(1, "tools/call", map[string]any{"name": "noop"})
	_, err := proxyDaemonRoundTripWithTimeout(
		context.Background(),
		&stubTransport{sendErr: io.EOF},
		req,
		"tools/call",
		5*time.Minute,
	)
	if err == nil {
		t.Fatal("expected send error")
	}
	var transportErr *proxyTransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("expected proxyTransportError, got %T: %v", err, err)
	}
}

func TestProxyDaemonRoundTripWithTimeout_RecvFailure(t *testing.T) {
	req, _ := mcp.NewRequest(1, "tools/call", map[string]any{"name": "noop"})
	_, err := proxyDaemonRoundTripWithTimeout(
		context.Background(),
		&stubTransport{recvErr: context.DeadlineExceeded},
		req,
		"tools/call",
		5*time.Minute,
	)
	if err == nil {
		t.Fatal("expected recv error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout in error, got %q", err.Error())
	}
}
