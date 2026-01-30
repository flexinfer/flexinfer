// mcp-elasticsearch provides MCP tools for Elasticsearch search and cluster management.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var (
	version = "0.1.0"

	esURL      = getEnv("ELASTICSEARCH_URL", "http://localhost:9200")
	esUsername = os.Getenv("ELASTICSEARCH_USERNAME")
	esPassword = os.Getenv("ELASTICSEARCH_PASSWORD")
	esAPIKey   = os.Getenv("ELASTICSEARCH_API_KEY")

	httpClient *http.Client
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

func init() {
	transport := &http.Transport{}
	if skipVerify := os.Getenv("TLS_SKIP_VERIFY"); strings.ToLower(skipVerify) == "true" || skipVerify == "1" {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	httpClient = &http.Client{
		Timeout:   time.Duration(getEnvInt("ELASTICSEARCH_TIMEOUT", 30)) * time.Second,
		Transport: transport,
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	server := mcp.NewServer("mcp-elasticsearch", version)
	server.SetInstructions("Elasticsearch search and cluster management tools. Configure with ELASTICSEARCH_URL, ELASTICSEARCH_USERNAME/PASSWORD or ELASTICSEARCH_API_KEY.")

	// Search
	server.AddTool(mcp.Tool{
		Name:        "es_search",
		Description: "Execute an Elasticsearch search query",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"index": map[string]any{
					"type":        "string",
					"description": "Index name or pattern (e.g., 'logs-*'). Omit for all indices.",
				},
				"query": map[string]any{
					"type":        "object",
					"description": "Elasticsearch query DSL object",
				},
				"size": map[string]any{
					"type":        "integer",
					"description": "Number of hits to return (default: 10, max: 10000)",
				},
				"from": map[string]any{
					"type":        "integer",
					"description": "Offset for pagination",
				},
				"sort": map[string]any{
					"type":        "array",
					"description": "Sort order, e.g., [{\"@timestamp\": \"desc\"}]",
				},
				"_source": map[string]any{
					"type":        "array",
					"description": "Fields to include in _source",
					"items":       map[string]any{"type": "string"},
				},
				"aggs": map[string]any{
					"type":        "object",
					"description": "Aggregation definitions",
				},
				"track_total_hits": map[string]any{
					"type":        "boolean",
					"description": "Track total hits accurately (default: true)",
				},
			},
		},
	}, handleSearch)

	// Simple query string search
	server.AddTool(mcp.Tool{
		Name:        "es_query",
		Description: "Simple query string search (Lucene syntax)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"index": map[string]any{
					"type":        "string",
					"description": "Index name or pattern",
				},
				"q": map[string]any{
					"type":        "string",
					"description": "Query string (Lucene syntax, e.g., 'status:error AND message:timeout')",
				},
				"size": map[string]any{
					"type":        "integer",
					"description": "Number of hits to return (default: 10)",
				},
				"sort": map[string]any{
					"type":        "string",
					"description": "Sort field:order (e.g., '@timestamp:desc')",
				},
				"default_field": map[string]any{
					"type":        "string",
					"description": "Default field for query terms",
				},
			},
			Required: []string{"q"},
		},
	}, handleSimpleQuery)

	// Get document
	server.AddTool(mcp.Tool{
		Name:        "es_get",
		Description: "Get a document by ID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"index": map[string]any{
					"type":        "string",
					"description": "Index name",
				},
				"id": map[string]any{
					"type":        "string",
					"description": "Document ID",
				},
				"_source": map[string]any{
					"type":        "array",
					"description": "Fields to include in _source",
					"items":       map[string]any{"type": "string"},
				},
			},
			Required: []string{"index", "id"},
		},
	}, handleGet)

	// Count documents
	server.AddTool(mcp.Tool{
		Name:        "es_count",
		Description: "Count documents matching a query",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"index": map[string]any{
					"type":        "string",
					"description": "Index name or pattern",
				},
				"query": map[string]any{
					"type":        "object",
					"description": "Elasticsearch query DSL object",
				},
			},
		},
	}, handleCount)

	// List indices
	server.AddTool(mcp.Tool{
		Name:        "es_indices",
		Description: "List indices with stats",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Index name pattern (e.g., 'logs-*')",
				},
				"health": map[string]any{
					"type":        "string",
					"description": "Filter by health status",
					"enum":        []string{"green", "yellow", "red"},
				},
				"include_hidden": map[string]any{
					"type":        "boolean",
					"description": "Include hidden indices (default: false)",
				},
			},
		},
	}, handleIndices)

	// Get mapping
	server.AddTool(mcp.Tool{
		Name:        "es_mapping",
		Description: "Get index mapping (field types and structure)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"index": map[string]any{
					"type":        "string",
					"description": "Index name or pattern",
				},
			},
			Required: []string{"index"},
		},
	}, handleMapping)

	// Get aliases
	server.AddTool(mcp.Tool{
		Name:        "es_aliases",
		Description: "List index aliases",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"index": map[string]any{
					"type":        "string",
					"description": "Index name or pattern",
				},
				"alias": map[string]any{
					"type":        "string",
					"description": "Alias name or pattern",
				},
			},
		},
	}, handleAliases)

	// Cluster health
	server.AddTool(mcp.Tool{
		Name:        "es_health",
		Description: "Get cluster health status",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"index": map[string]any{
					"type":        "string",
					"description": "Index to check health for",
				},
				"level": map[string]any{
					"type":        "string",
					"description": "Detail level",
					"enum":        []string{"cluster", "indices", "shards"},
				},
			},
		},
	}, handleHealth)

	// Cluster info
	server.AddTool(mcp.Tool{
		Name:        "es_info",
		Description: "Get Elasticsearch cluster info and version",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleInfo)

	// Index stats
	server.AddTool(mcp.Tool{
		Name:        "es_stats",
		Description: "Get index statistics",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"index": map[string]any{
					"type":        "string",
					"description": "Index name or pattern",
				},
				"metric": map[string]any{
					"type":        "string",
					"description": "Specific metrics (e.g., 'docs,store,indexing')",
				},
			},
		},
	}, handleStats)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// HTTP helpers

