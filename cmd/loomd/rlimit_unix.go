//go:build !windows

package main

import (
	"log/slog"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

const defaultNoFileTarget = 8192

// tuneNoFileLimit attempts to raise the process soft RLIMIT_NOFILE. This is
// critical on macOS where GUI/launchd-launched processes often inherit a very low
// soft limit (commonly 256), which prevents loomd from spawning many MCP servers.
//
// Child MCP server processes inherit this limit from loomd.
func tuneNoFileLimit(logger *slog.Logger) {
	target := defaultNoFileTarget
	if v := os.Getenv("LOOM_NOFILE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			target = n
		}
	}

	var lim unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &lim); err != nil {
		if logger != nil {
			logger.Debug("Getrlimit(RLIMIT_NOFILE) failed", "error", err)
		}
		return
	}

	cur := lim.Cur
	max := lim.Max
	desired := uint64(target)

	// Clamp to the hard limit if it is set.
	if max > 0 && desired > max {
		desired = max
	}

	if desired <= cur {
		return
	}

	lim.Cur = desired
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &lim); err != nil {
		if logger != nil {
			logger.Warn("failed to raise RLIMIT_NOFILE", "from", cur, "to", desired, "max", max, "error", err)
		}
		return
	}

	if logger != nil {
		logger.Info("raised RLIMIT_NOFILE", "from", cur, "to", lim.Cur, "max", max)
	}
}
