package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	gosync "sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

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
	// ordering. Reversed ordering (pool->lock) can deadlock against the
	// callPipeline path (lock->pool) when the pool is at capacity.
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
