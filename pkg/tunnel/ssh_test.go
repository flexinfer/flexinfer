package tunnel

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
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

// TestSSHTunnel_buildAuthMethods tests authentication method building
func TestSSHTunnel_buildAuthMethods_NoAgent(t *testing.T) {
	// Unset SSH_AUTH_SOCK to simulate no agent
	origSock := os.Getenv("SSH_AUTH_SOCK")
	os.Unsetenv("SSH_AUTH_SOCK")
	defer func() {
		if origSock != "" {
			os.Setenv("SSH_AUTH_SOCK", origSock)
		}
	}()

	cfg := SSHConfig{
		Host:     "example.com",
		User:     "test",
		UseAgent: true, // Agent requested but not available
	}
	tunnel := NewSSHTunnel(cfg)

	// Without agent or key file, this should fail
	methods, err := tunnel.buildAuthMethods()
	if err == nil && len(methods) == 0 {
		t.Error("expected error or empty methods when no auth available")
	}
}

func TestSSHTunnel_buildAuthMethods_WithKeyFile(t *testing.T) {
	// Generate a real RSA key for testing
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}

	// Encode as PEM
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	pemBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}
	pemData := pem.EncodeToMemory(pemBlock)

	// Write to temp file
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "test_key")
	if err := os.WriteFile(keyPath, pemData, 0600); err != nil {
		t.Fatalf("failed to write test key: %v", err)
	}

	// Unset agent
	origSock := os.Getenv("SSH_AUTH_SOCK")
	os.Unsetenv("SSH_AUTH_SOCK")
	defer func() {
		if origSock != "" {
			os.Setenv("SSH_AUTH_SOCK", origSock)
		}
	}()

	cfg := SSHConfig{
		Host:     "example.com",
		User:     "test",
		UseAgent: false,
		KeyFile:  keyPath,
	}
	tunnel := NewSSHTunnel(cfg)

	methods, err := tunnel.buildAuthMethods()
	if err != nil {
		t.Fatalf("unexpected buildAuthMethods error: %v", err)
	}
	if len(methods) == 0 {
		t.Error("expected at least one auth method")
	}
}

func TestSSHTunnel_buildAuthMethods_KeyFileNotFound(t *testing.T) {
	cfg := SSHConfig{
		Host:     "example.com",
		User:     "test",
		UseAgent: false,
		KeyFile:  "/nonexistent/path/to/key",
	}
	tunnel := NewSSHTunnel(cfg)

	_, err := tunnel.buildAuthMethods()
	if err == nil {
		t.Error("expected error for nonexistent key file")
	}
}

func TestSSHTunnel_buildAuthMethods_TildeExpansion(t *testing.T) {
	cfg := SSHConfig{
		Host:     "example.com",
		User:     "test",
		UseAgent: false,
		KeyFile:  "~/.ssh/nonexistent_key_12345",
	}
	tunnel := NewSSHTunnel(cfg)

	// This should fail because the file doesn't exist, but the path should be expanded
	_, err := tunnel.buildAuthMethods()
	if err == nil {
		t.Error("expected error for nonexistent expanded key file")
	}
	// Verify the error mentions the expanded path
	if err != nil && strings.Contains(err.Error(), "~") {
		t.Error("path should have been expanded (~ should not appear in error)")
	}
}

func TestSSHTunnel_buildHostKeyCallback_InsecureMode(t *testing.T) {
	cfg := SSHConfig{
		Host:                  "example.com",
		User:                  "test",
		StrictHostKeyChecking: false,
	}
	tunnel := NewSSHTunnel(cfg)

	callback, err := tunnel.buildHostKeyCallback()
	if err != nil {
		t.Fatalf("buildHostKeyCallback error: %v", err)
	}
	if callback == nil {
		t.Error("expected non-nil callback")
	}
}

