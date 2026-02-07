// Package daemon provides the main Loom daemon orchestrator.
package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ServerHealthStatus represents the health state of a server.
type ServerHealthStatus struct {
	Name              string    `json:"name"`
	Healthy           bool      `json:"healthy"`
	LastCheck         time.Time `json:"last_check"`
	LastHealthy       time.Time `json:"last_healthy,omitempty"`
	ConsecutiveFails  int       `json:"consecutive_fails"`
	TotalChecks       int       `json:"total_checks"`
	TotalFailures     int       `json:"total_failures"`
	AvgLatencyMs      float64   `json:"avg_latency_ms"`
	LastError         string    `json:"last_error,omitempty"`
	RestartCount      int       `json:"restart_count"`
	LastRestart       time.Time `json:"last_restart,omitempty"`
	AutoRestartFailed bool      `json:"auto_restart_failed,omitempty"`
}

// HealthMonitor monitors server health and handles auto-restarts.
type HealthMonitor struct {
	daemon   *Daemon
	logger   *slog.Logger
	statuses map[string]*ServerHealthStatus
	mu       sync.RWMutex

	// Configuration
	checkInterval      time.Duration
	healthyThreshold   int // consecutive successes to mark healthy
	unhealthyThreshold int // consecutive failures to mark unhealthy
	restartThreshold   int // failures before auto-restart
	maxRestarts        int // max restarts before giving up
	restartCooldown    time.Duration

	// Control
	done chan struct{}
	wg   sync.WaitGroup
}

// HealthMonitorConfig holds configuration for the health monitor.
type HealthMonitorConfig struct {
	CheckInterval      time.Duration
	HealthyThreshold   int
	UnhealthyThreshold int
	RestartThreshold   int
	MaxRestarts        int
	RestartCooldown    time.Duration
}

// DefaultHealthMonitorConfig returns sensible defaults.
func DefaultHealthMonitorConfig() HealthMonitorConfig {
	return HealthMonitorConfig{
		CheckInterval:      30 * time.Second,
		HealthyThreshold:   2,
		UnhealthyThreshold: 3,
		RestartThreshold:   3,
		MaxRestarts:        3,
		RestartCooldown:    5 * time.Minute,
	}
}

// NewHealthMonitor creates a new health monitor.
func NewHealthMonitor(daemon *Daemon, cfg HealthMonitorConfig) *HealthMonitor {
	return &HealthMonitor{
		daemon:             daemon,
		logger:             daemon.logger.With("component", "health-monitor"),
		statuses:           make(map[string]*ServerHealthStatus),
		checkInterval:      cfg.CheckInterval,
		healthyThreshold:   cfg.HealthyThreshold,
		unhealthyThreshold: cfg.UnhealthyThreshold,
		restartThreshold:   cfg.RestartThreshold,
		maxRestarts:        cfg.MaxRestarts,
		restartCooldown:    cfg.RestartCooldown,
		done:               make(chan struct{}),
	}
}

// Start begins the health monitoring loop.
func (h *HealthMonitor) Start() {
	h.wg.Add(1)
	go h.monitorLoop()
}

// Stop gracefully stops the health monitor.
func (h *HealthMonitor) Stop() {
	close(h.done)
	h.wg.Wait()
}

// GetStatus returns the health status for a server.
func (h *HealthMonitor) GetStatus(serverName string) *ServerHealthStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if status, ok := h.statuses[serverName]; ok {
		// Return a copy
		copy := *status
		return &copy
	}
	return nil
}

// GetAllStatuses returns health status for all monitored servers.
func (h *HealthMonitor) GetAllStatuses() map[string]*ServerHealthStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make(map[string]*ServerHealthStatus, len(h.statuses))
	for name, status := range h.statuses {
		copy := *status
		result[name] = &copy
	}
	return result
}

// monitorLoop runs the health check loop.
func (h *HealthMonitor) monitorLoop() {
	defer h.wg.Done()

	ticker := time.NewTicker(h.checkInterval)
	defer ticker.Stop()

	// Initial check
	h.checkAllServers()

	for {
		select {
		case <-h.done:
			return
		case <-ticker.C:
			h.checkAllServers()
		}
	}
}

// checkAllServers performs health checks on all registered servers.
func (h *HealthMonitor) checkAllServers() {
	if h.daemon.registry == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup

	for _, server := range h.daemon.registry.Servers {
		wg.Add(1)
		go func(serverName string) {
			defer wg.Done()
			h.checkServer(ctx, serverName)
		}(server.Name)
	}

	wg.Wait()
}

