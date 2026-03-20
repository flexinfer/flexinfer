package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// MemoryMonitor tracks memory hierarchy statistics and provides methods
// for browsing, promoting, and demoting memory items.
type MemoryMonitor struct {
	BaseMonitor[*bridge.MemoryStatsResult]
	agent        *bridge.AgentBridge
	tokenHistory *RingBuffer // sparkline history of TotalTokens
}

// NewMemoryMonitor creates a MemoryMonitor backed by the given agent bridge.
func NewMemoryMonitor(agent *bridge.AgentBridge, logger *slog.Logger) *MemoryMonitor {
	m := &MemoryMonitor{
		agent:        agent,
		tokenHistory: NewRingBuffer(DefaultRingSize),
	}
	m.InitBase(logger, nil, "memory-monitor")
	return m
}

// Start begins the background polling goroutine at the given interval.
func (m *MemoryMonitor) Start(interval time.Duration) {
	m.BaseMonitor.Start(interval, m.refresh)
}

// Stats returns the current memory hierarchy statistics.
// Returns nil if stats have never been successfully fetched.
func (m *MemoryMonitor) Stats() *bridge.MemoryStatsResult {
	m.RLock()
	defer m.RUnlock()

	stats := m.GetSnapshot()
	if stats == nil {
		return nil
	}

	// Return a copy to avoid data races.
	cp := *stats
	return &cp
}

// TokenHistory returns the sparkline history of TotalTokens readings
// (oldest first). Returns nil if no readings have been recorded yet.
func (m *MemoryMonitor) TokenHistory() []float64 {
	m.RLock()
	defer m.RUnlock()
	return m.tokenHistory.Values()
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
		m.Logger.Warn("memory: stats refresh after promote failed", "error", err)
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
		m.Logger.Warn("memory: stats refresh after demote failed", "error", err)
	}
	return nil
}

// refresh fetches the latest memory hierarchy statistics from the agent bridge.
func (m *MemoryMonitor) refresh(_ context.Context) (*bridge.MemoryStatsResult, error) {
	stats, err := m.agent.MemoryStats()
	if err != nil {
		m.Logger.Warn("memory: failed to fetch memory stats", "error", err)
		return nil, err
	}
	return stats, nil
}

// Update overrides BaseMonitor.Update to also record token history.
func (m *MemoryMonitor) Update(stats *bridge.MemoryStatsResult) {
	m.Lock()
	m.SetSnapshot(stats)
	if stats != nil {
		m.tokenHistory.Push(float64(stats.TotalTokens))
	}
	m.Unlock()

	if stats != nil {
		cp := *stats
		m.FireOnRefresh(&cp)
	}
}

// Refresh forces an immediate refresh. Exposed for external callers.
func (m *MemoryMonitor) Refresh() error {
	stats, err := m.refresh(context.Background())
	if err != nil {
		return err
	}
	m.Update(stats)
	return nil
}