func esRequest(ctx context.Context, method, path string, body any) (map[string]any, error) {
	reqURL, err := buildURL(path)
	if err != nil {
		return nil, fmt.Errorf("build URL: %w", err)
	}

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Authentication
	if esAPIKey != "" {
		req.Header.Set("Authorization", "ApiKey "+esAPIKey)
	} else if esUsername != "" && esPassword != "" {
		req.SetBasicAuth(esUsername, esPassword)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	maxBytes := getEnvInt("ELASTICSEARCH_MAX_RESPONSE_BYTES", 10*1024*1024)
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("elasticsearch error %d: %s", resp.StatusCode, truncate(string(respBody), 1000))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w (body: %s)", err, truncate(string(respBody), 500))
	}

	return result, nil
}

func esRequestRaw(ctx context.Context, method, path string, body any) ([]byte, error) {
	reqURL, err := buildURL(path)
	if err != nil {
		return nil, fmt.Errorf("build URL: %w", err)
	}

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if esAPIKey != "" {
		req.Header.Set("Authorization", "ApiKey "+esAPIKey)
	} else if esUsername != "" && esPassword != "" {
		req.SetBasicAuth(esUsername, esPassword)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	maxBytes := getEnvInt("ELASTICSEARCH_MAX_RESPONSE_BYTES", 10*1024*1024)
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("elasticsearch error %d: %s", resp.StatusCode, truncate(string(respBody), 1000))
	}

	return respBody, nil
}

