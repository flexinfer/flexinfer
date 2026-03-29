package daemon

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

const (
	toolSearchDetailName    = "name"
	toolSearchDetailSummary = "summary"
	toolSearchDetailSchema  = "schema"
	defaultToolSearchLimit  = 50
	maxToolSearchLimit      = 500
)

type toolsSearchParams struct {
	Query   string   `json:"query,omitempty"`
	Servers []string `json:"servers,omitempty"`
	Limit   int      `json:"limit,omitempty"`
	Cursor  string   `json:"cursor,omitempty"`
	Detail  string   `json:"detail,omitempty"`
}

type toolGetParams struct {
	Name   string `json:"name"`
	Server string `json:"server,omitempty"`
}

type toolsSearchItem struct {
	Name        string           `json:"name"`
	Server      string           `json:"server,omitempty"`
	Description string           `json:"description,omitempty"`
	InputSchema *mcp.InputSchema `json:"inputSchema,omitempty"`
}

type toolsSearchResult struct {
	Query       string            `json:"query,omitempty"`
	Detail      string            `json:"detail"`
	Servers     []string          `json:"servers,omitempty"`
	Limit       int               `json:"limit"`
	Cursor      string            `json:"cursor,omitempty"`
	NextCursor  string            `json:"nextCursor,omitempty"`
	Total       int               `json:"total"`
	Count       int               `json:"count"`
	Results     []toolsSearchItem `json:"results"`
	CachedAt    time.Time         `json:"cachedAt"`
	ServerCount int               `json:"serverCount"`
}

type toolGetResult struct {
	Name        string    `json:"name"`
	Server      string    `json:"server,omitempty"`
	ToolName    string    `json:"toolName,omitempty"`
	Tool        mcp.Tool  `json:"tool"`
	CachedAt    time.Time `json:"cachedAt"`
	ServerCount int       `json:"serverCount"`
	Source      string    `json:"source,omitempty"`
}

func (d *Daemon) handleTools(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	// Always return cached tools immediately if we have any (even if stale)
	d.toolCache.mu.RLock()
	hasCache := len(d.toolCache.tools) > 0
	cacheStale := time.Since(d.toolCache.updatedAt) >= d.toolCache.ttl
	cachedTools := d.toolCache.tools
	cachedAt := d.toolCache.updatedAt
	d.toolCache.mu.RUnlock()

	// If cache exists, return it immediately and refresh in background if stale
	if hasCache {
		if cacheStale {
			// Trigger background refresh (non-blocking, deduplicated)
			go func() {
				bgCtx := context.Background()
				d.refreshToolCacheDeduplicated(bgCtx)
			}()
		}
		result := toolsResult{
			Tools:       d.allVisibleTools(cachedTools),
			CachedAt:    cachedAt,
			ServerCount: len(d.registry.Servers),
		}
		d.logger.Debug("returning cached tools", "count", len(result.Tools), "stale", cacheStale)
		return mcp.NewResponse(msg.ID, result)
	}

	// No cache at all - check for static tools in registry first
	staticTools := d.getStaticToolsFromRegistry()
	if len(staticTools) > 0 {
		d.logger.Info("returning static tools from registry", "count", len(staticTools))
		// Trigger background refresh to get live tools (deduplicated)
		go func() {
			bgCtx := context.Background()
			d.refreshToolCacheDeduplicated(bgCtx)
		}()
		result := toolsResult{
			Tools:       d.allVisibleTools(staticTools),
			CachedAt:    time.Now(),
			ServerCount: len(d.registry.Servers),
		}
		return mcp.NewResponse(msg.ID, result)
	}

	// No static tools - must wait for initial refresh (with shorter timeout)
	refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	tools, err := d.refreshToolCache(refreshCtx)
	if err != nil {
		// Return empty tools rather than error - servers may still be starting
		d.logger.Warn("initial tool cache refresh failed", "error", err)
		result := toolsResult{
			Tools:       []mcp.Tool{},
			CachedAt:    time.Now(),
			ServerCount: len(d.registry.Servers),
		}
		return mcp.NewResponse(msg.ID, result)
	}

	result := toolsResult{
		Tools:       d.allVisibleTools(tools),
		CachedAt:    d.toolCache.updatedAt,
		ServerCount: len(d.registry.Servers),
	}
	return mcp.NewResponse(msg.ID, result)
}

