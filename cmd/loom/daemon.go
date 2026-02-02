// daemon.go contains utilities for communicating with the loom daemon.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// dial connects to the loom daemon socket with a timeout.
func dial(socketPath string) (net.Conn, error) {
	return net.DialTimeout("unix", socketPath, 5*time.Second)
}

// call makes a JSON-RPC request to the loom daemon and returns the result.
func call(socketPath string, method string, params any) (json.RawMessage, error) {
	conn, err := dial(socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	transport := mcp.NewStdioTransport(conn, conn)
	ctx := context.Background()

	req, err := mcp.NewRequest(1, method, params)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if err := transport.Send(ctx, req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	resp, err := transport.Recv(ctx)
	if err != nil {
		return nil, fmt.Errorf("receive response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("daemon error: %s", resp.Error.Message)
	}

	return resp.Result, nil
}
