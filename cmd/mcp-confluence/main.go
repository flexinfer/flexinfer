// mcp-confluence is an MCP server for Atlassian Confluence wiki access.
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
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/crb2nu/loom/pkg/validate"
	"gitlab.flexinfer.ai/libs/mcp-go"
)

var version = "1.0.0"

type confluenceServer struct {
	baseURL    string
	email      string
	apiToken   string
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

	baseURL := os.Getenv("CONFLUENCE_URL")
	if baseURL == "" {
		fmt.Fprintf(os.Stderr, "CONFLUENCE_URL environment variable is required\n")
		os.Exit(1)
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	email := os.Getenv("CONFLUENCE_EMAIL")
	apiToken := os.Getenv("CONFLUENCE_API_TOKEN")

	if email == "" || apiToken == "" {
		fmt.Fprintf(os.Stderr, "CONFLUENCE_EMAIL and CONFLUENCE_API_TOKEN are required\n")
		os.Exit(1)
	}

	cs := &confluenceServer{
		baseURL:  baseURL,
		email:    email,
		apiToken: apiToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	server := mcp.NewServer("mcp-confluence", version)
	server.SetInstructions("Confluence wiki MCP server. Search and access wiki pages, spaces, and content.")

	// confluence_search
	server.AddTool(mcp.Tool{
		Name:        "confluence_search",
		Description: "Search Confluence content using CQL (Confluence Query Language)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"cql": map[string]any{
					"type":        "string",
					"description": "CQL query (e.g., 'text ~ \"kubernetes\" AND space = DEV')",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max results (default: 25, max: 100)",
				},
				"start": map[string]any{
					"type":        "integer",
					"description": "Start index for pagination (default: 0)",
				},
			},
			Required: []string{"cql"},
		},
	}, cs.handleSearch)

	// confluence_get_page
	server.AddTool(mcp.Tool{
		Name:        "confluence_get_page",
		Description: "Get a Confluence page by ID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"page_id": map[string]any{
					"type":        "string",
					"description": "Page ID",
				},
				"expand": map[string]any{
					"type":        "string",
					"description": "Fields to expand (comma-separated: body.storage,version,ancestors,children.page)",
				},
			},
			Required: []string{"page_id"},
		},
	}, cs.handleGetPage)

	// confluence_get_page_by_title
	server.AddTool(mcp.Tool{
		Name:        "confluence_get_page_by_title",
		Description: "Get a Confluence page by space key and title",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"space_key": map[string]any{
					"type":        "string",
					"description": "Space key (e.g., 'DEV', 'DOCS')",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Page title",
				},
				"expand": map[string]any{
					"type":        "string",
					"description": "Fields to expand (comma-separated: body.storage,version,ancestors)",
				},
			},
			Required: []string{"space_key", "title"},
		},
	}, cs.handleGetPageByTitle)

	// confluence_list_spaces
	server.AddTool(mcp.Tool{
		Name:        "confluence_list_spaces",
		Description: "List Confluence spaces",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"type": map[string]any{
					"type":        "string",
					"description": "Space type filter: global, personal, all (default: all)",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "Status filter: current, archived (default: current)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max results (default: 25, max: 100)",
				},
			},
		},
	}, cs.handleListSpaces)

	// confluence_get_space
	server.AddTool(mcp.Tool{
		Name:        "confluence_get_space",
		Description: "Get details of a Confluence space",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"space_key": map[string]any{
					"type":        "string",
					"description": "Space key",
				},
				"expand": map[string]any{
					"type":        "string",
					"description": "Fields to expand (comma-separated: description,homepage)",
				},
			},
			Required: []string{"space_key"},
		},
	}, cs.handleGetSpace)

	// confluence_list_pages
	server.AddTool(mcp.Tool{
		Name:        "confluence_list_pages",
		Description: "List pages in a Confluence space",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"space_key": map[string]any{
					"type":        "string",
					"description": "Space key",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max results (default: 25, max: 100)",
				},
				"start": map[string]any{
					"type":        "integer",
					"description": "Start index for pagination",
				},
			},
			Required: []string{"space_key"},
		},
	}, cs.handleListPages)

	// confluence_get_children
	server.AddTool(mcp.Tool{
		Name:        "confluence_get_children",
		Description: "Get child pages of a Confluence page",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"page_id": map[string]any{
					"type":        "string",
					"description": "Parent page ID",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max results (default: 25, max: 100)",
				},
			},
			Required: []string{"page_id"},
		},
	}, cs.handleGetChildren)

	// confluence_get_ancestors
	server.AddTool(mcp.Tool{
		Name:        "confluence_get_ancestors",
		Description: "Get ancestor (parent) pages of a Confluence page",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"page_id": map[string]any{
					"type":        "string",
					"description": "Page ID",
				},
			},
			Required: []string{"page_id"},
		},
	}, cs.handleGetAncestors)

	// confluence_create_page
	server.AddTool(mcp.Tool{
		Name:        "confluence_create_page",
		Description: "Create a new Confluence page",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"space_key": map[string]any{
					"type":        "string",
					"description": "Space key",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Page title",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Page content (Confluence storage format or plain text)",
				},
				"parent_id": map[string]any{
					"type":        "string",
					"description": "Parent page ID (optional)",
				},
			},
			Required: []string{"space_key", "title", "content"},
		},
	}, cs.handleCreatePage)

	// confluence_update_page
	server.AddTool(mcp.Tool{
		Name:        "confluence_update_page",
		Description: "Update an existing Confluence page",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"page_id": map[string]any{
					"type":        "string",
					"description": "Page ID",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "New page title (optional, keeps existing if not provided)",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "New page content",
				},
				"version_message": map[string]any{
					"type":        "string",
					"description": "Version comment (optional)",
				},
			},
			Required: []string{"page_id", "content"},
		},
	}, cs.handleUpdatePage)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func (s *confluenceServer) request(ctx context.Context, method, path string, body any) (map[string]any, error) {
	reqURL := s.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(s.email, s.apiToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	maxBytes := getEnvInt("CONFLUENCE_MAX_RESPONSE_BYTES", 10*1024*1024)
	respBody, truncated, err := readBodyWithLimit(resp.Body, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if truncated && resp.StatusCode < 400 {
		return nil, fmt.Errorf("confluence response exceeded %d bytes (set CONFLUENCE_MAX_RESPONSE_BYTES to increase; narrow expand fields)", maxBytes)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("confluence API error %d: %s", resp.StatusCode, bodySnippet(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
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

func (s *confluenceServer) handleSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	cql := v.Required("cql")
	limit := v.IntRange("limit", 25, 1, 100)
	start := v.Int("start", 0)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("cql", cql)
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("start", fmt.Sprintf("%d", start))

	result, err := s.request(ctx, "GET", "/wiki/rest/api/content/search?"+params.Encode(), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Simplify results
	simplified := s.simplifySearchResults(result)
	return mcp.JSONResult(simplified)
}

func (s *confluenceServer) simplifySearchResults(result map[string]any) map[string]any {
	simplified := map[string]any{
		"ok": true,
	}

	if results, ok := result["results"].([]any); ok {
		pages := make([]map[string]any, 0, len(results))
		for _, r := range results {
			if page, ok := r.(map[string]any); ok {
				p := map[string]any{
					"id":    page["id"],
					"title": page["title"],
					"type":  page["type"],
				}
				if links, ok := page["_links"].(map[string]any); ok {
					p["webui"] = links["webui"]
				}
				if space, ok := page["space"].(map[string]any); ok {
					p["space_key"] = space["key"]
					p["space_name"] = space["name"]
				}
				pages = append(pages, p)
			}
		}
		simplified["results"] = pages
		simplified["count"] = len(pages)
	}

	if size, ok := result["size"].(float64); ok {
		simplified["total_size"] = int(size)
	}

	return simplified
}

func (s *confluenceServer) handleGetPage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	pageID := v.Required("page_id")
	expand := v.String("expand", "body.storage,version")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("expand", expand)

	result, err := s.request(ctx, "GET", "/wiki/rest/api/content/"+pageID+"?"+params.Encode(), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	simplified := s.simplifyPage(result)
	return mcp.JSONResult(simplified)
}

func (s *confluenceServer) simplifyPage(page map[string]any) map[string]any {
	simplified := map[string]any{
		"ok":    true,
		"id":    page["id"],
		"title": page["title"],
		"type":  page["type"],
	}

	if space, ok := page["space"].(map[string]any); ok {
		simplified["space_key"] = space["key"]
		simplified["space_name"] = space["name"]
	}

	if version, ok := page["version"].(map[string]any); ok {
		simplified["version"] = version["number"]
		if by, ok := version["by"].(map[string]any); ok {
			simplified["last_modified_by"] = by["displayName"]
		}
		if when, ok := version["when"].(string); ok {
			simplified["last_modified"] = when
		}
	}

	if body, ok := page["body"].(map[string]any); ok {
		if storage, ok := body["storage"].(map[string]any); ok {
			if value, ok := storage["value"].(string); ok {
				simplified["content"] = value
			}
		}
		if view, ok := body["view"].(map[string]any); ok {
			if value, ok := view["value"].(string); ok {
				simplified["content_html"] = value
			}
		}
	}

	if links, ok := page["_links"].(map[string]any); ok {
		simplified["webui"] = links["webui"]
	}

	if ancestors, ok := page["ancestors"].([]any); ok {
		ancestorList := make([]map[string]any, 0, len(ancestors))
		for _, a := range ancestors {
			if ancestor, ok := a.(map[string]any); ok {
				ancestorList = append(ancestorList, map[string]any{
					"id":    ancestor["id"],
					"title": ancestor["title"],
				})
			}
		}
		simplified["ancestors"] = ancestorList
	}

	return simplified
}

func (s *confluenceServer) handleGetPageByTitle(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	spaceKey := v.Required("space_key")
	title := v.Required("title")
	expand := v.String("expand", "body.storage,version")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("spaceKey", spaceKey)
	params.Set("title", title)
	params.Set("expand", expand)

	result, err := s.request(ctx, "GET", "/wiki/rest/api/content?"+params.Encode(), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	if results, ok := result["results"].([]any); ok && len(results) > 0 {
		if page, ok := results[0].(map[string]any); ok {
			return mcp.JSONResult(s.simplifyPage(page))
		}
	}

	return mcp.ErrorResult(fmt.Errorf("page not found: %s/%s", spaceKey, title)), nil
}

func (s *confluenceServer) handleListSpaces(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	spaceType := v.String("type", "")
	status := v.String("status", "current")
	limit := v.IntRange("limit", 25, 1, 100)

	params := url.Values{}
	if spaceType != "" && spaceType != "all" {
		params.Set("type", spaceType)
	}
	params.Set("status", status)
	params.Set("limit", fmt.Sprintf("%d", limit))

	result, err := s.request(ctx, "GET", "/wiki/rest/api/space?"+params.Encode(), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	simplified := map[string]any{
		"ok": true,
	}

	if results, ok := result["results"].([]any); ok {
		spaces := make([]map[string]any, 0, len(results))
		for _, r := range results {
			if space, ok := r.(map[string]any); ok {
				s := map[string]any{
					"key":  space["key"],
					"name": space["name"],
					"type": space["type"],
				}
				if links, ok := space["_links"].(map[string]any); ok {
					s["webui"] = links["webui"]
				}
				spaces = append(spaces, s)
			}
		}
		simplified["spaces"] = spaces
		simplified["count"] = len(spaces)
	}

	return mcp.JSONResult(simplified)
}

func (s *confluenceServer) handleGetSpace(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	spaceKey := v.Required("space_key")
	expand := v.String("expand", "description.plain,homepage")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("expand", expand)

	result, err := s.request(ctx, "GET", "/wiki/rest/api/space/"+spaceKey+"?"+params.Encode(), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	simplified := map[string]any{
		"ok":   true,
		"key":  result["key"],
		"name": result["name"],
		"type": result["type"],
	}

	if desc, ok := result["description"].(map[string]any); ok {
		if plain, ok := desc["plain"].(map[string]any); ok {
			simplified["description"] = plain["value"]
		}
	}

	if homepage, ok := result["homepage"].(map[string]any); ok {
		simplified["homepage_id"] = homepage["id"]
		simplified["homepage_title"] = homepage["title"]
	}

	if links, ok := result["_links"].(map[string]any); ok {
		simplified["webui"] = links["webui"]
	}

	return mcp.JSONResult(simplified)
}

func (s *confluenceServer) handleListPages(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	spaceKey := v.Required("space_key")
	limit := v.IntRange("limit", 25, 1, 100)
	start := v.Int("start", 0)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("spaceKey", spaceKey)
	params.Set("type", "page")
	params.Set("limit", fmt.Sprintf("%d", limit))
	params.Set("start", fmt.Sprintf("%d", start))

	result, err := s.request(ctx, "GET", "/wiki/rest/api/content?"+params.Encode(), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(s.simplifySearchResults(result))
}

func (s *confluenceServer) handleGetChildren(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	pageID := v.Required("page_id")
	limit := v.IntRange("limit", 25, 1, 100)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("limit", fmt.Sprintf("%d", limit))

	result, err := s.request(ctx, "GET", "/wiki/rest/api/content/"+pageID+"/child/page?"+params.Encode(), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(s.simplifySearchResults(result))
}

func (s *confluenceServer) handleGetAncestors(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	pageID := v.Required("page_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := s.request(ctx, "GET", "/wiki/rest/api/content/"+pageID+"?expand=ancestors", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	simplified := map[string]any{
		"ok":      true,
		"page_id": pageID,
	}

	if ancestors, ok := result["ancestors"].([]any); ok {
		ancestorList := make([]map[string]any, 0, len(ancestors))
		for _, a := range ancestors {
			if ancestor, ok := a.(map[string]any); ok {
				ancestorList = append(ancestorList, map[string]any{
					"id":    ancestor["id"],
					"title": ancestor["title"],
				})
			}
		}
		simplified["ancestors"] = ancestorList
		simplified["count"] = len(ancestorList)
	}

	return mcp.JSONResult(simplified)
}

func (s *confluenceServer) handleCreatePage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	spaceKey := v.Required("space_key")
	title := v.Required("title")
	content := v.Required("content")
	parentID := v.String("parent_id", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	body := map[string]any{
		"type":  "page",
		"title": title,
		"space": map[string]any{
			"key": spaceKey,
		},
		"body": map[string]any{
			"storage": map[string]any{
				"value":          content,
				"representation": "storage",
			},
		},
	}

	if parentID != "" {
		body["ancestors"] = []map[string]any{
			{"id": parentID},
		}
	}

	result, err := s.request(ctx, "POST", "/wiki/rest/api/content", body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	simplified := map[string]any{
		"ok":      true,
		"created": true,
		"id":      result["id"],
		"title":   result["title"],
	}

	if links, ok := result["_links"].(map[string]any); ok {
		simplified["webui"] = links["webui"]
	}

	return mcp.JSONResult(simplified)
}

func (s *confluenceServer) handleUpdatePage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	pageID := v.Required("page_id")
	content := v.Required("content")
	title := v.String("title", "")
	versionMessage := v.String("version_message", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// First, get the current page to get version number and title
	currentPage, err := s.request(ctx, "GET", "/wiki/rest/api/content/"+pageID+"?expand=version", nil)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to get current page: %w", err)), nil
	}

	currentVersion := 1
	if version, ok := currentPage["version"].(map[string]any); ok {
		if num, ok := version["number"].(float64); ok {
			currentVersion = int(num)
		}
	}

	if title == "" {
		if t, ok := currentPage["title"].(string); ok {
			title = t
		}
	}

	body := map[string]any{
		"type":  "page",
		"title": title,
		"body": map[string]any{
			"storage": map[string]any{
				"value":          content,
				"representation": "storage",
			},
		},
		"version": map[string]any{
			"number": currentVersion + 1,
		},
	}

	if versionMessage != "" {
		body["version"].(map[string]any)["message"] = versionMessage
	}

	result, err := s.request(ctx, "PUT", "/wiki/rest/api/content/"+pageID, body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	simplified := map[string]any{
		"ok":      true,
		"updated": true,
		"id":      result["id"],
		"title":   result["title"],
		"version": currentVersion + 1,
	}

	if links, ok := result["_links"].(map[string]any); ok {
		simplified["webui"] = links["webui"]
	}

	return mcp.JSONResult(simplified)
}
