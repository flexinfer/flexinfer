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
	"strings"
	"syscall"
	"time"

	"github.com/crb2nu/loom/pkg/mcp"
	"github.com/google/uuid"
)

var (
	version           = "0.1.0"
	morphAPIKey       = os.Getenv("MORPH_API_KEY")
	morphBaseURL      = strings.TrimRight(getEnv("MORPH_BASE_URL", "https://api.morphllm.com/v1"), "/")
	defaultEmbedModel = getEnv("MORPH_EMBED_MODEL", "morph-embedding-v3")
	qdrantURL         = strings.TrimRight(getEnv("MORPH_QDRANT_URL", getEnv("QDRANT_URL", "http://localhost:6333")), "/")
	qdrantAPIKey      = getEnv("MORPH_QDRANT_API_KEY", os.Getenv("QDRANT_API_KEY"))
	defaultCollection = getEnv("MORPH_QDRANT_COLLECTION", getEnv("COLLECTION_NAME", "codex"))
	defaultDistance   = getEnv("MORPH_QDRANT_DISTANCE", "Cosine")
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
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

	server := mcp.NewServer("mcp-morph-embeddings", version)
	server.SetInstructions("Morph embeddings and Qdrant vector search")

	registerTools(server)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func registerTools(server *mcp.Server) {
	server.AddTool(mcp.Tool{
		Name:        "morph_embeddings_embed",
		Description: "Generate embeddings using Morph's embedding API",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"input": map[string]any{
					"anyOf": []map[string]any{
						{"type": "string"},
						{"type": "array", "items": map[string]any{"type": "string"}},
					},
				},
				"model": map[string]any{"type": "string"},
			},
			Required: []string{"input"},
		},
	}, handleEmbed)

	server.AddTool(mcp.Tool{
		Name:        "morph_embeddings_upsert",
		Description: "Embed text with Morph and upsert into a Qdrant collection",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"collection": map[string]any{"type": "string"},
				"records": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":       map[string]any{"type": "string"},
							"text":     map[string]any{"type": "string"},
							"metadata": map[string]any{"type": "object"},
						},
						"required": []string{"text"},
					},
				},
				"text":     map[string]any{"type": "string"},
				"id":       map[string]any{"type": "string"},
				"metadata": map[string]any{"type": "object"},
				"model":    map[string]any{"type": "string"},
			},
		},
	}, handleUpsert)

	server.AddTool(mcp.Tool{
		Name:        "morph_embeddings_search",
		Description: "Semantic search in Qdrant using Morph embeddings",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"collection":      map[string]any{"type": "string"},
				"query":           map[string]any{"type": "string"},
				"top_k":           map[string]any{"type": "integer"},
				"filter":          map[string]any{"type": "object"},
				"with_payload":    map[string]any{"type": "boolean"},
				"with_vectors":    map[string]any{"type": "boolean"},
				"model":           map[string]any{"type": "string"},
				"score_threshold": map[string]any{"type": "number"},
			},
			Required: []string{"query"},
		},
	}, handleSearch)
}

// Morph API

