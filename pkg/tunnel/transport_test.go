package tunnel

import (
	"context"
	"testing"
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
