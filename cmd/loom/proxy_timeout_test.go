package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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

func TestShouldResetDaemonTransport(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: true},
		{name: "eof", err: io.EOF, want: true},
		{name: "wrapped epipe", err: fmt.Errorf("wrapped: %w", syscall.EPIPE), want: true},
		{name: "wrapped econnreset", err: fmt.Errorf("wrapped: %w", syscall.ECONNRESET), want: true},
		{name: "broken pipe text", err: errors.New("write unix /tmp/loom.sock: broken pipe"), want: true},
		{name: "closed network text", err: errors.New("use of closed network connection"), want: true},
		{name: "unexpected eof text", err: errors.New("stream read failed: unexpected EOF"), want: true},
		{name: "generic", err: errors.New("permission denied"), want: false},
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
	if !shouldResetDaemonTransport(err) {
		t.Fatalf("expected timeout wrapper to require transport reset, got %v", err)
	}
}

func TestProxyDaemonRoundTripRecvFailureRequiresReset(t *testing.T) {
	req, _ := mcp.NewRequest(1, "tools/list", nil)
	_, err := proxyDaemonRoundTrip(context.Background(), &stubTransport{recvErr: io.EOF}, req, "tools/list")
	if err == nil {
		t.Fatal("expected recv error")
	}
	if !shouldResetDaemonTransport(err) {
		t.Fatalf("expected recv EOF to require reset, got %v", err)
	}
}
