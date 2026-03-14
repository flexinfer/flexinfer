package runtime

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// RuntimeInfo is a constant gauge with runtime metadata labels.
	RuntimeInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_runtime_info",
			Help: "Runtime instance info (always 1). Labels carry metadata.",
		},
		[]string{"node", "gpu_vendor", "gpu_arch"},
	)

	// RuntimeUptimeSeconds tracks how long the runtime has been running.
	RuntimeUptimeSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "flexinfer_runtime_uptime_seconds",
			Help: "Seconds since the runtime process started.",
		},
	)

	// ModelLoadsTotal counts model load operations.
	ModelLoadsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_runtime_model_loads_total",
			Help: "Total model load operations by backend and result.",
		},
		[]string{"backend", "result"},
	)

	// ModelUnloadsTotal counts model unload operations.
	ModelUnloadsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_runtime_model_unloads_total",
			Help: "Total model unload operations.",
		},
		[]string{"backend", "reason"},
	)

	// ModelLoadDurationSeconds tracks time to start a backend subprocess and reach Ready.
	ModelLoadDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_runtime_model_load_duration_seconds",
			Help:    "Duration from load request to model Ready.",
			Buckets: []float64{1, 2, 5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300},
		},
		[]string{"model", "backend"},
	)

	// ModelState reports the current state of the active model (1 for current state).
	ModelActiveState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_runtime_model_state",
			Help: "Current model state (1 for active state, 0 otherwise).",
		},
		[]string{"model", "backend", "state"},
	)

	// BackendSubprocessCrashesTotal counts unexpected subprocess exits.
	BackendSubprocessCrashesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_runtime_backend_crashes_total",
			Help: "Total unexpected backend subprocess crashes.",
		},
		[]string{"model", "backend"},
	)

	// HealthCheckFailuresTotal counts health check failures.
	HealthCheckFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_runtime_health_check_failures_total",
			Help: "Total health check failures by model.",
		},
		[]string{"model", "backend"},
	)

	// GPUVRAMTotalBytesRT tracks total VRAM from the runtime's perspective.
	GPUVRAMTotalBytesRT = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_runtime_gpu_vram_total_bytes",
			Help: "Total GPU VRAM in bytes (reported by runtime).",
		},
		[]string{"gpu_vendor", "gpu_arch"},
	)

	// GPUVRAMUsedBytesRT tracks used VRAM from the runtime's perspective.
	GPUVRAMUsedBytesRT = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_runtime_gpu_vram_used_bytes",
			Help: "Used GPU VRAM in bytes (reported by runtime).",
		},
		[]string{"gpu_vendor", "gpu_arch"},
	)

	// GPUVRAMFreeBytesRT tracks free VRAM from the runtime's perspective.
	GPUVRAMFreeBytesRT = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_runtime_gpu_vram_free_bytes",
			Help: "Free GPU VRAM in bytes (reported by runtime).",
		},
		[]string{"gpu_vendor", "gpu_arch"},
	)

	// GPUTemperatureCelsiusRT tracks GPU temperature from the runtime.
	GPUTemperatureCelsiusRT = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_runtime_gpu_temperature_celsius",
			Help: "GPU temperature in Celsius (reported by runtime).",
		},
		[]string{"gpu_vendor", "gpu_arch"},
	)
)

// RegisterMetrics registers all runtime metrics with the default Prometheus registry.
func RegisterMetrics() {
	prometheus.MustRegister(
		RuntimeInfo,
		RuntimeUptimeSeconds,
		ModelLoadsTotal,
		ModelUnloadsTotal,
		ModelLoadDurationSeconds,
		ModelActiveState,
		BackendSubprocessCrashesTotal,
		HealthCheckFailuresTotal,
		GPUVRAMTotalBytesRT,
		GPUVRAMUsedBytesRT,
		GPUVRAMFreeBytesRT,
		GPUTemperatureCelsiusRT,
	)
}
