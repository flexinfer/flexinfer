package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestStartDaemon_AlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "loom.sock")

	// Start a real Unix socket listener to simulate a running daemon.
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	// startDaemonInBackground should return nil immediately.
	if err := startDaemonInBackground(socketPath); err != nil {
		t.Fatalf("expected nil when daemon already running, got: %v", err)
	}
}

func TestStartDaemon_NoLoomd(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "loom.sock")

	// Ensure no LaunchAgent plist exists (test env won't have one).
	// Ensure loomd is not in PATH by using a restricted PATH.
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", dir) // empty dir — no loomd binary
	defer os.Setenv("PATH", origPath)

	// Also ensure the loom binary sibling path doesn't exist.
	// startDaemonInBackground should fail to find loomd.
	err := startDaemonInBackground(socketPath)
	if err == nil {
		t.Fatal("expected error when loomd not found")
	}
}

func TestStartDaemon_NestedSocketPath(t *testing.T) {
	// Use /tmp for shorter paths — macOS 108-char Unix socket limit.
	dir, err := os.MkdirTemp("/tmp", "loom-test-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(dir)

	socketPath := filepath.Join(dir, "s", "l.sock")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Start a real Unix socket listener at the nested path.
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	// Should detect the active daemon.
	if err := startDaemonInBackground(socketPath); err != nil {
		t.Fatalf("expected nil for active socket, got: %v", err)
	}
}
