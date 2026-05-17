package main

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// captureSources mirrors the CAPTURE_SOURCES enum the ICC backend
// validates against. Kept in lockstep with project-management
// integration_command_center.app.CAPTURE_SOURCES. Validated client-side
// for fail-fast UX (saves a round trip on obvious typos).
var captureSources = []string{
	"slack", "email", "meeting", "research", "deliverable", "query", "standup",
}

// captureModes mirrors CAPTURE_MODES on the backend.
var captureModes = []string{"raw", "ingest", "both"}

// writeCaptureSchema declares the input contract for icc_write_capture.
func writeCaptureSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"project_id", "source", "markdown", "suggested_path"},
		Properties: map[string]any{
			"project_id": map[string]any{
				"type":        "string",
				"description": "ICC project_id (prj_...)",
			},
			"source": map[string]any{
				"type":        "string",
				"enum":        anyStrings(captureSources),
				"description": "One of: slack|email|meeting|research|deliverable|query|standup",
			},
			"markdown": map[string]any{
				"type":        "string",
				"description": "Pre-formatted markdown including frontmatter",
			},
			"suggested_path": map[string]any{
				"type":        "string",
				"description": "Absolute path under the workspace allowlist",
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
			"title":   map[string]any{"type": "string"},
			"summary": map[string]any{"type": "string"},
		},
	}
}

// writeCaptureResult is the typed payload we unwrap from the ICC
// envelope. All fields are pointers so the JSON null-vs-omitted
// distinction stays visible to MCP callers (e.g. mode=raw returns
// artifact=null but path_written set).
type writeCaptureResult struct {
	CodeRef     json.RawMessage `json:"code_ref"`
	Artifact    json.RawMessage `json:"artifact"`
	PathWritten *string         `json:"path_written"`
}

// writeCaptureRequest is the JSON body shape posted to /api/captures.
type writeCaptureRequest struct {
	ProjectID      string `json:"project_id"`
	Source         string `json:"source"`
	Markdown       string `json:"markdown"`
	SuggestedPath  string `json:"suggested_path,omitempty"`
	Mode           string `json:"mode"`
	Classification string `json:"classification,omitempty"`
	Title          string `json:"title,omitempty"`
	Summary        string `json:"summary,omitempty"`
}

func makeWriteCaptureHandler(icc *iccClient) iccToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		req := writeCaptureRequest{
			ProjectID:     v.Required("project_id"),
			Source:        v.Required("source"),
			Markdown:      v.Required("markdown"),
			SuggestedPath: v.Required("suggested_path"),
			// Mode/Source enum-validate after Required so the error
			// surface includes "missing" vs "invalid" cleanly.
			Mode:           v.Enum("mode", "both", captureModes...),
			Classification: v.String("classification", "possible_phi"),
			Title:          v.String("title", ""),
			Summary:        v.String("summary", ""),
		}
		// Validate source against the allowlist (fail-fast — saves a
		// round trip; backend rejects the same set with 400).
		if req.Source != "" && !contains(captureSources, req.Source) {
			return mcp.ErrorResult(fmt.Errorf(
				"source: must be one of: %v", captureSources,
			)), nil
		}
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		_, result, err := postJSON[writeCaptureResult](ctx, icc, "/api/captures", req)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("write_capture: %w", err)), nil
		}
		return jsonResult(result)
	}
}

// contains is a tiny helper so handlers don't pull in slices.Contains
// for one call site (avoids an extra import on Go-toolchains that gate
// slices on a build tag).
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// anyStrings returns the input slice typed as []any so it can drop
// into a JSON-schema enum array. Inline conversion keeps the schema
// declarations readable.
func anyStrings(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
