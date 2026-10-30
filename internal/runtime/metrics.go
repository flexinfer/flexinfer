package runtime

import (
	"regexp"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	RuntimeProfileUnknown = "unknown"
	RuntimeDigestUnknown  = "unresolved"
)

var runtimeDigestPattern = regexp.MustCompile(`^sha256:[0-9a-fA-F]{64}$`)

var (
	ModelEngineStalled = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "flexinfer_model_engine_stalled", Help: "Whether a runtime model engine is stalled."}, []string{"model", "namespace"})

	// RuntimeInfo is a constant gauge with runtime metadata labels.
	RuntimeInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_runtime_info",
			Help: "Runtime instance info (always 1). Labels carry metadata.",
		},
		[]string{"node", "gpu_vendor", "gpu_arch", "runtime_profile", "runtime_digest"},
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

	// ModelLoadDurationSeconds tracks time from backend subprocess start to Ready.
	// It has always been measured from process start; the help text used to say
	// "from load request". The full request->Ready latency, including incumbent
	// unload and VRAM drain, is ModelSwapDurationSeconds.
	ModelLoadDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_runtime_model_load_duration_seconds",
			Help:    "Duration from backend process start to model Ready (excludes incumbent unload and VRAM drain; see flexinfer_runtime_model_swap_duration_seconds).",
			Buckets: []float64{1, 2, 5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300},
		},
		[]string{"model", "backend"},
	)

	// ModelSwapDurationSeconds tracks a load request end to end: incumbent
	// unload, VRAM drain, process start and readiness. outcome is ready,
	// failed (start error, crash or startup timeout before Ready), rejected
	// (VRAM admission) or cancelled (unloaded before Ready).
	ModelSwapDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_runtime_model_swap_duration_seconds",
			Help:    "Duration from runtime load request to model Ready or terminal failure, including incumbent unload and VRAM drain.",
			Buckets: []float64{0.5, 1, 2, 5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300, 450, 600, 900},
		},
		[]string{"model", "backend", "outcome"},
	)

	// LoadedModels reports how many backend subprocesses the runtime currently
	// tracks, per backend (any state: Loading, Ready, Failed, Stopping).
	LoadedModels = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_runtime_loaded_models",
			Help: "Backend subprocesses currently tracked by the runtime, by backend.",
		},
		[]string{"backend"},
	)

	// VRAMDrainSeconds tracks how long a load waited after unloading
	// incumbents for GPU memory to be released. outcome is drained (used VRAM
	// fell by half the unloaded estimate), plateau (stopped decreasing),
	// timeout, or telemetry_lost.
	VRAMDrainSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flexinfer_runtime_vram_drain_seconds",
			Help:    "Time spent waiting for VRAM release after unloading incumbents, before starting the next backend.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 1.5, 2, 3, 5, 7.5, 10, 15, 30},
		},
		[]string{"outcome"},
	)

	// VRAMDrainTimeoutsTotal counts drain waits that hit
	// FLEXINFER_RUNTIME_VRAM_DRAIN_TIMEOUT before VRAM was released.
	VRAMDrainTimeoutsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "flexinfer_runtime_vram_drain_timeouts_total",
			Help: "VRAM drain waits that timed out before the unloaded backend's memory was released.",
		},
	)

	// VRAMTelemetryUnknownTotal counts GPU memory readings the manager refused
	// to act on (absent, out of range, or below half the Ready models' summed
	// vramEstimateMB) and treated as unknown, failing open.
	VRAMTelemetryUnknownTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_runtime_vram_telemetry_unknown_total",
			Help: "VRAM telemetry readings treated as unknown (fail-open), by check (admission|drain) and reason.",
		},
		[]string{"check", "reason"},
	)

	// BackendGroupLeftoversTotal counts unloads where processes of the
	// backend's process group outlived the direct child. result is exited
	// (left on their own before the shutdown deadline), killed (needed
	// SIGKILL) or survived (still present after SIGKILL).
	BackendGroupLeftoversTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_runtime_backend_group_leftovers_total",
			Help: "Unloads where backend process-group members outlived the direct child, by result (exited|killed|survived).",
		},
		[]string{"backend", "result"},
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

	// NodeModeGauge reports the node's current operating mode: 1 for the active
	// mode, 0 for the other. Lets a dashboard/alert see when a node is gaming
	// (inference drained) vs serving. Set on every SetMode transition.
	NodeModeGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_runtime_node_mode",
			Help: "Node operating mode (1 for the active mode: inference|gaming).",
		},
		[]string{"mode"},
	)

	// GamingIdleRevertsTotal counts opt-in idle auto-reverts from gaming back to
	// inference (no Moonlight client for GAMING_IDLE_TIMEOUT).
	GamingIdleRevertsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "flexinfer_runtime_gaming_idle_reverts_total",
			Help: "Total automatic gaming->inference reverts triggered by the idle guard.",
		},
	)

	// GamingBackendRestartsTotal counts supervised restarts of the gaming
	// backend after a subprocess crash while the node is in gaming mode.
	GamingBackendRestartsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flexinfer_runtime_gaming_backend_restarts_total",
			Help: "Supervised gaming backend restart attempts after a crash, by result.",
		},
		[]string{"result"},
	)
)

// RegisterMetrics registers all runtime metrics with the default Prometheus registry.
func RegisterMetrics() {
	prometheus.MustRegister(ModelEngineStalled,
		RuntimeInfo,
		RuntimeUptimeSeconds,
		ModelLoadsTotal,
		ModelUnloadsTotal,
		ModelLoadDurationSeconds,
		ModelSwapDurationSeconds,
		LoadedModels,
		VRAMDrainSeconds,
		VRAMDrainTimeoutsTotal,
		VRAMTelemetryUnknownTotal,
		BackendGroupLeftoversTotal,
		ModelActiveState,
		BackendSubprocessCrashesTotal,
		HealthCheckFailuresTotal,
		GPUVRAMTotalBytesRT,
		GPUVRAMUsedBytesRT,
		GPUVRAMFreeBytesRT,
		GPUTemperatureCelsiusRT,
		NodeModeGauge,
		GamingIdleRevertsTotal,
		GamingBackendRestartsTotal,
		HWQueuesRT,
		HWQueueBudgetRT,
	)
}

func RuntimeProfileLabel(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return RuntimeProfileUnknown
	}
	return profile
}

func RuntimeDigestLabel(imageRef string) string {
	_, digest, ok := strings.Cut(strings.TrimSpace(imageRef), "@")
	if !ok || digest == "" {
		return RuntimeDigestUnknown
	}
	if !runtimeDigestPattern.MatchString(digest) {
		return RuntimeDigestUnknown
	}
	return digest
}
