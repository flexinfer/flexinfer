// Package mcplog provides standardized logging initialization for MCP servers.
package mcplog

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
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
	return newLogger(level, os.Getenv("MCP_LOG_FORMAT"), os.Stderr)
}

// NewWithLevel creates a new logger with the specified level.
func NewWithLevel(level slog.Level) *slog.Logger {
	return newLogger(level, os.Getenv("MCP_LOG_FORMAT"), os.Stderr)
}

// IsDebug returns true if MCP_DEBUG environment variable is set.
func IsDebug() bool {
	return os.Getenv("MCP_DEBUG") != ""
}

// newLogger creates a logger with the requested format.
// Supported formats: "text" (default), "json".
func newLogger(level slog.Level, format string, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}

	var base slog.Handler
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		base = slog.NewJSONHandler(w, opts)
	default:
		base = slog.NewTextHandler(w, opts)
	}

	logger := slog.New(&traceContextHandler{next: base})
	slog.SetDefault(logger)
	return logger
}

type traceContextHandler struct {
	next slog.Handler
}

func (h *traceContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *traceContextHandler) Handle(ctx context.Context, rec slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		rec.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.next.Handle(ctx, rec)
}

func (h *traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceContextHandler{next: h.next.WithAttrs(attrs)}
}

func (h *traceContextHandler) WithGroup(name string) slog.Handler {
	return &traceContextHandler{next: h.next.WithGroup(name)}
}
