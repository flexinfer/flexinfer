package tunnel

import (
	"context"
	"testing"
	"time"
)

func TestNewSSHTransport(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com:22",
		User: "testuser",
	}
	tunnel := NewSSHTunnel(cfg)

	transport := NewSSHTransport(tunnel, "mcp-server")

	if transport == nil {
		t.Fatal("expected non-nil transport")
	}
	if transport.tunnel != tunnel {
		t.Error("tunnel not set correctly")
	}
	if transport.command != "mcp-server" {
		t.Errorf("expected command 'mcp-server', got %q", transport.command)
	}
	if transport.connected {
		t.Error("expected transport to start disconnected")
	}
}

func TestSSHTransport_SendWithoutConnect(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com:22",
		User: "testuser",
	}
	tunnel := NewSSHTunnel(cfg)
	transport := NewSSHTransport(tunnel, "mcp-server")

	err := transport.Send(context.Background(), nil)
	if err == nil {
		t.Error("expected error sending without connection")
	}
}

func TestSSHTransport_RecvWithoutConnect(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com:22",
		User: "testuser",
	}
	tunnel := NewSSHTunnel(cfg)
	transport := NewSSHTransport(tunnel, "mcp-server")

	_, err := transport.Recv(context.Background())
	if err == nil {
		t.Error("expected error receiving without connection")
	}
}

func TestSSHTransport_CloseWithoutConnect(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com:22",
		User: "testuser",
	}
	tunnel := NewSSHTunnel(cfg)
	transport := NewSSHTransport(tunnel, "mcp-server")

	err := transport.Close()
	if err != nil {
		t.Errorf("unexpected error closing disconnected transport: %v", err)
	}
}

func TestSSHTransport_DoubleClose(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com:22",
		User: "testuser",
	}
	tunnel := NewSSHTunnel(cfg)
	transport := NewSSHTransport(tunnel, "mcp-server")

	// First close should succeed
	err := transport.Close()
	if err != nil {
		t.Errorf("first Close() error = %v", err)
	}

	// Second close should also succeed (idempotent)
	err = transport.Close()
	if err != nil {
		t.Errorf("second Close() error = %v", err)
	}
}

func TestSSHTransport_ConnectWithoutTunnel(t *testing.T) {
	cfg := SSHConfig{
		Host:     "127.0.0.1:1", // Invalid port
		User:     "testuser",
		UseAgent: false,
	}
	tunnel := NewSSHTunnel(cfg)
	transport := NewSSHTransport(tunnel, "mcp-server")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := transport.Connect(ctx)
	if err == nil {
		transport.Close()
		t.Error("expected error for invalid connection")
	}
}

func TestSSHTransport_Fields(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com:22",
		User: "testuser",
	}
	tunnel := NewSSHTunnel(cfg)
	transport := NewSSHTransport(tunnel, "my-command --flag")

	if transport.tunnel != tunnel {
		t.Error("tunnel not set correctly")
	}
	if transport.command != "my-command --flag" {
		t.Errorf("command = %q, want 'my-command --flag'", transport.command)
	}
	if transport.connected {
		t.Error("should start disconnected")
	}
	if transport.stdin != nil {
		t.Error("stdin should be nil initially")
	}
	if transport.stdout != nil {
		t.Error("stdout should be nil initially")
	}
}

// Integration tests would require an actual SSH server
// They should be gated with build tags or environment variables
// Example:
//
// func TestSSHTransport_Integration(t *testing.T) {
//     if os.Getenv("SSH_TEST_HOST") == "" {
//         t.Skip("SSH_TEST_HOST not set")
//     }
//     // ... actual integration test
// }
