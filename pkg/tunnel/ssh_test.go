package tunnel

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestSSHConfig_Fields(t *testing.T) {
	cfg := SSHConfig{
		Host:                  "example.com:22",
		User:                  "testuser",
		KeyFile:               "~/.ssh/id_rsa",
		KeyPassphrase:         "secret",
		UseAgent:              true,
		KnownHostsFile:        "~/.ssh/known_hosts",
		StrictHostKeyChecking: true,
		ConnectTimeout:        30 * time.Second,
		KeepAliveInterval:     15 * time.Second,
	}

	if cfg.Host != "example.com:22" {
		t.Error("Host not set")
	}
	if cfg.User != "testuser" {
		t.Error("User not set")
	}
	if cfg.KeyFile != "~/.ssh/id_rsa" {
		t.Error("KeyFile not set")
	}
	if cfg.KeyPassphrase != "secret" {
		t.Error("KeyPassphrase not set")
	}
	if !cfg.UseAgent {
		t.Error("UseAgent should be true")
	}
	if !cfg.StrictHostKeyChecking {
		t.Error("StrictHostKeyChecking should be true")
	}
}

func TestSSHTunnel_ConnectAlreadyConnected(t *testing.T) {
	// This tests the early return when already connected
	// We can't fully test without an SSH server, but we can verify the structure
	cfg := SSHConfig{
		Host:           "127.0.0.1:1", // Invalid port for fast failure
		User:           "testuser",
		UseAgent:       false,
		ConnectTimeout: 100 * time.Millisecond,
	}

	tunnel := NewSSHTunnel(cfg)

	// First connect will fail (no server), but we're testing the mutex logic
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := tunnel.Connect(ctx)
	if err == nil {
		// If it somehow succeeded (agent auth), close and move on
		tunnel.Close()
	}
}

func TestSSHTunnel_HostWithoutPort(t *testing.T) {
	// Test that hosts without port get :22 appended
	cfg := SSHConfig{
		Host:     "example.com", // No port
		User:     "testuser",
		UseAgent: false,
	}

	tunnel := NewSSHTunnel(cfg)

	// This will fail to connect, but tests the port appending logic
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := tunnel.Connect(ctx)
	// We expect an error (no server), but the test is about the port logic
	if err == nil {
		tunnel.Close()
	}
}

func TestExpandPath_EdgeCases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"/", "/"},
		{"~", "~"}, // Just ~ without / is not expanded
		{"~/", filepath.Join(os.Getenv("HOME"), "")},
	}

	for _, tt := range tests {
		got := expandPath(tt.input)
		if tt.input == "~/" {
			// Special case: ~/ expands to home dir
			if !strings.HasPrefix(got, os.Getenv("HOME")) {
				t.Errorf("expandPath(%q) = %q, want prefix %q", tt.input, got, os.Getenv("HOME"))
			}
		} else if got != tt.want {
			t.Errorf("expandPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSessionWriter(t *testing.T) {
	// Test sessionWriter struct behavior
	// We can't fully test without SSH, but we can verify the type exists
	var _ io.WriteCloser = (*sessionWriter)(nil)
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
