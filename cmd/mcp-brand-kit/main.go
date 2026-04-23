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
	srv, cleanup, err := mcpscaffold.NewServer(ctx, "mcp-brand-kit", version,
		mcpscaffold.WithInstructions("Brand asset management tools for banners, icons, repository README branding, linting, previews, and controlled fixes."),
	)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup(ctx) }()

	registerTools(srv)

	return srv.Run(ctx)
}
