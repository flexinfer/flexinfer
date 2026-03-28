package daemon

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/router"
)

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
