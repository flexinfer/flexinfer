package main

import (
	"encoding/json"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcpscaffold"
)

// registerTools wires up all tools exposed by mcp-icc-capture. Each
// tool gets a dedicated file (tools_<name>.go) for the handler so the
// surface stays easy to grep.
func registerTools(srv *mcpscaffold.Server) {
	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_format_slack_paste",
		Description: "Format a pasted Slack thread into structured Markdown with frontmatter, ready to write into icc-project-workspaces/projects/<slug>/slack/. Returns the formatted markdown as a string. The caller writes the file and creates the code_ref/artifact separately.",
		InputSchema: formatSlackPasteSchema(),
	}, handleFormatSlackPaste)

	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_lint_notes",
		Description: "Validate notes under icc-project-workspaces/projects/<slug>/<source>/ against the canonical frontmatter spec and naming convention. Reports findings; with fix=true, applies safe rewrites.",
		InputSchema: lintNotesSchema(),
	}, handleLintNotes)
}

// jsonResult is a small helper to keep handlers terse.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return mcp.TextResult(string(data)), nil
}
