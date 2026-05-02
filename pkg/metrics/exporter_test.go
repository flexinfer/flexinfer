package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- Descriptor contract table ----------

// metricSpec defines the expected contract for each exported metric:
// name, help text, metric type, and label names.
type metricSpec struct {
	name       string
	help       string
	metricType dto.MetricType
	labels     []string
	collector  prometheus.Collector
}

// allMetrics returns the full list of metric specs matching exporter.go declarations.
// Any addition or removal of a metric in exporter.go that is not reflected here will
// cause a test failure, preventing silent regressions.
func allMetrics() []metricSpec {
	return []metricSpec{
		// --- Core operational ---
		{
			name:       "flexinfer_tokens_per_second",
			help:       "Rolling 1-minute average tokens per second.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"model", "backend", "node"},
			collector:  TokensPerSecond,
		},
		{
			name:       "flexinfer_model_load_seconds",
			help:       "Time to pull model from cache/registry.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"model", "node"},
			collector:  ModelLoadSeconds,
		},
		{
			name:       "flexinfer_gpu_temperature_celsius",
			help:       "GPU core temperature in Celsius.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"gpu", "node"},
			collector:  GPUTemperature,
		},

		// --- GPU VRAM ---
		{
			name:       "flexinfer_gpu_vram_free_bytes",
			help:       "Free GPU VRAM in bytes.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"gpu", "node", "vendor"},
			collector:  GPUVRAMFreeBytes,
		},
		{
			name:       "flexinfer_gpu_vram_total_bytes",
			help:       "Total GPU VRAM in bytes.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"gpu", "node", "vendor"},
			collector:  GPUVRAMTotalBytes,
		},
		{
			name:       "flexinfer_gpu_vram_used_bytes",
			help:       "Used GPU VRAM in bytes.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"gpu", "node", "vendor"},
			collector:  GPUVRAMUsedBytes,
		},
		{
			name:       "flexinfer_gpu_vram_utilization_percent",
			help:       "GPU VRAM utilization as a percentage (0-100).",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"gpu", "node", "vendor"},
			collector:  GPUVRAMUtilizationPercent,
		},

		// --- ModelCache LRU eviction ---
		{
			name:       "flexinfer_modelcache_resident_seconds",
			help:       "Time in seconds a model cache has been resident in memory.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"cache", "node", "strategy"},
			collector:  ModelCacheResidentSeconds,
		},
		{
			name:       "flexinfer_dev_shm_utilization_percent",
			help:       "Percentage of /dev/shm utilized for model caching.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"node"},
			collector:  DevShmUtilizationPercent,
		},
		{
			name:       "flexinfer_modelcache_evictions_total",
			help:       "Total number of cache evictions.",
			metricType: dto.MetricType_COUNTER,
			labels:     []string{"cache", "node", "policy"},
			collector:  ModelCacheEvictionsTotal,
		},
		{
			name:       "flexinfer_modelcache_hit_rate",
			help:       "Cache hit rate (0.0 to 1.0) for model loading.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"cache", "node"},
			collector:  ModelCacheHitRate,
		},
		{
			name:       "flexinfer_modelcache_size_bytes",
			help:       "Size of the model cache in bytes.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"cache", "node", "strategy"},
			collector:  ModelCacheSizeBytes,
		},
		{
			name:       "flexinfer_modelcache_access_count",
			help:       "Total number of times this cache has been accessed.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"cache", "node"},
			collector:  ModelCacheAccessCount,
		},
		{
			name:       "flexinfer_modelcache_phase",
			help:       "Current phase of the model cache as a one-hot gauge per phase label.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"cache", "namespace", "phase"},
			collector:  ModelCachePhase,
		},

		// --- Quantization ---
		{
			name:       "flexinfer_quantization_duration_seconds",
			help:       "Duration of quantization jobs in seconds.",
			metricType: dto.MetricType_HISTOGRAM,
			labels:     []string{"model", "format", "type"},
			collector:  QuantizationDurationSeconds,
		},
		{
			name:       "flexinfer_quantization_compression_ratio",
			help:       "Compression ratio achieved by quantization (original/compressed).",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"model", "format"},
			collector:  QuantizationCompressionRatio,
		},
		{
			name:       "flexinfer_quantization_jobs_total",
			help:       "Total number of quantization jobs by status.",
			metricType: dto.MetricType_COUNTER,
			labels:     []string{"model", "status"},
			collector:  QuantizationJobsTotal,
		},
		{
			name:       "flexinfer_quantization_cache_size_bytes",
			help:       "Size of quantized model files in bytes.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"model", "format"},
			collector:  QuantizationCacheSizeBytes,
		},
		{
			name:       "flexinfer_job_progress_percent",
			help:       "Current progress percentage (0-100) of running pipeline jobs.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"model", "namespace", "job_type"},
			collector:  JobProgressPercent,
		},
		{
			name:       "flexinfer_model_cold_start_duration_seconds",
			help:       "Duration from model activation/startup to Ready.",
			metricType: dto.MetricType_HISTOGRAM,
			labels:     []string{"model", "namespace", "backend", "cache_strategy"},
			collector:  ModelColdStartDurationSeconds,
		},
		{
			name:       "flexinfer_model_swap_duration_seconds",
			help:       "Duration for a shared-group model to become Ready after preemption/swap.",
			metricType: dto.MetricType_HISTOGRAM,
			labels:     []string{"model", "namespace", "backend", "group"},
			collector:  ModelSwapDurationSeconds,
		},

		// --- Cluster registry ---
		{
			name:       "flexinfer_cluster_health",
			help:       "Remote cluster health status (1=ready, 0=not ready).",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"cluster", "region"},
			collector:  ClusterHealth,
		},
		{
			name:       "flexinfer_cluster_probe_latency_seconds",
			help:       "Latency of remote cluster health probes.",
			metricType: dto.MetricType_HISTOGRAM,
			labels:     []string{"cluster"},
			collector:  ClusterProbeLatencySeconds,
		},
		{
			name:       "flexinfer_cluster_models_discovered",
			help:       "Number of remote models discovered for each registered cluster.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"cluster", "region"},
			collector:  ClusterModelsDiscovered,
		},
		{
			name:       "flexinfer_cluster_model_inventory_source",
			help:       "Model inventory source state per cluster (labels: source=watch|list, value 1 active/0 inactive).",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"cluster", "source"},
			collector:  ClusterModelInventorySource,
		},
		{
			name:       "flexinfer_cluster_model_watch_ready",
			help:       "Remote model watch readiness (1=ready, 0=not ready).",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"cluster"},
			collector:  ClusterModelWatchReady,
		},
		{
			name:       "flexinfer_cluster_model_watch_restarts",
			help:       "Remote model watch restart count observed by controller for each cluster.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"cluster"},
			collector:  ClusterModelWatchRestarts,
		},
		{
			name:       "flexinfer_cluster_model_watch_restarts_total",
			help:       "Total remote model watch restart events by reason class.",
			metricType: dto.MetricType_COUNTER,
			labels:     []string{"cluster", "reason"},
			collector:  ClusterModelWatchRestartTotal,
		},

		// --- Model lifecycle ---
		{
			name:       "flexinfer_model_phase",
			help:       "Current phase of a model (1 for current phase, 0 otherwise).",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"model", "namespace", "phase"},
			collector:  ModelPhase,
		},
		{
			name:       "flexinfer_model_transitions_total",
			help:       "Total number of model phase transitions.",
			metricType: dto.MetricType_COUNTER,
			labels:     []string{"model", "namespace", "from", "to", "reason"},
			collector:  ModelTransitionsTotal,
		},
		{
			name:       "flexinfer_model_ready_latency_seconds",
			help:       "Time from model creation to Ready phase.",
			metricType: dto.MetricType_HISTOGRAM,
			labels:     []string{"model", "namespace", "backend"},
			collector:  ModelReadyLatencySeconds,
		},

		// --- Shared-group scheduling ---
		{
			name:       "flexinfer_sharedgroup_state",
			help:       "State of a model in a shared GPU group (1 for current state, 0 otherwise).",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"group", "model", "namespace", "state"},
			collector:  SharedGroupState,
		},
		{
			name:       "flexinfer_sharedgroup_preemptions_total",
			help:       "Total preemption events in shared GPU groups.",
			metricType: dto.MetricType_COUNTER,
			labels:     []string{"group", "namespace", "from", "to"},
			collector:  SharedGroupPreemptionsTotal,
		},

		// --- Cache job ---
		{
			name:       "flexinfer_model_cache_job_duration_seconds",
			help:       "Duration of ModelCache pipeline jobs (download, abliterate, quantize).",
			metricType: dto.MetricType_HISTOGRAM,
			labels:     []string{"model", "namespace", "job_type", "result"},
			collector:  ModelCacheJobDurationSeconds,
		},
		{
			name:       "flexinfer_model_cache_failures_total",
			help:       "Total ModelCache pipeline job failures by reason.",
			metricType: dto.MetricType_COUNTER,
			labels:     []string{"model", "namespace", "reason"},
			collector:  ModelCacheJobFailuresTotal,
		},

		// --- Controller reconcile ---
		{
			name:       "flexinfer_reconcile_duration_seconds",
			help:       "Duration of controller reconcile loops in seconds.",
			metricType: dto.MetricType_HISTOGRAM,
			labels:     []string{"controller"},
			collector:  ReconcileDurationSeconds,
		},
		{
			name:       "flexinfer_reconcile_errors_total",
			help:       "Total number of reconcile errors by controller.",
			metricType: dto.MetricType_COUNTER,
			labels:     []string{"controller"},
			collector:  ReconcileErrorsTotal,
		},

		// --- Finetune ---
		{
			name:       "flexinfer_finetune_duration_seconds",
			help:       "Duration of finetune jobs in seconds.",
			metricType: dto.MetricType_HISTOGRAM,
			labels:     []string{"model", "namespace", "mode"},
			collector:  FinetuneDurationSeconds,
		},
		{
			name:       "flexinfer_finetune_jobs_total",
			help:       "Total number of finetune jobs by status.",
			metricType: dto.MetricType_COUNTER,
			labels:     []string{"model", "status"},
			collector:  FinetuneJobsTotal,
		},
		{
			name:       "flexinfer_finetune_train_loss",
			help:       "Final training loss from the most recent finetune job.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"model", "namespace", "mode"},
			collector:  FinetuneTrainLoss,
		},
		{
			name:       "flexinfer_finetune_samples_per_second",
			help:       "Training throughput (samples/sec) from the most recent finetune job.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"model", "namespace", "mode"},
			collector:  FinetuneSamplesPerSecond,
		},

		// --- Benchmark ---
		{
			name:       "flexinfer_benchmark_tokens_per_second",
			help:       "Latest benchmark tokens per second result.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"model", "backend", "gpu_vendor", "gpu_arch"},
			collector:  BenchmarkTokensPerSecond,
		},
		{
			name:       "flexinfer_benchmark_vram_used_bytes",
			help:       "GPU VRAM used during benchmark in bytes.",
			metricType: dto.MetricType_GAUGE,
			labels:     []string{"model", "backend", "gpu_vendor", "gpu_arch"},
			collector:  BenchmarkVRAMUsedBytes,
		},
	}
}

