package monitor

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// MemoryMonitor tracks memory hierarchy statistics and provides methods
// for browsing, promoting, and demoting memory items.
type MemoryMonitor struct {
	agent  *bridge.AgentBridge
	logger *slog.Logger

	mu    sync.RWMutex
	stats *bridge.MemoryStatsResult

	onRefresh func(*bridge.MemoryStatsResult)

	stopCh   chan struct{}
	stopOnce sync.Once
}

// OnRefresh registers a callback that fires after each successful refresh
// with the new memory stats. Used to broadcast data via SSE.
func (m *MemoryMonitor) OnRefresh(fn func(*bridge.MemoryStatsResult)) {
	m.onRefresh = fn
}

// NewMemoryMonitor creates a MemoryMonitor backed by the given agent bridge.
func NewMemoryMonitor(agent *bridge.AgentBridge, logger *slog.Logger) *MemoryMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &MemoryMonitor{
		agent:  agent,
		logger: logger.With("component", "memory-monitor"),
		stopCh: make(chan struct{}),
	}
}

// Start begins the background polling goroutine at the given interval.
func (m *MemoryMonitor) Start(interval time.Duration) {
	// Run initial refresh asynchronously so HUD/TUI startup is non-blocking
	// when downstream services are slow or unavailable.
	go func() {
		if err := m.Refresh(); err != nil {
			m.logger.Warn("initial memory refresh failed", "error", err)
		}
	}()

	go m.pollLoop(interval)
}

// Stop signals the background goroutine to exit. It is safe to call multiple times.
func (m *MemoryMonitor) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
}

// Stats returns the current memory hierarchy statistics.
// Returns nil if stats have never been successfully fetched.
func (m *MemoryMonitor) Stats() *bridge.MemoryStatsResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.stats == nil {
		return nil
	}

	// Return a copy to avoid data races.
	cp := *m.stats
	return &cp
}

// Recall retrieves memory items from a specific tier and/or matching a query.
// This is a pass-through to the agent bridge (not cached).
func (m *MemoryMonitor) Recall(tier, query string, limit int) ([]bridge.MemoryItem, error) {
	items, err := m.agent.MemoryRecall(tier, query, limit)
	if err != nil {
		return nil, fmt.Errorf("memory recall: %w", err)
	}
	return items, nil
}

// Promote promotes a memory item to a higher tier and triggers a stats
// refresh so the cached stats reflect the change.
func (m *MemoryMonitor) Promote(id string) error {
	if err := m.agent.MemoryPromote(id); err != nil {
		return fmt.Errorf("memory promote %s: %w", id, err)
	}
	// Best-effort refresh after mutation.
	if err := m.Refresh(); err != nil {
		m.logger.Warn("memory: stats refresh after promote failed", "error", err)
	}
	return nil
}

// Demote demotes a memory item to a lower tier and triggers a stats
// refresh so the cached stats reflect the change.
func (m *MemoryMonitor) Demote(id string) error {
	if err := m.agent.MemoryDemote(id); err != nil {
		return fmt.Errorf("memory demote %s: %w", id, err)
	}
	// Best-effort refresh after mutation.
	if err := m.Refresh(); err != nil {
		m.logger.Warn("memory: stats refresh after demote failed", "error", err)
	}
	return nil
}

// Refresh fetches the latest memory hierarchy statistics from the agent bridge.
func (m *MemoryMonitor) Refresh() error {
	stats, err := m.agent.MemoryStats()
	if err != nil {
		m.logger.Warn("memory: failed to fetch memory stats", "error", err)
		return err
	}

	m.mu.Lock()
	m.stats = stats
	m.mu.Unlock()

	// Notify listeners (e.g., SSE hub) with the fresh stats (outside lock).
	if m.onRefresh != nil {
		cp := *stats
		m.onRefresh(&cp)
	}

	return nil
}

// pollLoop runs Refresh on a ticker until stopCh is closed.
// On consecutive errors, it backs off by skipping ticker ticks.
func (m *MemoryMonitor) pollLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	consecutiveErrors := 0
	for {
		select {
		case <-m.stopCh:
			m.logger.Debug("memory monitor stopped")
			return
		case <-ticker.C:
			if err := m.Refresh(); err != nil {
				consecutiveErrors++
				if consecutiveErrors <= 3 {
					m.logger.Warn("memory refresh error", "error", err)
				}
				skipTicks := min(consecutiveErrors-1, 4)
				for range skipTicks {
					select {
					case <-m.stopCh:
						return
					case <-ticker.C:
					}
				}
			} else {
				if consecutiveErrors > 0 {
					m.logger.Info("memory refresh recovered", "after_errors", consecutiveErrors)
				}
				consecutiveErrors = 0
			}
		}
	}
}
