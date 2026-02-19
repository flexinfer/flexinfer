package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"
)

type jobsearchServer struct {
	logger                  *slog.Logger
	client                  apiCaller
	defaultMaxResponseBytes int
	tracer                  trace.Tracer
}

type endpointToolSpec struct {
	Name                    string
	Description             string
	Method                  string
	PathTemplate            string
	PathArgs                []string
	AllowQuery              bool
	RequirePayload          bool
	AllowPayload            bool
	ConfirmField            string
	DefaultExpectedStatuses []int
}

func (s *jobsearchServer) addEndpointTool(server *mcp.Server, spec endpointToolSpec) {
	server.AddTool(mcp.Tool{
		Name:        spec.Name,
		Description: spec.Description,
		InputSchema: buildEndpointToolSchema(spec),
	}, s.wrapTool(spec.Name, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		return s.handleEndpointSpec(ctx, spec, args)
	}))
}

func (s *jobsearchServer) handleEndpointSpec(ctx context.Context, spec endpointToolSpec, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)

	pathArgs := map[string]string{}
	for _, key := range spec.PathArgs {
		pathArgs[key] = v.Required(key)
	}

	confirmValue := true
	if spec.ConfirmField != "" {
		confirmValue = v.RequiredBool(spec.ConfirmField)
	}

	var payload any
	if spec.RequirePayload {
		payload = v.RequiredAny("payload")
	} else if spec.AllowPayload {
		payload = args["payload"]
	}

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if spec.ConfirmField != "" && !confirmValue {
		return mcp.ErrorResult(mcperror.InvalidParam(spec.ConfirmField, "must be true to proceed")), nil
	}

	query, err := parseStringMapArg(args, "query")
	if err != nil {
		return mcp.ErrorResult(mcperror.InvalidParam("query", err.Error())), nil
	}
	if !spec.AllowQuery {
		query = nil
	}

	maxBytes := v.Int("max_bytes", s.defaultMaxResponseBytes)
	if maxBytes <= 0 {
		maxBytes = s.defaultMaxResponseBytes
	}

	expectedStatuses := spec.DefaultExpectedStatuses
	if rawExpected, ok := args["expected_statuses"]; ok {
		expectedStatuses, err = parseIntSlice(rawExpected)
		if err != nil {
			return mcp.ErrorResult(mcperror.InvalidParam("expected_statuses", err.Error())), nil
		}
	}

	resolvedPath := applyPathTemplate(spec.PathTemplate, pathArgs)
	resp, err := s.client.Request(ctx, requestOptions{
		Method:           spec.Method,
		Path:             resolvedPath,
		Query:            query,
		Payload:          payload,
		MaxBytes:         maxBytes,
		ExpectedStatuses: expectedStatuses,
	})
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":            true,
		"tool":          spec.Name,
		"method":        strings.ToUpper(spec.Method),
		"path":          resolvedPath,
		"status":        resp.StatusCode,
		"content_type":  resp.ContentType,
		"truncated":     resp.Truncated,
		"response":      resp.Data,
		"response_meta": resp.Headers,
	})
}

func buildEndpointToolSchema(spec endpointToolSpec) mcp.InputSchema {
	properties := map[string]any{}
	required := make([]string, 0, len(spec.PathArgs)+2)

	for _, key := range spec.PathArgs {
		properties[key] = map[string]any{"type": "string", "description": fmt.Sprintf("Path parameter %q", key)}
		required = append(required, key)
	}

	if spec.AllowQuery {
		properties["query"] = map[string]any{
			"type":                 "object",
			"description":          "Optional query string parameters",
			"additionalProperties": true,
		}
	}

	if spec.RequirePayload || spec.AllowPayload {
		properties["payload"] = map[string]any{
			"type":                 "object",
			"description":          "JSON request body",
			"additionalProperties": true,
		}
		if spec.RequirePayload {
			required = append(required, "payload")
		}
	}

	if spec.ConfirmField != "" {
		properties[spec.ConfirmField] = map[string]any{"type": "boolean", "description": "Safety confirmation flag; must be true"}
		required = append(required, spec.ConfirmField)
	}

	properties["max_bytes"] = map[string]any{"type": "integer", "description": "Maximum bytes to read from response body"}
	properties["expected_statuses"] = map[string]any{
		"type":        "array",
		"description": "Optional explicit list of expected HTTP statuses",
		"items":       map[string]any{"type": "integer"},
	}

	schema := mcp.InputSchema{Type: "object", Properties: properties}
	if len(required) > 0 {
		schema.Required = required
	}
	return schema
}

func applyPathTemplate(pathTemplate string, pathArgs map[string]string) string {
	resolved := pathTemplate
	for key, value := range pathArgs {
		resolved = strings.ReplaceAll(resolved, "{"+key+"}", url.PathEscape(value))
	}
	return resolved
}

func parseStringMapArg(args map[string]any, field string) (map[string]string, error) {
	raw, ok := args[field]
	if !ok || raw == nil {
		return map[string]string{}, nil
	}

	result := map[string]string{}
	switch v := raw.(type) {
	case map[string]any:
		for key, value := range v {
			result[key] = fmt.Sprint(value)
		}
		return result, nil
	case map[string]string:
		for key, value := range v {
			result[key] = value
		}
		return result, nil
	default:
		return nil, fmt.Errorf("must be an object")
	}
}

func parseIntSlice(raw any) ([]int, error) {
	items, ok := raw.([]any)
	if !ok {
		if ints, ok2 := raw.([]int); ok2 {
			return ints, nil
		}
		return nil, fmt.Errorf("must be an array of integers")
	}

	out := make([]int, 0, len(items))
	for idx, item := range items {
		v, ok := toInt(item)
		if !ok {
			return nil, fmt.Errorf("index %d must be an integer", idx)
		}
		out = append(out, v)
	}
	return out, nil
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint:
		return int(n), true
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case string:
		i, err := strconv.Atoi(n)
		if err != nil {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

func isMutatingMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case httpMethodPost, httpMethodPut, httpMethodPatch, httpMethodDelete:
		return true
	default:
		return false
	}
}

func normalizeMethod(method string) string {
	m := strings.ToUpper(strings.TrimSpace(method))
	switch m {
	case httpMethodGet, httpMethodPost, httpMethodPut, httpMethodPatch, httpMethodDelete:
		return m
	default:
		return ""
	}
}

func sortedPaths(paths map[string]any) []string {
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMethods(raw any) []string {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	methods := make([]string, 0, len(obj))
	for k := range obj {
		mk := strings.ToUpper(strings.TrimSpace(k))
		if mk == "GET" || mk == "POST" || mk == "PUT" || mk == "PATCH" || mk == "DELETE" {
			methods = append(methods, mk)
		}
	}
	sort.Strings(methods)
	return methods
}

func (s *jobsearchServer) wrapTool(name string, handler mcp.ToolHandler) mcp.ToolHandler {
	return mcpotel.TracedToolHandler(s.tracer, name, handler)
}

const (
	httpMethodGet    = "GET"
	httpMethodPost   = "POST"
	httpMethodPut    = "PUT"
	httpMethodPatch  = "PATCH"
	httpMethodDelete = "DELETE"
)