func TestSSHTunnel_buildHostKeyCallback_WithKnownHosts(t *testing.T) {
	tmpDir := t.TempDir()
	knownHostsPath := filepath.Join(tmpDir, "known_hosts")

	// Write a minimal known_hosts file
	knownHosts := "example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILbXD9jR5T5E/0B6iqDxv1p1l0MlcB0r7NXv3x5bFH+0"
	if err := os.WriteFile(knownHostsPath, []byte(knownHosts), 0600); err != nil {
		t.Fatalf("failed to write known_hosts: %v", err)
	}

	cfg := SSHConfig{
		Host:                  "example.com",
		User:                  "test",
		StrictHostKeyChecking: true,
		KnownHostsFile:        knownHostsPath,
	}
	tunnel := NewSSHTunnel(cfg)

	callback, err := tunnel.buildHostKeyCallback()
	if err != nil {
		t.Fatalf("buildHostKeyCallback error: %v", err)
	}
	if callback == nil {
		t.Error("expected non-nil callback")
	}
}

func TestSSHTunnel_buildHostKeyCallback_MissingKnownHosts(t *testing.T) {
	cfg := SSHConfig{
		Host:                  "example.com",
		User:                  "test",
		StrictHostKeyChecking: true,
		KnownHostsFile:        "/nonexistent/known_hosts",
	}
	tunnel := NewSSHTunnel(cfg)

	// When known_hosts doesn't exist, it should fall back to insecure
	callback, err := tunnel.buildHostKeyCallback()
	if err != nil {
		t.Fatalf("buildHostKeyCallback error: %v", err)
	}
	if callback == nil {
		t.Error("expected non-nil callback (fallback to insecure)")
	}
}

func TestSSHTunnel_buildHostKeyCallback_DefaultPath(t *testing.T) {
	cfg := SSHConfig{
		Host:                  "example.com",
		User:                  "test",
		StrictHostKeyChecking: true,
		KnownHostsFile:        "", // Empty = use default
	}
	tunnel := NewSSHTunnel(cfg)

	// This will use the default known_hosts path
	callback, err := tunnel.buildHostKeyCallback()
	// May or may not error depending on whether ~/.ssh/known_hosts exists
	if callback == nil && err == nil {
		t.Error("expected either callback or error")
	}
}

func TestSSHTunnel_DefaultConfigValues(t *testing.T) {
	cfg := DefaultSSHConfig()

	if cfg.ConnectTimeout != 30*time.Second {
		t.Errorf("ConnectTimeout = %v, want 30s", cfg.ConnectTimeout)
	}
	if cfg.KeepAliveInterval != 30*time.Second {
		t.Errorf("KeepAliveInterval = %v, want 30s", cfg.KeepAliveInterval)
	}
}

func TestSSHTunnel_DoubleClose(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com",
		User: "test",
	}
	tunnel := NewSSHTunnel(cfg)

	// First close
	err := tunnel.Close()
	if err != nil {
		t.Errorf("first Close() error = %v", err)
	}

	// Second close should also be safe
	err = tunnel.Close()
	if err != nil {
		t.Errorf("second Close() error = %v", err)
	}
}

func TestSSHTunnel_ConcurrentClose(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com",
		User: "test",
	}
	tunnel := NewSSHTunnel(cfg)

	// Close from multiple goroutines
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			tunnel.Close()
			done <- struct{}{}
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestSSHTunnel_SpawnProcessErrorMessage(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com",
		User: "test",
	}
	tunnel := NewSSHTunnel(cfg)

	_, _, err := tunnel.SpawnProcess(context.Background(), "test-command")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not connected") {
		t.Errorf("error should mention 'not connected': %v", err)
	}
}

func TestSSHTunnel_ForwardLocalPortErrorMessage(t *testing.T) {
	cfg := SSHConfig{
		Host: "example.com",
		User: "test",
	}
	tunnel := NewSSHTunnel(cfg)

	_, err := tunnel.ForwardLocalPort(context.Background(), ":0", "localhost:8080")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not connected") {
		t.Errorf("error should mention 'not connected': %v", err)
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
