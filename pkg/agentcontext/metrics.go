package agentcontext

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics provides observability for the agent context service
type Metrics struct {
	// Memory tier usage
	WorkingMemoryItems    atomic.Int64
	WorkingMemoryTokens   atomic.Int64
	ShortTermMemoryItems  atomic.Int64
	ShortTermMemoryTokens atomic.Int64
	LongTermMemoryItems   atomic.Int64
	LongTermMemoryTokens  atomic.Int64

	// Search latency (in microseconds)
	searchLatencies    []int64
	searchLatencyMu    sync.Mutex
	maxSearchLatencies int

	// Embedding costs
	EmbeddingRequests atomic.Int64
	EmbeddingTokens   atomic.Int64
	EmbeddingErrors   atomic.Int64

	// Recall quality
	RecallRequests  atomic.Int64
	RecallHits      atomic.Int64
	RecallMisses    atomic.Int64
	RecallTruncated atomic.Int64

	// Graph operations
	GraphEntitiesAdded   atomic.Int64
	GraphRelationsAdded  atomic.Int64
	GraphQueriesExecuted atomic.Int64

	// Workflow operations
	WorkflowsStarted   atomic.Int64
	WorkflowsCompleted atomic.Int64
	WorkflowsFailed    atomic.Int64
	WorkflowStepsTotal atomic.Int64

	// Compression operations
	CompressionJobs        atomic.Int64
	CompressionTokensSaved atomic.Int64

	// Deduplication
	DedupChecks atomic.Int64
	DedupHits   atomic.Int64

	// Staleness checks
	StalenessChecks   atomic.Int64
	StaleEntriesFound atomic.Int64
	EntriesRefreshed  atomic.Int64

	// Session tracking
	SessionsActive atomic.Int64
	SessionsTotal  atomic.Int64

	// Timestamps
	StartTime time.Time
}

// NewMetrics creates a new metrics instance
func NewMetrics() *Metrics {
	return &Metrics{
		maxSearchLatencies: 1000, // Keep last 1000 latencies
		searchLatencies:    make([]int64, 0, 1000),
		StartTime:          time.Now(),
	}
}

// RecordSearchLatency records a search latency in microseconds
func (m *Metrics) RecordSearchLatency(latencyMicros int64) {
	m.searchLatencyMu.Lock()
	defer m.searchLatencyMu.Unlock()

	if len(m.searchLatencies) >= m.maxSearchLatencies {
		// Remove oldest entry
		m.searchLatencies = m.searchLatencies[1:]
	}
	m.searchLatencies = append(m.searchLatencies, latencyMicros)
}

// GetSearchLatencyStats returns latency statistics
func (m *Metrics) GetSearchLatencyStats() LatencyStats {
	m.searchLatencyMu.Lock()
	defer m.searchLatencyMu.Unlock()

	if len(m.searchLatencies) == 0 {
		return LatencyStats{}
	}

	// Sort for percentile calculation
	sorted := make([]int64, len(m.searchLatencies))
	copy(sorted, m.searchLatencies)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	var sum int64
	for _, v := range sorted {
		sum += v
	}

	return LatencyStats{
		Count: len(sorted),
		Min:   sorted[0],
		Max:   sorted[len(sorted)-1],
		Avg:   sum / int64(len(sorted)),
		P50:   sorted[len(sorted)*50/100],
		P90:   sorted[len(sorted)*90/100],
		P99:   sorted[len(sorted)*99/100],
	}
}

// LatencyStats contains latency histogram statistics
type LatencyStats struct {
	Count int   `json:"count"`
	Min   int64 `json:"min_us"`
	Max   int64 `json:"max_us"`
	Avg   int64 `json:"avg_us"`
	P50   int64 `json:"p50_us"`
	P90   int64 `json:"p90_us"`
	P99   int64 `json:"p99_us"`
}

// MetricsSnapshot captures all metrics at a point in time
type MetricsSnapshot struct {
	// Memory
	WorkingMemory   MemoryTierMetrics `json:"working_memory"`
	ShortTermMemory MemoryTierMetrics `json:"short_term_memory"`
	LongTermMemory  MemoryTierMetrics `json:"long_term_memory"`

	// Search
	SearchLatency LatencyStats `json:"search_latency"`

	// Embedding
	EmbeddingRequests int64 `json:"embedding_requests"`
	EmbeddingTokens   int64 `json:"embedding_tokens"`
	EmbeddingErrors   int64 `json:"embedding_errors"`

	// Recall
	RecallRequests  int64   `json:"recall_requests"`
	RecallHits      int64   `json:"recall_hits"`
	RecallMisses    int64   `json:"recall_misses"`
	RecallHitRate   float64 `json:"recall_hit_rate"`
	RecallTruncated int64   `json:"recall_truncated"`

	// Graph
	GraphEntities  int64 `json:"graph_entities"`
	GraphRelations int64 `json:"graph_relations"`
	GraphQueries   int64 `json:"graph_queries"`

	// Workflows
	WorkflowsStarted   int64 `json:"workflows_started"`
	WorkflowsCompleted int64 `json:"workflows_completed"`
	WorkflowsFailed    int64 `json:"workflows_failed"`
	WorkflowSteps      int64 `json:"workflow_steps"`

	// Compression
	CompressionJobs        int64 `json:"compression_jobs"`
	CompressionTokensSaved int64 `json:"compression_tokens_saved"`

	// Deduplication
	DedupChecks  int64   `json:"dedup_checks"`
	DedupHits    int64   `json:"dedup_hits"`
	DedupHitRate float64 `json:"dedup_hit_rate"`

	// Staleness
	StalenessChecks   int64 `json:"staleness_checks"`
	StaleEntriesFound int64 `json:"stale_entries_found"`
	EntriesRefreshed  int64 `json:"entries_refreshed"`

	// Sessions
	SessionsActive int64 `json:"sessions_active"`
	SessionsTotal  int64 `json:"sessions_total"`

	// Uptime
	UptimeSeconds int64 `json:"uptime_seconds"`
}

