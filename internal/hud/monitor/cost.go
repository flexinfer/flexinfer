package monitor

import (
	"log/slog"
	"sync"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// CostSnapshot is a point-in-time view of cost/usage data from the daemon.
type CostSnapshot struct {
	Enabled       bool                `json:"enabled"`
	Timestamp     string              `json:"timestamp,omitempty"`
	TotalCalls    int64               `json:"total_calls"`
	TotalErrors   int64               `json:"total_errors"`
	TotalDenied   int64               `json:"total_denied"`
	TotalCached   int64               `json:"total_cached"`
	TotalDuration int64               `json:"total_duration_ms"`
	ByAgent       []CostAgentSummary  `json:"by_agent,omitempty"`
	ByServer      []CostServerSummary `json:"by_server,omitempty"`
}

// CostAgentSummary is per-agent usage for the frontend.
type CostAgentSummary struct {
	AgentID   string `json:"agent_id"`
	CallCount int64  `json:"call_count"`
	Errors    int64  `json:"errors"`
	Denied    int64  `json:"denied"`
	Cached    int64  `json:"cached"`
}

// CostServerSummary is per-server usage for the frontend.
type CostServerSummary struct {
	Server    string `json:"server"`
	CallCount int64  `json:"call_count"`
	Errors    int64  `json:"errors"`
}

// CostMonitor polls the daemon's cost-stats RPC and maintains a cached snapshot.
type CostMonitor struct {
	client *bridge.DaemonClient
	logger *slog.Logger

	mu       sync.RWMutex
	snapshot CostSnapshot

	onRefresh func(CostSnapshot)

	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewCostMonitor creates a CostMonitor backed by the given daemon client.
func NewCostMonitor(client *bridge.DaemonClient, logger *slog.Logger) *CostMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &CostMonitor{
		client: client,
		logger: logger.With("component", "cost-monitor"),
		stopCh: make(chan struct{}),
	}
}

// OnRefresh registers a callback that fires after each successful refresh.
func (m *CostMonitor) OnRefresh(fn func(CostSnapshot)) {
	m.onRefresh = fn
}

// Start begins the background polling goroutine at the given interval.
func (m *CostMonitor) Start(interval time.Duration) {
	go func() {
		if err := m.Refresh(); err != nil {
			m.logger.Debug("initial cost refresh failed", "error", err)
		}
	}()
	go m.pollLoop(interval)
}

// Stop signals the background goroutine to exit.
func (m *CostMonitor) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
}

// Snapshot returns the current cached cost data.
func (m *CostMonitor) Snapshot() CostSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

// Refresh fetches the latest cost stats from the daemon via bridge.
func (m *CostMonitor) Refresh() error {
	result, err := m.client.CostStats()
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}

	snap := CostSnapshot{
		Enabled:       result.Enabled,
		Timestamp:     result.Timestamp,
		TotalCalls:    result.Totals.CallCount,
		TotalErrors:   result.Totals.ErrorCount,
		TotalDenied:   result.Totals.DeniedCount,
		TotalCached:   result.Totals.CachedCount,
		TotalDuration: result.Totals.TotalDuration,
	}

	for _, a := range result.ByAgent {
		snap.ByAgent = append(snap.ByAgent, CostAgentSummary{
			AgentID:   a.AgentID,
			CallCount: a.CallCount,
			Errors:    a.ErrorCount,
			Denied:    a.DeniedCount,
			Cached:    a.CachedCount,
		})
	}
	for _, s := range result.ByServer {
		snap.ByServer = append(snap.ByServer, CostServerSummary{
			Server:    s.Server,
			CallCount: s.CallCount,
			Errors:    s.ErrorCount,
		})
	}

	m.mu.Lock()
	m.snapshot = snap
	m.mu.Unlock()

	if m.onRefresh != nil {
		m.onRefresh(snap)
	}

	return nil
}

// pollLoop runs Refresh on a ticker until stopCh is closed.
func (m *CostMonitor) pollLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	consecutiveErrors := 0
	for {
		select {
		case <-m.stopCh:
			m.logger.Debug("cost monitor stopped")
			return
		case <-ticker.C:
			if err := m.Refresh(); err != nil {
				consecutiveErrors++
				if consecutiveErrors <= 3 {
					m.logger.Debug("cost refresh error", "error", err)
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
					m.logger.Info("cost refresh recovered", "after_errors", consecutiveErrors)
				}
				consecutiveErrors = 0
			}
		}
	}
}
