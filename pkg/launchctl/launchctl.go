// Package launchctl provides context-aware wrappers for macOS launchctl
// and process management commands. It consolidates exec.CommandContext
// calls that were previously scattered across multiple files with
// nolint:noctx suppressions.
package launchctl

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Load loads a launchd plist by path.
func Load(ctx context.Context, plistPath string) error {
	return exec.CommandContext(ctx, "launchctl", "load", plistPath).Run()
}

// Unload unloads a launchd plist by path.
func Unload(ctx context.Context, plistPath string) error {
	return exec.CommandContext(ctx, "launchctl", "unload", plistPath).Run()
}

// Start starts a launchd service by label.
func Start(ctx context.Context, label string) error {
	return exec.CommandContext(ctx, "launchctl", "start", label).Run()
}

// Stop stops a launchd service by label.
func Stop(ctx context.Context, label string) error {
	return exec.CommandContext(ctx, "launchctl", "stop", label).Run()
}

// Kill sends a signal to processes matching the given pattern via pkill -f.
func Kill(ctx context.Context, signal string, pattern string) error {
	return exec.CommandContext(ctx, "pkill", signal, "-f", pattern).Run()
}

// FindProcessByPort returns the PID of a process listening on the given port
// using lsof. Returns an empty string if no process is found.
func FindProcessByPort(ctx context.Context, port string) (string, error) {
	out, err := exec.CommandContext(ctx, "lsof", "-ti", ":"+port).Output()
	if err != nil {
		return "", fmt.Errorf("lsof port %s: %w", port, err)
	}
	pid := strings.TrimSpace(string(out))
	if pid == "" {
		return "", fmt.Errorf("no process found on port %s", port)
	}
	return pid, nil
}

// KillPID sends SIGTERM to a process by PID string.
func KillPID(ctx context.Context, pid string) error {
	return exec.CommandContext(ctx, "kill", pid).Run()
}
