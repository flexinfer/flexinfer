package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/router"
)

type statusResult struct {
	Running             bool          `json:"running"`
	Servers             int           `json:"servers"`
	ActiveConns         int           `json:"activeConns"`
	IdleConns           int           `json:"idleConns"`
	LocalPool           *poolPressure `json:"localPool,omitempty"`
	HubPool             *poolPressure `json:"hubPool,omitempty"`
	Processes           []string      `json:"processes"`
	ActiveRPCs          int64         `json:"activeRPCs"`
	DrainReady          bool          `json:"drainReady"`
	Draining            bool          `json:"draining"`
	DaemonEpoch         int64         `json:"daemonEpoch"`
	ActiveProxySessions int           `json:"activeProxySessions"`
}

type poolPressure struct {
	ActiveConns int     `json:"activeConns"`
	IdleConns   int     `json:"idleConns"`
	MaxIdle     int     `json:"maxIdle"`
	MaxOpen     int     `json:"maxOpen"`
	PressurePct float64 `json:"pressurePct"`
	AtCapacity  bool    `json:"atCapacity"`
}

func (d *Daemon) handleStatus(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	stats := d.pool.Stats()
	rpcs := d.activeRPCs.Load()

	activeSessions := 0
	if d.sessions != nil {
		activeSessions = d.sessions.ActiveCount()
	}

	result := statusResult{
		Running:             true,
		Servers:             len(d.registry.Servers),
		ActiveConns:         stats.ActiveConns,
		IdleConns:           stats.IdleConns,
		LocalPool:           d.poolPressure(d.pool, false),
		HubPool:             d.poolPressure(d.hubPool, true),
		Processes:           d.procMgr.List(),
		ActiveRPCs:          rpcs,
		DrainReady:          rpcs == 0,
		Draining:            d.draining.Load(),
		DaemonEpoch:         d.daemonEpoch,
		ActiveProxySessions: activeSessions,
	}
	return mcp.NewResponse(msg.ID, result)
}

func (d *Daemon) poolPressure(p *pool.Pool, hub bool) *poolPressure {
	if p == nil {
		return nil
	}
	stats := p.Stats()
	maxIdle, maxOpen, _ := d.fileCfg.Resources.GetPoolConfig()
	if hub {
		maxIdle, maxOpen, _ = d.fileCfg.Resources.GetHubPoolConfig()
	}
	pressure := 0.0
	if maxOpen > 0 {
		pressure = float64(stats.ActiveConns) / float64(maxOpen) * 100
	}
	return &poolPressure{
		ActiveConns: stats.ActiveConns,
		IdleConns:   stats.IdleConns,
		MaxIdle:     maxIdle,
		MaxOpen:     maxOpen,
		PressurePct: pressure,
		AtCapacity:  maxOpen > 0 && stats.ActiveConns >= maxOpen,
	}
}

type serversResult struct {
	Servers []serverInfo `json:"servers"`
}

type serverInfo struct {
	Name        string   `json:"name"`
	Categories  []string `json:"categories,omitempty"`
	Description string   `json:"description,omitempty"`
	Running     bool     `json:"running"`
}

func (d *Daemon) handleServers(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	var servers []serverInfo
	running := d.procMgr.List()
	runningSet := make(map[string]bool)
	for _, name := range running {
		runningSet[name] = true
	}

	for _, s := range d.registry.Servers {
		desc := ""
		if s.Common != nil {
			desc = s.Common.Description
		}
		servers = append(servers, serverInfo{
			Name:        s.Name,
			Categories:  s.Categories,
			Description: desc,
			Running:     runningSet[s.Name],
		})
	}

	return mcp.NewResponse(msg.ID, serversResult{Servers: servers})
}

type healthResult struct {
	Servers    map[string]serverHealth `json:"servers"`
	Divergence []healthDivergenceEntry `json:"divergence,omitempty"`
}

type healthDivergenceEntry struct {
	Server string `json:"server"`
	Reason string `json:"reason"`
}

type serverHealth struct {
	Local      *healthStatus     `json:"local,omitempty"`
	Hub        *healthStatus     `json:"hub,omitempty"`
	Monitor    *healthStatus     `json:"monitor,omitempty"`
	Target     string            `json:"target"`
	Divergence *HealthDivergence `json:"divergence,omitempty"`
}

type healthStatus struct {
	Healthy      bool    `json:"healthy"`
	ConsecFails  int     `json:"consecFails"`
	AvgLatencyMs float64 `json:"avgLatencyMs,omitempty"`
	ErrorMessage string  `json:"errorMessage,omitempty"`
}

func (d *Daemon) handleHealth(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	allHealth := d.router.GetAllHealth()
	servers := make(map[string]serverHealth)
	var divergences []healthDivergenceEntry

	// Collect monitor statuses for divergence comparison.
	var monitorStatuses map[string]*ServerHealthStatus
	if d.healthMonitor != nil {
		monitorStatuses = d.healthMonitor.GetAllStatuses()
	}

	for name, h := range allHealth {
		decision, _ := d.router.Route(ctx, name)
		target := "unavailable"
		routerAvailable := false
		if decision != nil {
			target = decision.Target.String()
			routerAvailable = decision.Target != router.TargetUnavailable
		}

		sh := serverHealth{Target: target}
		if h.Local != nil {
			sh.Local = &healthStatus{
				Healthy:      h.Local.Healthy,
				ConsecFails:  h.Local.ConsecFails,
				AvgLatencyMs: h.Local.AvgLatencyMs,
				ErrorMessage: h.Local.ErrorMessage,
			}
		}
		if h.Hub != nil {
			sh.Hub = &healthStatus{
				Healthy:      h.Hub.Healthy,
				ConsecFails:  h.Hub.ConsecFails,
				AvgLatencyMs: h.Hub.AvgLatencyMs,
				ErrorMessage: h.Hub.ErrorMessage,
			}
		}

		// Include monitor slice if available.
		monStatus := monitorStatuses[name]
		if monStatus != nil {
			sh.Monitor = &healthStatus{
				Healthy:      monStatus.Healthy,
				ConsecFails:  monStatus.ConsecutiveFails,
				AvgLatencyMs: monStatus.AvgLatencyMs,
				ErrorMessage: monStatus.LastError,
			}
		}

		// Check for divergence between monitor and router.
		if div := computeHealthDivergence(monStatus, routerAvailable); div != nil {
			sh.Divergence = div
			divergences = append(divergences, healthDivergenceEntry{
				Server: name,
				Reason: div.Reason,
			})
		}

		servers[name] = sh
	}

	return mcp.NewResponse(msg.ID, healthResult{
		Servers:    servers,
		Divergence: divergences,
	})
}