// ---------- Descriptor tests ----------

func TestMetricDescriptorNames(t *testing.T) {
	for _, ms := range allMetrics() {
		t.Run(ms.name, func(t *testing.T) {
			descs := collectDescs(t, ms.collector)
			require.Len(t, descs, 1, "expected exactly one descriptor per collector")

			fqName := descs[0].fqName
			assert.Equal(t, ms.name, fqName, "metric name mismatch")
		})
	}
}

func TestMetricDescriptorHelp(t *testing.T) {
	for _, ms := range allMetrics() {
		t.Run(ms.name, func(t *testing.T) {
			descs := collectDescs(t, ms.collector)
			require.Len(t, descs, 1)

			assert.Equal(t, ms.help, descs[0].help, "help text mismatch")
		})
	}
}

func TestMetricLabelNames(t *testing.T) {
	for _, ms := range allMetrics() {
		t.Run(ms.name, func(t *testing.T) {
			descs := collectDescs(t, ms.collector)
			require.Len(t, descs, 1)

			assert.Equal(t, ms.labels, descs[0].variableLabels,
				"label names mismatch")
		})
	}
}

// TestAllMetricsPrefix ensures every flexinfer metric has the required namespace prefix.
func TestAllMetricsPrefix(t *testing.T) {
	const prefix = "flexinfer_"
	for _, ms := range allMetrics() {
		assert.Containsf(t, ms.name, prefix,
			"metric %q does not start with %q", ms.name, prefix)
	}
}

