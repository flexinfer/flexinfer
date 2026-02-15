// mcp-codebase-memory indexes and searches codebases for Loom via MCP.
package main

import (
	"context"
	"fmt"
	"os"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
)

var version = "1.0.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-codebase-memory", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-codebase-memory")

	svc, err := codebase.NewServiceFromEnv()
	if err != nil {
		logger.Error("initialization error", "error", err)
		return err
	}

	logger.Info("starting server", "name", "mcp-codebase-memory", "version", version)

	server := mcp.NewServer("mcp-codebase-memory", version)
	server.SetInstructions("Semantic codebase index + search (Go/TS/JS/Python/Rust). Indexing is async (start/poll/cancel).")

	registerTools(server, svc, tracer)

	return server.Run(ctx)
}
