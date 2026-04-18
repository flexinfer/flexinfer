package agentcontext

import (
	"fmt"
	"sort"
	"strings"
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

	// Recall latency by backend (in microseconds)
	recallLatencies    map[string][]int64
	recallLatencyMu    sync.Mutex
	maxRecallLatencies int

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

	// Worktree reconciler
	WorktreeOrphansRemoved   atomic.Int64
	WorktreeArtifactsCleaned atomic.Int64
	WorktreeBytesFreed       atomic.Int64
	WorktreeUntrackedFound   atomic.Int64
	WorktreeReconcileRuns    atomic.Int64

	// Session tracking
	SessionsActive atomic.Int64
	SessionsTotal  atomic.Int64

	// Timestamps
	StartTime time.Time

	// Rerank (Slice A1 / F1) — appended to keep diff surgical.
	RerankRequests atomic.Int64
	RerankTimeouts atomic.Int64
	RerankErrors   atomic.Int64

	// Rerank latency by backend (in microseconds).
	rerankLatencies    map[string][]int64
	rerankLatencyMu    sync.Mutex
	maxRerankLatencies int

	// Compaction fallbacks (Slice B / F2) — counts LLM-mode failures that
	// degraded to the extractive path.
	CompactionFallbacks atomic.Int64

	// Handoff triggers (Slice C1 / F5) — per-reason fired/suppressed
	// counters. Keyed by AutoHandoffReason* (input_tokens, cost,
	// stalled). Appended to keep diff surgical.
	handoffTriggerMu         sync.Mutex
	handoffTriggerFired      map[string]*atomic.Int64
	handoffTriggerSuppressed map[string]*atomic.Int64

	// Fleet dispatch (Slice C2 / F6) — counts agent_task_dispatch invocations
	// and mismatch outcomes (no_candidates or no_capability_match).
	FleetDispatchRequests   atomic.Int64
	FleetDispatchMismatches atomic.Int64
}

// NewMetrics creates a new metrics instance
func NewMetrics() *Metrics {
	return &Metrics{
		maxSearchLatencies: 1000, // Keep last 1000 latencies
		searchLatencies:    make([]int64, 0, 1000),
		maxRecallLatencies: 1000,
		recallLatencies:    make(map[string][]int64),
		maxRerankLatencies: 1000,
		rerankLatencies:    make(map[string][]int64),
		StartTime:          time.Now(),
	}
}

// RecordSearchLatency records a search latency in microseconds
func (m *Metrics) RecordSearchLatency(latencyMicros int64) {
	m.searchLatencyMu.Lock()
	defer m.searchLatencyMu.Unlock()

	if m.maxSearchLatencies > 0 && len(m.searchLatencies) >= m.maxSearchLatencies {
		// Remove oldest entry
		m.searchLatencies = m.searchLatencies[1:]
	}
	m.searchLatencies = append(m.searchLatencies, latencyMicros)
}