func (d *Daemon) handleToolsSearch(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	params := toolsSearchParams{}
	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "invalid params: "+err.Error()), nil
		}
	}

	detail := strings.ToLower(strings.TrimSpace(params.Detail))
	if detail == "" {
		detail = toolSearchDetailSummary
	}
	switch detail {
	case toolSearchDetailName, toolSearchDetailSummary, toolSearchDetailSchema:
	default:
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "detail must be one of: name, summary, schema"), nil
	}

	limit := params.Limit
	if limit == 0 {
		limit = defaultToolSearchLimit
	}
	if limit < 0 {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "limit must be >= 0"), nil
	}
	if limit > maxToolSearchLimit {
		limit = maxToolSearchLimit
	}

	offset := 0
	cursor := strings.TrimSpace(params.Cursor)
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil || n < 0 {
			return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "cursor must be a non-negative integer string"), nil
		}
		offset = n
	}

	toolsResp, err := d.handleTools(ctx, &mcp.Message{ID: msg.ID})
	if err != nil {
		return nil, err
	}
	if toolsResp != nil && toolsResp.Error != nil {
		return toolsResp, nil
	}

	var snapshot toolsResult
	if err := json.Unmarshal(toolsResp.Result, &snapshot); err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, "unmarshal loom/tools response: "+err.Error()), nil
	}

	query := strings.ToLower(strings.TrimSpace(params.Query))
	servers := normalizeServerFilters(params.Servers)

	filtered := make([]mcp.Tool, 0, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		server, shortName := splitNamespacedToolName(tool.Name)
		if len(servers) > 0 && !containsServerFilter(servers, strings.ToLower(server)) {
			continue
		}
		if query != "" {
			searchTarget := strings.ToLower(strings.Join([]string{
				tool.Name,
				shortName,
				server,
				tool.Description,
			}, " "))
			if !strings.Contains(searchTarget, query) {
				continue
			}
		}
		filtered = append(filtered, tool)
	}

	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}

	paged := filtered[offset:end]
	items := make([]toolsSearchItem, 0, len(paged))
	for _, tool := range paged {
		server, _ := splitNamespacedToolName(tool.Name)
		item := toolsSearchItem{
			Name: tool.Name,
		}
		switch detail {
		case toolSearchDetailName:
			// Name-only response.
		case toolSearchDetailSummary:
			item.Server = server
			item.Description = tool.Description
		case toolSearchDetailSchema:
			item.Server = server
			item.Description = tool.Description
			schema := tool.InputSchema
			item.InputSchema = &schema
		}
		items = append(items, item)
	}

	nextCursor := ""
	if end < total {
		nextCursor = strconv.Itoa(end)
	}

	return mcp.NewResponse(msg.ID, toolsSearchResult{
		Query:       strings.TrimSpace(params.Query),
		Detail:      detail,
		Servers:     servers,
		Limit:       limit,
		Cursor:      cursor,
		NextCursor:  nextCursor,
		Total:       total,
		Count:       len(items),
		Results:     items,
		CachedAt:    snapshot.CachedAt,
		ServerCount: snapshot.ServerCount,
	})
}

func (d *Daemon) handleToolGet(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	params := toolGetParams{}
	if len(msg.Params) > 0 {
		if err := json.Unmarshal(msg.Params, &params); err != nil {
			return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "invalid params: "+err.Error()), nil
		}
	}

	name := strings.TrimSpace(params.Name)
	server := strings.TrimSpace(params.Server)
	if name == "" {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "name is required"), nil
	}

	if server != "" && !strings.Contains(name, "__") {
		name = server + "__" + name
	}

	toolsResp, err := d.handleTools(ctx, &mcp.Message{ID: msg.ID})
	if err != nil {
		return nil, err
	}
	if toolsResp != nil && toolsResp.Error != nil {
		return toolsResp, nil
	}

	var snapshot toolsResult
	if err := json.Unmarshal(toolsResp.Result, &snapshot); err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, "unmarshal loom/tools response: "+err.Error()), nil
	}

	for _, tool := range snapshot.Tools {
		if tool.Name != name {
			continue
		}
		toolServer, shortName := splitNamespacedToolName(tool.Name)
		return mcp.NewResponse(msg.ID, toolGetResult{
			Name:        tool.Name,
			Server:      toolServer,
			ToolName:    shortName,
			Tool:        tool,
			CachedAt:    snapshot.CachedAt,
			ServerCount: snapshot.ServerCount,
			Source:      "cache",
		})
	}

	return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "tool not found: "+name), nil
}

// getStaticToolsFromRegistry converts registry tool schemas to MCP tools.
func (d *Daemon) getStaticToolsFromRegistry() []mcp.Tool {
	staticSchemas := d.registry.GetStaticTools(d.cfg.Target)
	if len(staticSchemas) == 0 {
		return nil
	}

	tools := make([]mcp.Tool, len(staticSchemas))
	for i, schema := range staticSchemas {
		tools[i] = mcp.Tool{
			Name:        schema.Name,
			Description: schema.Description,
			InputSchema: mcp.InputSchema{
				Type:       schema.InputSchema.Type,
				Properties: schema.InputSchema.Properties,
				Required:   schema.InputSchema.Required,
			},
		}
	}
	return tools
}
