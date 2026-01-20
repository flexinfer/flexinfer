package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var (
	version   = "0.1.0"
	qdrantURL = strings.TrimRight(getEnv("QDRANT_URL", "http://localhost:6333"), "/")
	apiKey    = getEnv("QDRANT_API_KEY", "")
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	server := mcp.NewServer("mcp-qdrant", version)
	server.SetInstructions("Qdrant vector database operations")

	registerTools(server)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
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

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("api-key", apiKey)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	maxBytes := getEnvInt("QDRANT_MAX_RESPONSE_BYTES", 5*1024*1024)
	respBody, truncated, err := readBodyWithLimit(resp.Body, maxBytes)
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
			return nil, fmt.Errorf("qdrant HTTP %d: %v", resp.StatusCode, apiResp)
		}
		return nil, fmt.Errorf("qdrant HTTP %d: %s", resp.StatusCode, bodySnippet(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse qdrant response: %w", err)
	}

	return result, nil
}

func bodySnippet(body []byte) string {
	const max = 4 * 1024
	truncated := false
	if len(body) > max {
		body = body[:max]
		truncated = true
	}
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "<empty response body>"
	}
	if truncated {
		return s + "…"
	}
	return s
}

func readBodyWithLimit(r io.Reader, maxBytes int) ([]byte, bool, error) {
	if maxBytes <= 0 {
		b, err := io.ReadAll(r)
		return b, false, err
	}

	b, err := io.ReadAll(io.LimitReader(r, int64(maxBytes+1)))
	if err != nil {
		return nil, false, err
	}
	if len(b) > maxBytes {
		return b[:maxBytes], true, nil
	}
	return b, false, nil
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
	name, _ := args["collection"].(string)
	size, _ := args["vector_size"].(float64)
	distance, _ := args["distance"].(string)
	if distance == "" {
		distance = "Cosine"
	}

	body := map[string]any{
		"vectors": map[string]any{
			"size":     int(size),
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
	name, _ := args["collection"].(string)
	res, err := qdrantRequest("DELETE", "collections/"+name, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(res)
}

func handleGetCollection(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	name, _ := args["collection"].(string)
	res, err := qdrantRequest("GET", "collections/"+name, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(res)
}

func handleSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	name, _ := args["collection"].(string)
	vector, _ := args["vector"].([]any)
	limit, _ := args["limit"].(float64)
	if limit == 0 {
		limit = 10
	}

	body := map[string]any{
		"vector":       vector,
		"limit":        int(limit),
		"with_payload": true,
	}
	if v, ok := args["score_threshold"].(float64); ok {
		body["score_threshold"] = v
	}
	if v, ok := args["filter"].(map[string]any); ok {
		body["filter"] = v
	}
	if v, ok := args["with_payload"].(bool); ok {
		body["with_payload"] = v
	}

	res, err := qdrantRequest("POST", fmt.Sprintf("collections/%s/points/search", name), body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(res)
}

func handleScroll(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	name, _ := args["collection"].(string)
	limit, _ := args["limit"].(float64)
	if limit == 0 {
		limit = 10
	}

	body := map[string]any{
		"limit": int(limit),
	}
	if v, ok := args["offset"].(string); ok {
		body["offset"] = v
	}
	if v, ok := args["filter"].(map[string]any); ok {
		body["filter"] = v
	}
	if v, ok := args["with_payload"].(bool); ok {
		body["with_payload"] = v
	}
	if v, ok := args["with_vector"].(bool); ok {
		body["with_vector"] = v
	}

	res, err := qdrantRequest("POST", fmt.Sprintf("collections/%s/points/scroll", name), body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(res)
}

func handleUpsert(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	name, _ := args["collection"].(string)
	points, _ := args["points"].([]any)
	wait, _ := args["wait"].(bool)

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
	name, _ := args["collection"].(string)
	wait, _ := args["wait"].(bool)

	body := map[string]any{}
	if points, ok := args["points"].([]any); ok {
		body["points"] = points
	} else if filter, ok := args["filter"].(map[string]any); ok {
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