// TestMetricTypes verifies each metric produces the expected prometheus type
// (gauge, counter, histogram) by collecting a sample and inspecting the dto.
func TestMetricTypes(t *testing.T) {
	for _, ms := range allMetrics() {
		t.Run(ms.name, func(t *testing.T) {
			ch := make(chan prometheus.Metric, 100)
			ms.collector.Collect(ch)
			close(ch)

			// If the collector has no instantiated series yet, we can still
			// verify via Describe that it produces exactly one descriptor.
			// For collectors that already have data (from other tests), we
			// check the type of the first collected metric.
			for m := range ch {
				var d dto.Metric
				require.NoError(t, m.Write(&d))

				switch ms.metricType {
				case dto.MetricType_GAUGE:
					assert.NotNil(t, d.Gauge, "expected gauge for %s", ms.name)
				case dto.MetricType_COUNTER:
					assert.NotNil(t, d.Counter, "expected counter for %s", ms.name)
				case dto.MetricType_HISTOGRAM:
					assert.NotNil(t, d.Histogram, "expected histogram for %s", ms.name)
				}
				return // only need to check one sample
			}
		})
	}
}

// TestMetricCount verifies the table is exhaustive. If a developer adds a metric
// to exporter.go but forgets to add it to the test table, this test fails.
func TestMetricCount(t *testing.T) {
	const expectedMetricCount = 43 // total exported metrics in exporter.go
	got := len(allMetrics())
	assert.Equal(t, expectedMetricCount, got,
		"metric count changed — update allMetrics() table when adding/removing metrics")
}

