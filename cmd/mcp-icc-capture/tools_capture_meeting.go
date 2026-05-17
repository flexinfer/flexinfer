package main

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// captureMeetingSchema declares the input contract for
// icc_capture_meeting.
func captureMeetingSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"text", "project_id", "project_slug", "participants"},
		Properties: map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Meeting notes (Gemini auto-notes or freeform)",
			},
			"project_id": map[string]any{
				"type":        "string",
				"description": "ICC project_id (prj_...)",
			},
			"project_slug": map[string]any{
				"type":        "string",
				"description": "Filesystem slug matching projects/<slug>/ in icc-project-workspaces",
			},
			"participants": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Required; meetings have known attendees",
			},
			"captured_at": map[string]any{
				"type":        "string",
				"description": "ISO 8601 timestamp; defaults to now",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "Optional short topic slug (e.g. '1on1', 'sprint-review')",
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

type captureMeetingResult struct {
	SuggestedFilename string          `json:"suggested_filename"`
	SuggestedPath     string          `json:"suggested_path"`
	Markdown          string          `json:"markdown"`
	CodeRef           json.RawMessage `json:"code_ref"`
	Artifact          json.RawMessage `json:"artifact"`
	PathWritten       *string         `json:"path_written"`
}

func makeCaptureMeetingHandler(icc *iccClient) iccToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		in := formatMeetingNotesInput{
			Text:         v.Required("text"),
			ProjectSlug:  v.Required("project_slug"),
			Participants: v.RequiredStringSlice("participants"),
			CapturedAt:   v.String("captured_at", ""),
			Topic:        v.String("topic", ""),
		}
		projectID := v.Required("project_id")
		mode := v.Enum("mode", "both", captureModes...)
		classification := v.String("classification", "possible_phi")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		formatted, err := formatMeetingNotes(in)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		req := writeCaptureRequest{
			ProjectID:      projectID,
			Source:         "meeting",
			Markdown:       formatted.Markdown,
			SuggestedPath:  formatted.SuggestedPath,
			Mode:           mode,
			Classification: classification,
		}
		_, captureResult, err := postJSON[writeCaptureResult](ctx, icc, "/api/captures", req)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("capture_meeting: %w", err)), nil
		}

		return jsonResult(captureMeetingResult{
			SuggestedFilename: formatted.SuggestedFilename,
			SuggestedPath:     formatted.SuggestedPath,
			Markdown:          formatted.Markdown,
			CodeRef:           captureResult.CodeRef,
			Artifact:          captureResult.Artifact,
			PathWritten:       captureResult.PathWritten,
		})
	}
}
