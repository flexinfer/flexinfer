package main

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// captureSlackSchema declares the input contract for icc_capture_slack.
// Note the two slug-shaped fields: project_id is the DB FK ICC needs;
// project_slug is the filesystem folder under
// /workspace/icc-project-workspaces/projects/<slug>/slack/. They could
// be derived from each other via an ICC lookup but explicit args keep
// the tool stateless. A future icc_resolve_project_slug tool can remove
// the duplication if it becomes painful.
func captureSlackSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"text", "project_id", "project_slug", "channel"},
		Properties: map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Raw Slack paste",
			},
			"project_id": map[string]any{
				"type":        "string",
				"description": "ICC project_id (prj_...)",
			},
			"project_slug": map[string]any{
				"type":        "string",
				"description": "Filesystem slug matching projects/<slug>/ in icc-project-workspaces",
			},
			"channel": map[string]any{
				"type":        "string",
				"description": "Slack channel name without leading #",
			},
			"captured_at": map[string]any{
				"type":        "string",
				"description": "ISO 8601 timestamp; defaults to now in local TZ",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "Optional short topic slug for the filename",
			},
			"participants": map[string]any{
				"type":        "array",
				"description": "Optional; inferred from paste if absent",
				"items":       map[string]any{"type": "string"},
			},
			"mode": map[string]any{
				"type":        "string",
				"enum":        anyStrings(captureModes),
				"default":     "both",
				"description": "raw=file+code_ref, ingest=artifact only, both=all three",
			},
			"classification": map[string]any{
				"type":        "string",
				"default":     "possible_phi",
				"description": "Classification floor for the capture",
			},
		},
	}
}

// captureSlackResult bundles the format output (so the caller sees the
// filename + markdown that landed) with the captures-endpoint result
// (code_ref / artifact / path_written). Field names mirror the wire
// shape from /api/captures, plus suggested_filename and markdown
// echoed back for visibility.
type captureSlackResult struct {
	SuggestedFilename string          `json:"suggested_filename"`
	SuggestedPath     string          `json:"suggested_path"`
	Markdown          string          `json:"markdown"`
	CodeRef           json.RawMessage `json:"code_ref"`
	Artifact          json.RawMessage `json:"artifact"`
	PathWritten       *string         `json:"path_written"`
}

func makeCaptureSlackHandler(icc *iccClient) iccToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		text := v.Required("text")
		projectID := v.Required("project_id")
		projectSlug := v.Required("project_slug")
		channel := v.Required("channel")
		capturedAt := v.String("captured_at", "")
		topic := v.String("topic", "")
		participants := v.StringSlice("participants")
		mode := v.Enum("mode", "both", captureModes...)
		classification := v.String("classification", "possible_phi")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		// Step 1: pure-local format pass. Shares the exact same code path
		// as icc_format_slack_paste so the markdown is byte-identical.
		formatted, err := formatSlackPaste(formatSlackPasteInput{
			Text:                 text,
			ProjectSlug:          projectSlug,
			Channel:              channel,
			CapturedAt:           capturedAt,
			Topic:                topic,
			ExplicitParticipants: participants,
		})
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		// Step 2: POST to /api/captures with the formatted markdown.
		req := writeCaptureRequest{
			ProjectID:      projectID,
			Source:         "slack",
			Markdown:       formatted.Markdown,
			SuggestedPath:  formatted.SuggestedPath,
			Mode:           mode,
			Classification: classification,
		}
		_, captureResult, err := postJSON[writeCaptureResult](ctx, icc, "/api/captures", req)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("capture_slack: %w", err)), nil
		}

		return jsonResult(captureSlackResult{
			SuggestedFilename: formatted.SuggestedFilename,
			SuggestedPath:     formatted.SuggestedPath,
			Markdown:          formatted.Markdown,
			CodeRef:           captureResult.CodeRef,
			Artifact:          captureResult.Artifact,
			PathWritten:       captureResult.PathWritten,
		})
	}
}
