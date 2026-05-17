package main

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// captureStandupSchema declares the input contract for
// icc_capture_standup. Default mode is "ingest" (not "both") per the
// per-source defaults in STRUCTURE.md — standup is ephemeral, the DB
// is its canonical surface, so writing a file by default would just
// add filesystem noise.
func captureStandupSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"text", "project_id", "project_slug"},
		Properties: map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Standup notes (personal prep or team standup transcript)",
			},
			"project_id": map[string]any{
				"type":        "string",
				"description": "ICC project_id (prj_...)",
			},
			"project_slug": map[string]any{
				"type":        "string",
				"description": "Filesystem slug matching projects/<slug>/ in icc-project-workspaces; use '_inbox' for personal prep",
			},
			"captured_at": map[string]any{
				"type":        "string",
				"description": "ISO 8601 timestamp; defaults to now",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "Optional short topic slug; defaults to 'standup' or 'standup-prep'",
			},
			"team": map[string]any{
				"type":        "string",
				"description": "Optional team name for team standups",
			},
			"participants": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional list of attendees",
			},
			"mode": map[string]any{
				"type":        "string",
				"enum":        anyStrings(captureModes),
				"default":     "ingest",
				"description": "raw=file+code_ref, ingest=artifact only, both=all three; standup defaults to ingest because the DB is its canonical surface",
			},
			"classification": map[string]any{
				"type":        "string",
				"default":     "possible_phi",
				"description": "Classification floor for the capture",
			},
		},
	}
}

type captureStandupResult struct {
	SuggestedFilename string          `json:"suggested_filename"`
	SuggestedPath     string          `json:"suggested_path"`
	Markdown          string          `json:"markdown"`
	CodeRef           json.RawMessage `json:"code_ref"`
	Artifact          json.RawMessage `json:"artifact"`
	PathWritten       *string         `json:"path_written"`
}

func makeCaptureStandupHandler(icc *iccClient) iccToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		in := formatStandupInput{
			Text:         v.Required("text"),
			ProjectSlug:  v.Required("project_slug"),
			CapturedAt:   v.String("captured_at", ""),
			Topic:        v.String("topic", ""),
			Team:         v.String("team", ""),
			Participants: v.StringSlice("participants"),
		}
		projectID := v.Required("project_id")
		// Standup defaults to "ingest" — see schema comment.
		mode := v.Enum("mode", "ingest", captureModes...)
		classification := v.String("classification", "possible_phi")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		formatted, err := formatStandup(in)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		req := writeCaptureRequest{
			ProjectID:      projectID,
			Source:         "standup",
			Markdown:       formatted.Markdown,
			SuggestedPath:  formatted.SuggestedPath,
			Mode:           mode,
			Classification: classification,
		}
		_, captureResult, err := postJSON[writeCaptureResult](ctx, icc, "/api/captures", req)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("capture_standup: %w", err)), nil
		}

		return jsonResult(captureStandupResult{
			SuggestedFilename: formatted.SuggestedFilename,
			SuggestedPath:     formatted.SuggestedPath,
			Markdown:          formatted.Markdown,
			CodeRef:           captureResult.CodeRef,
			Artifact:          captureResult.Artifact,
			PathWritten:       captureResult.PathWritten,
		})
	}
}
