// mcp-tavily is a fast Tavily AI search MCP server written in Go.
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

var version = "1.0.0"

type tavilyServer struct {
	apiKey     string
	httpClient *http.Client
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	apiKey := os.Getenv("TAVILY_API_KEY")
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "Warning: TAVILY_API_KEY not set\n")
	}

	tav := &tavilyServer{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}

	server := mcp.NewServer("mcp-tavily", version)
	server.SetInstructions("Fast Go-native Tavily AI search MCP server. Web search, news search, and content extraction.")

	// search
	server.AddTool(mcp.Tool{
		Name:        "search",
		Description: "Search the web using Tavily AI. Returns relevant results with content summaries.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
				"search_depth": map[string]any{
					"type":        "string",
					"description": "Search depth: 'basic' (faster) or 'advanced' (more thorough). Defaults to 'basic'.",
				},
				"include_answer": map[string]any{
					"type":        "boolean",
					"description": "Include AI-generated answer. Defaults to true.",
				},
				"include_raw_content": map[string]any{
					"type":        "boolean",
					"description": "Include raw HTML content. Defaults to false.",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results (1-10). Defaults to 5.",
				},
				"include_domains": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Only include results from these domains",
				},
				"exclude_domains": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Exclude results from these domains",
				},
			},
			Required: []string{"query"},
		},
	}, tav.handleSearch)

	// search_news
	server.AddTool(mcp.Tool{
		Name:        "search_news",
		Description: "Search for recent news articles using Tavily AI",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "News search query",
				},
				"days": map[string]any{
					"type":        "integer",
					"description": "Number of days back to search. Defaults to 3.",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results (1-10). Defaults to 5.",
				},
			},
			Required: []string{"query"},
		},
	}, tav.handleSearchNews)

	// extract
	server.AddTool(mcp.Tool{
		Name:        "extract",
		Description: "Extract content from URLs using Tavily AI",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"urls": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "URLs to extract content from",
				},
			},
			Required: []string{"urls"},
		},
	}, tav.handleExtract)

	// search_context
	server.AddTool(mcp.Tool{
		Name:        "search_context",
		Description: "Get search results optimized for LLM context (returns more content)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
				"max_tokens": map[string]any{
					"type":        "integer",
					"description": "Maximum tokens in response. Defaults to 4000.",
				},
				"search_depth": map[string]any{
					"type":        "string",
					"description": "Search depth: 'basic' or 'advanced'. Defaults to 'advanced'.",
				},
			},
			Required: []string{"query"},
		},
	}, tav.handleSearchContext)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func (t *tavilyServer) request(ctx context.Context, endpoint string, payload map[string]any) (map[string]any, error) {
	if t.apiKey == "" {
		return nil, fmt.Errorf("TAVILY_API_KEY not set")
	}

	payload["api_key"] = t.apiKey

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com"+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	maxBytes := getEnvInt("TAVILY_MAX_RESPONSE_BYTES", 5*1024*1024)
	respBody, truncated, err := readBodyWithLimit(resp.Body, maxBytes)
	if err != nil {
		return nil, err
	}
	if truncated && resp.StatusCode < 400 {
		return nil, fmt.Errorf("tavily response exceeded %d bytes (set TAVILY_MAX_RESPONSE_BYTES to increase; reduce max_results/max_tokens)", maxBytes)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("tavily API error %d: %s", resp.StatusCode, bodySnippet(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return result, nil
}

func getStringArg(args map[string]any, key, defaultVal string) string {
	if v, ok := args[key].(string); ok && v != "" {
		return v
	}
	return defaultVal
}

func getIntArg(args map[string]any, key string, defaultVal int) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	return defaultVal
}

func getBoolArg(args map[string]any, key string, defaultVal bool) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return defaultVal
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

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func getStringSliceArg(args map[string]any, key string) []string {
	if v, ok := args[key].([]any); ok {
		var result []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func (t *tavilyServer) handleSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := getStringArg(args, "query", "")
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	payload := map[string]any{
		"query":               query,
		"search_depth":        getStringArg(args, "search_depth", "basic"),
		"include_answer":      getBoolArg(args, "include_answer", true),
		"include_raw_content": getBoolArg(args, "include_raw_content", false),
		"max_results":         clampInt(getIntArg(args, "max_results", 5), 1, 10),
	}

	if domains := getStringSliceArg(args, "include_domains"); len(domains) > 0 {
		payload["include_domains"] = domains
	}
	if domains := getStringSliceArg(args, "exclude_domains"); len(domains) > 0 {
		payload["exclude_domains"] = domains
	}

	result, err := t.request(ctx, "/search", payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (t *tavilyServer) handleSearchNews(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := getStringArg(args, "query", "")
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	payload := map[string]any{
		"query":       query,
		"topic":       "news",
		"days":        clampInt(getIntArg(args, "days", 3), 1, 30),
		"max_results": clampInt(getIntArg(args, "max_results", 5), 1, 10),
	}

	result, err := t.request(ctx, "/search", payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (t *tavilyServer) handleExtract(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	urls := getStringSliceArg(args, "urls")
	if len(urls) == 0 {
		return nil, fmt.Errorf("urls is required")
	}

	payload := map[string]any{
		"urls": urls,
	}

	result, err := t.request(ctx, "/extract", payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (t *tavilyServer) handleSearchContext(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := getStringArg(args, "query", "")
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	payload := map[string]any{
		"query":               query,
		"search_depth":        getStringArg(args, "search_depth", "advanced"),
		"include_answer":      true,
		"include_raw_content": true,
		"max_results":         10,
	}

	maxTokens := clampInt(getIntArg(args, "max_tokens", 4000), 256, 16000)

	result, err := t.request(ctx, "/search", payload)
	if err != nil {
		return nil, err
	}

	// Format for LLM context
	var contextParts []string

	if answer, ok := result["answer"].(string); ok && answer != "" {
		contextParts = append(contextParts, fmt.Sprintf("## Summary\n%s", answer))
	}

	if results, ok := result["results"].([]any); ok {
		contextParts = append(contextParts, "\n## Sources")
		totalTokens := 0
		for i, r := range results {
			if item, ok := r.(map[string]any); ok {
				title, _ := item["title"].(string)
				url, _ := item["url"].(string)
				content, _ := item["content"].(string)

				// Rough token estimate
				tokens := len(content) / 4
				if totalTokens+tokens > maxTokens {
					break
				}
				totalTokens += tokens

				contextParts = append(contextParts, fmt.Sprintf("\n### %d. %s\nURL: %s\n%s", i+1, title, url, content))
			}
		}
	}

	return mcp.TextResult(strings.Join(contextParts, "\n")), nil
}
