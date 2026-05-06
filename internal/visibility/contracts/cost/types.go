// Package cost holds the cost/usage telemetry DTOs surfaced by the daemon's
// loom/cost-stats RPC. These are lifted from internal/hud/bridge/daemon.go
// and continue to be re-exported there as type aliases during EPIC 2 (#66).
package cost

// CostStatsResult holds the response from loom/cost-stats.
type CostStatsResult struct {
	Enabled   bool              `json:"enabled"`
	Reason    string            `json:"reason,omitempty"`
	Timestamp string            `json:"timestamp,omitempty"`
	ByAgent   []CostAgentUsage  `json:"by_agent,omitempty"`
	ByServer  []CostServerUsage `json:"by_server,omitempty"`
	Totals    CostTotals        `json:"totals"`
}

// CostAgentUsage summarizes per-agent cost data.
type CostAgentUsage struct {
	AgentID       string `json:"agent_id"`
	CallCount     int64  `json:"call_count"`
	ErrorCount    int64  `json:"error_count"`
	DeniedCount   int64  `json:"denied_count"`
	CachedCount   int64  `json:"cached_count"`
	TotalDuration int64  `json:"total_duration_ms"`
}

// CostServerUsage summarizes per-server cost data.
type CostServerUsage struct {
	Server        string `json:"server"`
	CallCount     int64  `json:"call_count"`
	ErrorCount    int64  `json:"error_count"`
	TotalDuration int64  `json:"total_duration_ms"`
}

// CostTotals summarizes aggregate cost data.
type CostTotals struct {
	CallCount     int64 `json:"call_count"`
	ErrorCount    int64 `json:"error_count"`
	DeniedCount   int64 `json:"denied_count"`
	CachedCount   int64 `json:"cached_count"`
	TotalDuration int64 `json:"total_duration_ms"`
}
