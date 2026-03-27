// Package mcpscaffold eliminates repeated initialization boilerplate across
// MCP servers by bundling logger, tracer, and server creation into a single
// factory call.
package mcpscaffold

import (
	"context"
	"log/slog"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
)

// Server wraps an mcp.Server with pre-configured logger, tracer, and helpers.
type Server struct {
	*mcp.Server
	Logger *slog.Logger
	Tracer trace.Tracer
}

// Option configures server creation.
type Option func(*config)

type config struct {
	instructions string
}

// WithInstructions sets server instructions.
func WithInstructions(s string) Option {
	return func(c *config) { c.instructions = s }
}

// NewServer creates a fully-configured MCP server with lifecycle, logging,
// and tracing. It returns a Server that can have tools added via
// AddTracedTool, then run via server.Run(ctx). The returned cleanup function
// must be deferred to flush the tracer on exit.
func NewServer(ctx context.Context, name, version string, opts ...Option) (*Server, func(context.Context) error, error) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}

	logger := mcplog.NewDefault()

	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, name, logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	tracer := mcpotel.Tracer(tp, name)

	logger.Info("starting server", "name", name, "version", version)

	srv := mcp.NewServer(name, version)
	if cfg.instructions != "" {
		srv.SetInstructions(cfg.instructions)
	}

	return &Server{
		Server: srv,
		Logger: logger,
		Tracer: tracer,
	}, shutdownTracer, nil
}

// AddTracedTool registers a tool with automatic OpenTelemetry tracing.
func (s *Server) AddTracedTool(tool mcp.Tool, handler mcp.ToolHandler) {
	s.AddTool(tool, mcpotel.TracedToolHandler(s.Tracer, tool.Name, handler))
}
