package mobile

import (
	"net/http"
	"strings"
	"time"
)

func (d *MobileDomain) handleMobilePing(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}
	d.writeMobileJSON(w, http.StatusOK, map[string]any{"pong": true})
}

func (d *MobileDomain) handleMobileDashboard(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	mon := d.deps.Monitors()
	fleetSnap := mon.Fleet.Snapshot()
	healthSum := mon.Health.Summary()

	var recentTimeline []TimelineEntry
	if el := d.deps.EventLog(); el != nil {
		recentTimeline = el.AllExcluding(10, "agent.heartbeat", "hud.fleet", "hud.health")
	}
	if recentTimeline == nil {
		recentTimeline = []TimelineEntry{}
	}

	var lastHeartbeat map[string]any
	if el := d.deps.EventLog(); el != nil {
		lastHeartbeat = buildHeartbeatSummary(el.All(1000))
	}
	if lastHeartbeat == nil {
		lastHeartbeat = map[string]any{
			"agent_id":  "",
			"timestamp": "",
			"count_1h":  0,
		}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"daemon_running":  fleetSnap.DaemonRunning,
		"server_count":    fleetSnap.ServerCount,
		"active_sessions": fleetSnap.ActiveSessions,
		"active_agents":   fleetSnap.ActiveAgents,
		"idle_agents":     fleetSnap.IdleAgents,
		"offline_agents":  fleetSnap.OfflineAgents,
		"updated_at":      fleetSnap.UpdatedAt,
		"health": map[string]any{
			"total_servers":    healthSum.TotalServers,
			"healthy_servers":  healthSum.HealthyServers,
			"degraded_servers": healthSum.DegradedServers,
			"down_servers":     healthSum.DownServers,
			"idle_servers":     healthSum.IdleServers,
		},
		"coordination": map[string]any{
			"summary":          fleetSnap.Coordination.Summary,
			"attention_agents": limitMobileSlice(fleetSnap.Coordination.Agents, 5),
			"risky_namespaces": limitMobileSlice(fleetSnap.Coordination.Namespaces, 5),
			"active_blockers":  limitMobileSlice(filterMobileBlockers(fleetSnap.Coordination.Blockers, true), 6),
			"top_relations":    limitMobileSlice(filterMobileRelations(fleetSnap.Coordination.Relations, ""), 6),
			"attention_lanes":  buildMobileAttentionLanes(fleetSnap.Coordination),
		},
		"recent_timeline": recentTimeline,
		"last_heartbeat":  lastHeartbeat,
	})
}

func (d *MobileDomain) handleMobileControlPlane(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	mon := d.deps.Monitors()

	costSnap := controlPlaneCost{}
	if mon.Cost != nil {
		snapshot := mon.Cost.Snapshot()
		costSnap = controlPlaneCost{
			Enabled:         snapshot.Enabled,
			Timestamp:       snapshot.Timestamp,
			TotalCalls:      snapshot.TotalCalls,
			TotalErrors:     snapshot.TotalErrors,
			TotalDenied:     snapshot.TotalDenied,
			TotalCached:     snapshot.TotalCached,
			TotalDurationMs: snapshot.TotalDuration,
		}
		for _, agent := range snapshot.ByAgent {
			if costSnap.TopAgent == nil || agent.CallCount > costSnap.TopAgent.CallCount {
				costSnap.TopAgent = &controlPlaneCostTopAgent{
					AgentID:   agent.AgentID,
					CallCount: agent.CallCount,
					Errors:    agent.Errors,
					Denied:    agent.Denied,
					Cached:    agent.Cached,
				}
			}
		}
		for _, server := range snapshot.ByServer {
			if costSnap.TopServer == nil || server.CallCount > costSnap.TopServer.CallCount {
				costSnap.TopServer = &controlPlaneCostTopServer{
					Server:    server.Server,
					CallCount: server.CallCount,
					Errors:    server.Errors,
				}
			}
		}
	}

	rbacResult := d.deps.FetchRBACConfig()
	rbac := controlPlaneRBAC{
		Enabled:         rbacResult.Enabled,
		DefaultPolicy:   strings.TrimSpace(rbacResult.DefaultPolicy),
		RoleCount:       len(rbacResult.Roles),
		BindingCount:    len(rbacResult.Bindings),
		GlobalDenyCount: len(rbacResult.GlobalDeny),
		RateLimitCount:  len(rbacResult.RateLimits),
		DeniedCount:     len(rbacResult.RecentDenied),
	}

	otelResult := d.deps.FetchOTelStatus()
	otel := controlPlaneOTel{
		OTLPConfigured:  otelResult.OTLPConfigured,
		OTLPEndpoint:    strings.TrimSpace(otelResult.OTLPEndpoint),
		JSONLogsEnabled: otelResult.JSONLogsEnabled,
		TracedServers:   otelResult.TracedServers,
		TotalServers:    otelResult.TotalServers,
		TraceCoverage:   strings.TrimSpace(otelResult.TraceCoverage),
	}

	health := controlPlaneHealth{}
	if mon.Health != nil {
		healthSum := mon.Health.Summary()
		health = controlPlaneHealth{
			TotalServers:    healthSum.TotalServers,
			HealthyServers:  healthSum.HealthyServers,
			DegradedServers: healthSum.DegradedServers,
			DownServers:     healthSum.DownServers,
			IdleServers:     healthSum.IdleServers,
		}
		for _, server := range mon.Health.Servers() {
			switch strings.ToLower(strings.TrimSpace(server.Target)) {
			case "hub":
				health.HubTargets++
			case "local":
				health.LocalTargets++
			default:
				health.Unavailable++
			}
		}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"cost":   costSnap,
		"rbac":   rbac,
		"otel":   otel,
		"health": health,
	})
}

func buildHeartbeatSummary(entries []TimelineEntry) map[string]any {
	if len(entries) == 0 {
		return nil
	}

	cutoff := time.Now().Add(-1 * time.Hour)
	var latest TimelineEntry
	var found bool
	count := 0

	for _, entry := range entries {
		if entry.EventType != "agent.heartbeat" {
			continue
		}
		if !found || entry.Timestamp.After(latest.Timestamp) {
			latest = entry
			found = true
		}
		if entry.Timestamp.Before(cutoff) {
			continue
		}
		count++
	}
	if !found {
		return map[string]any{
			"agent_id":  "",
			"timestamp": "",
			"count_1h":  0,
		}
	}
	if latest.Timestamp.IsZero() {
		return map[string]any{
			"agent_id":  "",
			"timestamp": "",
			"count_1h":  0,
		}
	}

	agentID := latest.AgentID

	return map[string]any{
		"agent_id":  agentID,
		"timestamp": latest.Timestamp.Format(time.RFC3339),
		"count_1h":  count,
	}
}