// MemoryTierMetrics contains metrics for a memory tier
type MemoryTierMetrics struct {
	Items  int64 `json:"items"`
	Tokens int64 `json:"tokens"`
}

// Snapshot captures current metrics state
func (m *Metrics) Snapshot() MetricsSnapshot {
	recallReqs := m.RecallRequests.Load()
	recallHits := m.RecallHits.Load()
	dedupChecks := m.DedupChecks.Load()
	dedupHits := m.DedupHits.Load()

	var recallHitRate, dedupHitRate float64
	if recallReqs > 0 {
		recallHitRate = float64(recallHits) / float64(recallReqs)
	}
	if dedupChecks > 0 {
		dedupHitRate = float64(dedupHits) / float64(dedupChecks)
	}

	return MetricsSnapshot{
		WorkingMemory: MemoryTierMetrics{
			Items:  m.WorkingMemoryItems.Load(),
			Tokens: m.WorkingMemoryTokens.Load(),
		},
		ShortTermMemory: MemoryTierMetrics{
			Items:  m.ShortTermMemoryItems.Load(),
			Tokens: m.ShortTermMemoryTokens.Load(),
		},
		LongTermMemory: MemoryTierMetrics{
			Items:  m.LongTermMemoryItems.Load(),
			Tokens: m.LongTermMemoryTokens.Load(),
		},
		SearchLatency:          m.GetSearchLatencyStats(),
		EmbeddingRequests:      m.EmbeddingRequests.Load(),
		EmbeddingTokens:        m.EmbeddingTokens.Load(),
		EmbeddingErrors:        m.EmbeddingErrors.Load(),
		RecallRequests:         recallReqs,
		RecallHits:             recallHits,
		RecallMisses:           m.RecallMisses.Load(),
		RecallHitRate:          recallHitRate,
		RecallTruncated:        m.RecallTruncated.Load(),
		GraphEntities:          m.GraphEntitiesAdded.Load(),
		GraphRelations:         m.GraphRelationsAdded.Load(),
		GraphQueries:           m.GraphQueriesExecuted.Load(),
		WorkflowsStarted:       m.WorkflowsStarted.Load(),
		WorkflowsCompleted:     m.WorkflowsCompleted.Load(),
		WorkflowsFailed:        m.WorkflowsFailed.Load(),
		WorkflowSteps:          m.WorkflowStepsTotal.Load(),
		CompressionJobs:        m.CompressionJobs.Load(),
		CompressionTokensSaved: m.CompressionTokensSaved.Load(),
		DedupChecks:            dedupChecks,
		DedupHits:              dedupHits,
		DedupHitRate:           dedupHitRate,
		StalenessChecks:        m.StalenessChecks.Load(),
		StaleEntriesFound:      m.StaleEntriesFound.Load(),
		EntriesRefreshed:       m.EntriesRefreshed.Load(),
		SessionsActive:         m.SessionsActive.Load(),
		SessionsTotal:          m.SessionsTotal.Load(),
		UptimeSeconds:          int64(time.Since(m.StartTime).Seconds()),
	}
}

// Reset resets all metrics
func (m *Metrics) Reset() {
	m.WorkingMemoryItems.Store(0)
	m.WorkingMemoryTokens.Store(0)
	m.ShortTermMemoryItems.Store(0)
	m.ShortTermMemoryTokens.Store(0)
	m.LongTermMemoryItems.Store(0)
	m.LongTermMemoryTokens.Store(0)

	m.searchLatencyMu.Lock()
	m.searchLatencies = make([]int64, 0, m.maxSearchLatencies)
	m.searchLatencyMu.Unlock()

	m.EmbeddingRequests.Store(0)
	m.EmbeddingTokens.Store(0)
	m.EmbeddingErrors.Store(0)

	m.RecallRequests.Store(0)
	m.RecallHits.Store(0)
	m.RecallMisses.Store(0)
	m.RecallTruncated.Store(0)

	m.GraphEntitiesAdded.Store(0)
	m.GraphRelationsAdded.Store(0)
	m.GraphQueriesExecuted.Store(0)

	m.WorkflowsStarted.Store(0)
	m.WorkflowsCompleted.Store(0)
	m.WorkflowsFailed.Store(0)
	m.WorkflowStepsTotal.Store(0)

	m.CompressionJobs.Store(0)
	m.CompressionTokensSaved.Store(0)

	m.DedupChecks.Store(0)
	m.DedupHits.Store(0)

	m.StalenessChecks.Store(0)
	m.StaleEntriesFound.Store(0)
	m.EntriesRefreshed.Store(0)

	m.SessionsActive.Store(0)
	m.SessionsTotal.Store(0)

	m.StartTime = time.Now()
}