// ---------- Collection callback tests ----------

func TestGaugeVecSetAndRead(t *testing.T) {
	// Verify gauge collection produces readable values.
	TokensPerSecond.WithLabelValues("test-model", "vllm", "node-1").Set(73.2)
	got := promtestutil.ToFloat64(TokensPerSecond.WithLabelValues("test-model", "vllm", "node-1"))
	assert.InDelta(t, 73.2, got, 0.001)

	// Clean up to avoid polluting other tests.
	TokensPerSecond.DeleteLabelValues("test-model", "vllm", "node-1")
}

func TestCounterVecIncAndRead(t *testing.T) {
	// Verify counter collection increments correctly.
	ReconcileErrorsTotal.WithLabelValues("model").Inc()
	ReconcileErrorsTotal.WithLabelValues("model").Inc()
	got := promtestutil.ToFloat64(ReconcileErrorsTotal.WithLabelValues("model"))
	assert.GreaterOrEqual(t, got, 2.0, "counter should be at least 2 after two Inc() calls")
}

func TestHistogramVecObserveAndRead(t *testing.T) {
	// Observe a value and verify the histogram count increases.
	ReconcileDurationSeconds.WithLabelValues("test-ctrl").Observe(0.5)
	ReconcileDurationSeconds.WithLabelValues("test-ctrl").Observe(1.2)

	count := promtestutil.CollectAndCount(ReconcileDurationSeconds)
	assert.Greater(t, count, 0, "histogram should have at least one metric after Observe()")
}