// checkServer performs a health check on a single server.
func (h *HealthMonitor) checkServer(ctx context.Context, serverName string) {
	start := time.Now()

	// Try to list tools as a health check
	_, err := h.daemon.fetchServerTools(ctx, serverName)

	latencyMs := float64(time.Since(start).Milliseconds())
	now := time.Now()

	h.mu.Lock()
	defer h.mu.Unlock()

	status, ok := h.statuses[serverName]
	if !ok {
		status = &ServerHealthStatus{
			Name:    serverName,
			Healthy: true,
		}
		h.statuses[serverName] = status
	}

	status.LastCheck = now
	status.TotalChecks++

	// Update rolling average latency
	if status.TotalChecks == 1 {
		status.AvgLatencyMs = latencyMs
	} else {
		// Exponential moving average
		alpha := 0.2
		status.AvgLatencyMs = alpha*latencyMs + (1-alpha)*status.AvgLatencyMs
	}

	if err != nil {
		status.ConsecutiveFails++
		status.TotalFailures++
		status.LastError = err.Error()

		// Update Prometheus metrics
		if h.daemon.metrics != nil {
			h.daemon.metrics.ServerHealth.WithLabelValues(serverName, "local").Set(0)
			h.daemon.metrics.ServerFailures.WithLabelValues(serverName, "local", "health_check").Inc()
		}

		// Check if we should mark as unhealthy
		if status.ConsecutiveFails >= h.unhealthyThreshold && status.Healthy {
			status.Healthy = false
			h.logger.Warn("server marked unhealthy",
				"server", serverName,
				"consecutive_failures", status.ConsecutiveFails,
				"error", err)

			// Emit health event
			if h.daemon.eventBus != nil {
				h.daemon.eventBus.Publish(EventServerHealth, map[string]any{
					"server":  serverName,
					"healthy": false,
					"error":   err.Error(),
				})
			}
		}

		// Check if we should auto-restart
		if status.ConsecutiveFails >= h.restartThreshold && !status.AutoRestartFailed {
			h.handleRestart(serverName, status)
		}
	} else {
		// Success
		wasUnhealthy := !status.Healthy
		status.ConsecutiveFails = 0
		status.LastHealthy = now
		status.LastError = ""

		// Update Prometheus metrics
		if h.daemon.metrics != nil {
			h.daemon.metrics.ServerHealth.WithLabelValues(serverName, "local").Set(1)
			h.daemon.metrics.ServerSuccesses.WithLabelValues(serverName, "local").Inc()
			h.daemon.metrics.ServerLatency.WithLabelValues(serverName, "local").Set(latencyMs)
		}

		// Mark as healthy after threshold successes
		if !status.Healthy {
			// Count as one success toward recovery
			if wasUnhealthy {
				status.Healthy = true
				status.AutoRestartFailed = false
				h.logger.Info("server recovered", "server", serverName)

				// Emit recovery event
				if h.daemon.eventBus != nil {
					h.daemon.eventBus.Publish(EventServerHealth, map[string]any{
						"server":  serverName,
						"healthy": true,
					})
				}
			}
		}
	}
}

// handleRestart attempts to restart an unhealthy server.
func (h *HealthMonitor) handleRestart(serverName string, status *ServerHealthStatus) {
	// Check cooldown
	if !status.LastRestart.IsZero() && time.Since(status.LastRestart) < h.restartCooldown {
		return
	}

	// Check max restarts
	if status.RestartCount >= h.maxRestarts {
		h.logger.Error("max restarts exceeded, giving up",
			"server", serverName,
			"restarts", status.RestartCount)
		status.AutoRestartFailed = true
		return
	}

	h.logger.Info("attempting auto-restart",
		"server", serverName,
		"restart_count", status.RestartCount+1)

	// Perform restart via process manager
	if h.daemon.procMgr != nil {
		// Stop the server
		if err := h.daemon.procMgr.Stop(serverName); err != nil {
			h.logger.Warn("failed to stop server during restart", "server", serverName, "error", err)
		}

		// Give it a moment to clean up
		time.Sleep(1 * time.Second)

		// Start it again - it will be started on next request
		status.RestartCount++
		status.LastRestart = time.Now()

		// Update Prometheus metrics
		if h.daemon.metrics != nil {
			h.daemon.metrics.ProcessRestarts.WithLabelValues(serverName).Inc()
		}
	}
}

// ResetRestartCount resets the restart count for a server (e.g., after manual intervention).
func (h *HealthMonitor) ResetRestartCount(serverName string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if status, ok := h.statuses[serverName]; ok {
		status.RestartCount = 0
		status.AutoRestartFailed = false
	}
}
