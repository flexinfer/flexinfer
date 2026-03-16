package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	gosync "sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/router"
	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/templatevars"
)

// toolsResult holds the aggregated tools response.
type toolsResult struct {
	Tools       []mcp.Tool `json:"tools"`
	CachedAt    time.Time  `json:"cachedAt"`
	ServerCount int        `json:"serverCount"`
}

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

// resourcesResult holds the aggregated resources response.
type resourcesResult struct {
	Resources          []mcp.Resource `json:"resources"`
	CachedAt           time.Time      `json:"cachedAt"`
	ServerCount        int            `json:"serverCount"`
	RunningServerCount int            `json:"runningServerCount"`
}

func daemonBuiltInResources() []mcp.Resource {
	return []mcp.Resource{
		{
			URI:         "loom://servers",
			Name:        "Loom servers",
			Description: "List MCP servers managed by the loom daemon",
			MimeType:    "application/json",
		},
		{
			URI:         "loom://tools",
			Name:        "Loom tools",
			Description: "Cached aggregated tools from loom daemon",
			MimeType:    "application/json",
		},
		{
			URI:         "loom://health",
			Name:        "Loom health",
			Description: "Health summary for all servers (local/hub) managed by loom",
			MimeType:    "application/json",
		},
		{
			URI:         "loom://config",
			Name:        "Loom config",
			Description: "Active profile and daemon configuration summary",
			MimeType:    "application/json",
		},
	}
}