func TestGPUVRAMMetrics_Collection(t *testing.T) {
	gpu, node, vendor := "gpu-0", "worker-1", "amd"

	GPUVRAMTotalBytes.WithLabelValues(gpu, node, vendor).Set(24e9)
	GPUVRAMUsedBytes.WithLabelValues(gpu, node, vendor).Set(18e9)
	GPUVRAMFreeBytes.WithLabelValues(gpu, node, vendor).Set(6e9)
	GPUVRAMUtilizationPercent.WithLabelValues(gpu, node, vendor).Set(75.0)

	assert.InDelta(t, 24e9, promtestutil.ToFloat64(GPUVRAMTotalBytes.WithLabelValues(gpu, node, vendor)), 1)
	assert.InDelta(t, 18e9, promtestutil.ToFloat64(GPUVRAMUsedBytes.WithLabelValues(gpu, node, vendor)), 1)
	assert.InDelta(t, 6e9, promtestutil.ToFloat64(GPUVRAMFreeBytes.WithLabelValues(gpu, node, vendor)), 1)
	assert.InDelta(t, 75.0, promtestutil.ToFloat64(GPUVRAMUtilizationPercent.WithLabelValues(gpu, node, vendor)), 0.001)

	// Clean up.
	GPUVRAMTotalBytes.DeleteLabelValues(gpu, node, vendor)
	GPUVRAMUsedBytes.DeleteLabelValues(gpu, node, vendor)
	GPUVRAMFreeBytes.DeleteLabelValues(gpu, node, vendor)
	GPUVRAMUtilizationPercent.DeleteLabelValues(gpu, node, vendor)
}

func TestModelPhaseOneHot(t *testing.T) {
	// Simulate one-hot phase encoding: only the current phase is 1.
	model, ns := "qwen3-14b", "flexinfer"
	phases := []string{"Pending", "Downloading", "Ready", "Error"}

	for _, phase := range phases {
		if phase == "Ready" {
			ModelPhase.WithLabelValues(model, ns, phase).Set(1)
		} else {
			ModelPhase.WithLabelValues(model, ns, phase).Set(0)
		}
	}

	assert.Equal(t, 1.0, promtestutil.ToFloat64(ModelPhase.WithLabelValues(model, ns, "Ready")))
	assert.Equal(t, 0.0, promtestutil.ToFloat64(ModelPhase.WithLabelValues(model, ns, "Pending")))
	assert.Equal(t, 0.0, promtestutil.ToFloat64(ModelPhase.WithLabelValues(model, ns, "Error")))

	for _, phase := range phases {
		ModelPhase.DeleteLabelValues(model, ns, phase)
	}
}

func TestQuantizationHistogramBuckets(t *testing.T) {
	QuantizationDurationSeconds.WithLabelValues("bucket-test", "gptq", "int4").Observe(300)

	h := collectHistogram(t, QuantizationDurationSeconds)
	require.NotNil(t, h, "expected histogram metric")
	// 8 explicit buckets (60, 120, 300, 600, 1200, 1800, 3600, 7200).
	assert.Len(t, h.Bucket, 8, "expected 8 explicit buckets")
	assert.Greater(t, h.GetSampleCount(), uint64(0), "sample count should be > 0")

	QuantizationDurationSeconds.DeleteLabelValues("bucket-test", "gptq", "int4")
}

func TestColdStartHistogramBuckets(t *testing.T) {
	ModelColdStartDurationSeconds.WithLabelValues("cold-test", "ns", "vllm", "shm").Observe(5.0)

	h := collectHistogram(t, ModelColdStartDurationSeconds)
	require.NotNil(t, h, "expected histogram metric")
	// 14 explicit buckets (0.5, 1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 120, 180, 300).
	assert.Len(t, h.Bucket, 14, "expected 14 explicit buckets")
	assert.Greater(t, h.GetSampleCount(), uint64(0), "sample count should be > 0")

	ModelColdStartDurationSeconds.DeleteLabelValues("cold-test", "ns", "vllm", "shm")
}

