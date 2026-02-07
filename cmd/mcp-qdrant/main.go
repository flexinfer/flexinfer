package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/strutil"
	"github.com/crb2nu/loom/pkg/validate"
)

var (
	version    = "0.1.0"
	qdrantURL  = strings.TrimRight(env.String("QDRANT_URL", "http://localhost:6333"), "/")
	apiKey     = env.String("QDRANT_API_KEY", "")
	httpClient = httpclient.NewDefault()
)

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	logger.Info("starting server", "name", "mcp-qdrant", "version", version, "url", qdrantURL)

	server := mcp.NewServer("mcp-qdrant", version)
	server.SetInstructions("Qdrant vector database operations")

	registerTools(server)

	return server.Run(ctx)
}

func registerTools(server *mcp.Server) {
	server.AddTool(mcp.Tool{
		Name:        "qdrant_list_collections",
		Description: "List all collections",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, handleListCollections)

	server.AddTool(mcp.Tool{
		Name:        "qdrant_create_collection",
		Description: "Create a new collection",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"collection":  map[string]any{"type": "string"},
				"vector_size": map[string]any{"type": "integer"},
				"distance":    map[string]any{"type": "string", "enum": []string{"Cosine", "Euclid", "Dot"}},
			},
			Required: []string{"collection", "vector_size"},
		},
	}, handleCreateCollection)

	server.AddTool(mcp.Tool{
		Name:        "qdrant_delete_collection",
		Description: "Delete a collection",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"collection": map[string]any{"type": "string"},
			},
			Required: []string{"collection"},
		},
	}, handleDeleteCollection)

	server.AddTool(mcp.Tool{
		Name:        "qdrant_get_collection",
		Description: "Get collection info",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"collection": map[string]any{"type": "string"},
			},
			Required: []string{"collection"},
		},
	}, handleGetCollection)

	server.AddTool(mcp.Tool{
		Name:        "qdrant_search",
		Description: "Search for similar vectors",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"collection":      map[string]any{"type": "string"},
				"vector":          map[string]any{"type": "array", "items": map[string]any{"type": "number"}},
				"limit":           map[string]any{"type": "integer"},
				"score_threshold": map[string]any{"type": "number"},
				"filter":          map[string]any{"type": "object"},
				"with_payload":    map[string]any{"type": "boolean"},
			},
			Required: []string{"collection", "vector"},
		},
	}, handleSearch)

	server.AddTool(mcp.Tool{
		Name:        "qdrant_scroll",
		Description: "Scroll/list points",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"collection":   map[string]any{"type": "string"},
				"limit":        map[string]any{"type": "integer"},
				"offset":       map[string]any{"type": "string"},
				"filter":       map[string]any{"type": "object"},
				"with_payload": map[string]any{"type": "boolean"},
				"with_vector":  map[string]any{"type": "boolean"},
			},
			Required: []string{"collection"},
		},
	}, handleScroll)

	server.AddTool(mcp.Tool{
		Name:        "qdrant_upsert",
		Description: "Upsert points",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"collection": map[string]any{"type": "string"},
				"points": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":      map[string]any{"type": "string"}, // or int
							"vector":  map[string]any{"type": "array", "items": map[string]any{"type": "number"}},
							"payload": map[string]any{"type": "object"},
						},
						"required": []string{"id", "vector"},
					},
				},
				"wait": map[string]any{"type": "boolean"},
			},
			Required: []string{"collection", "points"},
		},
	}, handleUpsert)

	server.AddTool(mcp.Tool{
		Name:        "qdrant_delete",
		Description: "Delete points",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"collection": map[string]any{"type": "string"},
				"points":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, // IDs
				"filter":     map[string]any{"type": "object"},
				"wait":       map[string]any{"type": "boolean"},
			},
			Required: []string{"collection"},
		},
	}, handleDelete)
}

// Qdrant Client

