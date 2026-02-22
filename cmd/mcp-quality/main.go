// mcp-quality provides code quality analysis tools via MCP.
// Agents call these tools during development to run lint, test,
// security, and architectural constraint checks — shifting quality
// gates into the agentic loop rather than relying on post-hoc CI.
package main

import (
	"context"
	"fmt"
	"os"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
)

var version = "0.1.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-quality", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-quality")

	logger.Info("starting server", "name", "mcp-quality", "version", version)

	server := mcp.NewServer("mcp-quality", version)
	server.SetInstructions("Code quality analysis tools. Run lint, test, security, and architectural checks on changed files. Use quality_check for a combined gate before committing.")

	registerTools(server, tracer)

	return server.Run(ctx)
}