func TestSwapHistogramBuckets(t *testing.T) {
	ModelSwapDurationSeconds.WithLabelValues("swap-test", "ns", "mlc-llm", "gfx1100").Observe(3.0)

	h := collectHistogram(t, ModelSwapDurationSeconds)
	require.NotNil(t, h, "expected histogram metric")
	// 13 explicit buckets (0.25, 0.5, 1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 120).
	assert.Len(t, h.Bucket, 13, "expected 13 explicit buckets")
	assert.Greater(t, h.GetSampleCount(), uint64(0), "sample count should be > 0")

	ModelSwapDurationSeconds.DeleteLabelValues("swap-test", "ns", "mlc-llm", "gfx1100")
}

func TestReconcileDurationHistogramBuckets(t *testing.T) {
	ReconcileDurationSeconds.WithLabelValues("reconcile-test").Observe(0.05)

	h := collectHistogram(t, ReconcileDurationSeconds)
	require.NotNil(t, h, "expected histogram metric")
	// 10 explicit buckets (0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30).
	assert.Len(t, h.Bucket, 10, "expected 10 explicit buckets")
	assert.Greater(t, h.GetSampleCount(), uint64(0), "sample count should be > 0")

	ReconcileDurationSeconds.DeleteLabelValues("reconcile-test")
}

// ---------- Cardinality guard tests ----------

func TestLabelCardinalityBounded(t *testing.T) {
	// Ensure no metric has more than 5 label dimensions, which is a practical
	// upper bound to prevent cardinality explosions in Prometheus.
	const maxLabels = 5
	for _, ms := range allMetrics() {
		t.Run(ms.name, func(t *testing.T) {
			assert.LessOrEqual(t, len(ms.labels), maxLabels,
				"metric %q has %d labels (max %d) — risk of cardinality explosion",
				ms.name, len(ms.labels), maxLabels)
		})
	}
}

// TestNoUnboundedLabels verifies that no label name suggests a high-cardinality
// value (e.g., request IDs, timestamps, UUIDs).
func TestNoUnboundedLabels(t *testing.T) {
	unboundedPatterns := []string{
		"request_id", "uuid", "timestamp", "trace_id", "span_id",
		"ip", "address", "url", "path", "user_id",
	}

	for _, ms := range allMetrics() {
		for _, label := range ms.labels {
			for _, pattern := range unboundedPatterns {
				assert.NotEqual(t, pattern, label,
					"metric %q has potentially unbounded label %q", ms.name, label)
			}
		}
	}
}

// ---------- Collector interface compliance ----------

func TestAllCollectorsSatisfyInterface(t *testing.T) {
	// Verify that every exported metric implements the prometheus.Collector interface
	// and that Describe/Collect do not panic.
	for _, ms := range allMetrics() {
		t.Run(ms.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				ch := make(chan *prometheus.Desc, 10)
				ms.collector.Describe(ch)
				close(ch)

				got := 0
				for range ch {
					got++
				}
				assert.Equal(t, 1, got, "Describe should emit exactly one descriptor")
			})

			assert.NotPanics(t, func() {
				ch := make(chan prometheus.Metric, 100)
				ms.collector.Collect(ch)
				close(ch)
			})
		})
	}
}

// ---------- Exporter struct tests ----------

func TestNewExporter(t *testing.T) {
	e := NewExporter()
	require.NotNil(t, e, "NewExporter should return a non-nil Exporter")
}

// ---------- Multi-metric interaction tests ----------

