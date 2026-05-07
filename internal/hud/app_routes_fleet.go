// app_routes_fleet.go contains fleet, presence, and status HTTP handlers.
//
// These handlers serve cached snapshots from the fleet and health monitors.
// They are pure reads with no side effects — the monitors poll the daemon
// independently and the handlers simply serialize the latest snapshot.
package hud

import (
	"net/http"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/visibility/contracts/health"
	"github.com/crb2nu/loom/internal/visibility/contracts/status"
)

// handleStatus returns daemon status from the fleet monitor snapshot.
// JSON contract: {"running": bool, "servers": int, "activeConns": int, "idleConns": int, "processes": []}
func (a *App) handleStatus(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, &status.DaemonRPCStatus{
		Running:     snap.DaemonRunning,
		Servers:     snap.ServerCount,
		ActiveConns: snap.ActiveConns,
		Processes:   snap.Processes,
	})
}

// handleHealth returns server health from the health monitor.
// JSON contract: {"servers": {"name": {"local": {...}, "hub": {...}, "target": "...", "divergence": ...}}, "divergence": [...]}
func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) {
	servers := a.healthMonitor.Servers()

	// Reshape into the existing HealthResult JSON contract.
	healthServers := make(map[string]health.ServerHealth, len(servers))
	var divergences []health.HealthDivergenceEntry
	for _, s := range servers {
		sh := health.ServerHealth{
			Target:     s.Target,
			Transport:  s.Transport,
			Divergence: s.Divergence,
		}
		// Map the consolidated entry back to the local/hub shape.
		if s.Target == "hub" {
			sh.Hub = health.HealthEntry{
				Healthy:      s.Healthy,
				ConsecFails:  s.ConsecFails,
				AvgLatencyMs: s.AvgLatencyMs,
				ErrorMessage: s.ErrorMessage,
			}
		} else {
			sh.Local = health.HealthEntry{
				Healthy:      s.Healthy,
				ConsecFails:  s.ConsecFails,
				AvgLatencyMs: s.AvgLatencyMs,
				ErrorMessage: s.ErrorMessage,
			}
		}
		if s.Divergence != nil {
			divergences = append(divergences, health.HealthDivergenceEntry{
				Server: s.Name,
				Reason: s.Divergence.Reason,
			})
		}
		healthServers[s.Name] = sh
	}
	a.writeJSON(w, http.StatusOK, struct {
		Servers      map[string]health.ServerHealth `json:"servers"`
		Divergence   []health.HealthDivergenceEntry `json:"divergence,omitempty"`
		CacheBackend string                         `json:"cache_backend"`
	}{
		Servers:      healthServers,
		Divergence:   divergences,
		CacheBackend: a.cacheBackend,
	})
}

// handleServers returns MCP server info from the health monitor.
// JSON contract: {"servers": [{"name": "...", "categories": [], "running": bool}]}
func (a *App) handleServers(w http.ResponseWriter, _ *http.Request) {
	servers := a.healthMonitor.Servers()

	// Reshape into the existing ServersResult JSON contract.
	infos := make([]bridge.ServerInfo, len(servers))
	for i, s := range servers {
		infos[i] = bridge.ServerInfo{
			Name:        s.Name,
			Categories:  s.Categories,
			Description: s.Description,
			Running:     s.Running,
			ToolCount:   s.ToolCount,
			Transport:   s.Transport,
		}
	}
	a.writeJSON(w, http.StatusOK, &bridge.ServersResult{Servers: infos})
}

// handleFleet returns the full fleet snapshot — a single aggregated view
// of daemon status, sessions, tasks, memory, graph, and workflows.
func (a *App) handleFleet(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, snap)
}

// handlePresence returns agent presence data from the fleet monitor snapshot.
func (a *App) handlePresence(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, map[string]any{
		"agents":         snap.Agents,
		"active_agents":  snap.ActiveAgents,
		"idle_agents":    snap.IdleAgents,
		"offline_agents": snap.OfflineAgents,
		"total":          snap.ActiveAgents + snap.IdleAgents + snap.OfflineAgents,
	})
}

// handleClaims returns file claims from the fleet monitor snapshot.
func (a *App) handleClaims(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, map[string]any{
		"claims": snap.FileClaims,
		"count":  len(snap.FileClaims),
	})
}

// handleWorktrees returns worktree assignments from the fleet monitor snapshot.
func (a *App) handleWorktrees(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, map[string]any{
		"worktrees":        snap.Worktrees,
		"active_worktrees": snap.ActiveWorktrees,
	})
}

// handleAgents returns the agent list from the fleet monitor snapshot.
func (a *App) handleAgents(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, map[string]any{"agents": snap.Agents})
}
