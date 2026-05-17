package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// unarchiveRawSchema declares the input contract for icc_unarchive_raw.
// The endpoint is POST /api/captures/unarchive (Slice E-1). The
// destination_path must land inside the workspace allowlist and must
// not already exist; the server enforces both. We pass the server
// "result" block through verbatim — no convenience flags needed since
// unarchive is not idempotent (it always returns 201 on success).
func unarchiveRawSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"code_ref_id", "destination_path"},
		Properties: map[string]any{
			"code_ref_id": map[string]any{
				"type":        "string",
				"description": "ID of the archived code_ref to restore (cref_...)",
			},
			"destination_path": map[string]any{
				"type":        "string",
				"description": "Absolute path; must be under the workspace allowlist",
			},
		},
	}
}

// unarchiveRawRequest is the JSON body posted to /api/captures/unarchive.
type unarchiveRawRequest struct {
	CodeRefID       string `json:"code_ref_id"`
	DestinationPath string `json:"destination_path"`
}

// unarchiveRawServerResult mirrors the ICC envelope's "result" payload.
// We pass these through to the caller as-is — there's no idempotent
// branch to surface, unlike archive.
type unarchiveRawServerResult struct {
	CodeRef      json.RawMessage `json:"code_ref"`
	ArchivedPath string          `json:"archived_path"`
	RestoredPath string          `json:"restored_path"`
}

func makeUnarchiveRawHandler(icc *iccClient) iccToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		codeRefID := v.Required("code_ref_id")
		destinationPath := v.Required("destination_path")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		// Client-side refusals beyond Required(): empty after trim.
		if strings.TrimSpace(codeRefID) == "" {
			return mcp.ErrorResult(errors.New("code_ref_id must not be empty")), nil
		}
		if strings.TrimSpace(destinationPath) == "" {
			return mcp.ErrorResult(errors.New("destination_path must not be empty")), nil
		}

		req := unarchiveRawRequest{
			CodeRefID:       codeRefID,
			DestinationPath: destinationPath,
		}

		_, result, err := postJSON[unarchiveRawServerResult](ctx, icc, "/api/captures/unarchive", req)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("unarchive_raw: %w", err)), nil
		}

		return jsonResult(result)
	}
}