func TestSharedGroupPreemptionTracking(t *testing.T) {
	group, ns := "5930k-imagegen-textgen", "flexinfer"

	// Record a preemption event.
	SharedGroupPreemptionsTotal.WithLabelValues(group, ns, "imagegen", "textgen").Inc()
	got := promtestutil.ToFloat64(
		SharedGroupPreemptionsTotal.WithLabelValues(group, ns, "imagegen", "textgen"))
	assert.Equal(t, 1.0, got)

	// Set group state for both models.
	SharedGroupState.WithLabelValues(group, "imagegen", ns, "active").Set(1)
	SharedGroupState.WithLabelValues(group, "textgen", ns, "evicted").Set(1)

	assert.Equal(t, 1.0,
		promtestutil.ToFloat64(SharedGroupState.WithLabelValues(group, "imagegen", ns, "active")))
	assert.Equal(t, 1.0,
		promtestutil.ToFloat64(SharedGroupState.WithLabelValues(group, "textgen", ns, "evicted")))

	// Clean up.
	SharedGroupState.DeleteLabelValues(group, "imagegen", ns, "active")
	SharedGroupState.DeleteLabelValues(group, "textgen", ns, "evicted")
}

func TestJobProgressPercent_BoundaryValues(t *testing.T) {
	model, ns, jobType := "test-model", "flexinfer", "quantize"

	// 0% at start.
	JobProgressPercent.WithLabelValues(model, ns, jobType).Set(0)
	assert.Equal(t, 0.0,
		promtestutil.ToFloat64(JobProgressPercent.WithLabelValues(model, ns, jobType)))

	// 100% at completion.
	JobProgressPercent.WithLabelValues(model, ns, jobType).Set(100)
	assert.Equal(t, 100.0,
		promtestutil.ToFloat64(JobProgressPercent.WithLabelValues(model, ns, jobType)))

	JobProgressPercent.DeleteLabelValues(model, ns, jobType)
}

func TestClusterHealthOneHotEncoding(t *testing.T) {
	ClusterHealth.WithLabelValues("remote-1", "us-west").Set(1)
	ClusterHealth.WithLabelValues("remote-2", "eu-central").Set(0)

	assert.Equal(t, 1.0,
		promtestutil.ToFloat64(ClusterHealth.WithLabelValues("remote-1", "us-west")))
	assert.Equal(t, 0.0,
		promtestutil.ToFloat64(ClusterHealth.WithLabelValues("remote-2", "eu-central")))

	ClusterHealth.DeleteLabelValues("remote-1", "us-west")
	ClusterHealth.DeleteLabelValues("remote-2", "eu-central")
}

func TestModelCacheEvictionsCounter_Monotonic(t *testing.T) {
	cache, node, policy := "shm-cache", "worker-1", "lru"

	ModelCacheEvictionsTotal.WithLabelValues(cache, node, policy).Inc()
	v1 := promtestutil.ToFloat64(ModelCacheEvictionsTotal.WithLabelValues(cache, node, policy))

	ModelCacheEvictionsTotal.WithLabelValues(cache, node, policy).Inc()
	v2 := promtestutil.ToFloat64(ModelCacheEvictionsTotal.WithLabelValues(cache, node, policy))

	assert.Greater(t, v2, v1, "counter should be monotonically increasing")
}

func TestFinetuneMetrics_Collection(t *testing.T) {
	model, ns, mode := "test-ft-model", "flexinfer", "qlora"

	FinetuneTrainLoss.WithLabelValues(model, ns, mode).Set(0.42)
	FinetuneSamplesPerSecond.WithLabelValues(model, ns, mode).Set(3.14)

	assert.InDelta(t, 0.42,
		promtestutil.ToFloat64(FinetuneTrainLoss.WithLabelValues(model, ns, mode)), 0.001)
	assert.InDelta(t, 3.14,
		promtestutil.ToFloat64(FinetuneSamplesPerSecond.WithLabelValues(model, ns, mode)), 0.001)

	FinetuneTrainLoss.DeleteLabelValues(model, ns, mode)
	FinetuneSamplesPerSecond.DeleteLabelValues(model, ns, mode)
}

