// daemon.go contains utilities for communicating with the loom daemon.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
)

const (
	defaultDaemonDialTimeout    = 5 * time.Second
	defaultDaemonControlTimeout = 30 * time.Second
	defaultDaemonToolTimeout    = 60 * time.Second
)

// dial connects to the loom daemon socket with a timeout.
func dial(socketPath string) (net.Conn, error) {
	timeout := normalizePositiveDuration(env.Duration("LOOM_DAEMON_DIAL_TIMEOUT", defaultDaemonDialTimeout), defaultDaemonDialTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	d := net.Dialer{Timeout: timeout}
	return d.DialContext(ctx, "unix", socketPath)
}

// call makes a JSON-RPC request to the loom daemon and returns the result.
func call(socketPath string, method string, params any) (json.RawMessage, error) {
	conn, err := dial(socketPath)
	if err != nil {
		dialTimeout := normalizePositiveDuration(env.Duration("LOOM_DAEMON_DIAL_TIMEOUT", defaultDaemonDialTimeout), defaultDaemonDialTimeout)
		return nil, daemonRPCPhaseError(method, "dial", dialTimeout, err)
	}
	defer conn.Close()

	transport := mcp.NewStdioTransport(conn, conn)
	ctx := context.Background()
	callTimeout := daemonRPCTimeout(method)

	req, err := mcp.NewRequest(1, method, params)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	sendCtx, sendCancel := context.WithTimeout(ctx, callTimeout)
	err = transport.Send(sendCtx, req)
	sendCancel()
	if err != nil {
		return nil, daemonRPCPhaseError(method, "send", callTimeout, err)
	}

	recvCtx, recvCancel := context.WithTimeout(ctx, callTimeout)
	resp, err := transport.Recv(recvCtx)
	recvCancel()
	if err != nil {
		return nil, daemonRPCPhaseError(method, "recv", callTimeout, err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("daemon error: %s", resp.Error.Message)
	}

	return resp.Result, nil
}

func daemonRPCTimeout(method string) time.Duration {
	if method == "tools/call" {
		return normalizePositiveDuration(env.Duration("LOOM_DAEMON_TOOL_TIMEOUT", defaultDaemonToolTimeout), defaultDaemonToolTimeout)
	}
	return normalizePositiveDuration(env.Duration("LOOM_DAEMON_CONTROL_TIMEOUT", defaultDaemonControlTimeout), defaultDaemonControlTimeout)
}

func daemonRPCPhaseError(method, phase string, timeout time.Duration, err error) error {
	if isRPCTimeout(err) {
		return fmt.Errorf("%s timeout during %s after %s (recoverable: retry the command): %w", method, phase, timeout, err)
	}
	return fmt.Errorf("%s failed during %s: %w", method, phase, err)
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

func normalizePositiveDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}
