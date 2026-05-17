package main

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// promoteToArtifactSchema declares the input contract for
// icc_promote_to_artifact.
func promoteToArtifactSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"code_ref_id"},
		Properties: map[string]any{
			"code_ref_id": map[string]any{
				"type":        "string",
				"description": "ID of the existing code_ref (cref_...)",
			},
			"classification": map[string]any{
				"type":        "string",
				"description": "Optional override; defaults to ref's classification",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Optional title for the artifact; defaults to ref's title",
			},
			"summary": map[string]any{
				"type":        "string",
				"description": "Optional summary; the store auto-derives from content, so this is largely advisory",
			},
		},
	}
}

// promoteRequest is the JSON body posted to /api/code/refs/promote.
type promoteRequest struct {
	CodeRefID      string `json:"code_ref_id"`
	Classification string `json:"classification,omitempty"`
	Title          string `json:"title,omitempty"`
	Summary        string `json:"summary,omitempty"`
}

// promoteServerResult is the shape inside the ICC envelope's "result"
// for promote. already_promoted is the load-bearing field: branch on it,
// not on HTTP status.
type promoteServerResult struct {
	AlreadyPromoted bool            `json:"already_promoted"`
	Artifact        json.RawMessage `json:"artifact"`
	CodeRef         json.RawMessage `json:"code_ref"`
}

// promoteToolResult is what we return to the MCP caller. Both fresh and
// already_promoted are exposed so callers can pick whichever phrasing
// reads best in their code path.
type promoteToolResult struct {
	AlreadyPromoted bool            `json:"already_promoted"`
	Fresh           bool            `json:"fresh"`
	Artifact        json.RawMessage `json:"artifact"`
	CodeRef         json.RawMessage `json:"code_ref"`
}

func makePromoteToArtifactHandler(icc *iccClient) iccToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		req := promoteRequest{
			CodeRefID:      v.Required("code_ref_id"),
			Classification: v.String("classification", ""),
			Title:          v.String("title", ""),
			Summary:        v.String("summary", ""),
		}
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		_, result, err := postJSON[promoteServerResult](ctx, icc, "/api/code/refs/promote", req)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("promote_to_artifact: %w", err)), nil
		}

		return jsonResult(promoteToolResult{
			AlreadyPromoted: result.AlreadyPromoted,
			Fresh:           !result.AlreadyPromoted,
			Artifact:        result.Artifact,
			CodeRef:         result.CodeRef,
		})
	}
}
