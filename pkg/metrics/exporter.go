// Package metrics provides a shared Prometheus metrics exporter.
package metrics

import (
	"log"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
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

	// === GPU VRAM Metrics ===

	// GPUVRAMFreeBytes tracks free VRAM per GPU.
	GPUVRAMFreeBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_gpu_vram_free_bytes",
			Help: "Free GPU VRAM in bytes.",
		},
		[]string{"gpu", "node", "vendor"},
	)

	// GPUVRAMTotalBytes tracks total VRAM per GPU.
	GPUVRAMTotalBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_gpu_vram_total_bytes",
			Help: "Total GPU VRAM in bytes.",
		},
		[]string{"gpu", "node", "vendor"},
	)

	// GPUVRAMUsedBytes tracks used VRAM per GPU.
	GPUVRAMUsedBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_gpu_vram_used_bytes",
			Help: "Used GPU VRAM in bytes.",
		},
		[]string{"gpu", "node", "vendor"},
	)

	// GPUVRAMUtilizationPercent tracks VRAM utilization as a percentage.
	GPUVRAMUtilizationPercent = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_gpu_vram_utilization_percent",
			Help: "GPU VRAM utilization as a percentage (0-100).",
		},
		[]string{"gpu", "node", "vendor"},
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
			Help: "Current phase of the model cache (1=Pending, 2=Initializing, 3=Provisioning, 4=Quantizing, 5=Ready, 6=Failed).",
		},
		[]string{"cache", "namespace", "phase"},
	)

	// === Quantization Metrics ===

	// QuantizationDurationSeconds tracks quantization job duration.
	QuantizationDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_quantization_duration_seconds",
			Help:    "Duration of quantization jobs in seconds.",
			Buckets: []float64{60, 120, 300, 600, 1200, 1800, 3600, 7200},
		},
		[]string{"model", "format", "type"},
	)

	// QuantizationCompressionRatio tracks the compression ratio achieved.
	QuantizationCompressionRatio = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_quantization_compression_ratio",
			Help: "Compression ratio achieved by quantization (original/compressed).",
		},
		[]string{"model", "format"},
	)

	// QuantizationJobsTotal counts quantization jobs by status.
	QuantizationJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_quantization_jobs_total",
			Help: "Total number of quantization jobs by status.",
		},
		[]string{"model", "status"},
	)

	// QuantizationCacheSizeBytes tracks the size of quantized model files.
	QuantizationCacheSizeBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_quantization_cache_size_bytes",
			Help: "Size of quantized model files in bytes.",
		},
		[]string{"model", "format"},
	)

	// ClusterHealth reports remote cluster readiness (1 ready, 0 not ready).
	ClusterHealth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_cluster_health",
			Help: "Remote cluster health status (1=ready, 0=not ready).",
		},
		[]string{"cluster", "region"},
	)

	// ClusterProbeLatencySeconds tracks probe latency for remote clusters.
	ClusterProbeLatencySeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_cluster_probe_latency_seconds",
			Help:    "Latency of remote cluster health probes.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20},
		},
		[]string{"cluster"},
	)
)

func init() {
	// Register the metrics with controller-runtime's registry so they are exposed by
	// the manager's /metrics endpoint (and any component that serves ctrlmetrics.Registry).
	ctrlmetrics.Registry.MustRegister(TokensPerSecond)
	ctrlmetrics.Registry.MustRegister(ModelLoadSeconds)
	ctrlmetrics.Registry.MustRegister(GPUTemperature)

	// GPU VRAM metrics
	ctrlmetrics.Registry.MustRegister(GPUVRAMFreeBytes)
	ctrlmetrics.Registry.MustRegister(GPUVRAMTotalBytes)
	ctrlmetrics.Registry.MustRegister(GPUVRAMUsedBytes)
	ctrlmetrics.Registry.MustRegister(GPUVRAMUtilizationPercent)

	// ModelCache LRU eviction metrics
	ctrlmetrics.Registry.MustRegister(ModelCacheResidentSeconds)
	ctrlmetrics.Registry.MustRegister(DevShmUtilizationPercent)
	ctrlmetrics.Registry.MustRegister(ModelCacheEvictionsTotal)
	ctrlmetrics.Registry.MustRegister(ModelCacheHitRate)
	ctrlmetrics.Registry.MustRegister(ModelCacheSizeBytes)
	ctrlmetrics.Registry.MustRegister(ModelCacheAccessCount)
	ctrlmetrics.Registry.MustRegister(ModelCachePhase)

	// Quantization metrics
	ctrlmetrics.Registry.MustRegister(QuantizationDurationSeconds)
	ctrlmetrics.Registry.MustRegister(QuantizationCompressionRatio)
	ctrlmetrics.Registry.MustRegister(QuantizationJobsTotal)
	ctrlmetrics.Registry.MustRegister(QuantizationCacheSizeBytes)

	// Cluster registry metrics
	ctrlmetrics.Registry.MustRegister(ClusterHealth)
	ctrlmetrics.Registry.MustRegister(ClusterProbeLatencySeconds)
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
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(ctrlmetrics.Registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.HTTPErrorOnError,
	}))
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil && err != http.ErrServerClosed {
			// Log error but don't panic - this is a background goroutine
			// The main application should continue even if metrics fail
			// This allows debugging without crashing the entire process
			log.Printf("metrics server error on %s: %v", addr, err)
		}
	}()
}
