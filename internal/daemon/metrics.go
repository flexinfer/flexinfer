// Package daemon provides the main Loom daemon orchestrator.
package daemon

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus metrics for the daemon.
type Metrics struct {
	// Request metrics
	RequestsTotal    *prometheus.CounterVec
	RequestDuration  *prometheus.HistogramVec
	RequestsInFlight *prometheus.GaugeVec

	// Server health metrics
	ServerHealth    *prometheus.GaugeVec
	ServerLatency   *prometheus.GaugeVec
	ServerFailures  *prometheus.CounterVec
	ServerSuccesses *prometheus.CounterVec

	// Pool metrics
	PoolConnections *prometheus.GaugeVec
	PoolIdleConns   *prometheus.GaugeVec
	PoolActiveConns *prometheus.GaugeVec

	// Process metrics
	ProcessCount    prometheus.Gauge
	ProcessRestarts *prometheus.CounterVec

	// Cache metrics (tool manifest cache)
	ToolCacheSize    prometheus.Gauge
	ToolCacheAge     prometheus.Gauge
	ToolCacheHits    prometheus.Counter
	ToolCacheMisses  prometheus.Counter
	ToolCacheRefresh prometheus.Counter

	// Response cache metrics
	ResponseCacheHits    *prometheus.CounterVec
	ResponseCacheMisses  *prometheus.CounterVec
	ResponseCacheSize    prometheus.Gauge
	ResponseCacheEntries prometheus.Gauge
	ResponseCacheEvicts  prometheus.Counter

	// Hub metrics
	HubConnected prometheus.Gauge
	HubLatency   prometheus.Gauge
	HubRequests  *prometheus.CounterVec
	HubFailures  prometheus.Counter

	// RBAC metrics
	RBACDenied prometheus.Counter

	// Runtime metrics
	GoroutineCount prometheus.Gauge
	MemAllocBytes  prometheus.Gauge
	MemSysBytes    prometheus.Gauge
	GCPauseNs      prometheus.Gauge

	// EventBus metrics
	EventsDropped prometheus.Counter

	// Concurrency metrics
	ConcurrentCalls prometheus.Gauge

	// Contention metrics
	CallLockWaitTotal *prometheus.CounterVec

	registry *prometheus.Registry
}

