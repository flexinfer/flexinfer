package coordinator

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds Prometheus metrics for the coordinator subsystem.
type Metrics struct {
	SubsystemRuns       *prometheus.CounterVec
	LLMCallDuration     *prometheus.HistogramVec
	PollDuration        prometheus.Histogram
	CircuitState        prometheus.Gauge
	CircuitTrips        prometheus.Counter
	Healthy             prometheus.Gauge
	ConsecutiveFailures prometheus.Gauge
	SummarizedSessions  prometheus.Gauge
	FallbackSummaries   prometheus.Counter

	registry *prometheus.Registry
}

// NewMetrics creates a Metrics instance with all metrics registered.
func NewMetrics() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
	}

	m.SubsystemRuns = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "coordinator",
			Name:      "subsystem_runs_total",
			Help:      "Total subsystem runs by subsystem name and status",
		},
		[]string{"subsystem", "status"},
	)

	m.LLMCallDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "loom",
			Subsystem: "coordinator",
			Name:      "llm_call_duration_seconds",
			Help:      "LLM call latency distribution per subsystem",
			Buckets:   prometheus.ExponentialBuckets(0.1, 2, 10), // 100ms to ~51s
		},
		[]string{"subsystem"},
	)

	m.PollDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "loom",
			Subsystem: "coordinator",
			Name:      "poll_duration_seconds",
			Help:      "Total poll cycle duration",
			Buckets:   prometheus.ExponentialBuckets(0.01, 2, 12), // 10ms to ~20s
		},
	)

	m.CircuitState = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "coordinator",
			Name:      "circuit_state",
			Help:      "Circuit breaker state: 0=closed, 1=open, 2=half-open",
		},
	)

	m.CircuitTrips = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "coordinator",
			Name:      "circuit_trips_total",
			Help:      "Total circuit breaker open events",
		},
	)

	m.Healthy = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "coordinator",
			Name:      "healthy",
			Help:      "Coordinator health: 1=healthy, 0=unhealthy",
		},
	)

	m.ConsecutiveFailures = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "coordinator",
			Name:      "consecutive_failures",
			Help:      "Current consecutive poll failure count",
		},
	)

	m.SummarizedSessions = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "coordinator",
			Name:      "summarized_sessions",
			Help:      "Number of sessions in the in-memory summarized map",
		},
	)

	m.FallbackSummaries = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "coordinator",
			Name:      "fallback_summaries_total",
			Help:      "Total extractive fallback summary count",
		},
	)

	m.registry.MustRegister(
		m.SubsystemRuns,
		m.LLMCallDuration,
		m.PollDuration,
		m.CircuitState,
		m.CircuitTrips,
		m.Healthy,
		m.ConsecutiveFailures,
		m.SummarizedSessions,
		m.FallbackSummaries,
	)

	return m
}

// Handler returns an HTTP handler for the /metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// RecordSubsystemRun records a subsystem execution with success or error status.
func (m *Metrics) RecordSubsystemRun(subsystem string, err error) {
	status := "success"
	if err != nil {
		status = "error"
	}
	m.SubsystemRuns.WithLabelValues(subsystem, status).Inc()
}

// RecordLLMCall records the duration of an LLM call for a subsystem.
func (m *Metrics) RecordLLMCall(subsystem string, duration time.Duration) {
	m.LLMCallDuration.WithLabelValues(subsystem).Observe(duration.Seconds())
}

// RecordPollCycle records the total duration of a poll cycle.
func (m *Metrics) RecordPollCycle(duration time.Duration) {
	m.PollDuration.Observe(duration.Seconds())
}

// UpdateHealth updates the healthy gauge and consecutive failure count.
func (m *Metrics) UpdateHealth(healthy bool, failures int) {
	if healthy {
		m.Healthy.Set(1)
	} else {
		m.Healthy.Set(0)
	}
	m.ConsecutiveFailures.Set(float64(failures))
}

// UpdateCircuit updates the circuit state gauge. Increments the trip
// counter when the state transitions to open.
func (m *Metrics) UpdateCircuit(state CircuitState) {
	m.CircuitState.Set(float64(state))
	if state == StateOpen {
		m.CircuitTrips.Inc()
	}
}
