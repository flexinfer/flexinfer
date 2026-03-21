package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/internal/router"
	"github.com/crb2nu/loom/pkg/registry"
)

func (d *Daemon) handleMessage(ctx context.Context, msg *mcp.Message) (resp *mcp.Message, err error) {
	if msg == nil {
		err = fmt.Errorf("nil message")
		return nil, err
	}

	attrs := []attribute.KeyValue{
		attribute.String("mcp.method", msg.Method),
	}
	if msg.ID != nil {
		attrs = append(attrs, attribute.String("mcp.request_id", fmt.Sprint(msg.ID)))
	}

	ctx, span := d.daemonTracer().Start(ctx, "daemon.rpc."+msg.Method,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrs...),
	)
	defer func() {
		span.SetAttributes(attribute.Bool("loom.has_response", resp != nil))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	switch msg.Method {
	case "initialize":
		resp, err = d.handleInitialize(ctx, msg)
	case "notifications/initialized":
		resp, err = nil, nil
	case "loom/status":
		resp, err = d.handleStatus(ctx, msg)
	case "loom/servers":
		resp, err = d.handleServers(ctx, msg)
	case "loom/health":
		resp, err = d.handleHealth(ctx, msg)
	case "loom/tools":
		resp, err = d.handleTools(ctx, msg)
	case "loom/tools/search":
		resp, err = d.handleToolsSearch(ctx, msg)
	case "loom/tools/get":
		resp, err = d.handleToolGet(ctx, msg)
	case "loom/resources":
		resp, err = d.handleResources(ctx, msg)
	case "loom/call", "tools/call":
		resp, err = d.handleCall(ctx, msg)
	case "loom/reload":
		resp, err = d.handleReload(ctx, msg)
	case "loom/config-hash":
		resp, err = d.handleConfigHash(ctx, msg)
	case "loom/profile":
		resp, err = d.handleProfile(ctx, msg)
	case "loom/tunnels":
		resp, err = d.handleTunnels(ctx, msg)
	case "loom/cache/stats":
		resp, err = d.handleCacheStats(ctx, msg)
	case "loom/cache/clear":
		resp, err = d.handleCacheClear(ctx, msg)
	case "loom/cost-stats":
		resp, err = d.handleCostStats(ctx, msg)
	case "loom/rbac-config":
		resp, err = d.handleRBACConfig(ctx, msg)
	case "loom/rbac-simulate":
		resp, err = d.handleRBACSimulate(ctx, msg)
	case "loom/otel-status":
		resp, err = d.handleOTelStatus(ctx, msg)
	case "loom/session/open":
		resp, err = d.handleSessionOpen(ctx, msg)
	case "loom/session/heartbeat":
		resp, err = d.handleSessionHeartbeat(ctx, msg)
	case "loom/session/status":
		resp, err = d.handleSessionStatus(ctx, msg)
	case "loom/session/close":
		resp, err = d.handleSessionClose(ctx, msg)
	default:
		resp = mcp.NewErrorResponse(msg.ID, mcp.MethodNotFound, fmt.Sprintf("unknown method: %s", msg.Method))
	}
	return resp, err
}

func (d *Daemon) handleInitialize(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	result := mcp.InitializeResult{
		ProtocolVersion: negotiateProtocolVersion(msg.Params),
		Capabilities:    mcp.Capabilities{},
		ServerInfo: mcp.ServerInfo{
			Name:    "loom",
			Version: "0.1.0",
		},
		Instructions: "Loom daemon - unified MCP hub management",
	}
	return mcp.NewResponse(msg.ID, result)
}

func negotiateProtocolVersion(raw json.RawMessage) string {
	defaultVersion := mcp.ProtocolVersion20250618

	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(raw) == 0 || string(raw) == "null" {
		return defaultVersion
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return defaultVersion
	}

	requested := strings.TrimSpace(params.ProtocolVersion)
	switch requested {
	case mcp.ProtocolVersion20250618, mcp.ProtocolVersion:
		return requested
	default:
		return defaultVersion
	}
}

type statusResult struct {
	Running             bool     `json:"running"`
	Servers             int      `json:"servers"`
	ActiveConns         int      `json:"activeConns"`
	IdleConns           int      `json:"idleConns"`
	Processes           []string `json:"processes"`
	ActiveRPCs          int64    `json:"activeRPCs"`
	DrainReady          bool     `json:"drainReady"`
	Draining            bool     `json:"draining"`
	DaemonEpoch         int64    `json:"daemonEpoch"`
	ActiveProxySessions int      `json:"activeProxySessions"`
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
		Processes:           d.procMgr.List(),
		ActiveRPCs:          rpcs,
		DrainReady:          rpcs == 0,
		Draining:            d.draining.Load(),
		DaemonEpoch:         d.daemonEpoch,
		ActiveProxySessions: activeSessions,
	}
	return mcp.NewResponse(msg.ID, result)
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

