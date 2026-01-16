// Package metrics provides a shared Prometheus metrics exporter.
package metrics

import (
	"log"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// TokensPerSecond is a gauge for the tokens per second.
	TokensPerSecond = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_tokens_per_second",
			Help: "Rolling 1-minute average tokens per second.",
		},
		[]string{"model", "backend", "node"},
	)

	// ModelLoadSeconds is a gauge for the model load time.
	ModelLoadSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_model_load_seconds",
			Help: "Time to pull model from cache/registry.",
		},
		[]string{"model", "node"},
	)

	// GPUTemperature is a gauge for the GPU temperature.
	GPUTemperature = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_gpu_temperature_celsius",
			Help: "GPU core temperature in Celsius.",
		},
		[]string{"gpu", "node"},
	)

	// === ModelCache LRU Eviction Metrics ===

	// ModelCacheResidentSeconds tracks how long a cache has been resident in memory.
	ModelCacheResidentSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_modelcache_resident_seconds",
			Help: "Time in seconds a model cache has been resident in memory.",
		},
		[]string{"cache", "node", "strategy"},
	)

	// DevShmUtilizationPercent tracks /dev/shm utilization on each node.
	DevShmUtilizationPercent = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_dev_shm_utilization_percent",
			Help: "Percentage of /dev/shm utilized for model caching.",
		},
		[]string{"node"},
	)

	// ModelCacheEvictionsTotal counts evictions per cache and policy.
	ModelCacheEvictionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_modelcache_evictions_total",
			Help: "Total number of cache evictions.",
		},
		[]string{"cache", "node", "policy"},
	)

	// ModelCacheHitRate tracks the hit rate of each cache.
	ModelCacheHitRate = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_modelcache_hit_rate",
			Help: "Cache hit rate (0.0 to 1.0) for model loading.",
		},
		[]string{"cache", "node"},
	)

	// ModelCacheSizeBytes tracks the size of each cache in bytes.
	ModelCacheSizeBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_modelcache_size_bytes",
			Help: "Size of the model cache in bytes.",
		},
		[]string{"cache", "node", "strategy"},
	)

	// ModelCacheAccessCount tracks total access count per cache.
	ModelCacheAccessCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_modelcache_access_count",
			Help: "Total number of times this cache has been accessed.",
		},
		[]string{"cache", "node"},
	)

	// ModelCachePhase tracks the current phase of each cache.
	ModelCachePhase = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_modelcache_phase",
			Help: "Current phase of the model cache (1=Pending, 2=Initializing, 3=Provisioning, 4=Ready, 5=Failed).",
		},
		[]string{"cache", "namespace", "phase"},
	)
)

func init() {
	// Register the metrics with the default registry.
	prometheus.MustRegister(TokensPerSecond)
	prometheus.MustRegister(ModelLoadSeconds)
	prometheus.MustRegister(GPUTemperature)

	// ModelCache LRU eviction metrics
	prometheus.MustRegister(ModelCacheResidentSeconds)
	prometheus.MustRegister(DevShmUtilizationPercent)
	prometheus.MustRegister(ModelCacheEvictionsTotal)
	prometheus.MustRegister(ModelCacheHitRate)
	prometheus.MustRegister(ModelCacheSizeBytes)
	prometheus.MustRegister(ModelCacheAccessCount)
	prometheus.MustRegister(ModelCachePhase)
}

// Exporter handles serving the Prometheus metrics.
type Exporter struct {
	// In the future, this could hold configuration for the exporter.
}

// NewExporter creates a new Exporter.
func NewExporter() *Exporter {
	return &Exporter{}
}

// Run starts an HTTP server to expose the metrics.
// The server runs in a goroutine and logs errors instead of panicking.
func (e *Exporter) Run(addr string) {
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil && err != http.ErrServerClosed {
			// Log error but don't panic - this is a background goroutine
			// The main application should continue even if metrics fail
			// This allows debugging without crashing the entire process
			log.Printf("metrics server error on %s: %v", addr, err)
		}
	}()
}
