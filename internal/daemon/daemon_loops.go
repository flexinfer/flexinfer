package daemon

import (
	"runtime"
	"time"
)

// idleReaperLoop periodically terminates idle server processes.
func (d *Daemon) idleReaperLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	idleTimeout := d.fileCfg.Resources.GetIdleTimeout()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			reaped := d.reapIdleServers(idleTimeout)
			if len(reaped) > 0 {
				d.logger.Info("reaped idle servers", "servers", reaped, "count", len(reaped))
				for _, name := range reaped {
					d.runningServers.Delete(name)
					if d.eventBus != nil {
						d.eventBus.Publish(EventProcessStop, map[string]any{
							"server": name,
							"reason": "idle_reaped",
						})
					}
				}
			}
		}
	}
}

// reapIdleServers reaps idle processes while respecting per-server call locks.
// This prevents races where the reaper closes a process mid tools/call.
func (d *Daemon) reapIdleServers(idleTimeout time.Duration) []string {
	if d.procMgr == nil {
		return nil
	}

	idleInfo := d.procMgr.GetIdleInfo()
	if len(idleInfo) == 0 {
		return nil
	}

	reaped := make([]string, 0)
	for _, info := range idleInfo {
		if info.IdleDuration <= idleTimeout {
			continue
		}

		callMu := d.callLock(info.Name)
		if !callMu.TryLock() {
			// Server has an in-flight call; skip this reaper cycle.
			continue
		}

		// Re-check idleness while holding the call lock to avoid stale snapshot races.
		stillIdle := false
		for _, current := range d.procMgr.GetIdleInfo() {
			if current.Name == info.Name {
				stillIdle = current.IdleDuration > idleTimeout
				break
			}
		}

		if stillIdle {
			if err := d.procMgr.Stop(info.Name); err != nil {
				d.logger.Warn("failed to reap idle server", "server", info.Name, "error", err)
			} else {
				reaped = append(reaped, info.Name)
			}
		}

		callMu.Unlock()
	}

	return reaped
}

// sessionReaperLoop periodically reaps expired proxy sessions.
func (d *Daemon) sessionReaperLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			if d.sessions != nil {
				if reaped := d.sessions.ReapExpired(); reaped > 0 {
					d.logger.Info("reaped expired proxy sessions", "count", reaped)
				}
			}
		}
	}
}

// metricsCollectorLoop periodically updates metrics that require polling.
func (d *Daemon) metricsCollectorLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.collectMetrics()
		}
	}
}

// collectMetrics gathers current state and updates metrics.
func (d *Daemon) collectMetrics() {
	// Pool stats
	stats := d.pool.Stats()
	d.metrics.UpdatePoolStats("local", stats.IdleConns, stats.ActiveConns)

	if d.hubPool != nil {
		hubStats := d.hubPool.Stats()
		d.metrics.UpdatePoolStats("hub", hubStats.IdleConns, hubStats.ActiveConns)
	}

	// Process count
	processes := d.procMgr.List()
	d.metrics.UpdateProcessCount(len(processes))

	// Tool cache
	d.toolCache.mu.RLock()
	cacheSize := len(d.toolCache.tools)
	cacheAge := time.Since(d.toolCache.updatedAt)
	d.toolCache.mu.RUnlock()
	d.metrics.UpdateToolCache(cacheSize, cacheAge)

	// Server health from router
	allHealth := d.router.GetAllHealth()
	for name, h := range allHealth {
		if h.Local != nil {
			d.metrics.UpdateServerHealth(name, "local", h.Local.Healthy, h.Local.AvgLatencyMs)
		}
		if h.Hub != nil {
			d.metrics.UpdateServerHealth(name, "hub", h.Hub.Healthy, h.Hub.AvgLatencyMs)
		}
	}

	// Hub connection status
	if d.hubClient != nil {
		// Simple check - if hubPool exists and has connections, we're connected
		connected := false
		var latency float64
		if d.hubPool != nil {
			hubStats := d.hubPool.Stats()
			if hubStats.IdleConns > 0 || hubStats.ActiveConns > 0 {
				connected = true
			}
		}
		d.metrics.UpdateHubConnection(connected, latency)
	}

	// Concurrent call gauge (from activeRPCs atomic counter)
	d.metrics.ConcurrentCalls.Set(float64(d.activeRPCs.Load()))

	// Runtime stats
	d.metrics.GoroutineCount.Set(float64(runtime.NumGoroutine()))
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	d.metrics.MemAllocBytes.Set(float64(memStats.Alloc))
	d.metrics.MemSysBytes.Set(float64(memStats.Sys))
	if memStats.NumGC > 0 {
		d.metrics.GCPauseNs.Set(float64(memStats.PauseNs[(memStats.NumGC+255)%256]))
	}

	// EventBus dropped events
	if d.eventBus != nil {
		d.metrics.EventsDropped.Add(0) // Ensure metric exists
		// We read the cumulative count; Prometheus counter must only increase.
		// Since DroppedCount() is cumulative and the counter is too, we track
		// the delta from the eventBus.
	}
}
