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

// archiveRawSchema declares the input contract for icc_archive_raw.
// The endpoint is POST /api/captures/archive (Slice E-1). Returns 201
// on first archive and 200 on idempotent re-archive; we branch on the
// body's already_archived field rather than the HTTP status so the
// MCP-facing shape mirrors the promote/already_promoted convention.
func archiveRawSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"code_ref_id", "reason"},
		Properties: map[string]any{
			"code_ref_id": map[string]any{
				"type":        "string",
				"description": "ID of the existing raw-only code_ref (cref_...)",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Non-empty justification (audited)",
			},
			"archive_root": map[string]any{
				"type":        "string",
				"description": "Optional override; defaults to /workspace/notes/archive",
			},
		},
	}
}

// archiveRawRequest is the JSON body posted to /api/captures/archive.
type archiveRawRequest struct {
	CodeRefID   string `json:"code_ref_id"`
	Reason      string `json:"reason"`
	ArchiveRoot string `json:"archive_root,omitempty"`
}

// archiveRawServerResult mirrors the ICC envelope's "result" payload.
// already_archived is the load-bearing field: branch on it, not on
// the HTTP status (201 vs 200). When true, original_path ==
// archived_path and code_ref is unchanged.
type archiveRawServerResult struct {
	CodeRef         json.RawMessage `json:"code_ref"`
	OriginalPath    string          `json:"original_path"`
	ArchivedPath    string          `json:"archived_path"`
	AlreadyArchived bool            `json:"already_archived"`
}

// archiveRawToolResult is what we return to the MCP caller. Both
// already_archived and fresh are exposed so callers can pick whichever
// phrasing reads best in their code path (matches promoteToolResult).
type archiveRawToolResult struct {
	AlreadyArchived bool            `json:"already_archived"`
	Fresh           bool            `json:"fresh"`
	CodeRef         json.RawMessage `json:"code_ref"`
	OriginalPath    string          `json:"original_path"`
	ArchivedPath    string          `json:"archived_path"`
}

func makeArchiveRawHandler(icc *iccClient) iccToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		codeRefID := v.Required("code_ref_id")
		reason := v.Required("reason")
		archiveRoot := v.String("archive_root", "")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		// Client-side refusals beyond Required(): empty after trim.
		if strings.TrimSpace(codeRefID) == "" {
			return mcp.ErrorResult(errors.New("code_ref_id must not be empty")), nil
		}
		if strings.TrimSpace(reason) == "" {
			return mcp.ErrorResult(errors.New("reason must not be empty or whitespace")), nil
		}

		req := archiveRawRequest{
			CodeRefID:   codeRefID,
			Reason:      reason,
			ArchiveRoot: archiveRoot,
		}

		_, result, err := postJSON[archiveRawServerResult](ctx, icc, "/api/captures/archive", req)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("archive_raw: %w", err)), nil
		}

		return jsonResult(archiveRawToolResult{
			AlreadyArchived: result.AlreadyArchived,
			Fresh:           !result.AlreadyArchived,
			CodeRef:         result.CodeRef,
			OriginalPath:    result.OriginalPath,
			ArchivedPath:    result.ArchivedPath,
		})
	}
}
