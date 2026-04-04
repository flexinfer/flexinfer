package weaver

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds Prometheus metrics for the weaver system.
type Metrics struct {
	QueriesTotal      *prometheus.CounterVec
	SubagentCallTotal *prometheus.CounterVec
	TokensTotal       *prometheus.CounterVec
	ErrorsTotal       *prometheus.CounterVec
	QueryDuration     *prometheus.HistogramVec
	SubagentDuration  *prometheus.HistogramVec
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