// RecordRecallLatency records a recall latency for the given backend in microseconds.
func (m *Metrics) RecordRecallLatency(backend string, latency time.Duration) {
	if backend == "" {
		backend = "unknown"
	}

	latencyMicros := latency.Microseconds()
	if latencyMicros < 0 {
		latencyMicros = 0
	}

	m.recallLatencyMu.Lock()
	defer m.recallLatencyMu.Unlock()

	if m.recallLatencies == nil {
		m.recallLatencies = make(map[string][]int64)
	}

	samples := m.recallLatencies[backend]
	if m.maxRecallLatencies > 0 && len(samples) >= m.maxRecallLatencies {
		samples = samples[1:]
	}
	samples = append(samples, latencyMicros)
	m.recallLatencies[backend] = samples
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

// GetRecallLatencyStats returns recall latency statistics grouped by backend.
func (m *Metrics) GetRecallLatencyStats() map[string]LatencyStats {
	m.recallLatencyMu.Lock()
	defer m.recallLatencyMu.Unlock()

	if len(m.recallLatencies) == 0 {
		return map[string]LatencyStats{}
	}

	stats := make(map[string]LatencyStats, len(m.recallLatencies))
	for backend, samples := range m.recallLatencies {
		stats[backend] = summarizeLatencySamples(samples)
	}
	return stats
}

func summarizeLatencySamples(samples []int64) LatencyStats {
	if len(samples) == 0 {
		return LatencyStats{}
	}

	sorted := make([]int64, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

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

	// Recall latency by backend
	RecallLatencyByBackend map[string]LatencyStats `json:"recall_latency_by_backend"`

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

	// Worktree reconciler
	WorktreeOrphansRemoved   int64 `json:"worktree_orphans_removed"`
	WorktreeArtifactsCleaned int64 `json:"worktree_artifacts_cleaned"`
	WorktreeBytesFreed       int64 `json:"worktree_bytes_freed"`
	WorktreeUntrackedFound   int64 `json:"worktree_untracked_found"`
	WorktreeReconcileRuns    int64 `json:"worktree_reconcile_runs"`

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
		SearchLatency:            m.GetSearchLatencyStats(),
		RecallLatencyByBackend:   m.GetRecallLatencyStats(),
		EmbeddingRequests:        m.EmbeddingRequests.Load(),
		EmbeddingTokens:          m.EmbeddingTokens.Load(),
		EmbeddingErrors:          m.EmbeddingErrors.Load(),
		RecallRequests:           recallReqs,
		RecallHits:               recallHits,
		RecallMisses:             m.RecallMisses.Load(),
		RecallHitRate:            recallHitRate,
		RecallTruncated:          m.RecallTruncated.Load(),
		GraphEntities:            m.GraphEntitiesAdded.Load(),
		GraphRelations:           m.GraphRelationsAdded.Load(),
		GraphQueries:             m.GraphQueriesExecuted.Load(),
		WorkflowsStarted:         m.WorkflowsStarted.Load(),
		WorkflowsCompleted:       m.WorkflowsCompleted.Load(),
		WorkflowsFailed:          m.WorkflowsFailed.Load(),
		WorkflowSteps:            m.WorkflowStepsTotal.Load(),
		CompressionJobs:          m.CompressionJobs.Load(),
		CompressionTokensSaved:   m.CompressionTokensSaved.Load(),
		DedupChecks:              dedupChecks,
		DedupHits:                dedupHits,
		DedupHitRate:             dedupHitRate,
		StalenessChecks:          m.StalenessChecks.Load(),
		StaleEntriesFound:        m.StaleEntriesFound.Load(),
		EntriesRefreshed:         m.EntriesRefreshed.Load(),
		WorktreeOrphansRemoved:   m.WorktreeOrphansRemoved.Load(),
		WorktreeArtifactsCleaned: m.WorktreeArtifactsCleaned.Load(),
		WorktreeBytesFreed:       m.WorktreeBytesFreed.Load(),
		WorktreeUntrackedFound:   m.WorktreeUntrackedFound.Load(),
		WorktreeReconcileRuns:    m.WorktreeReconcileRuns.Load(),
		SessionsActive:           m.SessionsActive.Load(),
		SessionsTotal:            m.SessionsTotal.Load(),
		UptimeSeconds:            int64(time.Since(m.StartTime).Seconds()),
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

	m.recallLatencyMu.Lock()
	m.recallLatencies = make(map[string][]int64)
	m.recallLatencyMu.Unlock()

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

	m.WorktreeOrphansRemoved.Store(0)
	m.WorktreeArtifactsCleaned.Store(0)
	m.WorktreeBytesFreed.Store(0)
	m.WorktreeUntrackedFound.Store(0)
	m.WorktreeReconcileRuns.Store(0)

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

# HELP agent_context_recall_duration_seconds Recall duration by backend in seconds
# TYPE agent_context_recall_duration_seconds summary
` + formatRecallLatencyMetrics(snap.RecallLatencyByBackend) + `

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

# HELP agent_context_worktree_orphans_removed_total Worktrees removed by reconciler
# TYPE agent_context_worktree_orphans_removed_total counter
agent_context_worktree_orphans_removed_total ` + formatInt64(snap.WorktreeOrphansRemoved) + `

# HELP agent_context_worktree_bytes_freed_total Bytes freed by worktree cleanup
# TYPE agent_context_worktree_bytes_freed_total counter
agent_context_worktree_bytes_freed_total ` + formatInt64(snap.WorktreeBytesFreed) + `

# HELP agent_context_worktree_untracked_found_total Untracked worktrees detected
# TYPE agent_context_worktree_untracked_found_total counter
agent_context_worktree_untracked_found_total ` + formatInt64(snap.WorktreeUntrackedFound) + `

# HELP agent_context_worktree_reconcile_runs_total Worktree reconciliation runs
# TYPE agent_context_worktree_reconcile_runs_total counter
agent_context_worktree_reconcile_runs_total ` + formatInt64(snap.WorktreeReconcileRuns) + `

# HELP agent_context_sessions_active Active sessions
# TYPE agent_context_sessions_active gauge
agent_context_sessions_active ` + formatInt64(snap.SessionsActive) + `

# HELP agent_context_uptime_seconds Service uptime in seconds
# TYPE agent_context_uptime_seconds counter
agent_context_uptime_seconds ` + formatInt64(snap.UptimeSeconds) + `
` + m.PrometheusFormatRerank()
}

func formatInt64(v int64) string {
	return fmt.Sprintf("%d", v)
}

func formatFloat64(v float64) string {
	return fmt.Sprintf("%.6f", v)
}

func formatRecallLatencyMetrics(stats map[string]LatencyStats) string {
	if len(stats) == 0 {
		return ""
	}

	backends := make([]string, 0, len(stats))
	for backend := range stats {
		backends = append(backends, backend)
	}
	sort.Strings(backends)

	var b strings.Builder
	for _, backend := range backends {
		s := stats[backend]
		if s.Count == 0 {
			continue
		}
		b.WriteString(`agent_context_recall_duration_seconds{backend="`)
		b.WriteString(backend)
		b.WriteString(`",quantile="0.5"} `)
		b.WriteString(formatFloat64(float64(s.P50) / 1e6))
		b.WriteString("\n")
		b.WriteString(`agent_context_recall_duration_seconds{backend="`)
		b.WriteString(backend)
		b.WriteString(`",quantile="0.9"} `)
		b.WriteString(formatFloat64(float64(s.P90) / 1e6))
		b.WriteString("\n")
		b.WriteString(`agent_context_recall_duration_seconds{backend="`)
		b.WriteString(backend)
		b.WriteString(`",quantile="0.99"} `)
		b.WriteString(formatFloat64(float64(s.P99) / 1e6))
		b.WriteString("\n")
		b.WriteString(`agent_context_recall_duration_seconds_count{backend="`)
		b.WriteString(backend)
		b.WriteString(`"} `)
		b.WriteString(formatInt64(int64(s.Count)))
		b.WriteString("\n")
	}
	return b.String()
}

// Global metrics instance
var globalMetrics = NewMetrics()

// GetMetrics returns the global metrics instance
func GetMetrics() *Metrics {
	return globalMetrics
}

// =========================================================================
// Rerank metrics (Slice A1 / F1)
// Appended so the recall reranker can record request/timeout/error counts
// and per-backend latency without reshuffling existing blocks.
// =========================================================================

// RecordRerankLatency records a rerank latency for the given backend in
// microseconds. Backend defaults to "unknown" when empty.
func (m *Metrics) RecordRerankLatency(backend string, latencyMicros int64) {
	if backend == "" {
		backend = "unknown"
	}
	if latencyMicros < 0 {
		latencyMicros = 0
	}

	m.rerankLatencyMu.Lock()
	defer m.rerankLatencyMu.Unlock()

	if m.rerankLatencies == nil {
		m.rerankLatencies = make(map[string][]int64)
	}
	if m.maxRerankLatencies == 0 {
		m.maxRerankLatencies = 1000
	}

	samples := m.rerankLatencies[backend]
	if m.maxRerankLatencies > 0 && len(samples) >= m.maxRerankLatencies {
		samples = samples[1:]
	}
	samples = append(samples, latencyMicros)
	m.rerankLatencies[backend] = samples
}

// GetRerankLatencyStats returns rerank latency stats grouped by backend.
func (m *Metrics) GetRerankLatencyStats() map[string]LatencyStats {
	m.rerankLatencyMu.Lock()
	defer m.rerankLatencyMu.Unlock()

	if len(m.rerankLatencies) == 0 {
		return map[string]LatencyStats{}
	}

	stats := make(map[string]LatencyStats, len(m.rerankLatencies))
	for backend, samples := range m.rerankLatencies {
		stats[backend] = summarizeLatencySamples(samples)
	}
	return stats
}

// PrometheusFormatRerank returns the rerank-specific Prometheus lines. The
// top-level PrometheusFormat concatenates this block at the end so existing
// metrics remain byte-stable.
func (m *Metrics) PrometheusFormatRerank() string {
	var b strings.Builder
	b.WriteString("\n# HELP loom_agentcontext_rerank_requests_total Total rerank requests\n")
	b.WriteString("# TYPE loom_agentcontext_rerank_requests_total counter\n")
	b.WriteString("loom_agentcontext_rerank_requests_total ")
	b.WriteString(formatInt64(m.RerankRequests.Load()))
	b.WriteString("\n")

	b.WriteString("\n# HELP loom_agentcontext_rerank_timeouts_total Total rerank timeouts\n")
	b.WriteString("# TYPE loom_agentcontext_rerank_timeouts_total counter\n")
	b.WriteString("loom_agentcontext_rerank_timeouts_total ")
	b.WriteString(formatInt64(m.RerankTimeouts.Load()))
	b.WriteString("\n")

	b.WriteString("\n# HELP loom_agentcontext_rerank_errors_total Total rerank errors (non-timeout)\n")
	b.WriteString("# TYPE loom_agentcontext_rerank_errors_total counter\n")
	b.WriteString("loom_agentcontext_rerank_errors_total ")
	b.WriteString(formatInt64(m.RerankErrors.Load()))
	b.WriteString("\n")

	// Latency summary by backend.
	stats := m.GetRerankLatencyStats()
	if len(stats) > 0 {
		b.WriteString("\n# HELP loom_agentcontext_rerank_duration_seconds Rerank duration by backend in seconds\n")
		b.WriteString("# TYPE loom_agentcontext_rerank_duration_seconds summary\n")

		backends := make([]string, 0, len(stats))
		for backend := range stats {
			backends = append(backends, backend)
		}
		sort.Strings(backends)

		for _, backend := range backends {
			s := stats[backend]
			if s.Count == 0 {
				continue
			}
			b.WriteString(`loom_agentcontext_rerank_duration_seconds{backend="`)
			b.WriteString(backend)
			b.WriteString(`",quantile="0.5"} `)
			b.WriteString(formatFloat64(float64(s.P50) / 1e6))
			b.WriteString("\n")
			b.WriteString(`loom_agentcontext_rerank_duration_seconds{backend="`)
			b.WriteString(backend)
			b.WriteString(`",quantile="0.9"} `)
			b.WriteString(formatFloat64(float64(s.P90) / 1e6))
			b.WriteString("\n")
			b.WriteString(`loom_agentcontext_rerank_duration_seconds{backend="`)
			b.WriteString(backend)
			b.WriteString(`",quantile="0.99"} `)
			b.WriteString(formatFloat64(float64(s.P99) / 1e6))
			b.WriteString("\n")
			b.WriteString(`loom_agentcontext_rerank_duration_seconds_count{backend="`)
			b.WriteString(backend)
			b.WriteString(`"} `)
			b.WriteString(formatInt64(int64(s.Count)))
			b.WriteString("\n")
		}
	}

	// F2: compaction fallback counter (appended for minimal diff).
	b.WriteString("\n# HELP loom_agentcontext_compaction_fallback_total Total LLM compaction failures that degraded to extractive\n")
	b.WriteString("# TYPE loom_agentcontext_compaction_fallback_total counter\n")
	b.WriteString("loom_agentcontext_compaction_fallback_total ")
	b.WriteString(formatInt64(m.CompactionFallbacks.Load()))
	b.WriteString("\n")

	// F5/Slice C1: handoff trigger counters by reason. Appended for
	// surgical diff — PrometheusFormatRerank is already called from the
	// top-level PrometheusFormat so these lines reach the scraper.
	b.WriteString(m.prometheusFormatHandoffTriggers())

	// F6: fleet dispatch counters (appended for minimal diff).
	b.WriteString("\n# HELP loom_fleet_dispatch_requests_total Total agent_task_dispatch requests\n")
	b.WriteString("# TYPE loom_fleet_dispatch_requests_total counter\n")
	b.WriteString("loom_fleet_dispatch_requests_total ")
	b.WriteString(formatInt64(m.FleetDispatchRequests.Load()))
	b.WriteString("\n")
	b.WriteString("# HELP loom_fleet_dispatch_mismatch_total agent_task_dispatch calls that returned no_capability_match or no_candidates\n")
	b.WriteString("# TYPE loom_fleet_dispatch_mismatch_total counter\n")
	b.WriteString("loom_fleet_dispatch_mismatch_total ")
	b.WriteString(formatInt64(m.FleetDispatchMismatches.Load()))
	b.WriteString("\n")

	return b.String()
}

// IncHandoffTriggerFired bumps the per-reason fired counter. Reason
// values are bounded to the AutoHandoffReason* constants defined in
// handoff_triggers.go.
func (m *Metrics) IncHandoffTriggerFired(reason string) {
	if reason == "" {
		reason = "unknown"
	}
	m.handoffTriggerMu.Lock()
	defer m.handoffTriggerMu.Unlock()
	if m.handoffTriggerFired == nil {
		m.handoffTriggerFired = make(map[string]*atomic.Int64)
	}
	c, ok := m.handoffTriggerFired[reason]
	if !ok {
		c = &atomic.Int64{}
		m.handoffTriggerFired[reason] = c
	}
	c.Add(1)
}

// IncHandoffTriggerSuppressed bumps the per-reason suppressed counter.
// Suppressions happen when a breach is observed but the gate does not
// fire (first breach of a run, within debounce, etc).
func (m *Metrics) IncHandoffTriggerSuppressed(reason string) {
	if reason == "" {
		reason = "unknown"
	}
	m.handoffTriggerMu.Lock()
	defer m.handoffTriggerMu.Unlock()
	if m.handoffTriggerSuppressed == nil {
		m.handoffTriggerSuppressed = make(map[string]*atomic.Int64)
	}
	c, ok := m.handoffTriggerSuppressed[reason]
	if !ok {
		c = &atomic.Int64{}
		m.handoffTriggerSuppressed[reason] = c
	}
	c.Add(1)
}

// HandoffTriggerFiredCount returns the fired count for a reason. Used
// in tests and aggregated dashboards.
func (m *Metrics) HandoffTriggerFiredCount(reason string) int64 {
	m.handoffTriggerMu.Lock()
	defer m.handoffTriggerMu.Unlock()
	if c, ok := m.handoffTriggerFired[reason]; ok {
		return c.Load()
	}
	return 0
}

// HandoffTriggerSuppressedCount returns the suppressed count for a reason.
func (m *Metrics) HandoffTriggerSuppressedCount(reason string) int64 {
	m.handoffTriggerMu.Lock()
	defer m.handoffTriggerMu.Unlock()
	if c, ok := m.handoffTriggerSuppressed[reason]; ok {
		return c.Load()
	}
	return 0
}

func (m *Metrics) prometheusFormatHandoffTriggers() string {
	m.handoffTriggerMu.Lock()
	defer m.handoffTriggerMu.Unlock()

	var b strings.Builder
	b.WriteString("\n# HELP loom_handoff_trigger_fired_total Total auto-handoff trigger fires by reason\n")
	b.WriteString("# TYPE loom_handoff_trigger_fired_total counter\n")
	// Emit 0 lines for all well-known reasons so the series exist even
	// before any fires. Keeps Grafana dashboards simple.
	for _, reason := range handoffTriggerReasons() {
		v := int64(0)
		if c, ok := m.handoffTriggerFired[reason]; ok {
			v = c.Load()
		}
		b.WriteString(`loom_handoff_trigger_fired_total{reason="`)
		b.WriteString(reason)
		b.WriteString(`"} `)
		b.WriteString(formatInt64(v))
		b.WriteString("\n")
	}

	b.WriteString("\n# HELP loom_handoff_trigger_suppressed_total Total auto-handoff trigger suppressions by reason\n")
	b.WriteString("# TYPE loom_handoff_trigger_suppressed_total counter\n")
	for _, reason := range handoffTriggerReasons() {
		v := int64(0)
		if c, ok := m.handoffTriggerSuppressed[reason]; ok {
			v = c.Load()
		}
		b.WriteString(`loom_handoff_trigger_suppressed_total{reason="`)
		b.WriteString(reason)
		b.WriteString(`"} `)
		b.WriteString(formatInt64(v))
		b.WriteString("\n")
	}
	return b.String()
}

// handoffTriggerReasons returns the stable list of reason labels in a
// sorted order for deterministic Prometheus output.
func handoffTriggerReasons() []string {
	return []string{
		AutoHandoffReasonCost,
		AutoHandoffReasonInputTokens,
		AutoHandoffReasonStalled,
	}
}
