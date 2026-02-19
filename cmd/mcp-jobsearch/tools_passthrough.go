package main

import (
	"context"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/validate"
)

func registerPassthroughTools(server *mcp.Server, s *jobsearchServer) {
	server.AddTool(mcp.Tool{
		Name:        "jobsearch_api_call",
		Description: "Guarded generic API passthrough for full route coverage",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"method": map[string]any{"type": "string", "description": "HTTP method: GET, POST, PUT, PATCH, DELETE"},
				"path":   map[string]any{"type": "string", "description": "Absolute API path beginning with /"},
				"query": map[string]any{
					"type":                 "object",
					"description":          "Optional query params",
					"additionalProperties": true,
				},
				"body": map[string]any{
					"type":                 "object",
					"description":          "Optional JSON body",
					"additionalProperties": true,
				},
				"confirm_write": map[string]any{"type": "boolean", "description": "Required true for mutating methods"},
				"max_bytes":     map[string]any{"type": "integer", "description": "Maximum bytes to read from response body"},
				"expected_statuses": map[string]any{
					"type":        "array",
					"description": "Optional explicit list of expected HTTP statuses",
					"items":       map[string]any{"type": "integer"},
				},
			},
			Required: []string{"method", "path"},
		},
	}, s.wrapTool("jobsearch_api_call", s.handleAPICall))
}

func (s *jobsearchServer) handleAPICall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	method := normalizeMethod(v.Required("method"))
	path := strings.TrimSpace(v.Required("path"))
	confirmWrite := v.Bool("confirm_write", false)
	maxBytes := v.Int("max_bytes", s.defaultMaxResponseBytes)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if method == "" {
		return mcp.ErrorResult(mcperror.InvalidParam("method", "must be one of GET, POST, PUT, PATCH, DELETE")), nil
	}
	if strings.Contains(path, "://") {
		return mcp.ErrorResult(mcperror.InvalidParam("path", "must be a relative API path, not a full URL")), nil
	}
	if !strings.HasPrefix(path, "/") {
		return mcp.ErrorResult(mcperror.InvalidParam("path", "must start with '/'")), nil
	}
	if isMutatingMethod(method) && !confirmWrite {
		return mcp.ErrorResult(mcperror.InvalidParam("confirm_write", "must be true for POST/PUT/PATCH/DELETE")), nil
	}
	if maxBytes <= 0 {
		maxBytes = s.defaultMaxResponseBytes
	}

	query, err := parseStringMapArg(args, "query")
	if err != nil {
		return mcp.ErrorResult(mcperror.InvalidParam("query", err.Error())), nil
	}

	var expectedStatuses []int
	if rawExpected, ok := args["expected_statuses"]; ok {
		expectedStatuses, err = parseIntSlice(rawExpected)
		if err != nil {
			return mcp.ErrorResult(mcperror.InvalidParam("expected_statuses", err.Error())), nil
		}
	}

	resp, err := s.client.Request(ctx, requestOptions{
		Method:           method,
		Path:             path,
		Query:            query,
		Payload:          args["body"],
		MaxBytes:         maxBytes,
		ExpectedStatuses: expectedStatuses,
	})
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":            true,
		"tool":          "jobsearch_api_call",
		"method":        method,
		"path":          path,
		"status":        resp.StatusCode,
		"content_type":  resp.ContentType,
		"truncated":     resp.Truncated,
		"response":      resp.Data,
		"response_meta": resp.Headers,
	})
}
