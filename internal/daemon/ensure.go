// Package daemon provides the main Loom daemon orchestrator.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/crb2nu/loom/pkg/registry"
)

// StartConfig configures how EnsureRunning finds and starts the daemon.
type StartConfig struct {
	SocketPath   string
	RegistryPath string
	LogFile      string        // Empty means inherit stderr (CLI mode).
	Timeout      time.Duration // How long to wait for socket readiness.
}

// EnsureRunning ensures a daemon is running and reachable on SocketPath.
// It returns nil if the daemon is already running or was started successfully.
//
// Start strategy:
//  1. Quick dial check — return immediately if already running.
//  2. If a LaunchAgent plist exists, use launchctl kickstart.
//  3. Wait for socket readiness up to Timeout.
//  4. Fallback: find loomd in PATH or sibling, spawn directly.
//  5. Wait for socket readiness up to Timeout.
func EnsureRunning(cfg StartConfig) error {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}

	// 1. Quick dial — daemon already running?
	if dialCheck(cfg.SocketPath, 200*time.Millisecond) {
		return nil
	}

	// 2. Try LaunchAgent (macOS).
	if tryLaunchAgent(cfg.SocketPath, cfg.Timeout) {
		return nil
	}

	// 3. Auto-detect registry if not provided.
	registryPath := cfg.RegistryPath
	if registryPath == "" {
		if path, found := registry.FindRegistry(); found {
			registryPath = path
		}
	}

	// 4. Find and spawn loomd directly.
	loomdPath := findLoomd()
	if loomdPath == "" {
		return fmt.Errorf("loomd not found in PATH (or alongside loom)")
	}

	args := []string{"--socket", cfg.SocketPath}
	if registryPath != "" {
		args = append(args, "--registry", registryPath)
	}

	cmd := exec.Command(loomdPath, args...) //nolint:noctx // daemon runs in background
	cmd.Stdin = nil

	if cfg.LogFile != "" {
		home, _ := os.UserHomeDir()
		logDir := filepath.Join(home, ".config", "loom", "logs")
		_ = os.MkdirAll(logDir, 0755)
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("open daemon log file: %w", err)
		}
		cmd.Stdout = f
		cmd.Stderr = f
		defer f.Close()
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start loomd: %w", err)
	}

	// Detach so the caller doesn't need to Wait().
	_ = cmd.Process.Release()

	// 5. Wait for readiness.
	if waitForSocket(cfg.SocketPath, cfg.Timeout) {
		return nil
	}

	return fmt.Errorf("daemon started but did not become ready within %s", cfg.Timeout)
}

// dialCheck returns true if the daemon socket is dialable.
func dialCheck(socketPath string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// tryLaunchAgent attempts to start the daemon via launchctl kickstart.
// Returns true if the daemon became reachable.
func tryLaunchAgent(socketPath string, timeout time.Duration) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.loom.daemon.plist")
	if _, statErr := os.Stat(plist); statErr != nil {
		return false
	}

	uid := os.Getuid()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	kick := exec.CommandContext(ctx, "launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/com.loom.daemon", uid))
	kick.Stdin = nil
	kick.Stdout = nil
	kick.Stderr = nil
	_ = kick.Run()

	return waitForSocket(socketPath, timeout)
}

// waitForSocket polls the socket until it becomes dialable or timeout expires.
func waitForSocket(socketPath string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		d := net.Dialer{Timeout: 200 * time.Millisecond}
		conn, err := d.DialContext(ctx, "unix", socketPath)
		cancel()
		if err == nil {
			_ = conn.Close()
			return true
		}
		// Only keep waiting on transient errors.
		var ne *net.OpError
		if errors.As(err, &ne) {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// findLoomd searches for the loomd binary in PATH and as a sibling of the
// current executable.
func findLoomd() string {
	if p, err := exec.LookPath("loomd"); err == nil {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "loomd")
		if _, statErr := os.Stat(sibling); statErr == nil {
			return sibling
		}
	}
	return ""
}
