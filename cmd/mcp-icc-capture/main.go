// mcp-icc-capture is the MCP server that owns ICC note-capture tools:
// Slack pastes, email extracts, meeting notes, etc. This scaffold ships
// with two starter tools (icc_format_slack_paste, icc_lint_notes) that
// run entirely locally. Future slices wire the (currently stubbed) ICC
// HTTP client and add file-write capability.
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
	srv, cleanup, err := mcpscaffold.NewServer(ctx, "mcp-icc-capture", version,
		mcpscaffold.WithInstructions(`ICC Capture MCP Server

Tools for capturing notes destined for the icc-project-workspaces repo
(Slack threads, email extracts, meeting notes, etc.).

Current tools are pure local helpers — formatting + lint — with no ICC
HTTP calls and no file writes. The caller decides what to do with the
returned markdown, and a separate workflow handles the write.`),
	)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup(ctx) }()

	// HTTP client to ICC backend is wired here when future slices add
	// network-backed tools. For now it is a stub that records config but
	// makes no calls.
	_ = newICCClient(srv.Logger)

	registerTools(srv)

	return srv.Run(ctx)
}
