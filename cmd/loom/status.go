// status.go contains functions for displaying status information about the daemon.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
	presencectr "github.com/crb2nu/loom/internal/visibility/contracts/presence"
	statusctr "github.com/crb2nu/loom/internal/visibility/contracts/status"
)

func showStatus(socketPath, hudPort string, jsonOutput bool) error {
	ps := collectPlatformStatus(socketPath, hudPort)

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(ps)
	}

	printPlatformStatus(ps, socketPath)

	if !ps.Daemon.Running {
		return fmt.Errorf("daemon not running")
	}
	return nil
}

func collectPlatformStatus(socketPath, hudPort string) statusctr.PlatformStatus {
	ps := statusctr.PlatformStatus{}

	// 1. Daemon status via socket RPC.
	result, err := call(socketPath, "loom/status", nil)
	if err != nil {
		return ps // daemon not running — everything zeroed
	}

	var raw struct {
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
		Health              *struct {
			Servers         map[string]statusctr.DaemonHealthServer `json:"servers"`
			DegradedServers []string                                `json:"degraded_servers"`
		} `json:"health"`
		Observability *statusctr.DaemonObservabilityStatus `json:"observability"`
	}
	if err := json.Unmarshal(result, &raw); err != nil {
		return ps
	}

	ps.Daemon = statusctr.DaemonStatus{
		Running:             true,
		Servers:             raw.Servers,
		ActiveConns:         raw.ActiveConns,
		IdleConns:           raw.IdleConns,
		ActiveRPCs:          raw.ActiveRPCs,
		ActiveProxySessions: raw.ActiveProxySessions,
		DaemonEpoch:         raw.DaemonEpoch,
		DrainReady:          raw.DrainReady,
		Draining:            raw.Draining,
		Processes:           raw.Processes,
	}
	if raw.Health != nil && (len(raw.Health.Servers) > 0 || len(raw.Health.DegradedServers) > 0) {
		ps.Health = &statusctr.DaemonHealthSnapshot{
			Servers:         raw.Health.Servers,
			DegradedServers: append([]string(nil), raw.Health.DegradedServers...),
		}
	}
	if raw.Observability != nil {
		ps.Observability = raw.Observability
	}
	ps.Healthy = true

	// 2. Agent presence from HUD (best-effort).
	needDaemonAgents := false
	needDaemonSessions := false
	presenceData, err := hudGetFast(hudPort, "/api/presence", 2*defaultHUDTimeout/5)
	if err == nil {
		ps.HUD.Reachable = true
		var presence struct {
			ActiveAgents  int `json:"active_agents"`
			IdleAgents    int `json:"idle_agents"`
			OfflineAgents int `json:"offline_agents"`
			Total         int `json:"total"`
		}
		if json.Unmarshal(presenceData, &presence) == nil {
			ps.Agents = statusctr.AgentStatus{
				Active:  presence.ActiveAgents,
				Idle:    presence.IdleAgents,
				Offline: presence.OfflineAgents,
				Total:   presence.Total,
			}
		} else {
			needDaemonAgents = true
		}
	} else {
		needDaemonAgents = true
	}

	// 3. Session counts from HUD (best-effort).
	if ps.HUD.Reachable {
		sessData, err := hudGetFast(hudPort, "/api/sessions", 2*defaultHUDTimeout/5)
		if err == nil {
			var sessResp struct {
				Sessions []bridge.SessionInfo `json:"sessions"`
			}
			if json.Unmarshal(sessData, &sessResp) == nil {
				ps.Sessions = countSessionStatuses(sessResp.Sessions)
			} else {
				needDaemonSessions = true
			}
		} else {
			needDaemonSessions = true
		}

		pipeData, err := hudGetFast(hudPort, "/api/mobile/v1/pipelines", 2*defaultHUDTimeout/5)
		if err == nil {
			var pipeResp struct {
				Available bool                     `json:"available"`
				Summary   statusctr.PipelineStatus `json:"summary"`
			}
			if json.Unmarshal(pipeData, &pipeResp) == nil && pipeResp.Available {
				pipeResp.Summary.Available = true
				ps.Pipelines = pipeResp.Summary
			}
		}
	} else {
		needDaemonSessions = true
	}

	if needDaemonAgents || needDaemonSessions {
		if agents, sessions, err := collectPlatformStatusFromDaemon(socketPath); err == nil {
			if needDaemonAgents {
				ps.Agents = agents
			}
			if needDaemonSessions {
				ps.Sessions = sessions
			}
		}
	}

	return ps
}