// NewMetrics creates a new Metrics instance with all metrics registered.
func NewMetrics() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
	}

	// Request metrics
	m.RequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "requests_total",
			Help:      "Total number of requests processed by the daemon",
		},
		[]string{"server", "method", "status"},
	)

	m.RequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "request_duration_seconds",
			Help:      "Request duration in seconds",
			Buckets:   prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to ~16s
		},
		[]string{"server", "method", "target"}, // target: local, hub
	)

	m.RequestsInFlight = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "requests_in_flight",
			Help:      "Number of requests currently being processed",
		},
		[]string{"server"},
	)

	// Server health metrics
	m.ServerHealth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "server_health",
			Help:      "Health status of servers (1=healthy, 0=unhealthy)",
		},
		[]string{"server", "target"}, // target: local, hub
	)

	m.ServerLatency = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "server_latency_ms",
			Help:      "Average latency to servers in milliseconds",
		},
		[]string{"server", "target"},
	)

	m.ServerFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "server_failures_total",
			Help:      "Total number of server failures",
		},
		[]string{"server", "target", "error_type"},
	)

	m.ServerSuccesses = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "server_successes_total",
			Help:      "Total number of successful server calls",
		},
		[]string{"server", "target"},
	)

	// Pool metrics
	m.PoolConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "pool_connections",
			Help:      "Number of connections in the pool",
		},
		[]string{"pool", "state"}, // state: idle, active
	)

	m.PoolIdleConns = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "pool_idle_connections",
			Help:      "Number of idle connections in the pool",
		},
		[]string{"pool"},
	)

	m.PoolActiveConns = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "pool_active_connections",
			Help:      "Number of active connections in the pool",
		},
		[]string{"pool"},
	)

	// Process metrics
	m.ProcessCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "processes_running",
			Help:      "Number of MCP server processes currently running",
		},
	)

	m.ProcessRestarts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "process_restarts_total",
			Help:      "Total number of process restarts",
		},
		[]string{"server"},
	)

	// Cache metrics
	m.ToolCacheSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "tool_cache_size",
			Help:      "Number of tools in the cache",
		},
	)

	m.ToolCacheAge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "tool_cache_age_seconds",
			Help:      "Age of the tool cache in seconds",
		},
	)

	m.ToolCacheHits = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "tool_cache_hits_total",
			Help:      "Total number of tool cache hits",
		},
	)

	m.ToolCacheMisses = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "tool_cache_misses_total",
			Help:      "Total number of tool cache misses",
		},
	)

	m.ToolCacheRefresh = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "tool_cache_refreshes_total",
			Help:      "Total number of tool cache refreshes",
		},
	)

	// Response cache metrics
	m.ResponseCacheHits = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "response_cache_hits_total",
			Help:      "Total number of response cache hits",
		},
		[]string{"server", "tool"},
	)

	m.ResponseCacheMisses = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "response_cache_misses_total",
			Help:      "Total number of response cache misses",
		},
		[]string{"server", "tool"},
	)

	m.ResponseCacheSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "response_cache_size_bytes",
			Help:      "Current size of the response cache in bytes",
		},
	)

	m.ResponseCacheEntries = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "response_cache_entries",
			Help:      "Current number of entries in the response cache",
		},
	)

	m.ResponseCacheEvicts = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "response_cache_evictions_total",
			Help:      "Total number of response cache evictions",
		},
	)

	// Hub metrics
	m.HubConnected = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "hub_connected",
			Help:      "Whether the daemon is connected to the hub (1=connected, 0=disconnected)",
		},
	)

	m.HubLatency = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "hub_latency_ms",
			Help:      "Latency to the hub in milliseconds",
		},
	)

	m.HubRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "hub_requests_total",
			Help:      "Total number of requests to the hub",
		},
		[]string{"method", "status"},
	)

	m.HubFailures = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "hub_failures_total",
			Help:      "Total number of hub connection failures",
		},
	)

	m.RBACDenied = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "rbac_denied_total",
			Help:      "Total number of tool calls denied by RBAC",
		},
	)

	// Runtime metrics
	m.GoroutineCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "goroutine_count",
			Help:      "Current number of goroutines",
		},
	)

	m.MemAllocBytes = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "mem_alloc_bytes",
			Help:      "Current bytes of allocated heap objects",
		},
	)

	m.MemSysBytes = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "mem_sys_bytes",
			Help:      "Total bytes of memory obtained from the OS",
		},
	)

	m.GCPauseNs = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "gc_pause_ns",
			Help:      "Most recent GC pause duration in nanoseconds",
		},
	)

	// EventBus metrics
	m.EventsDropped = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "events_dropped_total",
			Help:      "Total number of events dropped due to slow subscribers",
		},
	)

	// Concurrency metrics
	m.ConcurrentCalls = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "concurrent_calls",
			Help:      "Current number of in-flight tool calls across all servers",
		},
	)

	// Contention metrics
	m.CallLockWaitTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "daemon",
			Name:      "call_lock_wait_total",
			Help:      "Total number of call lock waits exceeding threshold",
		},
		[]string{"server"},
	)

	// Register all metrics
	m.registry.MustRegister(
		m.RequestsTotal,
		m.RequestDuration,
		m.RequestsInFlight,
		m.ServerHealth,
		m.ServerLatency,
		m.ServerFailures,
		m.ServerSuccesses,
		m.PoolConnections,
		m.PoolIdleConns,
		m.PoolActiveConns,
		m.ProcessCount,
		m.ProcessRestarts,
		m.ToolCacheSize,
		m.ToolCacheAge,
		m.ToolCacheHits,
		m.ToolCacheMisses,
		m.ToolCacheRefresh,
		m.ResponseCacheHits,
		m.ResponseCacheMisses,
		m.ResponseCacheSize,
		m.ResponseCacheEntries,
		m.ResponseCacheEvicts,
		m.HubConnected,
		m.HubLatency,
		m.HubRequests,
		m.HubFailures,
		m.RBACDenied,
		m.GoroutineCount,
		m.MemAllocBytes,
		m.MemSysBytes,
		m.GCPauseNs,
		m.EventsDropped,
		m.ConcurrentCalls,
		m.CallLockWaitTotal,
	)

	return m
}

// Handler returns an HTTP handler for the /metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// RecordRequest records a request with its duration and status.
func (m *Metrics) RecordRequest(server, method, status, target string, duration time.Duration) {
	m.RequestsTotal.WithLabelValues(server, method, status).Inc()
	m.RequestDuration.WithLabelValues(server, method, target).Observe(duration.Seconds())
}

