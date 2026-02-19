package main

import (
	"context"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/validate"
)

func registerCoreTools(server *mcp.Server, s *jobsearchServer) {
	s.addEndpointTool(server, endpointToolSpec{
		Name:         "jobsearch_health_get",
		Description:  "Get backend health status from /health",
		Method:       httpMethodGet,
		PathTemplate: "/health",
	})

	s.addEndpointTool(server, endpointToolSpec{
		Name:         "jobsearch_auth_me",
		Description:  "Get authenticated user details from /auth/me",
		Method:       httpMethodGet,
		PathTemplate: "/auth/me",
	})

	s.addEndpointTool(server, endpointToolSpec{
		Name:         "jobsearch_openapi_get",
		Description:  "Get OpenAPI schema from /openapi.json",
		Method:       httpMethodGet,
		PathTemplate: "/openapi.json",
	})

	server.AddTool(mcp.Tool{
		Name:        "jobsearch_routes_list",
		Description: "List all API routes discovered from OpenAPI schema",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"max_bytes": map[string]any{"type": "integer", "description": "Maximum bytes to read from OpenAPI response"},
			},
		},
	}, s.wrapTool("jobsearch_routes_list", s.handleRoutesList))

	server.AddTool(mcp.Tool{
		Name:        "jobsearch_endpoint_schema_get",
		Description: "Get OpenAPI operation schema for a specific path and method",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path":      map[string]any{"type": "string", "description": "API path (e.g. /entities/{entity_id})"},
				"method":    map[string]any{"type": "string", "description": "HTTP method (GET/POST/PUT/PATCH/DELETE), default GET"},
				"max_bytes": map[string]any{"type": "integer", "description": "Maximum bytes to read from OpenAPI response"},
			},
			Required: []string{"path"},
		},
	}, s.wrapTool("jobsearch_endpoint_schema_get", s.handleEndpointSchemaGet))
}

func (s *jobsearchServer) handleRoutesList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	maxBytes := v.Int("max_bytes", s.defaultMaxResponseBytes)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if maxBytes <= 0 {
		maxBytes = s.defaultMaxResponseBytes
	}

	openAPI, err := s.fetchOpenAPI(ctx, maxBytes)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	rawPaths, ok := openAPI["paths"].(map[string]any)
	if !ok {
		return mcp.ErrorResult(mcperror.ServerError("OpenAPI schema missing 'paths' object")), nil
	}

	routes := make([]map[string]any, 0, len(rawPaths))
	for _, path := range sortedPaths(rawPaths) {
		routes = append(routes, map[string]any{
			"path":    path,
			"methods": sortedMethods(rawPaths[path]),
		})
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"count":  len(routes),
		"routes": routes,
	})
}

func (s *jobsearchServer) handleEndpointSchemaGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	path := v.Required("path")
	method := strings.ToUpper(strings.TrimSpace(v.String("method", httpMethodGet)))
	maxBytes := v.Int("max_bytes", s.defaultMaxResponseBytes)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if normalizeMethod(method) == "" {
		return mcp.ErrorResult(mcperror.InvalidParam("method", "must be one of GET, POST, PUT, PATCH, DELETE")), nil
	}
	if maxBytes <= 0 {
		maxBytes = s.defaultMaxResponseBytes
	}

	openAPI, err := s.fetchOpenAPI(ctx, maxBytes)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	rawPaths, ok := openAPI["paths"].(map[string]any)
	if !ok {
		return mcp.ErrorResult(mcperror.ServerError("OpenAPI schema missing 'paths' object")), nil
	}

	opRaw, ok := rawPaths[path]
	if !ok {
		return mcp.ErrorResult(mcperror.NotFound("path", path)), nil
	}
	opObj, ok := opRaw.(map[string]any)
	if !ok {
		return mcp.ErrorResult(mcperror.ServerError(fmt.Sprintf("OpenAPI path %q has invalid schema type", path))), nil
	}

	op, ok := opObj[strings.ToLower(method)]
	if !ok {
		return mcp.ErrorResult(mcperror.NotFound("operation", method+" "+path)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"path":      path,
		"method":    method,
		"operation": op,
	})
}

func (s *jobsearchServer) fetchOpenAPI(ctx context.Context, maxBytes int) (map[string]any, error) {
	resp, err := s.client.Request(ctx, requestOptions{
		Method:   httpMethodGet,
		Path:     "/openapi.json",
		MaxBytes: maxBytes,
	})
	if err != nil {
		return nil, err
	}
	obj, ok := resp.Data.(map[string]any)
	if !ok {
		return nil, mcperror.ServerError("OpenAPI response is not a JSON object")
	}
	return obj, nil
}