// HealthResponse is the JSON response for the /health endpoint.
type HealthResponse struct {
	Status     string                         `json:"status"`
	Timestamp  string                         `json:"timestamp"`
	Uptime     string                         `json:"uptime,omitempty"`
	Servers    map[string]*ServerHealthStatus `json:"servers,omitempty"`
	Tunnels    map[string]*TunnelStatus       `json:"tunnels,omitempty"`
	Summary    HealthSummary                  `json:"summary"`
	Divergence []healthDivergenceEntry        `json:"divergence,omitempty"`
}

// HealthSummary provides aggregate health statistics.
type HealthSummary struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`
	Unhealthy int `json:"unhealthy"`
	Unknown   int `json:"unknown"`
}

// HealthHandler returns an HTTP handler for detailed health status.
func (d *Daemon) HealthHandler() http.HandlerFunc {
	startTime := time.Now()

	return func(w http.ResponseWriter, r *http.Request) {
		resp := HealthResponse{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Uptime:    time.Since(startTime).Round(time.Second).String(),
		}

		// Get server health from monitor
		if d.healthMonitor != nil {
			resp.Servers = d.healthMonitor.GetAllStatuses()
		}

		// Get tunnel status
		if d.tunnelMgr != nil {
			resp.Tunnels = d.tunnelMgr.GetAllStatuses()
		}

		// Check for divergence between monitor and router.
		if d.router != nil && resp.Servers != nil {
			for name, status := range resp.Servers {
				decision, _ := d.router.Route(r.Context(), name)
				routerAvailable := decision != nil && decision.Target != router.TargetUnavailable
				if div := computeHealthDivergence(status, routerAvailable); div != nil {
					resp.Divergence = append(resp.Divergence, healthDivergenceEntry{
						Server: name,
						Reason: div.Reason,
					})
				}
			}
		}

		// Calculate summary
		if d.registry != nil {
			resp.Summary.Total = len(d.registry.Servers)
		}
		for _, status := range resp.Servers {
			if status.Healthy {
				resp.Summary.Healthy++
			} else {
				resp.Summary.Unhealthy++
			}
		}
		resp.Summary.Unknown = resp.Summary.Total - resp.Summary.Healthy - resp.Summary.Unhealthy

		// Determine overall status
		if len(resp.Divergence) > 0 {
			resp.Status = "diverged"
			w.WriteHeader(http.StatusOK)
		} else if resp.Summary.Unhealthy > 0 {
			resp.Status = "degraded"
			w.WriteHeader(http.StatusOK) // Still return 200 for degraded
		} else if resp.Summary.Healthy == 0 && resp.Summary.Total > 0 {
			resp.Status = "unhealthy"
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			resp.Status = "healthy"
			w.WriteHeader(http.StatusOK)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

type configHashResult struct {
	RegistryHash string `json:"registryHash"`
	ManifestHash string `json:"manifestHash"`
	ToolCount    int    `json:"toolCount"`
	ServerCount  int    `json:"serverCount"`
}

// handleConfigHash returns hash of current configuration for drift detection.
func (d *Daemon) handleConfigHash(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	d.toolCache.mu.RLock()
	toolCount := len(visibleTools(d.toolCache.tools))
	d.toolCache.mu.RUnlock()

	result := configHashResult{
		ToolCount:   toolCount,
		ServerCount: len(d.registry.Servers),
	}

	return mcp.NewResponse(msg.ID, result)
}

type profileParams struct {
	Name string `json:"name,omitempty"`
}

type profileResult struct {
	Active    string   `json:"active"`
	Available []string `json:"available"`
	ToolCount int      `json:"toolCount"`
}

// handleProfile gets or sets the active profile.
func (d *Daemon) handleProfile(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	var params profileParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	// If name is provided, switch profile
	if params.Name != "" {
		profile := d.profiles.Get(params.Name)
		if profile == nil {
			return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, fmt.Sprintf("unknown profile: %s", params.Name)), nil
		}

		d.fileCfg.Context.ActiveProfile = params.Name
		d.logger.Info("switching profile", "profile", params.Name)

		// Refresh tool cache with new profile
		go func() {
			refreshCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if _, err := d.refreshToolCache(refreshCtx); err != nil {
				d.logger.Warn("failed to refresh after profile switch", "error", err)
			}
		}()
	}

	d.toolCache.mu.RLock()
	toolCount := len(visibleTools(d.toolCache.tools))
	d.toolCache.mu.RUnlock()

	result := profileResult{
		Active:    d.fileCfg.Context.ActiveProfile,
		Available: d.profiles.List(),
		ToolCount: toolCount,
	}

	if result.Active == "" {
		result.Active = "full"
	}

	return mcp.NewResponse(msg.ID, result)
}
