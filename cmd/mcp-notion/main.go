// mcp-notion provides MCP tools for Notion pages and databases.
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
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var (
	version = "0.1.0"

	notionAPIKey = os.Getenv("NOTION_API_KEY")
	notionURL    = "https://api.notion.com/v1"

	httpClient = &http.Client{
		Timeout: 30 * time.Second,
	}
)

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

	server := mcp.NewServer("mcp-notion", version)
	server.SetInstructions("Notion pages and databases tools. Configure with NOTION_API_KEY (integration token).")

	// Search
	server.AddTool(mcp.Tool{
		Name:        "notion_search",
		Description: "Search pages and databases by title",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
				"filter": map[string]any{
					"type":        "string",
					"description": "Filter by object type: page, database",
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "Number of results (default: 25, max: 100)",
				},
			},
		},
	}, handleSearch)

	// Pages
	server.AddTool(mcp.Tool{
		Name:        "notion_get_page",
		Description: "Get a page by ID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"page_id": map[string]any{
					"type":        "string",
					"description": "Page ID (UUID format, with or without hyphens)",
				},
			},
			Required: []string{"page_id"},
		},
	}, handleGetPage)

	server.AddTool(mcp.Tool{
		Name:        "notion_get_page_content",
		Description: "Get the content blocks of a page",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"block_id": map[string]any{
					"type":        "string",
					"description": "Page or block ID to get children of",
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "Number of blocks to return (default: 100)",
				},
			},
			Required: []string{"block_id"},
		},
	}, handleGetPageContent)

	// Databases
	server.AddTool(mcp.Tool{
		Name:        "notion_get_database",
		Description: "Get a database schema by ID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database_id": map[string]any{
					"type":        "string",
					"description": "Database ID",
				},
			},
			Required: []string{"database_id"},
		},
	}, handleGetDatabase)

	server.AddTool(mcp.Tool{
		Name:        "notion_query_database",
		Description: "Query a database with optional filters and sorts",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database_id": map[string]any{
					"type":        "string",
					"description": "Database ID",
				},
				"filter": map[string]any{
					"type":        "object",
					"description": "Filter object (Notion filter format)",
				},
				"sorts": map[string]any{
					"type":        "array",
					"description": "Array of sort objects",
					"items":       map[string]any{"type": "object"},
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "Number of results (default: 100)",
				},
			},
			Required: []string{"database_id"},
		},
	}, handleQueryDatabase)

	// Blocks
	server.AddTool(mcp.Tool{
		Name:        "notion_get_block",
		Description: "Get a block by ID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"block_id": map[string]any{
					"type":        "string",
					"description": "Block ID",
				},
			},
			Required: []string{"block_id"},
		},
	}, handleGetBlock)

	server.AddTool(mcp.Tool{
		Name:        "notion_get_block_children",
		Description: "Get children blocks of a block",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"block_id": map[string]any{
					"type":        "string",
					"description": "Block ID",
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "Number of blocks to return",
				},
			},
			Required: []string{"block_id"},
		},
	}, handleGetBlockChildren)

	// Users
	server.AddTool(mcp.Tool{
		Name:        "notion_list_users",
		Description: "List all users in the workspace",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleListUsers)

	server.AddTool(mcp.Tool{
		Name:        "notion_get_user",
		Description: "Get a user by ID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"user_id": map[string]any{
					"type":        "string",
					"description": "User ID",
				},
			},
			Required: []string{"user_id"},
		},
	}, handleGetUser)

	server.AddTool(mcp.Tool{
		Name:        "notion_me",
		Description: "Get the bot user (integration owner)",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleMe)

	// Comments
	server.AddTool(mcp.Tool{
		Name:        "notion_list_comments",
		Description: "List comments on a page or block",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"block_id": map[string]any{
					"type":        "string",
					"description": "Page or block ID",
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "Number of comments to return",
				},
			},
			Required: []string{"block_id"},
		},
	}, handleListComments)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

// notionRequest makes an authenticated request to Notion API
func notionRequest(ctx context.Context, method, path string, body any) (map[string]any, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, notionURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+notionAPIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", "2022-06-28")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp map[string]any
		if json.Unmarshal(respBody, &errResp) == nil {
			if msg, ok := errResp["message"].(string); ok {
				return nil, fmt.Errorf("notion API error (%d): %s", resp.StatusCode, msg)
			}
		}
		return nil, fmt.Errorf("notion API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
	}

	return result, nil
}

func handleSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	body := map[string]any{}

	if query, ok := args["query"].(string); ok && query != "" {
		body["query"] = query
	}
	if filter, ok := args["filter"].(string); ok && filter != "" {
		body["filter"] = map[string]any{
			"value":    filter,
			"property": "object",
		}
	}
	if pageSize, ok := args["page_size"].(float64); ok {
		body["page_size"] = int(pageSize)
	} else {
		body["page_size"] = 25
	}

	result, err := notionRequest(ctx, "POST", "/search", body)
	if err != nil {
		return nil, err
	}

	// Format results
	results := []map[string]any{}
	if items, ok := result["results"].([]any); ok {
		for _, item := range items {
			if obj, ok := item.(map[string]any); ok {
				results = append(results, formatObject(obj))
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"results":  results,
		"count":    len(results),
		"has_more": result["has_more"],
	})
}

func handleGetPage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	pageID, ok := args["page_id"].(string)
	if !ok || pageID == "" {
		return mcp.ErrorResult(fmt.Errorf("page_id is required")), nil
	}

	result, err := notionRequest(ctx, "GET", "/pages/"+pageID, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(formatObject(result))
}

func handleGetPageContent(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	blockID, ok := args["block_id"].(string)
	if !ok || blockID == "" {
		return mcp.ErrorResult(fmt.Errorf("block_id is required")), nil
	}

	path := "/blocks/" + blockID + "/children"
	if pageSize, ok := args["page_size"].(float64); ok {
		path += fmt.Sprintf("?page_size=%d", int(pageSize))
	}

	result, err := notionRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	// Format blocks
	blocks := []map[string]any{}
	if items, ok := result["results"].([]any); ok {
		for _, item := range items {
			if block, ok := item.(map[string]any); ok {
				blocks = append(blocks, formatBlock(block))
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"blocks":   blocks,
		"count":    len(blocks),
		"has_more": result["has_more"],
	})
}

func handleGetDatabase(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	databaseID, ok := args["database_id"].(string)
	if !ok || databaseID == "" {
		return mcp.ErrorResult(fmt.Errorf("database_id is required")), nil
	}

	result, err := notionRequest(ctx, "GET", "/databases/"+databaseID, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(formatObject(result))
}

func handleQueryDatabase(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	databaseID, ok := args["database_id"].(string)
	if !ok || databaseID == "" {
		return mcp.ErrorResult(fmt.Errorf("database_id is required")), nil
	}

	body := map[string]any{}
	if filter, ok := args["filter"].(map[string]any); ok {
		body["filter"] = filter
	}
	if sorts, ok := args["sorts"].([]any); ok {
		body["sorts"] = sorts
	}
	if pageSize, ok := args["page_size"].(float64); ok {
		body["page_size"] = int(pageSize)
	} else {
		body["page_size"] = 100
	}

	result, err := notionRequest(ctx, "POST", "/databases/"+databaseID+"/query", body)
	if err != nil {
		return nil, err
	}

	// Format results
	results := []map[string]any{}
	if items, ok := result["results"].([]any); ok {
		for _, item := range items {
			if obj, ok := item.(map[string]any); ok {
				results = append(results, formatDatabaseItem(obj))
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"results":  results,
		"count":    len(results),
		"has_more": result["has_more"],
	})
}

func handleGetBlock(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	blockID, ok := args["block_id"].(string)
	if !ok || blockID == "" {
		return mcp.ErrorResult(fmt.Errorf("block_id is required")), nil
	}

	result, err := notionRequest(ctx, "GET", "/blocks/"+blockID, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(formatBlock(result))
}

func handleGetBlockChildren(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	blockID, ok := args["block_id"].(string)
	if !ok || blockID == "" {
		return mcp.ErrorResult(fmt.Errorf("block_id is required")), nil
	}

	path := "/blocks/" + blockID + "/children"
	if pageSize, ok := args["page_size"].(float64); ok {
		path += fmt.Sprintf("?page_size=%d", int(pageSize))
	}

	result, err := notionRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	blocks := []map[string]any{}
	if items, ok := result["results"].([]any); ok {
		for _, item := range items {
			if block, ok := item.(map[string]any); ok {
				blocks = append(blocks, formatBlock(block))
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"blocks":   blocks,
		"count":    len(blocks),
		"has_more": result["has_more"],
	})
}

func handleListUsers(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := notionRequest(ctx, "GET", "/users", nil)
	if err != nil {
		return nil, err
	}

	users := []map[string]any{}
	if items, ok := result["results"].([]any); ok {
		for _, item := range items {
			if user, ok := item.(map[string]any); ok {
				users = append(users, formatUser(user))
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"users": users,
		"count": len(users),
	})
}

func handleGetUser(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	userID, ok := args["user_id"].(string)
	if !ok || userID == "" {
		return mcp.ErrorResult(fmt.Errorf("user_id is required")), nil
	}

	result, err := notionRequest(ctx, "GET", "/users/"+userID, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(formatUser(result))
}

func handleMe(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := notionRequest(ctx, "GET", "/users/me", nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(formatUser(result))
}

func handleListComments(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	blockID, ok := args["block_id"].(string)
	if !ok || blockID == "" {
		return mcp.ErrorResult(fmt.Errorf("block_id is required")), nil
	}

	path := "/comments?block_id=" + blockID
	if pageSize, ok := args["page_size"].(float64); ok {
		path += fmt.Sprintf("&page_size=%d", int(pageSize))
	}

	result, err := notionRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	comments := []map[string]any{}
	if items, ok := result["results"].([]any); ok {
		for _, item := range items {
			if comment, ok := item.(map[string]any); ok {
				comments = append(comments, formatComment(comment))
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"comments": comments,
		"count":    len(comments),
		"has_more": result["has_more"],
	})
}

// Helper functions

func formatObject(obj map[string]any) map[string]any {
	objType, _ := obj["object"].(string)

	formatted := map[string]any{
		"id":     obj["id"],
		"object": objType,
		"url":    obj["url"],
	}

	switch objType {
	case "page":
		formatted["title"] = extractTitle(obj)
		formatted["parent"] = formatParent(obj["parent"])
		formatted["created_time"] = obj["created_time"]
		formatted["last_edited_time"] = obj["last_edited_time"]
		if archived, ok := obj["archived"].(bool); ok {
			formatted["archived"] = archived
		}
	case "database":
		formatted["title"] = extractDatabaseTitle(obj)
		formatted["description"] = extractRichText(obj["description"])
		if props, ok := obj["properties"].(map[string]any); ok {
			propList := []map[string]any{}
			for name, prop := range props {
				if propObj, ok := prop.(map[string]any); ok {
					propList = append(propList, map[string]any{
						"name": name,
						"type": propObj["type"],
					})
				}
			}
			formatted["properties"] = propList
		}
	}

	return formatted
}

func formatDatabaseItem(obj map[string]any) map[string]any {
	formatted := map[string]any{
		"id":               obj["id"],
		"url":              obj["url"],
		"created_time":     obj["created_time"],
		"last_edited_time": obj["last_edited_time"],
	}

	if props, ok := obj["properties"].(map[string]any); ok {
		formattedProps := map[string]any{}
		for name, prop := range props {
			if propObj, ok := prop.(map[string]any); ok {
				formattedProps[name] = extractPropertyValue(propObj)
			}
		}
		formatted["properties"] = formattedProps
	}

	return formatted
}

func formatBlock(block map[string]any) map[string]any {
	blockType, _ := block["type"].(string)

	formatted := map[string]any{
		"id":           block["id"],
		"type":         blockType,
		"has_children": block["has_children"],
	}

	// Extract block content
	if content, ok := block[blockType].(map[string]any); ok {
		if richText, ok := content["rich_text"].([]any); ok {
			formatted["text"] = extractRichTextArray(richText)
		}
		if url, ok := content["url"].(string); ok {
			formatted["url"] = url
		}
		if caption, ok := content["caption"].([]any); ok && len(caption) > 0 {
			formatted["caption"] = extractRichTextArray(caption)
		}
		if language, ok := content["language"].(string); ok {
			formatted["language"] = language
		}
		if checked, ok := content["checked"].(bool); ok {
			formatted["checked"] = checked
		}
	}

	return formatted
}

func formatUser(user map[string]any) map[string]any {
	formatted := map[string]any{
		"id":     user["id"],
		"object": user["object"],
		"type":   user["type"],
		"name":   user["name"],
	}

	if avatarURL, ok := user["avatar_url"].(string); ok && avatarURL != "" {
		formatted["avatar_url"] = avatarURL
	}

	if person, ok := user["person"].(map[string]any); ok {
		if email, ok := person["email"].(string); ok {
			formatted["email"] = email
		}
	}

	if bot, ok := user["bot"].(map[string]any); ok {
		if owner, ok := bot["owner"].(map[string]any); ok {
			formatted["owner"] = owner["type"]
		}
	}

	return formatted
}

func formatComment(comment map[string]any) map[string]any {
	formatted := map[string]any{
		"id":           comment["id"],
		"created_time": comment["created_time"],
	}

	if richText, ok := comment["rich_text"].([]any); ok {
		formatted["text"] = extractRichTextArray(richText)
	}

	if createdBy, ok := comment["created_by"].(map[string]any); ok {
		formatted["created_by"] = map[string]any{
			"id":   createdBy["id"],
			"name": createdBy["name"],
		}
	}

	return formatted
}

func formatParent(parent any) map[string]any {
	if p, ok := parent.(map[string]any); ok {
		parentType, _ := p["type"].(string)
		result := map[string]any{"type": parentType}
		switch parentType {
		case "database_id":
			result["database_id"] = p["database_id"]
		case "page_id":
			result["page_id"] = p["page_id"]
		case "workspace":
			result["workspace"] = true
		}
		return result
	}
	return nil
}

func extractTitle(page map[string]any) string {
	if props, ok := page["properties"].(map[string]any); ok {
		for _, prop := range props {
			if propObj, ok := prop.(map[string]any); ok {
				if propObj["type"] == "title" {
					if title, ok := propObj["title"].([]any); ok {
						return extractRichTextArray(title)
					}
				}
			}
		}
	}
	return ""
}

func extractDatabaseTitle(db map[string]any) string {
	if title, ok := db["title"].([]any); ok {
		return extractRichTextArray(title)
	}
	return ""
}

func extractRichText(richText any) string {
	if rt, ok := richText.([]any); ok {
		return extractRichTextArray(rt)
	}
	return ""
}

func extractRichTextArray(richText []any) string {
	result := ""
	for _, item := range richText {
		if textObj, ok := item.(map[string]any); ok {
			if plainText, ok := textObj["plain_text"].(string); ok {
				result += plainText
			}
		}
	}
	return result
}

func extractPropertyValue(prop map[string]any) any {
	propType, _ := prop["type"].(string)

	switch propType {
	case "title", "rich_text":
		if arr, ok := prop[propType].([]any); ok {
			return extractRichTextArray(arr)
		}
	case "number":
		return prop["number"]
	case "select":
		if sel, ok := prop["select"].(map[string]any); ok {
			return sel["name"]
		}
	case "multi_select":
		if ms, ok := prop["multi_select"].([]any); ok {
			names := []string{}
			for _, item := range ms {
				if opt, ok := item.(map[string]any); ok {
					if name, ok := opt["name"].(string); ok {
						names = append(names, name)
					}
				}
			}
			return names
		}
	case "date":
		if date, ok := prop["date"].(map[string]any); ok {
			return date["start"]
		}
	case "checkbox":
		return prop["checkbox"]
	case "url":
		return prop["url"]
	case "email":
		return prop["email"]
	case "phone_number":
		return prop["phone_number"]
	case "status":
		if status, ok := prop["status"].(map[string]any); ok {
			return status["name"]
		}
	case "people":
		if people, ok := prop["people"].([]any); ok {
			names := []string{}
			for _, p := range people {
				if person, ok := p.(map[string]any); ok {
					if name, ok := person["name"].(string); ok {
						names = append(names, name)
					}
				}
			}
			return names
		}
	case "relation":
		if relations, ok := prop["relation"].([]any); ok {
			ids := []string{}
			for _, r := range relations {
				if rel, ok := r.(map[string]any); ok {
					if id, ok := rel["id"].(string); ok {
						ids = append(ids, id)
					}
				}
			}
			return ids
		}
	case "formula":
		if formula, ok := prop["formula"].(map[string]any); ok {
			fType, _ := formula["type"].(string)
			return formula[fType]
		}
	case "rollup":
		if rollup, ok := prop["rollup"].(map[string]any); ok {
			rType, _ := rollup["type"].(string)
			return rollup[rType]
		}
	}

	return nil
}