func buildURL(path string) (string, error) {
	base, err := url.Parse(esURL)
	if err != nil {
		return "", err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(path, "/")
	return base.String(), nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// Handlers

func handleSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	index, _ := args["index"].(string)

	// Build search body
	body := make(map[string]any)

	if q, ok := args["query"].(map[string]any); ok {
		body["query"] = q
	}

	if size, ok := args["size"].(float64); ok {
		body["size"] = clampInt(int(size), 0, 10000)
	} else {
		body["size"] = 10
	}

	if from, ok := args["from"].(float64); ok {
		body["from"] = int(from)
	}

	if sort, ok := args["sort"].([]any); ok {
		body["sort"] = sort
	}

	if source, ok := args["_source"].([]any); ok {
		body["_source"] = source
	}

	if aggs, ok := args["aggs"].(map[string]any); ok {
		body["aggs"] = aggs
	}

	if trackTotal, ok := args["track_total_hits"].(bool); ok {
		body["track_total_hits"] = trackTotal
	} else {
		body["track_total_hits"] = true
	}

	// Build path
	path := "_search"
	if index != "" {
		path = url.PathEscape(index) + "/_search"
	}

	result, err := esRequest(ctx, "POST", path, body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Format response
	response := map[string]any{
		"ok":        true,
		"took_ms":   result["took"],
		"timed_out": result["timed_out"],
	}

	if hits, ok := result["hits"].(map[string]any); ok {
		response["total"] = hits["total"]
		response["max_score"] = hits["max_score"]

		// Flatten hits
		if hitList, ok := hits["hits"].([]any); ok {
			docs := make([]map[string]any, 0, len(hitList))
			for _, h := range hitList {
				if hit, ok := h.(map[string]any); ok {
					doc := map[string]any{
						"_index": hit["_index"],
						"_id":    hit["_id"],
						"_score": hit["_score"],
					}
					if source, ok := hit["_source"].(map[string]any); ok {
						doc["_source"] = source
					}
					docs = append(docs, doc)
				}
			}
			response["hits"] = docs
		}
	}

	if aggs, ok := result["aggregations"].(map[string]any); ok {
		response["aggregations"] = aggs
	}

	return mcp.JSONResult(response)
}

func handleSimpleQuery(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	index, _ := args["index"].(string)
	q, _ := args["q"].(string)

	if q == "" {
		return mcp.ErrorResult(fmt.Errorf("q is required")), nil
	}

	// Build query params
	params := url.Values{}
	params.Set("q", q)

	if size, ok := args["size"].(float64); ok {
		params.Set("size", fmt.Sprintf("%d", clampInt(int(size), 1, 10000)))
	}

	if sort, ok := args["sort"].(string); ok && sort != "" {
		params.Set("sort", sort)
	}

	if df, ok := args["default_field"].(string); ok && df != "" {
		params.Set("df", df)
	}

	// Build path
	path := "_search"
	if index != "" {
		path = url.PathEscape(index) + "/_search"
	}
	path += "?" + params.Encode()

	result, err := esRequest(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Format response
	response := map[string]any{
		"ok":        true,
		"took_ms":   result["took"],
		"timed_out": result["timed_out"],
	}

	if hits, ok := result["hits"].(map[string]any); ok {
		response["total"] = hits["total"]

		if hitList, ok := hits["hits"].([]any); ok {
			docs := make([]map[string]any, 0, len(hitList))
			for _, h := range hitList {
				if hit, ok := h.(map[string]any); ok {
					doc := map[string]any{
						"_index": hit["_index"],
						"_id":    hit["_id"],
						"_score": hit["_score"],
					}
					if source, ok := hit["_source"].(map[string]any); ok {
						doc["_source"] = source
					}
					docs = append(docs, doc)
				}
			}
			response["hits"] = docs
		}
	}

	return mcp.JSONResult(response)
}

func handleGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	index, _ := args["index"].(string)
	id, _ := args["id"].(string)

	if index == "" || id == "" {
		return mcp.ErrorResult(fmt.Errorf("index and id are required")), nil
	}

	path := fmt.Sprintf("%s/_doc/%s", url.PathEscape(index), url.PathEscape(id))

	if source, ok := args["_source"].([]any); ok && len(source) > 0 {
		fields := make([]string, 0, len(source))
		for _, f := range source {
			if s, ok := f.(string); ok {
				fields = append(fields, s)
			}
		}
		path += "?_source=" + url.QueryEscape(strings.Join(fields, ","))
	}

	result, err := esRequest(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      result["found"],
		"_index":  result["_index"],
		"_id":     result["_id"],
		"_source": result["_source"],
	})
}

func handleCount(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	index, _ := args["index"].(string)

	path := "_count"
	if index != "" {
		path = url.PathEscape(index) + "/_count"
	}

	var body any
	if query, ok := args["query"].(map[string]any); ok {
		body = map[string]any{"query": query}
	}

	result, err := esRequest(ctx, "POST", path, body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"count": result["count"],
	})
}

