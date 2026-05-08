package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/registry"
)

// handleReload reloads the registry and refreshes the tool cache.
func (d *Daemon) handleReload(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if err := d.Reload(ctx); err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error()), nil
	}

	return mcp.NewResponse(msg.ID, map[string]any{
		"reloaded": true,
		"servers":  len(d.registry.Servers),
	})
}

// handleCacheStats returns response cache statistics.
func (d *Daemon) handleCacheStats(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.respCache == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"enabled": false,
		})
	}

	stats := d.respCache.Stats()
	return mcp.NewResponse(msg.ID, map[string]any{
		"enabled":    true,
		"entries":    stats.Entries,
		"size_bytes": stats.SizeBytes,
		"max_bytes":  stats.MaxBytes,
		"total_hits": stats.TotalHits,
	})
}

// handleCacheClear clears the response cache.
func (d *Daemon) handleCacheClear(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.respCache == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"cleared": false,
			"reason":  "cache not enabled",
		})
	}

	// Parse optional server parameter
	var params struct {
		Server string `json:"server,omitempty"`
	}
	if len(msg.Params) > 0 {
		json.Unmarshal(msg.Params, &params)
	}

	if params.Server != "" {
		d.respCache.ClearServer(params.Server)
		d.logger.Info("response cache cleared for server", "server", params.Server)
	} else {
		d.respCache.Clear()
		d.logger.Info("response cache cleared")
	}

	stats := d.respCache.Stats()
	d.metrics.UpdateResponseCacheStats(stats.Entries, stats.SizeBytes)

	return mcp.NewResponse(msg.ID, map[string]any{
		"cleared": true,
		"server":  params.Server,
	})
}

// handleCostStats returns cost tracking usage data.
func (d *Daemon) handleCostStats(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.cost == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"enabled": false,
			"reason":  "cost tracking not enabled",
		})
	}
	snap := d.cost.Snapshot()
	return mcp.NewResponse(msg.ID, snap)
}

// Reload reloads the registry and refreshes servers.
func (d *Daemon) Reload(ctx context.Context) error {
	d.logger.Info("reloading configuration")

	// Refresh runtime-mutable env settings (HUD admin token, etc.)
	// before touching the registry — keeps an out-of-band
	// X-Admin-Token rotation effective even if registry reload fails.
	if err := d.reloadEnvFile(); err != nil {
		d.logger.Warn("env file reload failed", "path", d.cfg.EnvFilePath, "error", err)
	}

	// Reload registry
	if d.cfg.RegistryPath != "" {
		oldReg := d.registry
		newReg, err := registry.LoadWithDefaults(d.cfg.RegistryPath)
		if err != nil {
			return fmt.Errorf("load registry: %w", err)
		}
		newReg, err = runtimeRegistryForTarget(newReg, d.cfg.Target)
		if err != nil {
			return fmt.Errorf("normalize runtime registry: %w", err)
		}

		// Find servers that were removed
		oldServers := make(map[string]bool)
		for _, s := range d.registry.Servers {
			oldServers[s.Name] = true
		}
		newServers := make(map[string]bool)
		for _, s := range newReg.Servers {
			newServers[s.Name] = true
		}

		// Stop removed servers
		for name := range oldServers {
			if !newServers[name] {
				d.logger.Info("stopping removed server", "server", name)
				d.procMgr.Stop(name)
				d.runningServers.Delete(name)
				d.manifest.RemoveServer(name)
				if d.eventBus != nil {
					d.eventBus.Publish(EventProcessStop, map[string]any{
						"server": name,
						"reason": "removed_from_config",
					})
				}
			}
		}

		d.procMgr.SetRegistry(newReg)
		d.router.SetRegistry(newReg)
		d.registry = newReg
		d.logger.Info("registry reloaded", "servers", len(newReg.Servers))

		invalidated := d.invalidateServersForReload(oldReg, newReg)
		if len(invalidated) > 0 {
			d.logger.Info("invalidated running servers after reload", "count", len(invalidated), "servers", invalidated)
		}
	}

	// Refresh tool cache
	refreshCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if _, err := d.refreshToolCache(refreshCtx); err != nil {
		d.logger.Warn("tool cache refresh failed after reload", "error", err)
	}

	// Emit config.reload event
	if d.eventBus != nil {
		d.eventBus.Publish(EventConfigReload, map[string]any{
			"servers": len(d.registry.Servers),
		})
	}
	if d.hudApp != nil {
		d.hudApp.RefreshMonitors()
	}

	return nil
}

type tunnelsResult struct {
	Tunnels   map[string]*TunnelStatus `json:"tunnels"`
	Total     int                      `json:"total"`
	Connected int                      `json:"connected"`
}

// handleTunnels returns the status of all SSH tunnels.
func (d *Daemon) handleTunnels(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.tunnelMgr == nil {
		return mcp.NewResponse(msg.ID, tunnelsResult{
			Tunnels: make(map[string]*TunnelStatus),
		})
	}

	result := tunnelsResult{
		Tunnels:   d.tunnelMgr.GetAllStatuses(),
		Total:     d.tunnelMgr.TunnelCount(),
		Connected: d.tunnelMgr.ConnectedCount(),
	}

	return mcp.NewResponse(msg.ID, result)
}

// startTunnelsForServers scans the registry and starts tunnels for servers with SSH config.
func (d *Daemon) startTunnelsForServers() {
	if d.tunnelMgr == nil || d.registry == nil {
		return
	}

	// Port allocation starts at 16443 for K8s API tunnels
	nextPort := 16443

	for _, server := range d.registry.Servers {
		if server == nil {
			continue
		}

		// Get target spec for current target profile
		spec, err := d.registry.GetServerSpec(server.Name, d.cfg.Target)
		if err != nil || spec == nil {
			continue
		}

		// Check if server has SSH configuration
		if spec.SSH == nil {
			continue
		}

		// Determine the remote address from server config
		// Common pattern: K8s API server on 6443, or use env var
		remoteAddr := "localhost:6443"
		if envHost, ok := spec.Env["KUBECONFIG_REMOTE_HOST"]; ok {
			remoteAddr = d.expandVars(envHost)
		}

		d.logger.Info("starting tunnel for server",
			"server", server.Name,
			"ssh_host", spec.SSH.Host,
			"local_port", nextPort,
			"remote_addr", remoteAddr)

		if err := d.tunnelMgr.AddTunnel(server.Name, spec.SSH, nextPort, remoteAddr); err != nil {
			d.logger.Warn("failed to start tunnel", "server", server.Name, "error", err)
			continue
		}

		nextPort++
	}

	count := d.tunnelMgr.TunnelCount()
	if count > 0 {
		d.logger.Info("tunnels started", "count", count)
	}
}
