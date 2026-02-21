// status.go contains functions for displaying status information about the daemon.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func showStatus(socketPath string) error {
	result, err := call(socketPath, "loom/status", nil)
	if err != nil {
		fmt.Println("Daemon: not running")
		return nil
	}

	var status struct {
		Running             bool     `json:"running"`
		Servers             int      `json:"servers"`
		ActiveConns         int      `json:"activeConns"`
		IdleConns           int      `json:"idleConns"`
		Processes           []string `json:"processes"`
		ActiveRPCs          int64    `json:"activeRPCs"`
		DrainReady          bool     `json:"drainReady"`
		DaemonEpoch         int64    `json:"daemonEpoch"`
		ActiveProxySessions int      `json:"activeProxySessions"`
	}

	if err := json.Unmarshal(result, &status); err != nil {
		return fmt.Errorf("parse status: %w", err)
	}

	fmt.Println("Daemon: running")
	fmt.Printf("Socket: %s\n", socketPath)
	fmt.Printf("Epoch: %d, Proxy Sessions: %d\n", status.DaemonEpoch, status.ActiveProxySessions)
	fmt.Printf("Servers: %d registered\n", status.Servers)
	fmt.Printf("Connections: %d active, %d idle\n", status.ActiveConns, status.IdleConns)
	fmt.Printf("RPCs: %d active, drain_ready=%v\n", status.ActiveRPCs, status.DrainReady)
	if len(status.Processes) > 0 {
		fmt.Printf("Processes: %v\n", status.Processes)
	}

	return nil
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
