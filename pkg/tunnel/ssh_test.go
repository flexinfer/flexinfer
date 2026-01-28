package tunnel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSSHConfig(t *testing.T) {
	cfg := DefaultSSHConfig()

	if !cfg.UseAgent {
		t.Error("expected UseAgent to be true by default")
	}
	if !cfg.StrictHostKeyChecking {
		t.Error("expected StrictHostKeyChecking to be true by default")
	}
	if cfg.ConnectTimeout == 0 {
		t.Error("expected ConnectTimeout to be set")
	}
	if cfg.KeepAliveInterval == 0 {
		t.Error("expected KeepAliveInterval to be set")
	}
}

func TestExpandPath(t *testing.T) {
	home := os.Getenv("HOME")

	tests := []struct {
		input    string
		expected string
	}{
		{"~/foo/bar", filepath.Join(home, "foo/bar")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~/.ssh/id_rsa", filepath.Join(home, ".ssh/id_rsa")},
	}

	for _, tt := range tests {
		result := expandPath(tt.input)
		if result != tt.expected {
			t.Errorf("expandPath(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestNewSSHTunnel(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com:22",
		User: "testuser",
	}

	tunnel := NewSSHTunnel(cfg)

	if tunnel == nil {
		t.Fatal("expected non-nil tunnel")
	}
	if tunnel.cfg.Host != "example.com:22" {
		t.Errorf("expected host example.com:22, got %s", tunnel.cfg.Host)
	}
	if tunnel.cfg.User != "testuser" {
		t.Errorf("expected user testuser, got %s", tunnel.cfg.User)
	}
}

func TestSSHTunnel_CloseWithoutConnect(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com:22",
		User: "testuser",
	}

	tunnel := NewSSHTunnel(cfg)

	// Should not error when closing an unconnected tunnel
	err := tunnel.Close()
	if err != nil {
		t.Errorf("unexpected error closing unconnected tunnel: %v", err)
	}
}

func TestSSHTunnel_SpawnWithoutConnect(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com:22",
		User: "testuser",
	}

	tunnel := NewSSHTunnel(cfg)

	_, _, err := tunnel.SpawnProcess(context.Background(), "echo hello")
	if err == nil {
		t.Error("expected error when spawning without connection")
	}
}

func TestSSHTunnel_ForwardWithoutConnect(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com:22",
		User: "testuser",
	}

	tunnel := NewSSHTunnel(cfg)

	_, err := tunnel.ForwardLocalPort(context.Background(), "localhost:0", "localhost:8080")
	if err == nil {
		t.Error("expected error when forwarding without connection")
	}
}

// Integration tests would require an actual SSH server
// They should be gated with build tags or environment variables
// Example:
//
// func TestSSHTunnel_Integration(t *testing.T) {
//     if os.Getenv("SSH_TEST_HOST") == "" {
//         t.Skip("SSH_TEST_HOST not set")
//     }
//     // ... actual integration test
// }
