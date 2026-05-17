package main

import (
	"encoding/json"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcpscaffold"
)

// registerTools wires up all tools exposed by mcp-icc-capture. Each
// tool gets a dedicated file (tools_<name>.go) for the handler so the
// surface stays easy to grep. Tools that talk to the ICC backend close
// over the shared iccClient so they share connection pooling and
// configuration.
func registerTools(srv *mcpscaffold.Server, icc *iccClient) {
	// Listed in alphabetic order so the registration order is grep-able
	// and stable across additions.
	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_capture_email",
		Description: "End-to-end email-extract capture. Formats raw email text (RFC 822, Gmail paste, or web-rendered) and writes the result via /api/captures. Equivalent to icc_format_email_extract followed by icc_write_capture. Use this for one-shot capture; use the two-step path when you want to edit the markdown between format and write.",
		InputSchema: captureEmailSchema(),
	}, makeCaptureEmailHandler(icc))

	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_capture_meeting",
		Description: "End-to-end meeting-notes capture. Formats notes (Gemini auto-notes or freeform) and writes the result via /api/captures. Equivalent to icc_format_meeting_notes followed by icc_write_capture.",
		InputSchema: captureMeetingSchema(),
	}, makeCaptureMeetingHandler(icc))

	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_capture_slack",
		Description: "End-to-end Slack-paste capture. Formats the paste, then writes the result via /api/captures. Equivalent to calling icc_format_slack_paste followed by icc_write_capture. Use this for one-shot capture; use the two-step path when you want to edit the markdown between format and write.",
		InputSchema: captureSlackSchema(),
	}, makeCaptureSlackHandler(icc))

	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_capture_standup",
		Description: "End-to-end standup capture. Formats notes (personal prep or team standup) and writes via /api/captures. Default mode is 'ingest' (DB only) because standups are ephemeral and the DB is their canonical surface.",
		InputSchema: captureStandupSchema(),
	}, makeCaptureStandupHandler(icc))

	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_demote_artifact",
		Description: "Reverse of icc_promote_to_artifact: soft-delete the artifact, optionally keep the underlying code_ref. Requires a non-empty reason (matches the existing /api/artifacts/delete contract).",
		InputSchema: demoteArtifactSchema(),
	}, makeDemoteArtifactHandler(icc))

	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_format_email_extract",
		Description: "Format raw email text (RFC 822, Gmail paste, or web-rendered) into structured Markdown with frontmatter, ready to write into icc-project-workspaces/projects/<slug>/email/. Returns the formatted markdown as a string. Caller writes the file separately (or use icc_capture_email for one-shot).",
		InputSchema: formatEmailExtractSchema(),
	}, handleFormatEmailExtract)

	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_format_meeting_notes",
		Description: "Format meeting notes (Gemini auto-notes or freeform) into structured Markdown with frontmatter, ready to write into icc-project-workspaces/projects/<slug>/meetings/.",
		InputSchema: formatMeetingNotesSchema(),
	}, handleFormatMeetingNotes)

	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_format_slack_paste",
		Description: "Format a pasted Slack thread into structured Markdown with frontmatter, ready to write into icc-project-workspaces/projects/<slug>/slack/. Returns the formatted markdown as a string. The caller writes the file and creates the code_ref/artifact separately.",
		InputSchema: formatSlackPasteSchema(),
	}, handleFormatSlackPaste)

	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_format_standup",
		Description: "Format standup notes (personal prep or team standup) into structured Markdown with frontmatter for projects/<slug>/research/ (project standups) or for the _inbox project (personal standup prep). Standups have no dedicated source folder in STRUCTURE.md, so they land under research/.",
		InputSchema: formatStandupSchema(),
	}, handleFormatStandup)

	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_lint_notes",
		Description: "Validate notes under icc-project-workspaces/projects/<slug>/<source>/ against the canonical frontmatter spec and naming convention. Reports findings; with fix=true, applies safe rewrites.",
		InputSchema: lintNotesSchema(),
	}, handleLintNotes)

	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_promote_to_artifact",
		Description: "Promote an existing raw-only code_ref to a full artifact (DB entry + link). Idempotent: re-promoting an already-promoted ref returns the existing artifact with already_promoted=true rather than creating a duplicate. The MCP tool surfaces already_promoted so the caller knows whether this was a fresh promotion or a no-op.",
		InputSchema: promoteToArtifactSchema(),
	}, makePromoteToArtifactHandler(icc))

	srv.AddTracedTool(mcp.Tool{
		Name:        "icc_write_capture",
		Description: "Write a pre-formatted capture (markdown + frontmatter) to the ICC workspace and/or DB. Use icc_format_slack_paste (or future format_* tools) to produce the markdown first; then pass it here with the project, source, suggested_path, and mode. Atomic — either all sides succeed or nothing is written. Returns the created code_ref and/or artifact and the path written.",
		InputSchema: writeCaptureSchema(),
	}, makeWriteCaptureHandler(icc))
}

// jsonResult is a small helper to keep handlers terse.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return mcp.TextResult(string(data)), nil
}

// iccToolHandler is an alias for mcp.ToolHandler used by the
// network-backed handler factories (make*Handler) so the signatures
// stay grep-able and the alias makes it obvious which handlers close
// over the iccClient.
type iccToolHandler = mcp.ToolHandler