// PrometheusFormat returns metrics in Prometheus text format
func (m *Metrics) PrometheusFormat() string {
	snap := m.Snapshot()

	return `# HELP agent_context_memory_items Number of items in memory tier
# TYPE agent_context_memory_items gauge
agent_context_memory_items{tier="working"} ` + formatInt64(snap.WorkingMemory.Items) + `
agent_context_memory_items{tier="short_term"} ` + formatInt64(snap.ShortTermMemory.Items) + `
agent_context_memory_items{tier="long_term"} ` + formatInt64(snap.LongTermMemory.Items) + `

# HELP agent_context_memory_tokens Total tokens in memory tier
# TYPE agent_context_memory_tokens gauge
agent_context_memory_tokens{tier="working"} ` + formatInt64(snap.WorkingMemory.Tokens) + `
agent_context_memory_tokens{tier="short_term"} ` + formatInt64(snap.ShortTermMemory.Tokens) + `
agent_context_memory_tokens{tier="long_term"} ` + formatInt64(snap.LongTermMemory.Tokens) + `

# HELP agent_context_search_latency_us Search latency in microseconds
# TYPE agent_context_search_latency_us summary
agent_context_search_latency_us{quantile="0.5"} ` + formatInt64(snap.SearchLatency.P50) + `
agent_context_search_latency_us{quantile="0.9"} ` + formatInt64(snap.SearchLatency.P90) + `
agent_context_search_latency_us{quantile="0.99"} ` + formatInt64(snap.SearchLatency.P99) + `
agent_context_search_latency_us_count ` + formatInt64(int64(snap.SearchLatency.Count)) + `

# HELP agent_context_embedding_requests_total Total embedding requests
# TYPE agent_context_embedding_requests_total counter
agent_context_embedding_requests_total ` + formatInt64(snap.EmbeddingRequests) + `

# HELP agent_context_embedding_tokens_total Total tokens embedded
# TYPE agent_context_embedding_tokens_total counter
agent_context_embedding_tokens_total ` + formatInt64(snap.EmbeddingTokens) + `

# HELP agent_context_embedding_errors_total Total embedding errors
# TYPE agent_context_embedding_errors_total counter
agent_context_embedding_errors_total ` + formatInt64(snap.EmbeddingErrors) + `

# HELP agent_context_recall_requests_total Total recall requests
# TYPE agent_context_recall_requests_total counter
agent_context_recall_requests_total ` + formatInt64(snap.RecallRequests) + `

# HELP agent_context_recall_hit_rate Recall hit rate
# TYPE agent_context_recall_hit_rate gauge
agent_context_recall_hit_rate ` + formatFloat64(snap.RecallHitRate) + `

# HELP agent_context_graph_entities_total Total graph entities
# TYPE agent_context_graph_entities_total counter
agent_context_graph_entities_total ` + formatInt64(snap.GraphEntities) + `

# HELP agent_context_graph_relations_total Total graph relations
# TYPE agent_context_graph_relations_total counter
agent_context_graph_relations_total ` + formatInt64(snap.GraphRelations) + `

# HELP agent_context_workflows_total Total workflows by status
# TYPE agent_context_workflows_total counter
agent_context_workflows_total{status="started"} ` + formatInt64(snap.WorkflowsStarted) + `
agent_context_workflows_total{status="completed"} ` + formatInt64(snap.WorkflowsCompleted) + `
agent_context_workflows_total{status="failed"} ` + formatInt64(snap.WorkflowsFailed) + `

# HELP agent_context_compression_tokens_saved Tokens saved by compression
# TYPE agent_context_compression_tokens_saved counter
agent_context_compression_tokens_saved ` + formatInt64(snap.CompressionTokensSaved) + `

# HELP agent_context_dedup_hit_rate Deduplication hit rate
# TYPE agent_context_dedup_hit_rate gauge
agent_context_dedup_hit_rate ` + formatFloat64(snap.DedupHitRate) + `

# HELP agent_context_sessions_active Active sessions
# TYPE agent_context_sessions_active gauge
agent_context_sessions_active ` + formatInt64(snap.SessionsActive) + `

# HELP agent_context_uptime_seconds Service uptime in seconds
# TYPE agent_context_uptime_seconds counter
agent_context_uptime_seconds ` + formatInt64(snap.UptimeSeconds) + `
`
}

func formatInt64(v int64) string {
	return fmt.Sprintf("%d", v)
}

func formatFloat64(v float64) string {
	return fmt.Sprintf("%.6f", v)
}

// Global metrics instance
var globalMetrics = NewMetrics()

// GetMetrics returns the global metrics instance
func GetMetrics() *Metrics {
	return globalMetrics
}