// handleRBACConfig returns RBAC configuration and recent denied calls.
func (d *Daemon) handleRBACConfig(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.rbac == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"enabled": false,
		})
	}

	cfg := d.rbac.Config()
	recentDenied := d.recentDeniedSnapshot()
	result := map[string]any{
		"enabled":        true,
		"audit_enabled":  d.audit != nil,
		"default_policy": cfg.DefaultPolicy,
		"global_deny":    cfg.GlobalDeny,
		"denied_count":   len(recentDenied),
	}

	var roles []map[string]any
	for name, role := range cfg.Roles {
		roles = append(roles, map[string]any{
			"name":  name,
			"allow": role.Allow,
			"deny":  role.Deny,
		})
	}
	result["roles"] = roles

	var bindings []map[string]any
	for _, b := range cfg.Bindings {
		bindings = append(bindings, map[string]any{
			"agent_id":   b.AgentID,
			"agent_type": b.AgentType,
			"role":       b.Role,
		})
	}
	result["bindings"] = bindings

	var rateLimits []map[string]any
	for _, rl := range cfg.RateLimits {
		rateLimits = append(rateLimits, map[string]any{
			"agent_id":            rl.AgentID,
			"tool":                rl.Tool,
			"requests_per_minute": rl.RequestsPerMinute,
		})
	}
	result["rate_limits"] = rateLimits

	result["recent_denied"] = recentDenied

	return mcp.NewResponse(msg.ID, result)
}

// handleRBACSimulate evaluates an RBAC decision for a provided request tuple.
func (d *Daemon) handleRBACSimulate(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	var params struct {
		AgentID   string `json:"agent_id,omitempty"`
		AgentType string `json:"agent_type,omitempty"`
		Server    string `json:"server"`
		Tool      string `json:"tool"`
		DryRun    bool   `json:"dry_run,omitempty"`
	}
	if len(msg.Params) > 0 {
		_ = json.Unmarshal(msg.Params, &params)
	}
	if params.Server == "" || params.Tool == "" {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "server and tool are required"), nil
	}

	if d.rbac == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"enabled": false,
			"decision": AccessDecision{
				Allowed:    true,
				AgentID:    params.AgentID,
				Server:     params.Server,
				Tool:       params.Tool,
				Reason:     "rbac disabled",
				ReasonCode: "rbac_disabled",
				DryRun:     params.DryRun,
			},
		})
	}

	var decision AccessDecision
	if params.DryRun {
		decision = d.rbac.Simulate(params.AgentID, params.AgentType, params.Server, params.Tool)
	} else {
		decision = d.rbac.Check(params.AgentID, params.AgentType, params.Server, params.Tool)
	}

	return mcp.NewResponse(msg.ID, map[string]any{
		"enabled":  true,
		"decision": decision,
	})
}

// handleOTelStatus returns observability configuration status.
func (d *Daemon) handleOTelStatus(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	// Fall back to file config if env var is not set.
	if endpoint == "" && d.fileCfg.OTel.Endpoint != "" {
		endpoint = d.fileCfg.OTel.Endpoint
	}
	logFormat := os.Getenv("MCP_LOG_FORMAT")
	if logFormat == "" {
		logFormat = "text"
	}

	tracedServers, totalServers := d.computeTracedServerCoverage()
	coverage := formatCoverage(tracedServers, totalServers)

	// Runtime daemon tracing surfaces that complement per-server pkg/mcpotel spans.
	runtimeSurfaces := map[string]bool{
		"rpc_dispatch":                true,
		"server_connect":              true,
		"client_connection_lifecycle": true,
		"transport_recovery_events":   true,
	}

	result := map[string]any{
		"otlp_endpoint":          endpoint,
		"otlp_configured":        endpoint != "",
		"log_format":             logFormat,
		"json_logs_enabled":      logFormat == "json",
		"traced_servers":         tracedServers,
		"total_servers":          totalServers,
		"trace_coverage":         coverage,
		"runtime_trace_surfaces": runtimeSurfaces,
		"runtime_trace_coverage": "100%",
	}
	return mcp.NewResponse(msg.ID, result)
}

func (d *Daemon) computeTracedServerCoverage() (traced, total int) {
	if d.registry == nil {
		return 0, 0
	}
	total = len(d.registry.Servers)
	for _, server := range d.registry.Servers {
		if server == nil {
			continue
		}
		spec, err := d.registry.GetServerSpec(server.Name, d.cfg.Target)
		if err != nil || spec == nil {
			continue
		}
		if isMCPServerCommand(spec.Command, spec.Args) {
			traced++
		}
	}
	return traced, total
}

func isMCPServerCommand(command string, args []any) bool {
	if isMCPServerToken(command) {
		return true
	}
	for _, arg := range args {
		if isMCPServerToken(fmt.Sprint(arg)) {
			return true
		}
	}
	return false
}

func isMCPServerToken(token string) bool {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "cmd/mcp-") {
		return true
	}
	base := filepath.Base(trimmed)
	return strings.HasPrefix(base, "mcp-")
}

func formatCoverage(numerator, denominator int) string {
	if denominator <= 0 {
		return "100%"
	}
	pct := float64(numerator) / float64(denominator) * 100
	return fmt.Sprintf("%.0f%%", pct)
}

// Reload reloads the registry and refreshes servers.
func (d *Daemon) Reload(ctx context.Context) error {
	d.logger.Info("reloading configuration")

	// Reload registry
	if d.cfg.RegistryPath != "" {
		oldReg := d.registry
		newReg, err := registry.LoadWithDefaults(d.cfg.RegistryPath)
		if err != nil {
			return fmt.Errorf("load registry: %w", err)
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

	return nil
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