func collectPlatformStatusFromDaemon(socketPath string) (statusctr.AgentStatus, statusctr.SessionCount, error) {
	client := bridge.NewDaemonClient(socketPath, nil)
	if err := client.Connect(); err != nil {
		return statusctr.AgentStatus{}, statusctr.SessionCount{}, err
	}
	defer client.Close()

	agentBridge := bridge.NewAgentBridge(client)

	agents, err := agentBridge.PresenceList(true)
	if err != nil {
		return statusctr.AgentStatus{}, statusctr.SessionCount{}, err
	}

	sessionsRaw, err := agentBridge.ListSessions(map[string]any{"limit": 1000})
	if err != nil {
		return statusctr.AgentStatus{}, statusctr.SessionCount{}, err
	}

	var sessionEnvelope struct {
		Sessions []bridge.SessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal(sessionsRaw, &sessionEnvelope); err != nil {
		return statusctr.AgentStatus{}, statusctr.SessionCount{}, err
	}

	return countPresenceStatuses(agents), countSessionStatuses(sessionEnvelope.Sessions), nil
}

func countPresenceStatuses(agents []presencectr.PresenceInfo) statusctr.AgentStatus {
	counts := statusctr.AgentStatus{Total: len(agents)}
	for _, agent := range agents {
		switch agent.Status {
		case "active":
			counts.Active++
		case "idle":
			counts.Idle++
		default:
			counts.Offline++
		}
	}
	return counts
}

func countSessionStatuses(sessions []bridge.SessionInfo) statusctr.SessionCount {
	counts := statusctr.SessionCount{Total: len(sessions)}
	seen := make(map[string]struct{})
	for _, session := range sessions {
		if !isActiveSession(session) {
			continue
		}
		if !hasSessionIdentity(session) {
			counts.Active++
			continue
		}
		key := sessionIdentityKey(session.AgentID, session.Namespace)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		counts.Active++
	}
	return counts
}

func isActiveSession(session bridge.SessionInfo) bool {
	return strings.TrimSpace(session.Status) == "active" || strings.TrimSpace(session.EndedAt) == ""
}

func hasSessionIdentity(session bridge.SessionInfo) bool {
	return strings.TrimSpace(session.AgentID) != "" || strings.TrimSpace(session.Namespace) != ""
}

func sessionIdentityKey(agentID, namespace string) string {
	return strings.TrimSpace(agentID) + "\x00" + strings.TrimSpace(namespace)
}

func printPlatformStatus(ps statusctr.PlatformStatus, socketPath string) {
	if !ps.Daemon.Running {
		fmt.Println("Daemon:   not running")
		fmt.Printf("Socket:   %s\n", socketPath)
		return
	}

	fmt.Println("Daemon:   running")
	fmt.Printf("Servers:  %d registered\n", ps.Daemon.Servers)
	fmt.Printf("Agents:   %d active, %d idle, %d offline\n",
		ps.Agents.Active, ps.Agents.Idle, ps.Agents.Offline)
	fmt.Printf("Sessions: %d active, %d total\n",
		ps.Sessions.Active, ps.Sessions.Total)
	if ps.Daemon.Draining {
		fmt.Println("Readiness: draining")
	} else if ps.Daemon.DrainReady {
		fmt.Println("Readiness: drain ready")
	} else {
		fmt.Println("Readiness: waiting on active work")
	}
	if ps.Health != nil {
		if len(ps.Health.DegradedServers) > 0 {
			fmt.Printf("Health:   degraded servers: %s\n", strings.Join(ps.Health.DegradedServers, ", "))
		} else {
			fmt.Println("Health:   all monitored servers healthy")
		}
		if degradedHealthDetails := degradedHealthServers(ps.Health.Servers); len(degradedHealthDetails) > 0 {
			fmt.Printf("Health:   %s\n", strings.Join(degradedHealthDetails, "; "))
		}
	}
	if ps.Pipelines.Available {
		fmt.Printf("Pipelines: %d running, %d pending, %d passed, %d failed\n",
			ps.Pipelines.Running, ps.Pipelines.Pending, ps.Pipelines.Passed, ps.Pipelines.Failed)
		if ps.Pipelines.LastActivity != "" {
			fmt.Printf("Pipelines: last activity %s\n", ps.Pipelines.LastActivity)
		}
	} else {
		fmt.Println("Pipelines: unavailable")
	}

	if ps.Observability != nil {
		if len(ps.Observability.Warnings) > 0 {
			fmt.Printf("OTel:     warning: %s\n", strings.Join(ps.Observability.Warnings, "; "))
		} else if ps.Observability.OTLPConfigured || ps.Observability.JSONLogsEnabled {
			otelSummary := ps.Observability.OTLPEndpoint
			if otelSummary == "" {
				otelSummary = "otlp disabled"
			}
			logState := "text"
			if ps.Observability.JSONLogsEnabled {
				logState = "json"
			}
			fmt.Printf("OTel:     %s, logs=%s\n", otelSummary, logState)
		}
	}

	hudLabel := "unavailable"
	if ps.HUD.Reachable {
		hudLabel = "running"
	}
	fmt.Printf("HUD:      %s\n", hudLabel)
}

func degradedHealthServers(servers map[string]statusctr.DaemonHealthServer) []string {
	if len(servers) == 0 {
		return nil
	}
	names := make([]string, 0, len(servers))
	for name, status := range servers {
		if status.Healthy && status.ConsecutiveFails == 0 && status.LastError == "" && !status.AutoRestartFailed {
			continue
		}
		names = append(names, fmt.Sprintf("%s(restarts=%d, latency=%.0fms, error=%s)",
			name, status.RestartCount, status.AvgLatencyMs, compactHealthError(status.LastError)))
	}
	sort.Strings(names)
	return names
}

func compactHealthError(err string) string {
	if err == "" {
		return "none"
	}
	return err
}

func showTunnelStatus(socketPath string, jsonOutput bool) error {
	result, err := call(socketPath, "loom/tunnels", nil)
	if err != nil {
		return fmt.Errorf("get tunnel status: %w", err)
	}

	var status struct {
		Tunnels   map[string]any `json:"tunnels"`
		Total     int            `json:"total"`
		Connected int            `json:"connected"`
	}

	if err := json.Unmarshal(result, &status); err != nil {
		return fmt.Errorf("parse tunnel status: %w", err)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(status)
	}

	if status.Total == 0 {
		fmt.Println("No SSH tunnels configured")
		fmt.Println("\nTo configure tunnels, add 'ssh' section to server definitions in registry.yaml:")
		fmt.Println(`
servers:
  - name: remote_k8s
    targets:
      vscode:
        ssh:
          host: "jump.example.com"
          user: "admin"
        command: "kubectl"
        env:
          KUBECONFIG_REMOTE_HOST: "k8s-api.internal:6443"`)
		return nil
	}

	fmt.Printf("SSH Tunnels: %d total, %d connected\n\n", status.Total, status.Connected)

	for name, tunnel := range status.Tunnels {
		t, ok := tunnel.(map[string]any)
		if !ok {
			continue
		}

		state := t["state"]
		localAddr := t["localAddr"]
		remoteHost := t["remoteHost"]
		lastError := t["lastError"]
		reconnectCount := t["reconnectCount"]

		stateStr := fmt.Sprintf("%v", state)
		statusIcon := "?"
		switch stateStr {
		case "connected":
			statusIcon = "✓"
		case "connecting", "reconnecting":
			statusIcon = "⟳"
		case "disconnected":
			statusIcon = "○"
		case "failed":
			statusIcon = "✗"
		}

		fmt.Printf("  %s %s\n", statusIcon, name)
		fmt.Printf("      State: %s\n", stateStr)
		if localAddr != nil && localAddr != "" {
			fmt.Printf("      Local: %s\n", localAddr)
		}
		if remoteHost != nil && remoteHost != "" {
			fmt.Printf("      Remote: %s\n", remoteHost)
		}
		if reconnectCount != nil && reconnectCount.(float64) > 0 {
			fmt.Printf("      Reconnects: %.0f\n", reconnectCount.(float64))
		}
		if lastError != nil && lastError != "" {
			fmt.Printf("      Error: %s\n", lastError)
		}
		fmt.Println()
	}

	return nil
}

func showCacheStats(socketPath string, jsonOutput bool) error {
	result, err := call(socketPath, "loom/cache/stats", nil)
	if err != nil {
		return fmt.Errorf("get cache stats: %w", err)
	}

	var stats struct {
		Enabled   bool  `json:"enabled"`
		Entries   int   `json:"entries"`
		SizeBytes int64 `json:"size_bytes"`
		MaxBytes  int64 `json:"max_bytes"`
		TotalHits int64 `json:"total_hits"`
	}

	if err := json.Unmarshal(result, &stats); err != nil {
		return fmt.Errorf("parse cache stats: %w", err)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(stats)
	}

	if !stats.Enabled {
		fmt.Println("Response caching is disabled")
		fmt.Println("\nTo enable, add to ~/.config/loom/config.yaml:")
		fmt.Println(`
cache:
  enabled: true
  default_ttl_seconds: 60
  max_size_mb: 100`)
		return nil
	}

	fmt.Println("Response Cache Statistics")
	fmt.Println("─────────────────────────")
	fmt.Printf("  Status:     enabled\n")
	fmt.Printf("  Entries:    %d\n", stats.Entries)
	fmt.Printf("  Size:       %s / %s\n", formatBytes(stats.SizeBytes), formatBytes(stats.MaxBytes))
	fmt.Printf("  Total Hits: %d\n", stats.TotalHits)

	if stats.MaxBytes > 0 {
		pct := float64(stats.SizeBytes) / float64(stats.MaxBytes) * 100
		fmt.Printf("  Usage:      %.1f%%\n", pct)
	}

	return nil
}

func clearCache(socketPath string, server string) error {
	params := map[string]any{}
	if server != "" {
		params["server"] = server
	}

	result, err := call(socketPath, "loom/cache/clear", params)
	if err != nil {
		return fmt.Errorf("clear cache: %w", err)
	}

	var resp struct {
		Cleared bool   `json:"cleared"`
		Server  string `json:"server,omitempty"`
		Reason  string `json:"reason,omitempty"`
	}

	if err := json.Unmarshal(result, &resp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if !resp.Cleared {
		fmt.Printf("Cache not cleared: %s\n", resp.Reason)
		return nil
	}

	if resp.Server != "" {
		fmt.Printf("Cache cleared for server: %s\n", resp.Server)
	} else {
		fmt.Println("Cache cleared")
	}

	return nil
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func listServers(socketPath string, outputJSON bool) error {
	result, err := call(socketPath, "loom/servers", nil)
	if err != nil {
		return err
	}

	var resp struct {
		Servers []struct {
			Name        string   `json:"name"`
			Categories  []string `json:"categories,omitempty"`
			Description string   `json:"description,omitempty"`
			Running     bool     `json:"running"`
		} `json:"servers"`
	}

	if err := json.Unmarshal(result, &resp); err != nil {
		return fmt.Errorf("parse servers: %w", err)
	}

	if outputJSON {
		// Output JSON format for programmatic consumption
		out, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal json: %w", err)
		}
		fmt.Println(string(out))
		return nil
	}

	fmt.Printf("%-20s %-8s %s\n", "NAME", "STATUS", "DESCRIPTION")
	fmt.Printf("%-20s %-8s %s\n", "----", "------", "-----------")

	for _, s := range resp.Servers {
		status := "idle"
		if s.Running {
			status = "running"
		}
		desc := s.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}
		fmt.Printf("%-20s %-8s %s\n", s.Name, status, desc)
	}

	return nil
}
