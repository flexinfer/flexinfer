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

	// GPU VRAM metrics

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

	// GPUComputeUtilizationPercent tracks GPU compute (core busy) utilization as a
	// percentage. Distinct from VRAM utilization: this is how busy the shader/compute
	// engine is — the signal for fleet idle-time (near-zero means an idle card).
	GPUComputeUtilizationPercent = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_gpu_compute_utilization_percent",
			Help: "GPU compute (core) utilization as a percentage (0-100). Near-zero indicates an idle card.",
		},
		[]string{"gpu", "node", "vendor"},
	)

	// ModelCache LRU eviction metrics

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
			Help: "Current phase of the model cache as a one-hot gauge per phase label.",
		},
		[]string{"cache", "namespace", "phase"},
	)

	// KV-cache pressure metrics

	// KVCachePressureEvictionsTotal counts KV-cache pressure evictions per model.
	// Incremented each time the Evict pressure policy scales a model down to relieve
	// KV-cache pressure (one increment per eviction transition, not per reconcile).
	KVCachePressureEvictionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_kvcache_pressure_evictions_total",
			Help: "Total number of KV-cache pressure evictions (Evict policy scale-downs).",
		},
		[]string{"model", "namespace"},
	)

	// Quantization metrics

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

	// JobProgressPercent tracks live progress (0-100) of running pipeline jobs.
	JobProgressPercent = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_job_progress_percent",
			Help: "Current progress percentage (0-100) of running pipeline jobs.",
		},
		[]string{"model", "namespace", "job_type"},
	)

	// QuantizationLayerIndex tracks the highest transformer-layer index
	// completed by a running quantize job (0-based). Unlike
	// JobProgressPercent, which is a time-elapsed/deadline estimate, this
	// reflects the actual quantization work completed. `rate()` or
	// `changes()` on this series over a time window detects stalls even
	// when the time-based progress keeps advancing.
	QuantizationLayerIndex = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_quantization_layer_index",
			Help: "Highest transformer-layer index (0-based) completed by a running quantize job. Not set for jobs that haven't completed a layer yet.",
		},
		[]string{"model", "namespace"},
	)

	// ModelColdStartDurationSeconds tracks time from activation/startup to Ready.
	ModelColdStartDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_model_cold_start_duration_seconds",
			Help:    "Duration from model activation/startup to Ready.",
			Buckets: []float64{0.5, 1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 120, 180, 300},
		},
		[]string{"model", "namespace", "backend", "cache_strategy"},
	)

	// ModelSwapDurationSeconds tracks shared-GPU swap-in latency to Ready.
	ModelSwapDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_model_swap_duration_seconds",
			Help:    "Duration for a shared-group model to become Ready after preemption/swap.",
			Buckets: []float64{0.25, 0.5, 1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 120},
		},
		[]string{"model", "namespace", "backend", "group"},
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

	// ClusterModelsDiscovered reports count of remote models discovered per cluster.
	ClusterModelsDiscovered = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_cluster_models_discovered",
			Help: "Number of remote models discovered for each registered cluster.",
		},
		[]string{"cluster", "region"},
	)

	// ClusterModelInventorySource reports which source provided model inventory
	// during the latest successful probe (one-hot: 1 active, 0 inactive).
	ClusterModelInventorySource = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_cluster_model_inventory_source",
			Help: "Model inventory source state per cluster (labels: source=watch|list, value 1 active/0 inactive).",
		},
		[]string{"cluster", "source"},
	)

	// ClusterModelWatchReady reports whether the remote model watch cache is ready.
	ClusterModelWatchReady = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_cluster_model_watch_ready",
			Help: "Remote model watch readiness (1=ready, 0=not ready).",
		},
		[]string{"cluster"},
	)

	// ClusterModelWatchRestarts reports remote watch restart count tracked by controller.
	ClusterModelWatchRestarts = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_cluster_model_watch_restarts",
			Help: "Remote model watch restart count observed by controller for each cluster.",
		},
		[]string{"cluster"},
	)

	// ClusterModelWatchRestartTotal counts remote watch restart events by reason class.
	ClusterModelWatchRestartTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_cluster_model_watch_restarts_total",
			Help: "Total remote model watch restart events by reason class.",
		},
		[]string{"cluster", "reason"},
	)

	// Model lifecycle metrics

	// ModelPhase reports the current phase for each model (1 for current phase, 0 otherwise).
	ModelPhase = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_model_phase",
			Help: "Current phase of a model (1 for current phase, 0 otherwise).",
		},
		[]string{"model", "namespace", "phase"},
	)

	// ModelTransitionsTotal counts phase transitions by model.
	ModelTransitionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_model_transitions_total",
			Help: "Total number of model phase transitions.",
		},
		[]string{"model", "namespace", "from", "to", "reason"},
	)

	// ModelPreloadActive reports whether a model is currently held warm by the
	// preload-on-deploy policy (1) ahead of its first request, or not (0). It
	// returns to 0 once the model serves its first request and normal idle
	// scale-to-zero resumes.
	ModelPreloadActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_model_preload_active",
			Help: "1 if a model is held warm by preload-on-deploy before its first request, else 0.",
		},
		[]string{"model", "namespace"},
	)

	// ModelReadyLatencySeconds tracks the time from model creation to Ready.
	ModelReadyLatencySeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_model_ready_latency_seconds",
			Help:    "Time from model creation to Ready phase.",
			Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600, 900, 1800},
		},
		[]string{"model", "namespace", "backend"},
	)

	// Shared-group scheduling metrics (v1alpha2)

	// SharedGroupState tracks the state of each model in a shared GPU group.
	SharedGroupState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_sharedgroup_state",
			Help: "State of a model in a shared GPU group (1 for current state, 0 otherwise).",
		},
		[]string{"group", "model", "namespace", "state"},
	)

	// SharedGroupPreemptionsTotal counts preemption events in shared GPU groups.
	SharedGroupPreemptionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_sharedgroup_preemptions_total",
			Help: "Total preemption events in shared GPU groups.",
		},
		[]string{"group", "namespace", "from", "to"},
	)

	// OwnedJobsStaleGeneration reports how many Jobs owned by a Model/ModelCache
	// were created under a superseded spec generation — their
	// flexinfer.ai/owner-generation stamp is older than the owner's current
	// metadata.generation. A nonzero value flags stale Jobs lingering after a
	// spec change or controller rollout. The series is cleared when none remain.
	OwnedJobsStaleGeneration = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_owned_jobs_stale_generation",
			Help: "Jobs owned by a Model/ModelCache created under a superseded spec generation (owner-generation annotation older than the owner's current generation).",
		},
		[]string{"owner_kind", "namespace", "owner"},
	)

	// GPULeaseActive is 1 while a training/quant GPU lease holds a shared-GPU
	// group (the serving incumbent is parked), 0 once released.
	GPULeaseActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_gpu_lease_active",
			Help: "1 while a training GPU lease holds a shared-GPU group (serving parked), 0 otherwise.",
		},
		[]string{"group", "namespace", "owner"},
	)

	// GPULeaseAcquiredTotal counts GPU lease acquisitions by training workloads.
	GPULeaseAcquiredTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_gpu_lease_acquired_total",
			Help: "Total GPU lease acquisitions (a training workload parked a serving group).",
		},
		[]string{"group", "namespace", "owner"},
	)

	// ModelBackfillStartsTotal counts admitted background Job attempts. A retry
	// after foreground or gaming preemption is a new start.
	ModelBackfillStartsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_model_backfill_starts_total",
			Help: "Total ModelBackfill Job attempts admitted after the foreground-idle gate.",
		},
		[]string{"backfill", "namespace", "model"},
	)

	// ModelBackfillCompletionsTotal counts terminal Job attempts. Result is a
	// bounded controller value such as succeeded or failed.
	ModelBackfillCompletionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_model_backfill_completions_total",
			Help: "Total terminal ModelBackfill Job attempts by result.",
		},
		[]string{"backfill", "namespace", "model", "result"},
	)

	// ModelBackfillPreemptionsTotal counts attempts cancelled so foreground
	// demand or gaming intent can take precedence.
	ModelBackfillPreemptionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_model_backfill_preemptions_total",
			Help: "Total ModelBackfill Job attempts preempted by bounded reason.",
		},
		[]string{"backfill", "namespace", "model", "reason"},
	)

	// ModelBackfillUsefulRunningSecondsTotal accumulates completed background
	// execution time. It intentionally excludes time spent waiting for idle.
	ModelBackfillUsefulRunningSecondsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_model_backfill_useful_running_seconds_total",
			Help: "Total seconds ModelBackfill Jobs spent running useful background work.",
		},
		[]string{"backfill", "namespace", "model"},
	)

	// Cache job metrics

	// ModelCacheJobDurationSeconds tracks duration of ModelCache pipeline jobs.
	ModelCacheJobDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_model_cache_job_duration_seconds",
			Help:    "Duration of ModelCache pipeline jobs (download, abliterate, quantize).",
			Buckets: []float64{30, 60, 120, 300, 600, 1200, 1800, 3600, 7200, 14400},
		},
		[]string{"model", "namespace", "job_type", "result"},
	)

	// ModelCacheJobFailuresTotal counts ModelCache pipeline job failures.
	ModelCacheJobFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_model_cache_failures_total",
			Help: "Total ModelCache pipeline job failures by reason.",
		},
		[]string{"model", "namespace", "reason"},
	)

	// ModelCacheJobCreateConflictsTotal counts AlreadyExists conflicts tolerated
	// when creating ModelCache pipeline jobs. These arise when two controller
	// generations briefly reconcile the same parent during a rolling update.
	// The idempotent-create guard swallows the conflict as success, so this
	// counter preserves visibility of how often the rollout race actually fires
	// (it was previously observable only as reconcile error spam).
	ModelCacheJobCreateConflictsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_model_cache_job_create_conflicts_total",
			Help: "Total AlreadyExists conflicts tolerated when creating ModelCache pipeline jobs (controller rollout race).",
		},
		[]string{"job_type"},
	)

	// OwnedJobsReapedTotal counts pipeline Jobs reaped because their stage was
	// removed from the owner's current spec (orphans from a superseded
	// generation). Only non-running orphans are reaped, so this never reflects
	// killed in-flight work.
	OwnedJobsReapedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_owned_jobs_reaped_total",
			Help: "Total orphaned-stage pipeline Jobs reaped (stage removed from the owner spec; non-running only).",
		},
		[]string{"owner_kind", "namespace", "stage"},
	)

	// Controller reconcile metrics

	// ReconcileDurationSeconds tracks the duration of each controller reconcile loop.
	ReconcileDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_reconcile_duration_seconds",
			Help:    "Duration of controller reconcile loops in seconds.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		},
		[]string{"controller"},
	)

	// ReconcileErrorsTotal counts reconcile errors by controller.
	ReconcileErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_reconcile_errors_total",
			Help: "Total number of reconcile errors by controller.",
		},
		[]string{"controller"},
	)

	// Finetune metrics

	// FinetuneDurationSeconds tracks finetune job duration.
	FinetuneDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_finetune_duration_seconds",
			Help:    "Duration of finetune jobs in seconds.",
			Buckets: []float64{300, 600, 1200, 1800, 3600, 7200, 14400, 28800},
		},
		[]string{"model", "namespace", "mode"},
	)

	// FinetuneJobsTotal counts finetune jobs by status.
	FinetuneJobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_finetune_jobs_total",
			Help: "Total number of finetune jobs by status.",
		},
		[]string{"model", "status"},
	)

	// FinetuneTrainLoss reports the final training loss from the most recent finetune job.
	FinetuneTrainLoss = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_finetune_train_loss",
			Help: "Final training loss from the most recent finetune job.",
		},
		[]string{"model", "namespace", "mode"},
	)

	// FinetuneSamplesPerSecond reports training throughput from the most recent finetune job.
	FinetuneSamplesPerSecond = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_finetune_samples_per_second",
			Help: "Training throughput (samples/sec) from the most recent finetune job.",
		},
		[]string{"model", "namespace", "mode"},
	)

	// Benchmark result metrics

	// BenchmarkTokensPerSecond publishes the latest benchmark TPS result.
	BenchmarkTokensPerSecond = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_benchmark_tokens_per_second",
			Help: "Latest benchmark tokens per second result.",
		},
		[]string{"model", "backend", "gpu_vendor", "gpu_arch"},
	)

	// BenchmarkVRAMUsedBytes publishes VRAM used during benchmark.
	BenchmarkVRAMUsedBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_benchmark_vram_used_bytes",
			Help: "GPU VRAM used during benchmark in bytes.",
		},
		[]string{"model", "backend", "gpu_vendor", "gpu_arch"},
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
	ctrlmetrics.Registry.MustRegister(GPUComputeUtilizationPercent)

	// ModelCache LRU eviction metrics
	ctrlmetrics.Registry.MustRegister(ModelCacheResidentSeconds)
	ctrlmetrics.Registry.MustRegister(DevShmUtilizationPercent)
	ctrlmetrics.Registry.MustRegister(ModelCacheEvictionsTotal)
	ctrlmetrics.Registry.MustRegister(ModelCacheHitRate)
	ctrlmetrics.Registry.MustRegister(ModelCacheSizeBytes)
	ctrlmetrics.Registry.MustRegister(ModelCacheAccessCount)
	ctrlmetrics.Registry.MustRegister(ModelCachePhase)

	// KV-cache pressure metrics
	ctrlmetrics.Registry.MustRegister(KVCachePressureEvictionsTotal)

	// Quantization metrics
	ctrlmetrics.Registry.MustRegister(QuantizationDurationSeconds)
	ctrlmetrics.Registry.MustRegister(QuantizationCompressionRatio)
	ctrlmetrics.Registry.MustRegister(QuantizationJobsTotal)
	ctrlmetrics.Registry.MustRegister(QuantizationCacheSizeBytes)
	ctrlmetrics.Registry.MustRegister(JobProgressPercent)
	ctrlmetrics.Registry.MustRegister(QuantizationLayerIndex)
	ctrlmetrics.Registry.MustRegister(ModelColdStartDurationSeconds)
	ctrlmetrics.Registry.MustRegister(ModelSwapDurationSeconds)

	// Cluster registry metrics
	ctrlmetrics.Registry.MustRegister(ClusterHealth)
	ctrlmetrics.Registry.MustRegister(ClusterProbeLatencySeconds)
	ctrlmetrics.Registry.MustRegister(ClusterModelsDiscovered)
	ctrlmetrics.Registry.MustRegister(ClusterModelInventorySource)
	ctrlmetrics.Registry.MustRegister(ClusterModelWatchReady)
	ctrlmetrics.Registry.MustRegister(ClusterModelWatchRestarts)
	ctrlmetrics.Registry.MustRegister(ClusterModelWatchRestartTotal)

	// Model lifecycle metrics
	ctrlmetrics.Registry.MustRegister(ModelPhase)
	ctrlmetrics.Registry.MustRegister(ModelTransitionsTotal)
	ctrlmetrics.Registry.MustRegister(ModelPreloadActive)
	ctrlmetrics.Registry.MustRegister(ModelReadyLatencySeconds)

	// Shared-group scheduling metrics (v1alpha2)
	ctrlmetrics.Registry.MustRegister(SharedGroupState)
	ctrlmetrics.Registry.MustRegister(SharedGroupPreemptionsTotal)
	ctrlmetrics.Registry.MustRegister(OwnedJobsStaleGeneration)
	ctrlmetrics.Registry.MustRegister(GPULeaseActive)
	ctrlmetrics.Registry.MustRegister(GPULeaseAcquiredTotal)
	ctrlmetrics.Registry.MustRegister(ModelBackfillStartsTotal)
	ctrlmetrics.Registry.MustRegister(ModelBackfillCompletionsTotal)
	ctrlmetrics.Registry.MustRegister(ModelBackfillPreemptionsTotal)
	ctrlmetrics.Registry.MustRegister(ModelBackfillUsefulRunningSecondsTotal)

	// Cache job metrics
	ctrlmetrics.Registry.MustRegister(ModelCacheJobDurationSeconds)
	ctrlmetrics.Registry.MustRegister(ModelCacheJobFailuresTotal)
	ctrlmetrics.Registry.MustRegister(ModelCacheJobCreateConflictsTotal)
	ctrlmetrics.Registry.MustRegister(OwnedJobsReapedTotal)

	// Controller reconcile metrics
	ctrlmetrics.Registry.MustRegister(ReconcileDurationSeconds)
	ctrlmetrics.Registry.MustRegister(ReconcileErrorsTotal)

	// Finetune metrics
	ctrlmetrics.Registry.MustRegister(FinetuneDurationSeconds)
	ctrlmetrics.Registry.MustRegister(FinetuneJobsTotal)
	ctrlmetrics.Registry.MustRegister(FinetuneTrainLoss)
	ctrlmetrics.Registry.MustRegister(FinetuneSamplesPerSecond)

	// Benchmark result metrics
	ctrlmetrics.Registry.MustRegister(BenchmarkTokensPerSecond)
	ctrlmetrics.Registry.MustRegister(BenchmarkVRAMUsedBytes)
}

// Exporter handles serving the Prometheus metrics.
type Exporter struct{}

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
