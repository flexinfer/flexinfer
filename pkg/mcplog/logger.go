// Package mcplog provides standardized logging initialization for MCP servers.
package mcplog

import (
	"log/slog"
	"os"
)

// NewDefault creates a new logger with the standard MCP configuration.
// If MCP_DEBUG environment variable is set, debug-level logging is enabled.
// Otherwise, info-level logging is used.
//
// Usage:
//
//	logger := mcplog.NewDefault()
//	logger.Info("starting server", "name", serverName)
func NewDefault() *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("MCP_DEBUG") != "" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// NewWithLevel creates a new logger with the specified level.
func NewWithLevel(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// IsDebug returns true if MCP_DEBUG environment variable is set.
func IsDebug() bool {
	return os.Getenv("MCP_DEBUG") != ""
}
