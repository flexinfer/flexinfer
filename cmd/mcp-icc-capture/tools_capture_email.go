package main

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// captureEmailSchema declares the input contract for icc_capture_email.
// As with icc_capture_slack, project_id (DB FK) and project_slug
// (filesystem path) are explicit so the tool stays stateless. Subject
// and topic are optional overrides — the formatter detects them from
// headers when omitted.
func captureEmailSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"text", "project_id", "project_slug"},
		Properties: map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Raw email text",
			},
			"project_id": map[string]any{
				"type":        "string",
				"description": "ICC project_id (prj_...)",
			},
			"project_slug": map[string]any{
				"type":        "string",
				"description": "Filesystem slug matching projects/<slug>/ in icc-project-workspaces",
			},
			"captured_at": map[string]any{
				"type":        "string",
				"description": "ISO 8601 timestamp; defaults to detected Date header or now",
			},
			"subject": map[string]any{
				"type":        "string",
				"description": "Optional override for detected Subject",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "Optional short topic slug for the filename",
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

// captureEmailResult bundles the format output with the captures
// endpoint result. Same shape as captureSlackResult plus detected_*
// echoes so the caller can confirm header parsing without re-running
// the formatter.
type captureEmailResult struct {
	SuggestedFilename string          `json:"suggested_filename"`
	SuggestedPath     string          `json:"suggested_path"`
	Markdown          string          `json:"markdown"`
	DetectedSubject   string          `json:"detected_subject"`
	DetectedFrom      []string        `json:"detected_from"`
	Warnings          []string        `json:"warnings,omitempty"`
	CodeRef           json.RawMessage `json:"code_ref"`
	Artifact          json.RawMessage `json:"artifact"`
	PathWritten       *string         `json:"path_written"`
}

func makeCaptureEmailHandler(icc *iccClient) iccToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		in := formatEmailExtractInput{
			Text:        v.Required("text"),
			ProjectSlug: v.Required("project_slug"),
			CapturedAt:  v.String("captured_at", ""),
			Subject:     v.String("subject", ""),
			Topic:       v.String("topic", ""),
		}
		projectID := v.Required("project_id")
		mode := v.Enum("mode", "both", captureModes...)
		classification := v.String("classification", "possible_phi")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		// Step 1: pure-local format pass. Shares the exact same code
		// path as icc_format_email_extract so the markdown is byte-
		// identical between the one-shot and two-step flows.
		formatted, err := formatEmailExtract(in)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		// Step 2: POST to /api/captures with the formatted markdown.
		// Source is hardcoded — backend re-validates against
		// CAPTURE_SOURCES, but we want a typo in our code to fail fast
		// rather than reach the wire.
		req := writeCaptureRequest{
			ProjectID:      projectID,
			Source:         "email",
			Markdown:       formatted.Markdown,
			SuggestedPath:  formatted.SuggestedPath,
			Mode:           mode,
			Classification: classification,
		}
		_, captureResult, err := postJSON[writeCaptureResult](ctx, icc, "/api/captures", req)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("capture_email: %w", err)), nil
		}

		return jsonResult(captureEmailResult{
			SuggestedFilename: formatted.SuggestedFilename,
			SuggestedPath:     formatted.SuggestedPath,
			Markdown:          formatted.Markdown,
			DetectedSubject:   formatted.DetectedSubject,
			DetectedFrom:      formatted.DetectedFrom,
			Warnings:          formatted.Warnings,
			CodeRef:           captureResult.CodeRef,
			Artifact:          captureResult.Artifact,
			PathWritten:       captureResult.PathWritten,
		})
	}
}
