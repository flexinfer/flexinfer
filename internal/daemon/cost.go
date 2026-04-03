// Package daemon provides cost tracking and usage attribution.
package daemon

import (
	"log/slog"
	"sync"
	"time"
)

// CostConfig controls cost tracking and usage attribution.
type CostConfig struct {
	// Enabled activates cost tracking. When false, no usage data is collected.
	Enabled bool `yaml:"enabled"`
}

// DefaultCostConfig returns a disabled cost tracking configuration.
func DefaultCostConfig() CostConfig {
	return CostConfig{
		Enabled: false,
	}
}

// UsageRecord captures a single tool call's resource usage.
type UsageRecord struct {
	AgentID       string `json:"agent_id"`
	AgentType     string `json:"agent_type,omitempty"`
	Server        string `json:"server"`
	Tool          string `json:"tool"`
	DurationMs    int64  `json:"duration_ms"`
	RequestBytes  int64  `json:"request_bytes"`
	ResponseBytes int64  `json:"response_bytes"`
	Status        string `json:"status"` // "success", "error", "denied", "cached"
}

// UsageBucket holds aggregated usage for a single agent+server+tool combination.
type UsageBucket struct {
	AgentID       string `json:"agent_id"`
	Server        string `json:"server"`
	Tool          string `json:"tool"`
	CallCount     int64  `json:"call_count"`
	ErrorCount    int64  `json:"error_count"`
	DeniedCount   int64  `json:"denied_count"`
	CachedCount   int64  `json:"cached_count"`
	TotalDuration int64  `json:"total_duration_ms"`
	TotalReqBytes int64  `json:"total_request_bytes"`
	TotalResBytes int64  `json:"total_response_bytes"`
}

// CostSnapshot is a point-in-time view of usage data.
type CostSnapshot struct {
	Timestamp time.Time     `json:"timestamp"`
	ByAgent   []AgentUsage  `json:"by_agent"`
	ByServer  []ServerUsage `json:"by_server"`
	Totals    TotalUsage    `json:"totals"`
}

// AgentUsage summarizes usage per agent.
type AgentUsage struct {
	AgentID       string `json:"agent_id"`
	CallCount     int64  `json:"call_count"`
	ErrorCount    int64  `json:"error_count"`
	DeniedCount   int64  `json:"denied_count"`
	CachedCount   int64  `json:"cached_count"`
	TotalDuration int64  `json:"total_duration_ms"`
	TotalReqBytes int64  `json:"total_request_bytes"`
	TotalResBytes int64  `json:"total_response_bytes"`
}

// ServerUsage summarizes usage per server.
type ServerUsage struct {
	Server        string `json:"server"`
	CallCount     int64  `json:"call_count"`
	ErrorCount    int64  `json:"error_count"`
	TotalDuration int64  `json:"total_duration_ms"`
	TotalReqBytes int64  `json:"total_request_bytes"`
	TotalResBytes int64  `json:"total_response_bytes"`
}

// TotalUsage summarizes all usage.
type TotalUsage struct {
	CallCount     int64 `json:"call_count"`
	ErrorCount    int64 `json:"error_count"`
	DeniedCount   int64 `json:"denied_count"`
	CachedCount   int64 `json:"cached_count"`
	TotalDuration int64 `json:"total_duration_ms"`
	TotalReqBytes int64 `json:"total_request_bytes"`
	TotalResBytes int64 `json:"total_response_bytes"`
}

// CostTracker collects and aggregates tool call usage data.
type CostTracker struct {
	mu      sync.RWMutex
	buckets map[string]*UsageBucket // key: agent_id|server|tool
	logger  *slog.Logger
}

// NewCostTracker creates a cost tracker. Returns nil if tracking is disabled.
func NewCostTracker(cfg CostConfig, logger *slog.Logger) *CostTracker {
	if !cfg.Enabled {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("cost tracking enabled")
	return &CostTracker{
		buckets: make(map[string]*UsageBucket),
		logger:  logger,
	}
}

// Record adds a usage record to the tracker.
func (ct *CostTracker) Record(rec UsageRecord) {
	key := rec.AgentID + "|" + rec.Server + "|" + rec.Tool

	ct.mu.Lock()
	defer ct.mu.Unlock()

	b, ok := ct.buckets[key]
	if !ok {
		b = &UsageBucket{
			AgentID: rec.AgentID,
			Server:  rec.Server,
			Tool:    rec.Tool,
		}
		ct.buckets[key] = b
	}

	b.CallCount++
	b.TotalDuration += rec.DurationMs
	b.TotalReqBytes += rec.RequestBytes
	b.TotalResBytes += rec.ResponseBytes

	switch rec.Status {
	case "error":
		b.ErrorCount++
	case "denied":
		b.DeniedCount++
	case "cached":
		b.CachedCount++
	}
}

// AddBytes adds byte counts to an existing bucket without creating a new call record.
// This is useful when byte sizes are computed separately from the initial Record call.
func (ct *CostTracker) AddBytes(agentID, server, tool string, reqBytes, resBytes int64) {
	if reqBytes == 0 && resBytes == 0 {
		return
	}
	key := agentID + "|" + server + "|" + tool

	ct.mu.Lock()
	defer ct.mu.Unlock()

	b, ok := ct.buckets[key]
	if !ok {
		// No existing bucket — create one (byte-only record).
		b = &UsageBucket{
			AgentID: agentID,
			Server:  server,
			Tool:    tool,
		}
		ct.buckets[key] = b
	}
	b.TotalReqBytes += reqBytes
	b.TotalResBytes += resBytes
}

// Snapshot returns a point-in-time aggregation of all usage data.
func (ct *CostTracker) Snapshot() CostSnapshot {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	agentMap := make(map[string]*AgentUsage)
	serverMap := make(map[string]*ServerUsage)
	var totals TotalUsage

	for _, b := range ct.buckets {
		// Per-agent aggregation
		a, ok := agentMap[b.AgentID]
		if !ok {
			a = &AgentUsage{AgentID: b.AgentID}
			agentMap[b.AgentID] = a
		}
		a.CallCount += b.CallCount
		a.ErrorCount += b.ErrorCount
		a.DeniedCount += b.DeniedCount
		a.CachedCount += b.CachedCount
		a.TotalDuration += b.TotalDuration
		a.TotalReqBytes += b.TotalReqBytes
		a.TotalResBytes += b.TotalResBytes

		// Per-server aggregation
		s, ok := serverMap[b.Server]
		if !ok {
			s = &ServerUsage{Server: b.Server}
			serverMap[b.Server] = s
		}
		s.CallCount += b.CallCount
		s.ErrorCount += b.ErrorCount
		s.TotalDuration += b.TotalDuration
		s.TotalReqBytes += b.TotalReqBytes
		s.TotalResBytes += b.TotalResBytes

		// Totals
		totals.CallCount += b.CallCount
		totals.ErrorCount += b.ErrorCount
		totals.DeniedCount += b.DeniedCount
		totals.CachedCount += b.CachedCount
		totals.TotalDuration += b.TotalDuration
		totals.TotalReqBytes += b.TotalReqBytes
		totals.TotalResBytes += b.TotalResBytes
	}

	snap := CostSnapshot{
		Timestamp: time.Now().UTC(),
		Totals:    totals,
	}
	for _, a := range agentMap {
		snap.ByAgent = append(snap.ByAgent, *a)
	}
	for _, s := range serverMap {
		snap.ByServer = append(snap.ByServer, *s)
	}
	return snap
}
