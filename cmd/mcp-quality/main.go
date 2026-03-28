// mcp-quality provides code quality analysis tools via MCP.
// Agents call these tools during development to run lint, test,
// security, and architectural constraint checks — shifting quality
// gates into the agentic loop rather than relying on post-hoc CI.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcpscaffold"
)

var version = "0.1.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	srv, cleanup, err := mcpscaffold.NewServer(ctx, "mcp-quality", version,
		mcpscaffold.WithInstructions("Code quality analysis tools. Run lint, test, security, and architectural checks on changed files. Use quality_check for a combined gate before committing."),
	)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup(ctx) }()

	registerTools(srv)

	return srv.Run(ctx)
}