func qdrantRequest(method, endpoint string, body any) (map[string]any, error) {
	url := fmt.Sprintf("%s/%s", qdrantURL, strings.TrimPrefix(endpoint, "/"))
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewBuffer(b)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("api-key", apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	maxBytes := env.Int("QDRANT_MAX_RESPONSE_BYTES", 5*1024*1024)
	respBody, truncated, err := httpclient.ReadBodyWithLimit(resp.Body, maxBytes)
	if err != nil {
		return nil, err
	}
	if truncated && resp.StatusCode < 400 {
		return nil, fmt.Errorf("qdrant response exceeded %d bytes (set QDRANT_MAX_RESPONSE_BYTES to increase)", maxBytes)
	}

	if len(respBody) == 0 {
		return map[string]any{}, nil
	}

	if resp.StatusCode >= 400 {
		var apiResp any
		if err := json.Unmarshal(respBody, &apiResp); err == nil {
			return nil, mcperror.APIError("Qdrant", resp.StatusCode, fmt.Sprintf("%v", apiResp))
		}
		return nil, mcperror.APIError("Qdrant", resp.StatusCode, strutil.BodySnippet(respBody, 4096))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse qdrant response: %w", err)
	}

	return result, nil
}

// Handlers

func handleListCollections(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	res, err := qdrantRequest("GET", "collections", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(res)
}

func handleCreateCollection(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("collection")
	size := v.RequiredInt("vector_size")
	distance := v.Enum("distance", "Cosine", "Cosine", "Euclid", "Dot")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	body := map[string]any{
		"vectors": map[string]any{
			"size":     size,
			"distance": distance,
		},
	}

	res, err := qdrantRequest("PUT", "collections/"+name, body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(res)
}

func handleDeleteCollection(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("collection")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	res, err := qdrantRequest("DELETE", "collections/"+name, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(res)
}

func handleGetCollection(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("collection")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	res, err := qdrantRequest("GET", "collections/"+name, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(res)
}

func handleSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("collection")
	vector := v.RequiredAny("vector")
	limit := v.Int("limit", 10)
	scoreThreshold := v.Float("score_threshold", 0)
	filter := v.Any("filter")
	withPayload := v.Bool("with_payload", true)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	body := map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": withPayload,
	}
	if scoreThreshold > 0 {
		body["score_threshold"] = scoreThreshold
	}
	if filter != nil {
		body["filter"] = filter
	}

	res, err := qdrantRequest("POST", fmt.Sprintf("collections/%s/points/search", name), body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(res)
}

func handleScroll(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("collection")
	limit := v.Int("limit", 10)
	offset := v.String("offset", "")
	filter := v.Any("filter")
	withPayload := v.Bool("with_payload", false)
	withVector := v.Bool("with_vector", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	body := map[string]any{
		"limit": limit,
	}
	if offset != "" {
		body["offset"] = offset
	}
	if filter != nil {
		body["filter"] = filter
	}
	if withPayload {
		body["with_payload"] = withPayload
	}
	if withVector {
		body["with_vector"] = withVector
	}

	res, err := qdrantRequest("POST", fmt.Sprintf("collections/%s/points/scroll", name), body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(res)
}

func handleUpsert(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("collection")
	points := v.RequiredAny("points")
	wait := v.Bool("wait", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	body := map[string]any{
		"points": points,
	}

	url := fmt.Sprintf("collections/%s/points", name)
	if wait {
		url += "?wait=true"
	}

	res, err := qdrantRequest("PUT", url, body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(res)
}

func handleDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("collection")
	points := v.Any("points")
	filter := v.Any("filter")
	wait := v.Bool("wait", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	body := map[string]any{}
	if points != nil {
		body["points"] = points
	} else if filter != nil {
		body["filter"] = filter
	} else {
		return mcp.ErrorResult(fmt.Errorf("provide 'points' or 'filter'")), nil
	}

	url := fmt.Sprintf("collections/%s/points/delete", name)
	if wait {
		url += "?wait=true"
	}

	res, err := qdrantRequest("POST", url, body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(res)
}
