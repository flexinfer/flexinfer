package weaver

import (
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds Prometheus metrics for the weaver system.
type Metrics struct {
	QueriesTotal      *prometheus.CounterVec
	SubagentCallTotal *prometheus.CounterVec
	TokensTotal       *prometheus.CounterVec
	ErrorsTotal       *prometheus.CounterVec
	QueryDuration     *prometheus.HistogramVec
	SubagentDuration  *prometheus.HistogramVec

	// Atomic lifetime counters for direct reads (HUD metrics endpoint).
	lifetimeQueries   atomic.Int64
	lifetimeErrors    atomic.Int64
	lifetimeTokens    atomic.Int64
	lifetimeLatencyMs atomic.Int64
}

// NewMetrics creates and registers weaver Prometheus metrics.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		QueriesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loom_weaver_queries_total",
			Help: "Total weaver queries by status.",
		}, []string{"status"}),
		SubagentCallTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loom_weaver_subagent_calls_total",
			Help: "Total subagent invocations by domain and status.",
		}, []string{"domain", "status"}),
		TokensTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loom_weaver_tokens_total",
			Help: "Total tokens consumed by direction.",
		}, []string{"domain", "direction"}),
		ErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loom_weaver_errors_total",
			Help: "Total weaver errors by domain.",
		}, []string{"domain"}),
		QueryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "loom_weaver_query_duration_seconds",
			Help:    "Query latency distribution.",
			Buckets: prometheus.DefBuckets,
		}, []string{"status"}),
		SubagentDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "loom_weaver_subagent_duration_seconds",
			Help:    "Per-subagent latency distribution.",
			Buckets: prometheus.DefBuckets,
		}, []string{"domain"}),
	}

	if reg != nil {
		reg.MustRegister(
			m.QueriesTotal,
			m.SubagentCallTotal,
			m.TokensTotal,
			m.ErrorsTotal,
			m.QueryDuration,
			m.SubagentDuration,
		)
	}
	return m
}

// RecordQuery increments lifetime query counters.
func (m *Metrics) RecordQuery(status string, latencyMs int64, tokens int) {
	m.lifetimeQueries.Add(1)
	m.lifetimeLatencyMs.Add(latencyMs)
	m.lifetimeTokens.Add(int64(tokens))
	if status == "error" {
		m.lifetimeErrors.Add(1)
	}
}

// Summary returns lifetime metric values for the HUD.
func (m *Metrics) Summary() map[string]any {
	total := m.lifetimeQueries.Load()
	errors := m.lifetimeErrors.Load()
	totalLatency := m.lifetimeLatencyMs.Load()
	tokens := m.lifetimeTokens.Load()

	var avgLatency float64
	var errorRate float64
	if total > 0 {
		avgLatency = float64(totalLatency) / float64(total)
		errorRate = float64(errors) / float64(total)
	}

	return map[string]any{
		"total_queries":  total,
		"avg_latency_ms": avgLatency,
		"error_rate":     errorRate,
		"total_tokens":   tokens,
		"error_count":    errors,
	}
}
