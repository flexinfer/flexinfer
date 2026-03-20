package monitor

import (
	"context"
	"log/slog"
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
	BaseMonitor[CostSnapshot]
	client *bridge.DaemonClient
}

// NewCostMonitor creates a CostMonitor backed by the given daemon client.
func NewCostMonitor(client *bridge.DaemonClient, logger *slog.Logger) *CostMonitor {
	m := &CostMonitor{client: client}
	m.InitBase(logger, nil, "cost-monitor")
	return m
}

// Start begins the background polling goroutine at the given interval.
func (m *CostMonitor) Start(interval time.Duration) {
	m.BaseMonitor.Start(interval, m.refresh)
}

// refresh fetches the latest cost stats from the daemon via bridge.
func (m *CostMonitor) refresh(_ context.Context) (CostSnapshot, error) {
	result, err := m.client.CostStats()
	if err != nil {
		return CostSnapshot{}, err
	}
	if result == nil {
		// Return current snapshot unchanged when the daemon has no data.
		return m.Snapshot(), nil
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

	return snap, nil
}

// Refresh forces an immediate refresh. Exposed for external callers.
func (m *CostMonitor) Refresh() error {
	snap, err := m.refresh(context.Background())
	if err != nil {
		return err
	}
	m.Update(snap)
	return nil
}