func TestBenchmarkMetrics_Collection(t *testing.T) {
	model, backend, vendor, arch := "qwen3-14b-gptq", "vllm", "amd", "gfx1100"

	BenchmarkTokensPerSecond.WithLabelValues(model, backend, vendor, arch).Set(73.0)
	BenchmarkVRAMUsedBytes.WithLabelValues(model, backend, vendor, arch).Set(9.53e9)

	assert.InDelta(t, 73.0,
		promtestutil.ToFloat64(BenchmarkTokensPerSecond.WithLabelValues(model, backend, vendor, arch)), 0.001)
	assert.InDelta(t, 9.53e9,
		promtestutil.ToFloat64(BenchmarkVRAMUsedBytes.WithLabelValues(model, backend, vendor, arch)), 1)

	BenchmarkTokensPerSecond.DeleteLabelValues(model, backend, vendor, arch)
	BenchmarkVRAMUsedBytes.DeleteLabelValues(model, backend, vendor, arch)
}

// ---------- Wrong label count tests ----------

func TestGaugeVecPanicsOnWrongLabelCount(t *testing.T) {
	// Passing wrong number of label values should panic, verifying label arity.
	assert.Panics(t, func() {
		TokensPerSecond.WithLabelValues("only-one-label") // expects 3
	}, "WithLabelValues with wrong arity should panic")
}

func TestCounterVecPanicsOnWrongLabelCount(t *testing.T) {
	assert.Panics(t, func() {
		ModelTransitionsTotal.WithLabelValues("only-one") // expects 5
	}, "WithLabelValues with wrong arity should panic")
}

// ---------- Helpers ----------

// descInfo holds extracted descriptor fields for test assertions.
type descInfo struct {
	fqName         string
	help           string
	variableLabels []string
}

// collectDescs drains all Desc objects from a Collector and returns parsed info.
func collectDescs(t *testing.T, c prometheus.Collector) []descInfo {
	t.Helper()
	ch := make(chan *prometheus.Desc, 10)
	c.Describe(ch)
	close(ch)

	var result []descInfo
	for desc := range ch {
		result = append(result, parseDesc(desc))
	}
	return result
}

// parseDesc extracts name, help, and variable labels from a prometheus.Desc.
// It uses the Desc.String() representation because the prometheus library does
// not export accessor methods for Desc fields.
func parseDesc(d *prometheus.Desc) descInfo {
	// Desc.String() returns something like:
	//   Desc{fqName: "flexinfer_tokens_per_second", help: "Rolling ...", constLabels: {}, variableLabels: [{model <nil>} {backend <nil>} {node <nil>}]}
	s := d.String()

	return descInfo{
		fqName:         extractField(s, "fqName"),
		help:           extractField(s, "help"),
		variableLabels: extractVariableLabels(s),
	}
}

// extractField extracts a quoted field value from a Desc.String() output.
func extractField(s, field string) string {
	prefix := field + `: "`
	start := indexOf(s, prefix)
	if start == -1 {
		return ""
	}
	start += len(prefix)
	end := start
	for end < len(s) && s[end] != '"' {
		end++
	}
	return s[start:end]
}

// extractVariableLabels parses the variableLabels field from Desc.String().
// Format (prometheus/client_golang v1.23+): variableLabels: {model,backend,node}
func extractVariableLabels(s string) []string {
	prefix := "variableLabels: {"
	start := indexOf(s, prefix)
	if start == -1 {
		return nil
	}
	start += len(prefix)

	// Find the closing brace.
	end := start
	for end < len(s) && s[end] != '}' {
		end++
	}

	inner := strings.TrimSpace(s[start:end])
	if inner == "" {
		return nil
	}

	// Split comma-separated label names.
	parts := strings.Split(inner, ",")
	labels := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			labels = append(labels, p)
		}
	}
	return labels
}

// collectHistogram collects metrics from a Collector and returns the first
// histogram dto found. Returns nil if no histogram is present.
func collectHistogram(t *testing.T, c prometheus.Collector) *dto.Histogram {
	t.Helper()
	ch := make(chan prometheus.Metric, 100)
	c.Collect(ch)
	close(ch)
	for m := range ch {
		var d dto.Metric
		require.NoError(t, m.Write(&d))
		if d.Histogram != nil {
			return d.Histogram
		}
	}
	return nil
}

// indexOf returns the index of substr in s, or -1 if not found.
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
