// mcp-elasticsearch provides MCP tools for Elasticsearch search and cluster management.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

var (
	version = "0.1.0"

	esURL      = getEnv("ELASTICSEARCH_URL", "http://localhost:9200")
	esUsername = os.Getenv("ELASTICSEARCH_USERNAME")
	esPassword = os.Getenv("ELASTICSEARCH_PASSWORD")
	esAPIKey   = os.Getenv("ELASTICSEARCH_API_KEY")

	httpClient *httpclient.Client
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
	httpClient = httpclient.NewDefault()
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	logger.Info("starting server", "name", "mcp-elasticsearch", "version", version, "url", esURL)

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

	return server.Run(ctx)
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

	resp, err := httpClient.HTTP().Do(req)
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

	resp, err := httpClient.HTTP().Do(req)
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

// Handlers

func handleSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	index := v.String("index", "")
	size := v.IntRange("size", 10, 0, 10000)
	from := v.Int("from", 0)
	trackTotal := v.Bool("track_total_hits", true)
	source := v.StringSlice("_source")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Build search body
	body := make(map[string]any)

	if q, ok := v.Any("query").(map[string]any); ok {
		body["query"] = q
	}

	body["size"] = size

	if from > 0 {
		body["from"] = from
	}

	if sort, ok := v.Any("sort").([]any); ok {
		body["sort"] = sort
	}

	if len(source) > 0 {
		body["_source"] = source
	}

	if aggs, ok := v.Any("aggs").(map[string]any); ok {
		body["aggs"] = aggs
	}

	body["track_total_hits"] = trackTotal

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
	v := validate.NewArgs(args)
	index := v.String("index", "")
	q := v.Required("q")
	size := v.IntRange("size", 10, 1, 10000)
	sort := v.String("sort", "")
	defaultField := v.String("default_field", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Build query params
	params := url.Values{}
	params.Set("q", q)
	params.Set("size", fmt.Sprintf("%d", size))

	if sort != "" {
		params.Set("sort", sort)
	}

	if defaultField != "" {
		params.Set("df", defaultField)
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
	v := validate.NewArgs(args)
	index := v.Required("index")
	id := v.Required("id")
	source := v.StringSlice("_source")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("%s/_doc/%s", url.PathEscape(index), url.PathEscape(id))

	if len(source) > 0 {
		path += "?_source=" + url.QueryEscape(strings.Join(source, ","))
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
	v := validate.NewArgs(args)
	index := v.String("index", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := "_count"
	if index != "" {
		path = url.PathEscape(index) + "/_count"
	}

	var body any
	if query, ok := v.Any("query").(map[string]any); ok {
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
	v := validate.NewArgs(args)
	pattern := v.String("pattern", "")
	health := v.Enum("health", "", "green", "yellow", "red")
	includeHidden := v.Bool("include_hidden", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := "_cat/indices"
	if pattern != "" {
		path += "/" + url.PathEscape(pattern)
	}

	params := url.Values{}
	params.Set("format", "json")
	params.Set("h", "index,health,status,pri,rep,docs.count,store.size,creation.date.string")
	params.Set("s", "index")

	if health != "" {
		params.Set("health", health)
	}

	if includeHidden {
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
	v := validate.NewArgs(args)
	index := v.Required("index")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
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
	v := validate.NewArgs(args)
	index := v.String("index", "")
	alias := v.String("alias", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := "_cat/aliases"

	params := url.Values{}
	params.Set("format", "json")
	params.Set("h", "alias,index,filter,routing.index,routing.search")
	params.Set("s", "alias")

	if index != "" {
		path = url.PathEscape(index) + "/_alias"
		if alias != "" {
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

	if alias != "" {
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
	v := validate.NewArgs(args)
	index := v.String("index", "")
	level := v.Enum("level", "", "cluster", "indices", "shards")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := "_cluster/health"

	if index != "" {
		path += "/" + url.PathEscape(index)
	}

	params := url.Values{}
	if level != "" {
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
	v := validate.NewArgs(args)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

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
	v := validate.NewArgs(args)
	index := v.String("index", "")
	metric := v.String("metric", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := "_stats"

	if index != "" {
		path = url.PathEscape(index) + "/_stats"
	}

	if metric != "" {
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
