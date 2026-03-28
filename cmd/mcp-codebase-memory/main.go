// mcp-codebase-memory indexes and searches codebases for Loom via MCP.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/crb2nu/loom/pkg/codebase"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcpscaffold"
)

var version = "1.0.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	srv, cleanup, err := mcpscaffold.NewServer(ctx, "mcp-codebase-memory", version,
		mcpscaffold.WithInstructions("Semantic codebase index + search (Go/TS/JS/Python/Rust). Indexing is async (start/poll/cancel)."),
	)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup(ctx) }()

	svc, err := codebase.NewServiceFromEnv()
	if err != nil {
		srv.Logger.Error("initialization error", "error", err)
		return err
	}

	registerTools(srv, svc)

	return srv.Run(ctx)
}