func (d *Daemon) handleResources(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	serverCount := 0
	if d.registry != nil {
		serverCount = len(d.registry.Servers)
	}

	var runningServers []string
	if d.procMgr != nil {
		runningServers = d.procMgr.List()
	}
	runningSet := make(map[string]struct{}, len(runningServers))
	for _, serverName := range runningServers {
		runningSet[serverName] = struct{}{}
	}

	// Always return cached resources immediately if available (even if stale).
	d.resourceCache.mu.RLock()
	hasCache := len(d.resourceCache.resources) > 0
	cacheStale := time.Since(d.resourceCache.updatedAt) >= d.resourceCache.ttl
	cachedResources := d.resourceCache.resources
	cachedAt := d.resourceCache.updatedAt
	d.resourceCache.mu.RUnlock()

	if hasCache {
		if cacheStale {
			go func() {
				bgCtx := context.Background()
				_, _ = d.refreshResourcesCacheDeduplicated(bgCtx)
			}()
		}
		return mcp.NewResponse(msg.ID, resourcesResult{
			Resources:          cachedResources,
			CachedAt:           cachedAt,
			ServerCount:        serverCount,
			RunningServerCount: len(runningSet),
		})
	}

	// No cache yet: return built-ins immediately and refresh asynchronously.
	builtins := daemonBuiltInResources()
	now := time.Now()
	d.resourceCache.mu.Lock()
	d.resourceCache.resources = builtins
	d.resourceCache.updatedAt = now
	d.resourceCache.mu.Unlock()

	go func() {
		bgCtx := context.Background()
		_, _ = d.refreshResourcesCacheDeduplicated(bgCtx)
	}()

	return mcp.NewResponse(msg.ID, resourcesResult{
		Resources:          builtins,
		CachedAt:           now,
		ServerCount:        serverCount,
		RunningServerCount: len(runningSet),
	})
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
			Tools:       visibleTools(cachedTools),
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
			Tools:       visibleTools(staticTools),
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
		Tools:       visibleTools(tools),
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

func normalizeServerFilters(servers []string) []string {
	if len(servers) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(servers))
	out := make([]string, 0, len(servers))
	for _, raw := range servers {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func splitNamespacedToolName(name string) (server, tool string) {
	parts := strings.SplitN(strings.TrimSpace(name), "__", 2)
	if len(parts) != 2 {
		return "", strings.TrimSpace(name)
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func containsServerFilter(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

// refreshResourcesCacheDeduplicated wraps refreshResourcesCache via singleflight to
// prevent redundant concurrent refreshes.
func (d *Daemon) refreshResourcesCacheDeduplicated(ctx context.Context) ([]mcp.Resource, error) {
	v, err, _ := d.refreshGroup.Do("resources-refresh", func() (any, error) {
		return d.refreshResourcesCache(ctx)
	})
	if err != nil {
		return nil, err
	}
	return v.([]mcp.Resource), nil
}

// refreshResourcesCache fetches resources from currently running servers and updates the cache.
func (d *Daemon) refreshResourcesCache(ctx context.Context) ([]mcp.Resource, error) {
	refreshConcurrency := d.fileCfg.Resources.GetRefreshConcurrency()
	if refreshConcurrency <= 0 {
		refreshConcurrency = 1
	}

	base := daemonBuiltInResources()
	var running []string
	if d.procMgr != nil {
		running = d.procMgr.List()
	}
	runningSet := make(map[string]struct{}, len(running))
	for _, serverName := range running {
		runningSet[serverName] = struct{}{}
	}

	if len(runningSet) == 0 {
		d.resourceCache.mu.Lock()
		d.resourceCache.resources = base
		d.resourceCache.updatedAt = time.Now()
		d.resourceCache.mu.Unlock()
		return base, nil
	}

	if d.registry == nil {
		d.resourceCache.mu.Lock()
		d.resourceCache.resources = base
		d.resourceCache.updatedAt = time.Now()
		d.resourceCache.mu.Unlock()
		return base, nil
	}

	serverResources := make(map[string][]mcp.Resource, len(runningSet))
	var (
		mu  gosync.Mutex
		wg  gosync.WaitGroup
		sem = make(chan struct{}, refreshConcurrency)
	)

	for _, server := range d.registry.Servers {
		if server == nil {
			continue
		}
		if _, ok := runningSet[server.Name]; !ok {
			continue
		}

		wg.Add(1)
		go func(serverName string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			resources, err := d.fetchServerResources(callCtx, serverName)
			if err != nil {
				d.logger.Debug("resources probe failed", "server", serverName, "error", err)
				return
			}
			if len(resources) == 0 {
				return
			}
			mu.Lock()
			serverResources[serverName] = resources
			mu.Unlock()
		}(server.Name)
	}
	wg.Wait()

	// Keep output deterministic by walking servers in registry order.
	allResources := make([]mcp.Resource, 0, len(base)+len(serverResources)*4)
	allResources = append(allResources, base...)
	for _, server := range d.registry.Servers {
		if server == nil {
			continue
		}
		resources, ok := serverResources[server.Name]
		if !ok {
			continue
		}
		for _, r := range resources {
			r.URI = server.Name + "__" + r.URI
			allResources = append(allResources, r)
		}
	}

	d.resourceCache.mu.Lock()
	oldResources := d.resourceCache.resources
	d.resourceCache.resources = allResources
	d.resourceCache.updatedAt = time.Now()
	d.resourceCache.mu.Unlock()

	if resourceNamesChanged(oldResources, allResources) && d.eventBus != nil {
		d.eventBus.Publish(EventResourcesChanged, map[string]any{
			"old_count": len(oldResources),
			"new_count": len(allResources),
		})
	}

	return allResources, nil
}

func (d *Daemon) fetchServerResources(ctx context.Context, serverName string) ([]mcp.Resource, error) {
	if d.pool == nil {
		return nil, fmt.Errorf("local pool not configured")
	}

	// Acquire callLock BEFORE pool.Get to match callPipeline.routeAndConnect
	// ordering. Reversed ordering (pool→lock) can deadlock against the
	// callPipeline path (lock→pool) when the pool is at capacity.
	mu, _, err := d.acquireCallLock(ctx, serverName)
	if err != nil {
		return nil, fmt.Errorf("acquire call lock: %w", err)
	}
	defer mu.Unlock()

	conn, err := d.pool.Get(ctx, serverName)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer d.pool.Put(conn)

	req, _ := mcp.NewRequest(1, "resources/list", nil)
	if err := conn.Transport.Send(ctx, req); err != nil {
		conn.Healthy = false
		return nil, fmt.Errorf("send resources/list: %w", err)
	}

	resp, err := conn.Transport.Recv(ctx)
	if err != nil {
		conn.Healthy = false
		return nil, fmt.Errorf("recv resources/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("server error: %s", resp.Error.Message)
	}

	var result struct {
		Resources []mcp.Resource `json:"resources"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse resources/list: %w", err)
	}

	return result.Resources, nil
}

// refreshToolCacheDeduplicated wraps refreshToolCache via singleflight to prevent
// redundant concurrent refreshes. Multiple callers get the same result.
func (d *Daemon) refreshToolCacheDeduplicated(ctx context.Context) ([]mcp.Tool, error) {
	v, err, _ := d.refreshGroup.Do("refresh", func() (any, error) {
		return d.refreshToolCache(ctx)
	})
	if err != nil {
		return nil, err
	}
	return v.([]mcp.Tool), nil
}

// refreshToolCache fetches tools from all servers concurrently and updates the cache.
func (d *Daemon) refreshToolCache(ctx context.Context) ([]mcp.Tool, error) {
	refreshConcurrency := d.fileCfg.Resources.GetRefreshConcurrency()

	// Build a unified list of sources (local + hub) and fetch them with bounded concurrency.
	sources := make([]toolSource, 0, len(d.registry.Servers)+20)
	for _, server := range d.registry.Servers {
		sources = append(sources, toolSource{name: server.Name, kind: toolSourceLocal})
	}

	var hubClient *router.HubClient
	if d.cfg.HubFallback && d.hubClient != nil {
		now := time.Now()
		if d.hubAuthDisabled {
			d.logger.Debug("hub fallback disabled after auth-gated discovery failure")
		} else if !d.hubAuthBackoffUntil.IsZero() && now.Before(d.hubAuthBackoffUntil) {
			d.logger.Debug("skipping hub discovery during auth backoff", "until", d.hubAuthBackoffUntil)
		} else {
			// Fetch token from secret store if needed.
			token := d.expandVars("${secret:MCP_HUB_TOKEN}")
			if token == "" {
				token = os.Getenv("MCP_HUB_TOKEN")
			}

			hubClient = router.NewHubClientWithCFAccess(
				d.cfg.HubURL, token,
				d.fileCfg.Hub.CFAccessClientID,
				d.fileCfg.Hub.CFAccessClientSecret,
			)
			hostNames, err := hubClient.DiscoverHosts(ctx)
			if err != nil {
				if isHubAuthError(err) {
					hint := "check MCP_HUB_TOKEN or Cloudflare Access credentials, or set hub.disable_on_auth_failure"
					if d.fileCfg.Hub.DisableOnAuthFailure {
						d.hubAuthDisabled = true
						d.logger.Warn("hub discovery auth required; disabling hub fallback", "error", err, "hint", hint)
					} else {
						d.hubAuthBackoffUntil = now.Add(hubAuthBackoff)
						d.logger.Warn("hub discovery auth required; backing off", "until", d.hubAuthBackoffUntil, "error", err, "hint", hint)
					}
				} else {
					d.logger.Warn("failed to discover hub hosts", "error", err)
				}
				hubClient = nil
			} else {
				d.hubAuthBackoffUntil = time.Time{}
				for _, host := range hostNames {
					// Avoid shadowing local servers if they have the same name.
					isLocal := false
					for _, s := range d.registry.Servers {
						if s.Name == host {
							isLocal = true
							break
						}
					}
					if isLocal {
						continue
					}
					sources = append(sources, toolSource{name: host, kind: toolSourceHub})
				}
			}
		}
	}

	d.logger.Info("refreshing tool cache", "sources", len(sources), "local", len(d.registry.Servers))

	results := fetchToolsBounded(ctx, sources, refreshConcurrency, func(ctx context.Context, src toolSource) ([]mcp.Tool, error) {
		switch src.kind {
		case toolSourceHub:
			if hubClient == nil {
				return nil, fmt.Errorf("hub client unavailable")
			}
			return hubClient.FetchTools(ctx, src.name)
		default:
			return d.fetchServerTools(ctx, src.name)
		}
	})

	// Aggregate results
	var allTools []mcp.Tool
	successCount := 0

	// Helper to sanitize tool names
	sanitize := func(s string) string {
		// Replace dots with underscores
		s = strings.ReplaceAll(s, ".", "_")
		// Remove any other invalid characters (keep alphanumeric, _, -)
		var b strings.Builder
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				b.WriteRune(r)
			}
		}
		res := b.String()
		// Truncate to 64 chars
		if len(res) > 64 {
			res = res[:64]
		}
		return res
	}

	for _, result := range results {
		if result.err != nil {
			d.logger.Debug("failed to get tools from server", "server", result.name, "error", result.err)
			continue
		}
		successCount++

		// Namespace tools with server prefix and enhance descriptions
		var namespacedTools []mcp.Tool
		for _, tool := range result.tools {
			originalToolName := tool.Name

			// Add to router index for smart routing (prefix-less calls)
			d.router.AddToolToIndex(originalToolName, result.name)

			// Sanitize the original tool name first
			safeToolName := sanitize(tool.Name)
			// Create namespaced name
			namespacedName := result.name + "__" + safeToolName
			// Sanitize again just in case server name had issues (though registry should be clean)
			tool.Name = sanitize(namespacedName)

			// Enhance description with metadata if available
			if d.metadata != nil && d.fileCfg.Context.EnrichDescriptions {
				tool.Description = d.metadata.EnhanceDescription(result.name, originalToolName, tool.Description)
			}

			namespacedTools = append(namespacedTools, tool)
			allTools = append(allTools, tool)
		}

		// Update manifest with this server's tools
		d.manifest.UpdateServerTools(result.name, namespacedTools)
	}

	// Apply profile filtering
	activeProfile := d.fileCfg.Context.ActiveProfile
	if activeProfile == "" {
		activeProfile = "full"
	}

	filterResult := d.profiles.Filter(allTools, activeProfile)
	if filterResult.Truncated {
		d.logger.Warn("tools truncated by profile",
			"profile", activeProfile,
			"before", filterResult.TotalBefore,
			"after", filterResult.TotalAfter)
	}
	allTools = filterResult.Tools

	d.logger.Info("tool cache refreshed",
		"profile", activeProfile,
		"total_tools", len(allTools),
		"servers_succeeded", successCount,
		"servers_total", len(d.registry.Servers))

	// Update cache, detecting changes for notification.
	d.toolCache.mu.Lock()
	oldTools := d.toolCache.tools
	d.toolCache.tools = allTools
	d.toolCache.updatedAt = time.Now()
	d.toolCache.mu.Unlock()

	if toolNamesChanged(oldTools, allTools) && d.eventBus != nil {
		d.eventBus.Publish(EventToolsChanged, map[string]any{
			"old_count": len(oldTools),
			"new_count": len(allTools),
		})
	}

	// Update metrics
	d.metrics.RecordToolCacheRefresh()
	d.metrics.UpdateToolCache(len(allTools), 0)
	d.metrics.UpdateProcessCount(len(d.procMgr.List()))

	// Persist manifest in background
	go func() {
		if err := d.manifest.Save(); err != nil {
			d.logger.Warn("failed to save manifest", "error", err)
		}
	}()

	return allTools, nil
}

// fetchServerTools gets tools from a single server using its own dedicated process.
func (d *Daemon) fetchServerTools(ctx context.Context, serverName string) ([]mcp.Tool, error) {
	// Get server spec
	spec, err := d.registry.GetServerSpec(serverName, d.cfg.Target)
	if err != nil {
		return nil, fmt.Errorf("get server spec: %w", err)
	}

	if spec.Command == "" {
		return nil, fmt.Errorf("no command defined")
	}

	// Create timeout context - use shorter timeout to fail fast
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Expand variables in command
	command := d.expandVars(spec.Command)

	// Build command
	args := make([]string, len(spec.Args))
	for i, arg := range spec.Args {
		args[i] = d.expandVars(fmt.Sprint(arg))
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = os.Environ()
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, d.expandVars(v)))
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start: %w", err)
	}
	defer func() {
		stdin.Close()
		stdout.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}()

	transport := mcp.NewStdioTransport(stdout, stdin)

	if err := initializeMCPTransport(ctx, transport); err != nil {
		return nil, err
	}

	// Get tools
	toolsReq, _ := mcp.NewRequest(2, "tools/list", nil)
	if err := transport.Send(ctx, toolsReq); err != nil {
		return nil, fmt.Errorf("send tools/list: %w", err)
	}
	toolsResp, err := transport.Recv(ctx)
	if err != nil {
		return nil, fmt.Errorf("recv tools/list: %w", err)
	}
	if toolsResp.Error != nil {
		return nil, fmt.Errorf("server error: %s", toolsResp.Error.Message)
	}

	var toolsList struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(toolsResp.Result, &toolsList); err != nil {
		return nil, fmt.Errorf("unmarshal tools: %w", err)
	}

	return toolsList.Tools, nil
}

// fetchServerToolsViaPool performs a tools/list health probe using the connection
// pool, reusing an existing idle connection when available. This avoids spawning a
// fresh process for every health check interval.
func (d *Daemon) fetchServerToolsViaPool(ctx context.Context, serverName string) ([]mcp.Tool, error) {
	if d.pool == nil {
		return nil, fmt.Errorf("local pool not configured")
	}

	// Acquire callLock BEFORE pool.Get to match callPipeline.routeAndConnect
	// ordering. Reversed ordering (pool→lock) can deadlock against the
	// callPipeline path (lock→pool) when the pool is at capacity.
	mu, _, err := d.acquireCallLock(ctx, serverName)
	if err != nil {
		return nil, fmt.Errorf("acquire call lock: %w", err)
	}
	defer mu.Unlock()

	conn, err := d.pool.Get(ctx, serverName)
	if err != nil {
		return nil, fmt.Errorf("pool connect: %w", err)
	}
	defer d.pool.Put(conn)

	req, _ := mcp.NewRequest(1, "tools/list", nil)
	if err := conn.Transport.Send(ctx, req); err != nil {
		conn.Healthy = false
		return nil, fmt.Errorf("send tools/list: %w", err)
	}

	resp, err := conn.Transport.Recv(ctx)
	if err != nil {
		conn.Healthy = false
		return nil, fmt.Errorf("recv tools/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("server error: %s", resp.Error.Message)
	}

	var toolsList struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &toolsList); err != nil {
		return nil, fmt.Errorf("unmarshal tools: %w", err)
	}

	return toolsList.Tools, nil
}

// initializeMCPTransport performs the MCP initialize handshake on a fresh transport.
func initializeMCPTransport(ctx context.Context, transport mcp.Transport) error {
	versions := []string{
		mcp.ProtocolVersion20250618,
		mcp.ProtocolVersion,
	}
	var lastErr error
	for _, protocolVersion := range versions {
		initReq, _ := mcp.NewRequest(1, "initialize", mcp.InitializeParams{
			ProtocolVersion: protocolVersion,
			Capabilities:    mcp.Capabilities{},
			ClientInfo:      mcp.ClientInfo{Name: "loom-daemon", Version: "0.1.0"},
		})
		if err := transport.Send(ctx, initReq); err != nil {
			lastErr = fmt.Errorf("send init (%s): %w", protocolVersion, err)
			continue
		}
		initResp, err := transport.Recv(ctx)
		if err != nil {
			lastErr = fmt.Errorf("recv init (%s): %w", protocolVersion, err)
			continue
		}
		if initResp != nil && initResp.Error != nil {
			lastErr = fmt.Errorf("init error (%s): %s", protocolVersion, initResp.Error.Message)
			continue
		}

		initNotif := &mcp.Message{JSONRPC: "2.0", Method: "notifications/initialized"}
		if err := transport.Send(ctx, initNotif); err != nil {
			return fmt.Errorf("send initialized (%s): %w", protocolVersion, err)
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("initialize failed: no protocol versions attempted")
}

// toolNamesChanged reports whether the set of tool names differs between old and new.
func toolNamesChanged(oldTools, newTools []mcp.Tool) bool {
	if len(oldTools) != len(newTools) {
		return true
	}
	old := make(map[string]bool, len(oldTools))
	for _, t := range oldTools {
		old[t.Name] = true
	}
	for _, t := range newTools {
		if !old[t.Name] {
			return true
		}
	}
	return false
}

// resourceNamesChanged reports whether the set of resource URIs differs between old and new.
func resourceNamesChanged(oldRes, newRes []mcp.Resource) bool {
	if len(oldRes) != len(newRes) {
		return true
	}
	old := make(map[string]bool, len(oldRes))
	for _, r := range oldRes {
		old[r.URI] = true
	}
	for _, r := range newRes {
		if !old[r.URI] {
			return true
		}
	}
	return false
}

// expandVarsWithRegistry expands variable patterns with registry-based env aliases.
// - ${repo}: Repository root
// - ${HOME}: User home directory
// - ${env:VAR}: Environment variable (with fallback alias support)
// - ${keychain:VAR}: Keychain reference (treated as env var for now)
func expandVarsWithRegistry(s string, repoRoot string, reg *registry.Registry) string {
	// Expand ${HOME}
	if home, err := os.UserHomeDir(); err == nil {
		s = strings.ReplaceAll(s, "${HOME}", home)
	}

	// Expand ${repo}
	if repoRoot != "" {
		s = strings.ReplaceAll(s, "${repo}", repoRoot)
	}

	// Delegate ${env:}, ${keychain:}, ${secret:} to the shared expander
	exp := templatevars.New(
		templatevars.WithRegistry(reg),
		templatevars.WithLazySecrets(),
	)
	return exp.Expand(s)
}

// expandVars expands variable patterns in strings (uses daemon's repoRoot and registry).
func (d *Daemon) expandVars(s string) string {
	return expandVarsWithRegistry(s, d.repoRoot, d.registry)
}
