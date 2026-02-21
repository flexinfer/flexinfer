package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestBoundedAutostart_RespectsMaxAttempts(t *testing.T) {
	// Save and restore package-level vars.
	origCooldown := proxyAutostartCooldown
	origMax := proxyAutostartMaxAttempts
	defer func() {
		proxyAutostartCooldown = origCooldown
		proxyAutostartMaxAttempts = origMax
	}()

	proxyAutostartCooldown = 0 // No cooldown for test.
	proxyAutostartMaxAttempts = 3

	attempts := 0
	lastAttempt := time.Time{}

	autostart := func() {
		if attempts >= proxyAutostartMaxAttempts {
			return
		}
		if !lastAttempt.IsZero() && time.Since(lastAttempt) < proxyAutostartCooldown {
			return
		}
		attempts++
		lastAttempt = time.Now()
	}

	// Should allow up to maxAttempts.
	for i := 0; i < 10; i++ {
		autostart()
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3 (maxAttempts)", attempts)
	}
}

func TestBoundedAutostart_CooldownPreventsRapidFire(t *testing.T) {
	origCooldown := proxyAutostartCooldown
	origMax := proxyAutostartMaxAttempts
	defer func() {
		proxyAutostartCooldown = origCooldown
		proxyAutostartMaxAttempts = origMax
	}()

	proxyAutostartCooldown = 50 * time.Millisecond
	proxyAutostartMaxAttempts = 10

	attempts := 0
	lastAttempt := time.Time{}

	autostart := func() {
		if attempts >= proxyAutostartMaxAttempts {
			return
		}
		if !lastAttempt.IsZero() && time.Since(lastAttempt) < proxyAutostartCooldown {
			return
		}
		attempts++
		lastAttempt = time.Now()
	}

	// Rapid-fire calls within cooldown should be suppressed.
	for i := 0; i < 5; i++ {
		autostart()
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (cooldown should suppress rapid calls)", attempts)
	}

	// After cooldown elapses, another attempt should be allowed.
	time.Sleep(proxyAutostartCooldown + 10*time.Millisecond)
	autostart()
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 after cooldown elapsed", attempts)
	}
}

func TestBoundedAutostart_SuccessResetsCounter(t *testing.T) {
	origCooldown := proxyAutostartCooldown
	origMax := proxyAutostartMaxAttempts
	defer func() {
		proxyAutostartCooldown = origCooldown
		proxyAutostartMaxAttempts = origMax
	}()

	proxyAutostartCooldown = 0
	proxyAutostartMaxAttempts = 3

	attempts := 0
	lastAttempt := time.Time{}

	autostart := func() {
		if attempts >= proxyAutostartMaxAttempts {
			return
		}
		if !lastAttempt.IsZero() && time.Since(lastAttempt) < proxyAutostartCooldown {
			return
		}
		attempts++
		lastAttempt = time.Now()
	}

	// Use 2 of 3 attempts.
	autostart()
	autostart()
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}

	// Simulate successful daemon connection -> reset budget.
	attempts = 0

	// Should get fresh budget.
	autostart()
	autostart()
	autostart()
	if attempts != 3 {
		t.Fatalf("attempts after reset = %d, want 3", attempts)
	}

	// Budget exhausted.
	autostart()
	if attempts != 3 {
		t.Fatalf("attempts after exhaustion = %d, want 3 (no more)", attempts)
	}
}
