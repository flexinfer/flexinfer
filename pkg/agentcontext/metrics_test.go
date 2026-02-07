package agentcontext

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMetrics(t *testing.T) {
	m := NewMetrics()
	require.NotNil(t, m)
	assert.False(t, m.StartTime.IsZero())
	assert.Equal(t, int64(0), m.SessionsActive.Load())
	assert.Equal(t, int64(0), m.EmbeddingRequests.Load())
}

func TestMetrics_Snapshot(t *testing.T) {
	m := NewMetrics()

	m.SessionsActive.Add(3)
	m.SessionsTotal.Add(5)
	m.EmbeddingRequests.Add(10)
	m.EmbeddingErrors.Add(2)
	m.RecallRequests.Add(20)
	m.RecallHits.Add(15)
	m.RecallMisses.Add(5)
	m.GraphEntitiesAdded.Add(42)
	m.GraphRelationsAdded.Add(17)
	m.GraphQueriesExecuted.Add(8)
	m.WorkflowsStarted.Add(3)
	m.WorkflowsCompleted.Add(2)
	m.WorkflowsFailed.Add(1)
	m.CompressionJobs.Add(7)
	m.CompressionTokensSaved.Add(1000)

	snap := m.Snapshot()

	assert.Equal(t, int64(3), snap.SessionsActive)
	assert.Equal(t, int64(5), snap.SessionsTotal)
	assert.Equal(t, int64(10), snap.EmbeddingRequests)
	assert.Equal(t, int64(2), snap.EmbeddingErrors)
	assert.Equal(t, int64(20), snap.RecallRequests)
	assert.Equal(t, int64(15), snap.RecallHits)
	assert.Equal(t, int64(5), snap.RecallMisses)
	assert.InDelta(t, 0.75, snap.RecallHitRate, 0.001)
	assert.Equal(t, int64(42), snap.GraphEntities)
	assert.Equal(t, int64(17), snap.GraphRelations)
	assert.Equal(t, int64(8), snap.GraphQueries)
	assert.Equal(t, int64(3), snap.WorkflowsStarted)
	assert.Equal(t, int64(2), snap.WorkflowsCompleted)
	assert.Equal(t, int64(1), snap.WorkflowsFailed)
	assert.Equal(t, int64(7), snap.CompressionJobs)
	assert.Equal(t, int64(1000), snap.CompressionTokensSaved)
	assert.Greater(t, snap.UptimeSeconds, int64(-1))
}

func TestMetrics_RecordSearchLatency(t *testing.T) {
	m := NewMetrics()

	// Record some latencies
	latencies := []int64{100, 200, 300, 400, 500, 600, 700, 800, 900, 1000}
	for _, l := range latencies {
		m.RecordSearchLatency(l)
	}

	stats := m.GetSearchLatencyStats()
	assert.Equal(t, 10, stats.Count)
	assert.Equal(t, int64(100), stats.Min)
	assert.Equal(t, int64(1000), stats.Max)
	assert.Equal(t, int64(550), stats.Avg)  // mean of 100..1000
	assert.Equal(t, int64(600), stats.P50)  // sorted[5]
	assert.Equal(t, int64(1000), stats.P90) // sorted[9]
	assert.Equal(t, int64(1000), stats.P99) // sorted[9] (only 10 items)
}

func TestMetrics_RecordSearchLatency_Empty(t *testing.T) {
	m := NewMetrics()

	stats := m.GetSearchLatencyStats()
	assert.Equal(t, 0, stats.Count)
	assert.Equal(t, int64(0), stats.Min)
	assert.Equal(t, int64(0), stats.Max)
}

func TestMetrics_RecordSearchLatency_Overflow(t *testing.T) {
	m := NewMetrics()
	m.maxSearchLatencies = 5

	// Record more than max
	for i := int64(1); i <= 10; i++ {
		m.RecordSearchLatency(i * 100)
	}

	stats := m.GetSearchLatencyStats()
	assert.Equal(t, 5, stats.Count)
	assert.Equal(t, int64(600), stats.Min, "oldest entries should be evicted")
	assert.Equal(t, int64(1000), stats.Max)
}

func TestMetrics_PrometheusFormat(t *testing.T) {
	m := NewMetrics()

	m.SessionsActive.Add(2)
	m.EmbeddingRequests.Add(5)
	m.CompressionTokensSaved.Add(500)

	output := m.PrometheusFormat()

	// Verify it contains expected metric names and HELP/TYPE annotations
	assert.Contains(t, output, "# HELP agent_context_memory_items")
	assert.Contains(t, output, "# TYPE agent_context_memory_items gauge")
	assert.Contains(t, output, "# HELP agent_context_sessions_active")
	assert.Contains(t, output, "agent_context_sessions_active 2")
	assert.Contains(t, output, "agent_context_embedding_requests_total 5")
	assert.Contains(t, output, "agent_context_compression_tokens_saved 500")

	// Verify format validity: each non-empty line should either be a comment or a metric
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		isComment := strings.HasPrefix(line, "#")
		isMetric := strings.Contains(line, " ") && !strings.HasPrefix(line, "#")
		assert.True(t, isComment || isMetric, "unexpected line format: %s", line)
	}
}

func TestMetrics_Reset(t *testing.T) {
	m := NewMetrics()

	m.SessionsActive.Add(5)
	m.EmbeddingRequests.Add(10)
	m.RecordSearchLatency(100)

	m.Reset()

	assert.Equal(t, int64(0), m.SessionsActive.Load())
	assert.Equal(t, int64(0), m.EmbeddingRequests.Load())
	stats := m.GetSearchLatencyStats()
	assert.Equal(t, 0, stats.Count)
}

func TestMetrics_SnapshotHitRateZeroDivision(t *testing.T) {
	m := NewMetrics()
	// No requests — hit rates should be 0, not NaN/panic
	snap := m.Snapshot()
	assert.Equal(t, float64(0), snap.RecallHitRate)
	assert.Equal(t, float64(0), snap.DedupHitRate)
}

func TestGetMetrics_ReturnsSingleton(t *testing.T) {
	m1 := GetMetrics()
	m2 := GetMetrics()
	assert.Same(t, m1, m2, "GetMetrics should return the same global instance")
}
