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
	// BackendDispatchTotal tracks whether subagent dispatch went to the
	// local FlexInfer path or to a real headless-agent pod via the
	// SpawnBridge. Labels: backend (flexinfer|claude-code|codex|gemini),
	// outcome (success|error|timeout). Surfaces the post-Slice-3 routing
	// decision in HUD/Prometheus so operators can see which domains fan
	// out to pods vs. in-process LLM calls.
	BackendDispatchTotal *prometheus.CounterVec

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
		BackendDispatchTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loom_weaver_backend_dispatch_total",
			Help: "Weaver subagent dispatches by execution backend and outcome.",
		}, []string{"backend", "outcome"}),
	}

	if reg != nil {
		reg.MustRegister(
			m.QueriesTotal,
			m.SubagentCallTotal,
			m.TokensTotal,
			m.ErrorsTotal,
			m.QueryDuration,
			m.SubagentDuration,
			m.BackendDispatchTotal,
		)
	}
	return m
}

// RecordBackendDispatch increments the backend-dispatch counter. backend is
// one of "flexinfer", "claude-code", "codex", "gemini"; outcome is
// "success", "error", or "timeout". Nil-safe so runSubAgent can call it
// unconditionally even before Metrics is wired.
func (m *Metrics) RecordBackendDispatch(backend, outcome string) {
	if m == nil || m.BackendDispatchTotal == nil {
		return
	}
	m.BackendDispatchTotal.WithLabelValues(backend, outcome).Inc()
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

// F10 auto-compose metrics (append-only).

// AutoComposeMetrics holds counters specific to the auto-compose feature.
// It is a separate struct so it can be registered independently from the
// main Metrics struct without modifying that constructor.
type AutoComposeMetrics struct {
	// Total auto-compose attempts labeled by outcome: success|refused|empty.
	Total *prometheus.CounterVec
	// Cumulative count of domains dispatched via auto-compose.
	DomainsUsed prometheus.Counter

	// Atomic lifetime counters for direct reads.
	successCount atomic.Int64
	refusedCount atomic.Int64
	emptyCount   atomic.Int64
	domainsUsed  atomic.Int64
}

// NewAutoComposeMetrics creates and (optionally) registers auto-compose metrics.
func NewAutoComposeMetrics(reg prometheus.Registerer) *AutoComposeMetrics {
	m := &AutoComposeMetrics{
		Total: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loom_weaver_auto_compose_total",
			Help: "Total weaver auto-compose attempts by outcome.",
		}, []string{"outcome"}),
		DomainsUsed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "loom_weaver_auto_compose_domains_used_total",
			Help: "Cumulative domains dispatched via auto-compose.",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.Total, m.DomainsUsed)
	}
	return m
}

// RecordOutcome bumps the auto-compose outcome counter.
// outcome is one of: "success", "refused", "empty".
func (m *AutoComposeMetrics) RecordOutcome(outcome string) {
	if m == nil {
		return
	}
	if m.Total != nil {
		m.Total.WithLabelValues(outcome).Inc()
	}
	switch outcome {
	case "success":
		m.successCount.Add(1)
	case "refused":
		m.refusedCount.Add(1)
	case "empty":
		m.emptyCount.Add(1)
	}
}

// RecordDomainsUsed bumps the domains-used counter by n.
func (m *AutoComposeMetrics) RecordDomainsUsed(n int) {
	if m == nil || n <= 0 {
		return
	}
	if m.DomainsUsed != nil {
		m.DomainsUsed.Add(float64(n))
	}
	m.domainsUsed.Add(int64(n))
}

// Summary returns lifetime auto-compose counts.
func (m *AutoComposeMetrics) Summary() map[string]any {
	if m == nil {
		return nil
	}
	return map[string]any{
		"success":      m.successCount.Load(),
		"refused":      m.refusedCount.Load(),
		"empty":        m.emptyCount.Load(),
		"domains_used": m.domainsUsed.Load(),
	}
}