func handleIndices(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	pattern, _ := args["pattern"].(string)

	path := "_cat/indices"
	if pattern != "" {
		path += "/" + url.PathEscape(pattern)
	}

	params := url.Values{}
	params.Set("format", "json")
	params.Set("h", "index,health,status,pri,rep,docs.count,store.size,creation.date.string")
	params.Set("s", "index")

	if health, ok := args["health"].(string); ok && health != "" {
		params.Set("health", health)
	}

	if hidden, ok := args["include_hidden"].(bool); ok && hidden {
		params.Set("expand_wildcards", "all")
	}

	path += "?" + params.Encode()

	respBody, err := esRequestRaw(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var indices []map[string]any
	if err := json.Unmarshal(respBody, &indices); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse indices: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"count":   len(indices),
		"indices": indices,
	})
}

func handleMapping(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	index, _ := args["index"].(string)
	if index == "" {
		return mcp.ErrorResult(fmt.Errorf("index is required")), nil
	}

	path := url.PathEscape(index) + "/_mapping"

	result, err := esRequest(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"mappings": result,
	})
}

func handleAliases(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := "_cat/aliases"

	params := url.Values{}
	params.Set("format", "json")
	params.Set("h", "alias,index,filter,routing.index,routing.search")
	params.Set("s", "alias")

	if index, ok := args["index"].(string); ok && index != "" {
		path = url.PathEscape(index) + "/_alias"
		if alias, ok := args["alias"].(string); ok && alias != "" {
			path += "/" + url.PathEscape(alias)
		}
		// Use _alias endpoint for specific index, returns different format
		result, err := esRequest(ctx, "GET", path, nil)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		return mcp.JSONResult(map[string]any{
			"ok":      true,
			"aliases": result,
		})
	}

	if alias, ok := args["alias"].(string); ok && alias != "" {
		path = "_alias/" + url.PathEscape(alias)
		result, err := esRequest(ctx, "GET", path, nil)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		return mcp.JSONResult(map[string]any{
			"ok":      true,
			"aliases": result,
		})
	}

	path += "?" + params.Encode()

	respBody, err := esRequestRaw(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var aliases []map[string]any
	if err := json.Unmarshal(respBody, &aliases); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse aliases: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"count":   len(aliases),
		"aliases": aliases,
	})
}

func handleHealth(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := "_cluster/health"

	if index, ok := args["index"].(string); ok && index != "" {
		path += "/" + url.PathEscape(index)
	}

	params := url.Values{}
	if level, ok := args["level"].(string); ok && level != "" {
		params.Set("level", level)
	}

	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	result, err := esRequest(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"health": result,
	})
}

func handleInfo(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := esRequest(ctx, "GET", "", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":   true,
		"info": result,
	})
}

func handleStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := "_stats"

	if index, ok := args["index"].(string); ok && index != "" {
		path = url.PathEscape(index) + "/_stats"
	}

	if metric, ok := args["metric"].(string); ok && metric != "" {
		path += "/" + url.PathEscape(metric)
	}

	result, err := esRequest(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"stats": result,
	})
}