// RecordRequestStart records the start of a request (increments in-flight counter).
func (m *Metrics) RecordRequestStart(server string) {
	m.RequestsInFlight.WithLabelValues(server).Inc()
}

// RecordRequestEnd records the end of a request (decrements in-flight counter).
func (m *Metrics) RecordRequestEnd(server string) {
	m.RequestsInFlight.WithLabelValues(server).Dec()
}

// UpdateServerHealth updates the health status of a server.
func (m *Metrics) UpdateServerHealth(server, target string, healthy bool, latencyMs float64) {
	healthVal := 0.0
	if healthy {
		healthVal = 1.0
	}
	m.ServerHealth.WithLabelValues(server, target).Set(healthVal)
	m.ServerLatency.WithLabelValues(server, target).Set(latencyMs)
}

// RecordServerFailure records a server failure.
func (m *Metrics) RecordServerFailure(server, target, errorType string) {
	m.ServerFailures.WithLabelValues(server, target, errorType).Inc()
}

// RecordServerSuccess records a successful server call.
func (m *Metrics) RecordServerSuccess(server, target string) {
	m.ServerSuccesses.WithLabelValues(server, target).Inc()
}

// UpdatePoolStats updates the pool connection metrics.
func (m *Metrics) UpdatePoolStats(pool string, idle, active int) {
	m.PoolIdleConns.WithLabelValues(pool).Set(float64(idle))
	m.PoolActiveConns.WithLabelValues(pool).Set(float64(active))
	m.PoolConnections.WithLabelValues(pool, "idle").Set(float64(idle))
	m.PoolConnections.WithLabelValues(pool, "active").Set(float64(active))
}

// UpdateProcessCount updates the number of running processes.
func (m *Metrics) UpdateProcessCount(count int) {
	m.ProcessCount.Set(float64(count))
}

// RecordProcessRestart records a process restart.
func (m *Metrics) RecordProcessRestart(server string) {
	m.ProcessRestarts.WithLabelValues(server).Inc()
}

// UpdateToolCache updates the tool cache metrics.
func (m *Metrics) UpdateToolCache(size int, age time.Duration) {
	m.ToolCacheSize.Set(float64(size))
	m.ToolCacheAge.Set(age.Seconds())
}

// RecordToolCacheHit records a tool cache hit.
func (m *Metrics) RecordToolCacheHit() {
	m.ToolCacheHits.Inc()
}

// RecordToolCacheMiss records a tool cache miss.
func (m *Metrics) RecordToolCacheMiss() {
	m.ToolCacheMisses.Inc()
}

// RecordToolCacheRefresh records a tool cache refresh.
func (m *Metrics) RecordToolCacheRefresh() {
	m.ToolCacheRefresh.Inc()
}

// RecordResponseCacheHit records a response cache hit.
func (m *Metrics) RecordResponseCacheHit(server, tool string) {
	m.ResponseCacheHits.WithLabelValues(server, tool).Inc()
}

// RecordResponseCacheMiss records a response cache miss.
func (m *Metrics) RecordResponseCacheMiss(server, tool string) {
	m.ResponseCacheMisses.WithLabelValues(server, tool).Inc()
}

// UpdateResponseCacheStats updates the response cache statistics.
func (m *Metrics) UpdateResponseCacheStats(entries int, sizeBytes int64) {
	m.ResponseCacheEntries.Set(float64(entries))
	m.ResponseCacheSize.Set(float64(sizeBytes))
}

// RecordResponseCacheEviction records a cache eviction.
func (m *Metrics) RecordResponseCacheEviction() {
	m.ResponseCacheEvicts.Inc()
}

// UpdateHubConnection updates the hub connection status.
func (m *Metrics) UpdateHubConnection(connected bool, latencyMs float64) {
	connVal := 0.0
	if connected {
		connVal = 1.0
	}
	m.HubConnected.Set(connVal)
	m.HubLatency.Set(latencyMs)
}

// RecordHubRequest records a hub request.
func (m *Metrics) RecordHubRequest(method, status string) {
	m.HubRequests.WithLabelValues(method, status).Inc()
}

// RecordHubFailure records a hub connection failure.
func (m *Metrics) RecordHubFailure() {
	m.HubFailures.Inc()
}
