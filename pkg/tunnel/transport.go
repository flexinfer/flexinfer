// Package tunnel provides secure tunneling for remote MCP server connections.
package tunnel

import (
	"context"
	"fmt"
	"io"
	"sync"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// SSHTransport wraps an SSH tunnel to provide MCP transport over a remote process.
type SSHTransport struct {
	tunnel    *SSHTunnel
	command   string
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	transport *mcp.StdioTransport
	mu        sync.Mutex
	connected bool
}

// NewSSHTransport creates a new SSH transport for a remote MCP server.
func NewSSHTransport(tunnel *SSHTunnel, command string) *SSHTransport {
	return &SSHTransport{
		tunnel:  tunnel,
		command: command,
	}
}

// Connect establishes the SSH connection and spawns the remote process.
func (t *SSHTransport) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.connected {
		return nil
	}

	// Connect the tunnel
	if err := t.tunnel.Connect(ctx); err != nil {
		return fmt.Errorf("ssh connect: %w", err)
	}

	// Spawn the remote process
	stdin, stdout, err := t.tunnel.SpawnProcess(ctx, t.command)
	if err != nil {
		t.tunnel.Close()
		return fmt.Errorf("spawn remote process: %w", err)
	}

	t.stdin = stdin
	t.stdout = stdout
	t.transport = mcp.NewStdioTransport(stdout, stdin)
	t.connected = true

	return nil
}

// Send sends a message over the SSH transport.
func (t *SSHTransport) Send(ctx context.Context, msg *mcp.Message) error {
	t.mu.Lock()
	if !t.connected {
		t.mu.Unlock()
		return fmt.Errorf("ssh transport not connected")
	}
	transport := t.transport
	t.mu.Unlock()

	return transport.Send(ctx, msg)
}

// Recv receives a message from the SSH transport.
func (t *SSHTransport) Recv(ctx context.Context) (*mcp.Message, error) {
	t.mu.Lock()
	if !t.connected {
		t.mu.Unlock()
		return nil, fmt.Errorf("ssh transport not connected")
	}
	transport := t.transport
	t.mu.Unlock()

	return transport.Recv(ctx)
}

// Close closes the SSH transport and underlying tunnel.
func (t *SSHTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected {
		return nil
	}

	var errs []error

	if t.stdin != nil {
		if err := t.stdin.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if t.stdout != nil {
		if err := t.stdout.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := t.tunnel.Close(); err != nil {
		errs = append(errs, err)
	}

	t.connected = false

	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}
	return nil
}

// Verify SSHTransport implements mcp.Transport
var _ mcp.Transport = (*SSHTransport)(nil)
