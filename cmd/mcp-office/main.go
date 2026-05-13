// mcp-office is a Loom MCP server for parsing and producing Microsoft Office
// and PDF documents (.pdf, .docx, .xlsx, .pptx). It is designed for restricted
// environments (e.g., code-server on a Chromebook) where the agent host cannot
// install office tooling. All parsing is pure Go.
//
// Tools accept either `path` (filesystem) or `bytes_b64` (inline base64) so the
// hub container does not need to share a filesystem with the calling agent.
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
	srv, cleanup, err := mcpscaffold.NewServer(ctx, "mcp-office", version,
		mcpscaffold.WithInstructions(
			"Office and PDF document parsing/production for .pdf, .docx, .xlsx, .pptx. "+
				"Every read tool accepts either `path` (filesystem) or `bytes_b64` (inline). "+
				"Format is auto-detected from extension or magic bytes when `format` is omitted. "+
				"Tools: office_extract_text, office_extract_metadata, office_extract_structure, "+
				"office_extract_tables, office_search, office_inspect, office_create_xlsx, "+
				"office_modify_docx.",
		),
	)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup(ctx) }()

	registerReadTools(srv)
	registerWriteTools(srv)

	return srv.Run(ctx)
}

// commonSourceProps describes the `path`/`bytes_b64`/`format` inputs every read
// tool accepts. Centralized so the schema stays consistent across tools.
func commonSourceProps() map[string]any {
	return map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "Filesystem path to the document. Mutually exclusive with bytes_b64.",
		},
		"bytes_b64": map[string]any{
			"type":        "string",
			"description": "Base64-encoded document bytes. Use when the MCP server cannot reach the file's filesystem (e.g., code-server / hub split).",
		},
		"format": map[string]any{
			"type":        "string",
			"enum":        []string{"pdf", "docx", "xlsx", "pptx"},
			"description": "Optional format override. Auto-detected from extension or magic bytes when omitted.",
		},
	}
}
