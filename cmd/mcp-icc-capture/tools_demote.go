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

// demoteArtifactSchema declares the input contract for
// icc_demote_artifact. The artifact id lives in the URL path on the
// wire, but the MCP tool takes it as a regular arg so callers don't
// have to think about that.
func demoteArtifactSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"artifact_id", "reason"},
		Properties: map[string]any{
			"artifact_id": map[string]any{
				"type":        "string",
				"description": "ID of the artifact to demote (art_...)",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Non-empty audit reason for the demotion",
			},
			"keep_code_ref": map[string]any{
				"type":        "boolean",
				"default":     true,
				"description": "Keep underlying code_ref(s) when true; hard-delete when false",
			},
		},
	}
}

// demoteRequest is the JSON body posted to /api/artifacts/<id>/demote.
type demoteRequest struct {
	Reason      string `json:"reason"`
	KeepCodeRef bool   `json:"keep_code_ref"`
}

// demoteServerResult mirrors the ICC envelope's "result" payload. The
// kept/dropped split is the contract the backend tests assert against
// (tests/test_capture_endpoints.py:DemoteArtifactTest).
type demoteServerResult struct {
	Artifact        json.RawMessage   `json:"artifact"`
	KeptCodeRefs    []json.RawMessage `json:"kept_code_refs"`
	DroppedCodeRefs []json.RawMessage `json:"dropped_code_refs"`
}

// demoteToolResult is what we return to the MCP caller. We surface the
// kept/dropped lists from the server and add convenience booleans so
// callers don't have to count list lengths.
type demoteToolResult struct {
	Artifact        json.RawMessage   `json:"artifact"`
	KeptCodeRefs    []json.RawMessage `json:"kept_code_refs"`
	DroppedCodeRefs []json.RawMessage `json:"dropped_code_refs"`
	CodeRefUnlinked bool              `json:"code_ref_unlinked"`
	CodeRefDeleted  bool              `json:"code_ref_deleted"`
}

func makeDemoteArtifactHandler(icc *iccClient) iccToolHandler {
	return func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		v := validate.NewArgs(args)
		artifactID := v.Required("artifact_id")
		reason := v.Required("reason")
		keepCodeRef := v.Bool("keep_code_ref", true)
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		// Client-side refusals beyond Required(): empty after trim.
		if strings.TrimSpace(artifactID) == "" {
			return mcp.ErrorResult(errors.New("artifact_id must not be empty")), nil
		}
		if strings.TrimSpace(reason) == "" {
			return mcp.ErrorResult(errors.New("reason must not be empty or whitespace")), nil
		}

		path := fmt.Sprintf("/api/artifacts/%s/demote", artifactID)
		req := demoteRequest{Reason: reason, KeepCodeRef: keepCodeRef}

		_, result, err := postJSON[demoteServerResult](ctx, icc, path, req)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("demote_artifact: %w", err)), nil
		}

		return jsonResult(demoteToolResult{
			Artifact:        result.Artifact,
			KeptCodeRefs:    result.KeptCodeRefs,
			DroppedCodeRefs: result.DroppedCodeRefs,
			CodeRefUnlinked: len(result.KeptCodeRefs) > 0,
			CodeRefDeleted:  len(result.DroppedCodeRefs) > 0,
		})
	}
}