func morphEmbeddings(input []string, model string) (map[string]any, error) {
	if morphAPIKey == "" {
		return nil, fmt.Errorf("MORPH_API_KEY is not set")
	}
	if model == "" {
		model = defaultEmbedModel
	}

	payload := map[string]any{
		"model": model,
		"input": input,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", morphBaseURL+"/embeddings", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+morphAPIKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("morph API HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	data, _ := result["data"].([]any)
	var embeddings [][]float64
	for _, item := range data {
		if itemMap, ok := item.(map[string]any); ok {
			if emb, ok := itemMap["embedding"].([]any); ok {
				var vec []float64
				for _, v := range emb {
					if f, ok := v.(float64); ok {
						vec = append(vec, f)
					}
				}
				embeddings = append(embeddings, vec)
			}
		}
	}

	return map[string]any{
		"embeddings": embeddings,
		"usage":      result["usage"],
		"model":      model,
	}, nil
}

// Qdrant API

func qdrantRequest(method, endpoint string, body any) (map[string]any, error) {
	url := fmt.Sprintf("%s/%s", qdrantURL, strings.TrimPrefix(endpoint, "/"))
	var bodyReader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewBuffer(b)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if qdrantAPIKey != "" {
		req.Header.Set("api-key", qdrantAPIKey)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if len(respBody) == 0 {
		return map[string]any{}, nil
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse qdrant response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("qdrant HTTP %d: %v", resp.StatusCode, result)
	}

	return result, nil
}

func ensureCollection(collection string, vectorSize int) error {
	_, err := qdrantRequest("GET", "collections/"+collection, nil)
	if err == nil {
		return nil
	}

	// Assuming 404 if error (simplified)
	createBody := map[string]any{
		"vectors": map[string]any{
			"size":     vectorSize,
			"distance": defaultDistance,
		},
	}
	_, err = qdrantRequest("PUT", "collections/"+collection, createBody)
	return err
}

// Handlers

func handleEmbed(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	var inputList []string
	if v, ok := args["input"].(string); ok {
		inputList = []string{v}
	} else if v, ok := args["input"].([]any); ok {
		for _, s := range v {
			if str, ok := s.(string); ok {
				inputList = append(inputList, str)
			}
		}
	}

	if len(inputList) == 0 {
		return mcp.ErrorResult(fmt.Errorf("'input' is required")), nil
	}

	model, _ := args["model"].(string)
	res, err := morphEmbeddings(inputList, model)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	embeddings, _ := res["embeddings"].([][]float64)
	return mcp.JSONResult(map[string]any{
		"ok": true,
		"summary": map[string]any{
			"count": len(embeddings),
			"model": res["model"],
		},
		"embeddings": embeddings,
		"usage":      res["usage"],
		"model":      res["model"],
	})
}

func handleUpsert(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	collection, _ := args["collection"].(string)
	if collection == "" {
		collection = defaultCollection
	}

	var records []map[string]any
	if v, ok := args["records"].([]any); ok {
		for _, r := range v {
			if rec, ok := r.(map[string]any); ok {
				records = append(records, rec)
			}
		}
	} else if text, ok := args["text"].(string); ok {
		id, _ := args["id"].(string)
		if id == "" {
			id = uuid.New().String()
		}
		meta, _ := args["metadata"].(map[string]any)
		records = append(records, map[string]any{
			"id":       id,
			"text":     text,
			"metadata": meta,
		})
	}

	if len(records) == 0 {
		return mcp.ErrorResult(fmt.Errorf("provide 'records' array or 'text'")), nil
	}

	var texts []string
	var normalized []map[string]any

	for _, rec := range records {
		text, _ := rec["text"].(string)
		if text == "" {
			return mcp.ErrorResult(fmt.Errorf("record missing 'text'")), nil
		}
		id, _ := rec["id"].(string)
		if id == "" {
			id = uuid.New().String()
		}
		meta, _ := rec["metadata"].(map[string]any)
		if meta == nil {
			meta = make(map[string]any)
		}

		texts = append(texts, text)
		normalized = append(normalized, map[string]any{
			"id":       id,
			"text":     text,
			"metadata": meta,
		})
	}

	model, _ := args["model"].(string)
	embedRes, err := morphEmbeddings(texts, model)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	embeddings, _ := embedRes["embeddings"].([][]float64)
	if len(embeddings) == 0 {
		return mcp.ErrorResult(fmt.Errorf("no embeddings returned")), nil
	}

	if err := ensureCollection(collection, len(embeddings[0])); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection failed: %v", err)), nil
	}

	var points []map[string]any
	for i, rec := range normalized {
		payload := rec["metadata"].(map[string]any)
		payload["text"] = rec["text"]
		points = append(points, map[string]any{
			"id":      rec["id"],
			"vector":  embeddings[i],
			"payload": payload,
		})
	}

	upsertBody := map[string]any{
		"points": points,
		"wait":   true,
	}
	upsertRes, err := qdrantRequest("PUT", fmt.Sprintf("collections/%s/points", collection), upsertBody)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("upsert failed: %v", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":              true,
		"collection":      collection,
		"upserted":        len(points),
		"qdrant_result":   upsertRes,
		"embedding_model": embedRes["model"],
	})
}

func handleSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	collection, _ := args["collection"].(string)
	if collection == "" {
		collection = defaultCollection
	}
	query, _ := args["query"].(string)
	if query == "" {
		return mcp.ErrorResult(fmt.Errorf("'query' is required")), nil
	}
	limit, _ := args["top_k"].(float64)
	if limit == 0 {
		limit = 5
	}

	model, _ := args["model"].(string)
	embedRes, err := morphEmbeddings([]string{query}, model)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	embeddings, _ := embedRes["embeddings"].([][]float64)
	if len(embeddings) == 0 {
		return mcp.ErrorResult(fmt.Errorf("no embedding for query")), nil
	}

	body := map[string]any{
		"vector":       embeddings[0],
		"limit":        int(limit),
		"with_payload": true,
		"with_vectors": false,
	}
	if v, ok := args["with_payload"].(bool); ok {
		body["with_payload"] = v
	}
	if v, ok := args["with_vectors"].(bool); ok {
		body["with_vectors"] = v
	}
	if v, ok := args["score_threshold"].(float64); ok {
		body["score_threshold"] = v
	}
	if v, ok := args["filter"].(map[string]any); ok {
		body["filter"] = v
	}

	searchRes, err := qdrantRequest("POST", fmt.Sprintf("collections/%s/points/search", collection), body)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("search failed: %v", err)), nil
	}

	hits, _ := searchRes["result"].([]any)
	var summary []map[string]any
	for _, h := range hits {
		if hit, ok := h.(map[string]any); ok {
			summary = append(summary, map[string]any{
				"id":    hit["id"],
				"score": hit["score"],
			})
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":              true,
		"collection":      collection,
		"results":         hits,
		"summary":         summary,
		"embedding_model": embedRes["model"],
	})
}
