// Package runtime implements the flexinfer-runtime process manager.
// It manages backend subprocesses (vLLM, llamacpp, ollama, diffusers)
// on a single GPU node, enabling near-instant model swapping without
// pod scheduling overhead.
package runtime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ModelState tracks the lifecycle phase of a loaded model.
type ModelState string

const (
	ModelStateLoading  ModelState = "Loading"
	ModelStateReady    ModelState = "Ready"
	ModelStateFailed   ModelState = "Failed"
	ModelStateStopping ModelState = "Stopping"
)

// LoadRequest describes a model to load into the runtime.
type LoadRequest struct {
	Backend   string          `json:"backend"`
	Model     string          `json:"model"`
	ModelPath string          `json:"modelPath,omitempty"`
	Config    map[string]any  `json:"config,omitempty"`
	Env       []corev1.EnvVar `json:"env,omitempty"`

	// Generation and SpecHash identify the Model spec the request was rendered
	// from (pkg/runtime.LoadPayload). They are recorded on the loaded model and
	// reported back; the runtime never compares them itself.
	Generation int64  `json:"generation,omitempty"`
	SpecHash   string `json:"specHash,omitempty"`
	// Reload restarts an already-loaded model of the same name with this
	// request instead of returning early. Request drain still applies.
	Reload bool `json:"reload,omitempty"`
	// HWQueues is the GPUProfile compute-queue budget to admit this load
	// against (pkg/runtime.LoadPayload.HWQueues); nil keeps the last one seen.
	HWQueues *pkgrt.HWQueueBudget `json:"hwQueues,omitempty"`
}

// LoadedModel tracks a running backend subprocess and its model.
type LoadedModel struct {
	Name        string             `json:"name"`
	Backend     string             `json:"backend"`
	Model       string             `json:"model"`
	State       ModelState         `json:"state"`
	Port        int32              `json:"port"`
	PID         int                `json:"pid,omitempty"`
	LoadedAt    time.Time          `json:"loadedAt,omitempty"`
	Error       string             `json:"error,omitempty"`
	Generation  int64              `json:"generation,omitempty"`
	SpecHash    string             `json:"specHash,omitempty"`
	HealthURL   string             `json:"-"`
	cmd         *exec.Cmd          `json:"-"`
	cancel      context.CancelFunc `json:"-"`
	done        chan error         `json:"-"`
	backendPort int32
	ingress     *modelIngress

	stallWindow time.Duration

	// pgid is the backend's own process group (its PID as group leader), or 0
	// when it was not started in a dedicated group. Unload signals the whole
	// group so engine workers and runner grandchildren do not keep VRAM.
	pgid int
	// progress follows the backend's streamed output for the load substage
	// and the vLLM KV-cache profile reported in model health.
	progress *loadProgress
	// vramEstimateMB is the load request's config.vramEstimateMB (0 = unknown).
	vramEstimateMB int64
	// hwQueues is the hardware compute queues this subprocess is estimated to
	// hold (hwQueueEstimateFor).
	hwQueues int32
	// requestedAt is when Load was called; swapObserved guards the single
	// ModelSwapDurationSeconds observation per load. Both guarded by m.mu.
	requestedAt  time.Time
	swapObserved bool
}

// observeSwapLocked records the request->terminal-state latency for this load
// exactly once. Caller must hold m.mu.
func (lm *LoadedModel) observeSwapLocked(outcome string) {
	if lm.swapObserved || lm.requestedAt.IsZero() {
		return
	}
	lm.swapObserved = true
	ModelSwapDurationSeconds.WithLabelValues(lm.Name, lm.Backend, outcome).Observe(time.Since(lm.requestedAt).Seconds())
}

// NodeMode represents the operating mode of a GPU node.
type NodeMode string

const (
	ModeInference NodeMode = "inference"
	ModeGaming    NodeMode = "gaming"
)

// gamingModelName is the reserved model slot for the gaming backend subprocess.
const gamingModelName = "__gaming__"

// gamingStableUptime is how long the gaming backend must have been up for a
// crash to be treated as fresh (backoff restarts from the base delay) rather
// than a continuation of a crash loop.
const gamingStableUptime = 5 * time.Minute

// Supervised-restart backoff bounds for the gaming backend.
const (
	defaultGamingRestartBase = 2 * time.Second
	defaultGamingRestartMax  = time.Minute
)

// Manager controls backend subprocess lifecycle on a GPU node.
//
// By default a single backend subprocess runs at a time (single-GPU,
// single-slot). When MultiModel is enabled the manager holds a VRAM-bounded
// SET of concurrent subprocesses, each on its own port — letting e.g. an
// embeddings lane and a reranker co-reside on the same card. Single-slot is
// the default; the multi-model path is opt-in and additive.
type Manager struct {
	// opMu serializes lifecycle operations. m.mu protects state reads/writes,
	// but must not be held while waiting for backend subprocess shutdown.
	opMu sync.Mutex

	mu sync.RWMutex

	// models holds every loaded model keyed by name. In single-slot mode it
	// contains at most one entry (Load unloads the incumbent first); in
	// multi-model mode it can hold several VRAM-permitting concurrent models.
	models map[string]*LoadedModel

	// multiModel enables holding multiple concurrent subprocesses (VRAM-bounded).
	multiModel bool

	// vramHeadroomMB is the free-VRAM safety margin kept when admitting a new
	// model in multi-model mode. Admission is skipped (fail-open) when GPU
	// telemetry is unavailable.
	vramHeadroomMB int64

	// mode tracks whether the node is in inference or gaming mode.
	mode NodeMode

	// gamingBackend is the backend name loaded when switching to gaming mode.
	// Defaults to "sunshine"; set to "steam" to opt into the legacy Remote Play
	// path. Resolved via backend.Get, so aliases (e.g. "gaming") also work.
	gamingBackend string

	// gamingIdleTimeout, when > 0, opts into auto-reverting a gaming node back to
	// inference after this long with no connected Moonlight client. 0 (default)
	// disables it — matching the SteamBackend/SunshineBackend "never auto-idle"
	// contract; gaming sessions then persist until an explicit inference switch.
	gamingIdleTimeout time.Duration

	// gamingClientActive reports whether a Moonlight client is currently streaming
	// (injectable for tests; default probes the Sunshine ports). Used only by the
	// idle guard when gamingIdleTimeout > 0.
	gamingClientActive func() bool

	// nowFn is the clock (injectable for tests; default time.Now).
	nowFn func() time.Time

	// gamingIdleCancel stops the idle-guard goroutine when leaving gaming mode.
	gamingIdleCancel context.CancelFunc

	// gamingRestart re-drives the gaming backend after a subprocess crash
	// (injectable for tests; default reloads gamingBackend via Load).
	gamingRestart func(ctx context.Context) error

	// gamingRestartBase and gamingRestartMax bound the supervised-restart
	// exponential backoff for the gaming backend.
	gamingRestartBase time.Duration
	gamingRestartMax  time.Duration

	// gamingRestartAttempt counts consecutive gaming-backend crashes and failed
	// restarts, driving the backoff. Guarded by mu; reset on mode switches and
	// on a crash after gamingStableUptime of healthy runtime.
	gamingRestartAttempt int

	// gpuVendor and gpuArch describe the GPU on this node.
	gpuVendor backend.GPUVendor
	gpuArch   string

	// shutdownTimeout is how long to wait for graceful subprocess exit, for the
	// direct child and the rest of its process group together.
	shutdownTimeout time.Duration

	// groupKillGrace bounds the wait for a process group to disappear after
	// the final SIGKILL.
	groupKillGrace time.Duration

	// vramDrainTimeout bounds the wait for GPU memory to be released after
	// unloading incumbents and before starting the next backend (0 = disabled).
	vramDrainTimeout time.Duration

	// vramDrainPoll is the telemetry sampling interval while draining.
	vramDrainPoll time.Duration

	// gpuQuery reads GPU memory telemetry (injectable for tests; default reads
	// sysfs/rocm-smi/nvidia-smi for gpuVendor without the temperature lookup).
	gpuQuery func() GPUInfo

	// healthCheckInterval controls how often the subprocess is probed.
	healthCheckInterval time.Duration

	// modelBasePath is the root path where models are mounted.
	modelBasePath string

	// requestDrain gives each HTTP model a request-counting ingress on Port.
	requestDrain bool

	// gaugedBackends remembers every backend label LoadedModels was set for,
	// so a backend whose last model unloads is reported as 0. Guarded by mu.
	gaugedBackends map[string]struct{}

	// hwQueueBudget is the last GPUProfile queue budget a load carried (nil =
	// none yet, admission open). Guarded by mu.
	hwQueueBudget *pkgrt.HWQueueBudget
	// hwQueueQuery counts the compute queues KFD reports on this GPU
	// (injectable for tests; nil or ok=false = no telemetry).
	hwQueueQuery func() (int32, bool)
}

// defaultVRAMHeadroomMB is the free-VRAM margin kept before admitting a new
// concurrent model in multi-model mode.
const defaultVRAMHeadroomMB int64 = 1024

// DefaultVRAMDrainTimeout is the runtime binary's default for
// FLEXINFER_RUNTIME_VRAM_DRAIN_TIMEOUT.
const DefaultVRAMDrainTimeout = 10 * time.Second

const (
	defaultVRAMDrainPoll  = 500 * time.Millisecond
	defaultGroupKillGrace = 2 * time.Second
	groupPollInterval     = 50 * time.Millisecond
)

// ManagerConfig holds configuration for creating a Manager.
type ManagerConfig struct {
	GPUVendor           backend.GPUVendor
	GPUArch             string
	ShutdownTimeout     time.Duration
	HealthCheckInterval time.Duration
	ModelBasePath       string
	// RequestDrain routes accepted inference through a managed ingress and
	// refuses unload/swap until those requests finish. Default false.
	RequestDrain bool

	// MultiModel enables concurrent VRAM-bounded subprocesses (default false =
	// single-slot, behavior identical to the pre-multi-model runtime).
	MultiModel bool
	// VRAMHeadroomMB overrides the default free-VRAM admission margin.
	VRAMHeadroomMB int64

	// VRAMDrainTimeout bounds how long Load waits, after unloading incumbents,
	// for used VRAM to fall before starting the next backend. 0 disables the
	// wait; the runtime binary defaults it to DefaultVRAMDrainTimeout via
	// FLEXINFER_RUNTIME_VRAM_DRAIN_TIMEOUT.
	VRAMDrainTimeout time.Duration

	// GamingBackend overrides the backend loaded for gaming mode. Empty =
	// "sunshine" (the default). Set to "steam" for the legacy Remote Play path.
	GamingBackend string

	// GamingIdleTimeout opts into auto-reverting a gaming node to inference after
	// this long with no Moonlight client (0 = disabled, the default). Wired from
	// the GAMING_IDLE_TIMEOUT env var by the runtime entrypoint.
	GamingIdleTimeout time.Duration
}

// NewManager creates a runtime process manager.
func NewManager(cfg ManagerConfig) *Manager {
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = 30 * time.Second
	}
	if cfg.HealthCheckInterval == 0 {
		cfg.HealthCheckInterval = 5 * time.Second
	}
	if cfg.ModelBasePath == "" {
		cfg.ModelBasePath = "/models"
	}
	if cfg.VRAMHeadroomMB <= 0 {
		cfg.VRAMHeadroomMB = defaultVRAMHeadroomMB
	}
	if cfg.GamingBackend == "" {
		cfg.GamingBackend = backend.NameSunshine
	}
	m := &Manager{
		models:              make(map[string]*LoadedModel),
		multiModel:          cfg.MultiModel,
		vramHeadroomMB:      cfg.VRAMHeadroomMB,
		mode:                ModeInference,
		gamingBackend:       cfg.GamingBackend,
		gamingIdleTimeout:   cfg.GamingIdleTimeout,
		gamingRestartBase:   defaultGamingRestartBase,
		gamingRestartMax:    defaultGamingRestartMax,
		nowFn:               time.Now,
		gpuVendor:           cfg.GPUVendor,
		gpuArch:             cfg.GPUArch,
		shutdownTimeout:     cfg.ShutdownTimeout,
		groupKillGrace:      defaultGroupKillGrace,
		vramDrainTimeout:    cfg.VRAMDrainTimeout,
		vramDrainPoll:       defaultVRAMDrainPoll,
		healthCheckInterval: cfg.HealthCheckInterval,
		modelBasePath:       cfg.ModelBasePath,
		requestDrain:        cfg.RequestDrain,
		gaugedBackends:      make(map[string]struct{}),
	}
	m.gpuQuery = func() GPUInfo { return queryVRAM(string(m.gpuVendor)) }
	if m.gpuVendor == backend.GPUVendorAMD {
		m.hwQueueQuery = func() (int32, bool) { return ObservedHWQueues(m.gpuArch) }
	}
	m.gamingClientActive = m.sunshineClientActive
	m.gamingRestart = m.loadGamingBackend
	// Reflect the initial mode in the gauge.
	NodeModeGauge.WithLabelValues(string(ModeInference)).Set(1)
	NodeModeGauge.WithLabelValues(string(ModeGaming)).Set(0)
	return m
}

// allModelStates enumerates the state label values used by ModelActiveState, so
// per-model metric series can be cleared without Reset() wiping other models.
var allModelStates = []string{"Loading", "Ready", "Failed", "Stopping"}

// setModelStateMetric marks the given model's current state as 1 and clears its
// other state series. Unlike ModelActiveState.Reset() it touches only this
// model's label series, so concurrent models' gauges are preserved.
func setModelStateMetric(name, backend, state string) {
	for _, s := range allModelStates {
		ModelActiveState.DeleteLabelValues(name, backend, s)
	}
	ModelActiveState.WithLabelValues(name, backend, state).Set(1)
}

// clearModelStateMetric removes all state series for a model (on unload).
func clearModelStateMetric(name, backend string) {
	for _, s := range allModelStates {
		ModelActiveState.DeleteLabelValues(name, backend, s)
	}
}

// primaryLocked returns a representative loaded model for back-compat with the
// single-active API surface (api.go Active(), Status().ActiveModel). It prefers
// a Ready model, then a Loading one, then any. Caller must hold m.mu.
func (m *Manager) primaryLocked() *LoadedModel {
	var loading, any *LoadedModel
	for _, lm := range m.models {
		switch lm.State {
		case ModelStateReady:
			return lm
		case ModelStateLoading:
			if loading == nil {
				loading = lm
			}
		}
		if any == nil {
			any = lm
		}
	}
	if loading != nil {
		return loading
	}
	return any
}

// usedPortsLocked returns the set of ports currently claimed by loaded models.
// Caller must hold m.mu.
func (m *Manager) usedPortsLocked() map[int32]bool {
	ports := make(map[int32]bool, len(m.models))
	for _, lm := range m.models {
		ports[lm.Port] = true
		if lm.backendPort != 0 {
			ports[lm.backendPort] = true
		}
	}
	return ports
}

// allocatePortLocked picks a port for a new model. It honors an explicitly
// requested port (config.port, surfaced via preferred) when free; otherwise it
// scans upward from the default backend port for a free port not used by an
// existing model and not equal to the runtime API port. Caller must hold m.mu.
func (m *Manager) allocatePortLocked(preferred int32) int32 {
	used := m.usedPortsLocked()
	if preferred > 0 && preferred != pkgrt.RuntimeAPIPort && !used[preferred] {
		return preferred
	}
	for p := pkgrt.RuntimeBackendPort; p < pkgrt.RuntimeBackendPort+512; p++ {
		if p == pkgrt.RuntimeAPIPort || used[p] {
			continue
		}
		return p
	}
	return preferred
}

// readyEstimateLocked sums vramEstimateMB over Ready models — the VRAM the
// runtime believes is resident. Loading models are excluded because they may
// not have allocated yet. Caller must hold m.mu.
func (m *Manager) readyEstimateLocked() int64 {
	var sum int64
	for _, lm := range m.models {
		if lm.State == ModelStateReady {
			sum += lm.vramEstimateMB
		}
	}
	return sum
}

// refreshLoadedModelsLocked publishes LoadedModels from m.models, zeroing
// backends that no longer have a tracked subprocess. Caller must hold m.mu.
func (m *Manager) refreshLoadedModelsLocked() {
	counts := make(map[string]int, len(m.models))
	for _, lm := range m.models {
		counts[lm.Backend]++
		m.gaugedBackends[lm.Backend] = struct{}{}
	}
	for b := range m.gaugedBackends {
		LoadedModels.WithLabelValues(b).Set(float64(counts[b]))
	}
	m.publishHWQueuesLocked()
}

// canAdmit reports whether a new model with the given VRAM estimate fits
// alongside the currently loaded models. It is fail-open: when GPU telemetry is
// unavailable, or implausible next to the Ready models' summed estimates (see
// vramTelemetryPlausible), it admits and lets the backend's own OOM handling
// apply. Caller must not hold m.mu (the query may shell out); callers pass
// estimateMB=0 to mean "unknown estimate" (admission then only enforces the
// headroom margin).
func (m *Manager) canAdmit(logger logr.Logger, estimateMB int64) (bool, string) {
	info := m.gpuQuery()
	m.mu.RLock()
	readyMB := m.readyEstimateLocked()
	m.mu.RUnlock()
	if ok, reason := vramTelemetryPlausible(info, readyMB); !ok {
		VRAMTelemetryUnknownTotal.WithLabelValues("admission", reason).Inc()
		if reason != "absent" {
			logger.Info("WARNING: VRAM telemetry implausible; treating as unknown",
				"reason", reason, "usedMB", info.VRAMUsedMB, "totalMB", info.VRAMTotalMB, "readyEstimateMB", readyMB)
		}
		return true, fmt.Sprintf("vram telemetry unknown (%s: used=%dMB total=%dMB readyEstimate=%dMB); admitting (fail-open)",
			reason, info.VRAMUsedMB, info.VRAMTotalMB, readyMB)
	}
	if info.VRAMFreeMB-estimateMB < m.vramHeadroomMB {
		return false, fmt.Sprintf("insufficient VRAM: free=%dMB estimate=%dMB headroom=%dMB",
			info.VRAMFreeMB, estimateMB, m.vramHeadroomMB)
	}
	return true, fmt.Sprintf("admit: free=%dMB estimate=%dMB headroom=%dMB",
		info.VRAMFreeMB, estimateMB, m.vramHeadroomMB)
}

// Load starts a backend subprocess for the named model. If another model
// is already active, it is unloaded first (serialize load/unload), and the
// next backend only starts once the unloaded process groups are gone and GPU
// memory has drained (bounded by vramDrainTimeout). The whole sequence runs
// under opMu so loads and unloads stay serialized.
func (m *Manager) Load(ctx context.Context, name string, req LoadRequest) error {
	requestedAt := time.Now()
	logger := log.FromContext(ctx).WithValues("model", name, "backend", req.Backend)

	b, ok := backend.Get(req.Backend)
	if !ok {
		return fmt.Errorf("%w: %s", backend.ErrUnknownBackend, req.Backend)
	}

	if !b.SupportsGPUVendor(m.gpuVendor) {
		return fmt.Errorf("backend %q does not support GPU vendor %q", req.Backend, m.gpuVendor)
	}

	if b.Name() == backend.NameComfyUI {
		return fmt.Errorf("backend %q is not bundled in flexinfer-runtime images; use the dedicated ComfyUI image", req.Backend)
	}

	m.opMu.Lock()
	defer m.opMu.Unlock()

	// Idempotency: if the same model+backend is already loaded and healthy,
	// skip the unload/reload cycle.  This prevents the controller and proxy
	// from fighting over the same model. An explicit reload (the controller
	// applying a spec edit or a KV-pool recheck) restarts it instead, through
	// the same incumbent unload below, so request drain still applies.
	m.mu.Lock()
	if existing, ok := m.models[name]; ok && existing.Backend == req.Backend &&
		(existing.State == ModelStateReady || existing.State == ModelStateLoading) && !modelIsDraining(existing) {
		state := string(existing.State)
		if !req.Reload {
			m.mu.Unlock()
			logger.Info("Model already loaded, skipping reload",
				"state", state)
			return nil
		}
		logger.Info("Reloading model",
			"state", state,
			"loadedGeneration", existing.Generation, "generation", req.Generation,
			"loadedSpecHash", existing.SpecHash, "specHash", req.SpecHash)
	}

	// Determine which incumbents to unload before loading.
	//   - single-slot mode: unload every other model (only one runs at a time).
	//   - multi-model mode: unload only a stale/failed entry with this same name
	//     (a reload); leave the other concurrent models running.
	var toUnload []*LoadedModel
	if m.multiModel {
		if stale, ok := m.models[name]; ok {
			toUnload = append(toUnload, stale)
		}
	} else {
		for _, lm := range m.models {
			toUnload = append(toUnload, lm)
		}
	}
	readyEstimateMB := m.readyEstimateLocked()
	var unloadEstimateMB int64
	for _, lm := range toUnload {
		unloadEstimateMB += lm.vramEstimateMB
	}
	m.mu.Unlock()
	if len(toUnload) > 0 {
		var pre GPUInfo
		if m.vramDrainTimeout > 0 {
			pre = m.gpuQuery()
		}
		for _, lm := range toUnload {
			logger.Info("Unloading model before loading new one", "unloading", lm.Name, "loading", name)
			if err := m.unloadActive(ctx, lm); err != nil {
				return fmt.Errorf("unloading incumbent %q before loading %q: %w", lm.Name, name, err)
			}
		}
		m.waitVRAMDrain(logger, pre, readyEstimateMB, unloadEstimateMB)
	}

	// Build the model spec for arg/env generation.
	modelPath := req.ModelPath
	if modelPath == "" && b.NeedsVolume() {
		modelPath = fmt.Sprintf("%s/%s", m.modelBasePath, name)
	}

	spec := &backend.ModelSpec{
		Name:      name,
		Model:     req.Model,
		ModelPath: modelPath,
		Config:    req.Config,
		GPUVendor: m.gpuVendor,
		GPUArch:   m.gpuArch,
	}
	backendPort := runtimeBackendPort(b, spec)
	var estimateMB int64
	if v := spec.ConfigInt("vramEstimateMB", 0); v > 0 {
		estimateMB = int64(v)
	}

	// Hardware compute-queue admission (GPUProfile maxHWQueues). It runs in
	// both modes: the queues KFD reports include tenants outside this runtime.
	hwQueues := hwQueueEstimateFor(b.Name(), req)
	ok, reason := m.admitHWQueues(logger, name, hwQueues, req.HWQueues)
	if !ok {
		ModelSwapDurationSeconds.WithLabelValues(name, req.Backend, "rejected").Observe(time.Since(requestedAt).Seconds())
		return fmt.Errorf("%w: cannot load %q: %s", ErrInsufficientHWQueues, name, reason)
	}
	if reason != hwQueueNoBudget {
		logger.Info("Hardware queue admission passed", "reason", reason)
	}

	// Multi-model mode: allocate a distinct port per concurrent subprocess and
	// enforce VRAM admission. Single-slot mode keeps the fixed backend port and
	// skips admission (the incumbent was already unloaded above).
	if m.multiModel {
		m.mu.Lock()
		backendPort = m.allocatePortLocked(backendPort)
		m.mu.Unlock()
		// Reflect the chosen port back into the spec config so backends that read
		// config.port (e.g. llama-server --port) bind where we expect to probe.
		if spec.Config == nil {
			spec.Config = make(map[string]any, 1)
		}
		spec.Config["port"] = float64(backendPort)
		if ok, reason := m.canAdmit(logger, estimateMB); !ok {
			ModelSwapDurationSeconds.WithLabelValues(name, req.Backend, "rejected").Observe(time.Since(requestedAt).Seconds())
			return fmt.Errorf("%w: cannot load %q: %s", ErrInsufficientVRAM, name, reason)
		} else {
			logger.Info("VRAM admission passed", "model", name, "reason", reason, "port", backendPort)
		}
	}
	ingressPort := backendPort

	// Reserve the advertised listener before starting the child so binding
	// failures cannot leave an untracked subprocess. The backend gets a separate
	// kernel-selected port; only ingressPort is exposed to callers.
	var ingress *modelIngress
	var privatePortReservation net.Listener
	if m.requestDrain && supportsRequestIngress(b.Name()) {
		m.mu.RLock()
		used := m.usedPortsLocked()
		m.mu.RUnlock()
		used[ingressPort] = true
		used[pkgrt.RuntimeAPIPort] = true
		reservation, privatePort, err := reserveBackendPort(used)
		if err != nil {
			return err
		}
		defer func() { _ = reservation.Close() }()
		ingress, err = newModelIngress(ingressPort, privatePort)
		if err != nil {
			return err
		}
		backendPort = privatePort
		// exec.Cmd cannot inherit a listener for arbitrary backends. Keep the
		// selected private port in this model's allocation set until cleanup.
		// Closed below once the full child command has been prepared.
		privatePortReservation = reservation
	}

	// Build command and args.
	command := b.Command()
	args := b.Args(spec)
	env := b.Env(spec)

	// Add architecture-specific env vars for AMD.
	//
	// ROCmEnvVars provides the in-code fallback. GPUProfile-declared env is
	// applied later via overlayEnvVars(req.Env) at the end of this block (the
	// payload builder injects profile.Env into req.Env), so profile entries
	// override these baseline values per the GPUProfile-first contract — see
	// backend.ResolveBackendROCmEnv.
	if m.gpuVendor == backend.GPUVendorAMD {
		env = append(env, backend.ROCmEnvVars(m.gpuArch)...)
		env = append(env, backend.DeviceIsolationEnvVars(spec)...)
	}

	// In the runtime, all models share /models without SubPath mounts.
	// Set LOCAL_MODEL_PATH so backends that use env-based model discovery
	// (e.g. diffusers server-diffusers.py) find the correct subdirectory.
	if modelPath != "" {
		env = overlayEnvVars(env, []corev1.EnvVar{{Name: "LOCAL_MODEL_PATH", Value: modelPath}})
	}
	env = overlayEnvVars(env, req.Env)
	if ingress != nil {
		args, env = configureIngressBackend(b.Name(), backendPort, args, env)
	}

	// Determine the executable.
	var executable string
	var execArgs []string
	if len(command) > 0 {
		executable = command[0]
		execArgs = append(command[1:], args...)
	} else {
		// Infer executable and default args from backend name.
		// Python-based backends (vLLM, diffusers) need a module or script
		// path since the runtime container doesn't use Dockerfile CMD.
		var defaultArgs []string
		executable, defaultArgs = inferCommand(b.Name())
		execArgs = append(defaultArgs, args...)
	}
	if resolved, ok := resolveExecutable(executable); ok && resolved != executable {
		logger.Info("Resolved backend executable from PATH",
			"requested", executable,
			"resolved", resolved)
		executable = resolved
	}

	subCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(subCtx, executable, execArgs...)
	setProcessGroup(cmd)

	// Inherit current process env, then overlay backend-specific vars.
	cmd.Env = os.Environ()
	for _, e := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", e.Name, e.Value))
	}

	// Pipe stdout/stderr to structured logging. Errors here typically mean
	// Start() was already called (programming error) — log and continue
	// so the subprocess still launches, just without log streaming.
	stdout, stdoutErr := cmd.StdoutPipe()
	stderr, stderrErr := cmd.StderrPipe()
	if stdoutErr != nil {
		logger.Error(stdoutErr, "Failed to create stdout pipe, subprocess logs will be lost")
	}
	if stderrErr != nil {
		logger.Error(stderrErr, "Failed to create stderr pipe, subprocess logs will be lost")
	}

	ModelEngineStalled.WithLabelValues(name, runtimeMetricsNamespace()).Set(0)
	loaded := &LoadedModel{
		Name:        name,
		Backend:     req.Backend,
		Model:       req.Model,
		State:       ModelStateLoading,
		Port:        ingressPort,
		backendPort: backendPort,
		ingress:     ingress,
		cmd:         cmd,
		cancel:      cancel,
		done:        make(chan error, 1),

		Generation: req.Generation,
		SpecHash:   req.SpecHash,
		progress:   &loadProgress{},

		vramEstimateMB: estimateMB,
		hwQueues:       hwQueues,
		requestedAt:    requestedAt,
	}
	loaded.stallWindow = backend.StallWindowForSpec(spec)

	m.mu.Lock()
	m.models[name] = loaded
	m.refreshLoadedModelsLocked()
	m.mu.Unlock()

	setModelStateMetric(name, req.Backend, "Loading")

	logger.Info("Starting backend subprocess",
		"executable", executable,
		"args", execArgs,
		"port", backendPort,
	)
	if privatePortReservation != nil {
		_ = privatePortReservation.Close()
	}

	if err := cmd.Start(); err != nil {
		ingress.close()
		m.mu.Lock()
		loaded.State = ModelStateFailed
		loaded.Error = err.Error()
		if cur, ok := m.models[name]; ok && cur == loaded {
			delete(m.models, name)
		}
		loaded.observeSwapLocked("failed")
		m.refreshLoadedModelsLocked()
		m.mu.Unlock()
		cancel()
		close(loaded.done)
		ModelLoadsTotal.WithLabelValues(req.Backend, "error").Inc()
		setModelStateMetric(name, req.Backend, "Failed")
		return fmt.Errorf("failed to start backend: %w", err)
	}

	m.mu.Lock()
	loaded.PID = cmd.Process.Pid
	loaded.pgid = processGroupOf(cmd)
	loaded.LoadedAt = time.Now()
	m.mu.Unlock()

	ModelLoadsTotal.WithLabelValues(req.Backend, "ok").Inc()
	if ingress != nil {
		ingress.start()
	}

	// Stream subprocess output to logger. Only start goroutines if pipes
	// were created successfully — a nil reader would panic in bufio.NewScanner.
	if stdout != nil {
		go streamLogs(stdout, logger, "stdout", loaded.progress.observe)
	}
	if stderr != nil {
		go streamLogs(stderr, logger, "stderr", loaded.progress.observe)
	}

	// Monitor subprocess exit.
	go m.monitorProcess(subCtx, loaded)

	// Start health checking in background. The load request config may raise
	// the backend's default startup timeout (the proxy/controller pass the
	// model's coldStartTimeout) but not lower it: the controller's 60 s
	// coldStartTimeout default would otherwise fail a vLLM or diffusers cold
	// load that the backend itself budgets 300 s / 900 s for.
	startupTimeout, requested, floored := resolveStartupTimeout(b.StartupTimeout(), req.Config)
	if floored {
		logger.Info("Payload startupTimeoutSeconds is below the backend default; using the backend default",
			"requested", requested, "backendDefault", startupTimeout)
	}
	go m.healthCheckLoop(subCtx, loaded, b, backendPort, startupTimeout)

	return nil
}

// resolveStartupTimeout applies the load payload's startupTimeoutSeconds
// (seconds as a number, or a Go duration string) on top of the backend's own
// default, flooring it at that default. requested is the parsed payload value
// (0 when absent or invalid); floored reports that it was raised.
func resolveStartupTimeout(backendDefault time.Duration, config map[string]any) (timeout, requested time.Duration, floored bool) {
	switch t := config["startupTimeoutSeconds"].(type) {
	case float64:
		requested = time.Duration(t * float64(time.Second))
	case int:
		requested = time.Duration(t) * time.Second
	case int64:
		requested = time.Duration(t) * time.Second
	case string:
		if d, err := time.ParseDuration(t); err == nil {
			requested = d
		}
	}
	if requested <= 0 {
		return backendDefault, 0, false
	}
	if requested < backendDefault {
		return backendDefault, requested, true
	}
	return requested, requested, false
}

// Unload stops the active model's backend subprocess.
func (m *Manager) Unload(ctx context.Context, name string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	active, ok := m.models[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("model %q is not loaded", name)
	}
	m.mu.Unlock()

	return m.unloadActive(ctx, active)
}

// unloadActive stops the given subprocess without holding m.mu while waiting.
func (m *Manager) unloadActive(ctx context.Context, active *LoadedModel) error {
	if active == nil {
		return nil
	}

	logger := log.FromContext(ctx).WithValues("model", active.Name)
	m.mu.Lock()
	if cur, ok := m.models[active.Name]; !ok || cur != active {
		m.mu.Unlock()
		return nil
	}
	if active.ingress != nil {
		if count := active.ingress.beginDrain(); count > 0 {
			m.mu.Unlock()
			return fmt.Errorf("%w: model %q has %d active requests", ErrModelBusy, active.Name, count)
		}
	}
	if active.State == ModelStateLoading {
		active.observeSwapLocked("cancelled")
	}
	active.State = ModelStateStopping
	m.mu.Unlock()
	active.ingress.close()

	if active.cmd != nil && active.cmd.Process != nil {
		m.stopBackend(logger, active)
	}

	if active.cancel != nil {
		active.cancel()
	}

	ModelUnloadsTotal.WithLabelValues(active.Backend, "requested").Inc()
	clearModelStateMetric(active.Name, active.Backend)
	m.mu.Lock()
	if cur, ok := m.models[active.Name]; ok && cur == active {
		delete(m.models, active.Name)
		ModelEngineStalled.DeleteLabelValues(active.Name, runtimeMetricsNamespace())
	}
	m.refreshLoadedModelsLocked()
	m.mu.Unlock()
	return nil
}

// stopBackend terminates a backend subprocess and everything in its process
// group. SIGTERM goes to the direct child and the group; whatever is still
// running when shutdownTimeout elapses (measured once, for the child and its
// group together) is SIGKILLed. It returns only once the direct child has been
// reaped and the group is empty or has survived SIGKILL for groupKillGrace.
func (m *Manager) stopBackend(logger logr.Logger, active *LoadedModel) {
	deadline := time.Now().Add(m.shutdownTimeout)
	pgid := active.pgid

	logger.Info("Sending SIGTERM to backend", "pid", active.PID, "pgid", pgid)
	_ = active.cmd.Process.Signal(syscall.SIGTERM)
	if pgid > 0 {
		if err := signalProcessGroup(pgid, syscall.SIGTERM); err != nil {
			logger.Info("Signalling backend process group failed", "pgid", pgid, "error", err)
		}
	}

	timer := time.NewTimer(m.shutdownTimeout)
	defer timer.Stop()
	select {
	case <-timer.C:
		// Only kill if the process has not yet exited. We use a
		// non-blocking check on done to avoid a race with PID reuse:
		// if Wait() already returned, the PID is reaped and could be
		// reassigned to an unrelated process. The group is safe to signal
		// either way: its ID cannot be reused while any member is alive.
		select {
		case err := <-active.done:
			// Process already exited before we could kill it.
			if err != nil {
				logger.Info("Backend exited during shutdown timeout", "error", err)
			}
		default:
			logger.Info("Shutdown timeout exceeded, sending SIGKILL", "pid", active.PID, "pgid", pgid)
			if err := active.cmd.Process.Kill(); err != nil {
				logger.Info("Kill returned error (process may have already exited)", "error", err)
			}
			if pgid > 0 {
				_ = signalProcessGroup(pgid, syscall.SIGKILL)
			}
			<-active.done // Wait for the process to be reaped after kill.
		}
	case err := <-active.done:
		if err != nil {
			logger.Info("Backend exited", "error", err)
		}
	}

	if pgid > 0 {
		m.reapProcessGroup(logger, active.Backend, pgid, deadline)
	}
}

// reapProcessGroup waits for the rest of a backend's process group (engine
// workers, runner grandchildren) to exit after the direct child has, so their
// VRAM is released before the next load. Survivors past deadline are
// SIGKILLed; anything still present after groupKillGrace is logged and
// counted, since it may still hold GPU memory.
func (m *Manager) reapProcessGroup(logger logr.Logger, backendName string, pgid int, deadline time.Time) {
	alive, members := processGroupAlive(pgid)
	if !alive {
		return
	}
	logger.Info("Backend exited but its process group still has members; waiting", "pgid", pgid, "members", members)
	if waitProcessGroupGone(pgid, deadline) {
		BackendGroupLeftoversTotal.WithLabelValues(backendName, "exited").Inc()
		return
	}
	_, members = processGroupAlive(pgid)
	logger.Info("Backend process group outlived the shutdown timeout, sending SIGKILL", "pgid", pgid, "members", members)
	_ = signalProcessGroup(pgid, syscall.SIGKILL)
	if waitProcessGroupGone(pgid, time.Now().Add(m.groupKillGrace)) {
		BackendGroupLeftoversTotal.WithLabelValues(backendName, "killed").Inc()
		return
	}
	_, members = processGroupAlive(pgid)
	logger.Error(nil, "Backend process group survived SIGKILL; its GPU memory may not be released",
		"pgid", pgid, "members", members)
	BackendGroupLeftoversTotal.WithLabelValues(backendName, "survived").Inc()
}

// waitProcessGroupGone polls until pgid has no live members or deadline passes.
func waitProcessGroupGone(pgid int, deadline time.Time) bool {
	for {
		if alive, _ := processGroupAlive(pgid); !alive {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		time.Sleep(min(groupPollInterval, remaining))
	}
}

// waitVRAMDrain blocks after incumbents were unloaded until the GPU reports
// their memory released, so the next backend does not start against VRAM that
// is still held (and OOM, or size its KV cache to the leftover). pre is the
// reading taken before the unload, readyEstimateMB the Ready models' summed
// estimates at that time, unloadedEstimateMB the unloaded models' estimates.
//
// It returns when used VRAM falls to pre - unloadedEstimateMB/2 (when the
// estimate is known), when two consecutive samples stop decreasing, or at
// vramDrainTimeout. It is skipped, with the reason logged and counted, when
// telemetry is absent or implausible. Runs under opMu: releasing the lock
// between unload and start would let another load interleave. The request
// context is deliberately not consulted — a caller that disconnected still
// gets a backend started against drained VRAM.
func (m *Manager) waitVRAMDrain(logger logr.Logger, pre GPUInfo, readyEstimateMB, unloadedEstimateMB int64) {
	if m.vramDrainTimeout <= 0 {
		return
	}
	if ok, reason := vramTelemetryPlausible(pre, readyEstimateMB); !ok {
		VRAMTelemetryUnknownTotal.WithLabelValues("drain", reason).Inc()
		logger.Info("Skipping VRAM drain wait: telemetry unknown",
			"reason", reason, "usedMB", pre.VRAMUsedMB, "totalMB", pre.VRAMTotalMB, "readyEstimateMB", readyEstimateMB)
		return
	}

	start := time.Now()
	deadline := start.Add(m.vramDrainTimeout)
	target := pre.VRAMUsedMB - unloadedEstimateMB/2
	prev := int64(-1)
	flat := 0
	outcome := "timeout"
	var last GPUInfo
	for {
		last = m.gpuQuery()
		if last.VRAMTotalMB <= 0 {
			outcome = "telemetry_lost"
			break
		}
		if unloadedEstimateMB > 0 && last.VRAMUsedMB <= target {
			outcome = "drained"
			break
		}
		if prev >= 0 && last.VRAMUsedMB >= prev {
			flat++
		} else {
			flat = 0
		}
		if flat >= 2 {
			outcome = "plateau"
			break
		}
		prev = last.VRAMUsedMB
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		time.Sleep(min(m.vramDrainPoll, remaining))
	}

	elapsed := time.Since(start)
	VRAMDrainSeconds.WithLabelValues(outcome).Observe(elapsed.Seconds())
	if outcome == "timeout" {
		VRAMDrainTimeoutsTotal.Inc()
		logger.Info("WARNING: VRAM drain timed out; starting the next backend anyway",
			"timeout", m.vramDrainTimeout, "preUsedMB", pre.VRAMUsedMB, "usedMB", last.VRAMUsedMB,
			"targetMB", target, "unloadedEstimateMB", unloadedEstimateMB)
		return
	}
	logger.Info("VRAM drain wait finished", "outcome", outcome, "elapsed", elapsed,
		"preUsedMB", pre.VRAMUsedMB, "usedMB", last.VRAMUsedMB, "unloadedEstimateMB", unloadedEstimateMB)
}

// Active returns a representative loaded model, or nil. Prefers a Ready model.
// For the full set (multi-model mode) use Status().ActiveModels.
func (m *Manager) Active() *LoadedModel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.primaryLocked()
}

func summarize(lm *LoadedModel) ModelSummary {
	summary := ModelSummary{
		Name:     lm.Name,
		Backend:  lm.Backend,
		Model:    lm.Model,
		State:    string(lm.State),
		Port:     lm.Port,
		PID:      lm.PID,
		LoadedAt: lm.LoadedAt,
		Error:    lm.Error,

		Generation: lm.Generation,
		SpecHash:   lm.SpecHash,
	}
	substage, message, kvTokens, kvConcurrency := lm.progress.snapshot()
	if lm.State == ModelStateLoading {
		summary.LoadingSubstage, summary.LoadingMessage = substage, message
	}
	summary.KVCacheTokens, summary.KVMaxConcurrency = kvTokens, kvConcurrency
	if lm.ingress != nil {
		count, draining, lastRequest := lm.ingress.snapshot()
		summary.ActiveRequests = count
		summary.Draining = draining
		if !lastRequest.IsZero() {
			summary.LastRequestTime = &lastRequest
		}
	}
	return summary
}

// Model returns a summary of the named loaded model, or ok=false if it is not
// loaded. Used by the per-model health endpoint so each concurrent model is
// addressable by name (and reports its own port) in multi-model mode.
func (m *Manager) Model(name string) (ModelSummary, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if lm, ok := m.models[name]; ok {
		return summarize(lm), true
	}
	return ModelSummary{}, false
}

// Status returns summary information about the runtime. ActiveModel is a
// representative model (back-compat); ActiveModels lists all loaded models.
func (m *Manager) Status() RuntimeStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s := RuntimeStatus{
		GPUVendor:  string(m.gpuVendor),
		GPUArch:    m.gpuArch,
		Mode:       string(m.mode),
		MultiModel: m.multiModel,
	}

	if primary := m.primaryLocked(); primary != nil {
		summary := summarize(primary)
		s.ActiveModel = &summary
	}
	for _, lm := range m.models {
		s.ActiveModels = append(s.ActiveModels, summarize(lm))
	}

	return s
}

// Shutdown gracefully stops all active subprocesses.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.RLock()
	all := make([]*LoadedModel, 0, len(m.models))
	for _, lm := range m.models {
		all = append(all, lm)
	}
	m.mu.RUnlock()
	var firstErr error
	// Stop admission on every ingress before waiting for any one model, so
	// another model cannot accept fresh work during a slow shutdown drain.
	for _, lm := range all {
		if lm.ingress != nil {
			lm.ingress.beginDrain()
		}
	}
	for _, lm := range all {
		if lm.ingress != nil {
			if err := lm.ingress.waitForDrain(ctx); err != nil {
				return fmt.Errorf("waiting for model %q to drain: %w", lm.Name, err)
			}
		}
		if err := m.unloadActive(ctx, lm); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Mode returns the current node operating mode.
func (m *Manager) Mode() NodeMode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mode
}

// SetMode switches the node between inference and gaming mode.
// Gaming mode unloads any active model and starts the gaming backend (Sunshine
// by default; see gamingBackend). Inference mode unloads it and leaves the node
// available for models. It updates NodeModeGauge and, when GamingIdleTimeout > 0,
// starts/stops the opt-in idle guard that auto-reverts an unused gaming session.
//
// Lock ordering: this method does NOT hold m.mu across the Load() call.
// Load() acquires m.mu internally. Holding it here would deadlock.
// Instead we: (1) read/write state under the lock, (2) release,
// (3) call Load() which takes its own lock, (4) re-acquire to
// verify and finalize mode state.
func (m *Manager) SetMode(ctx context.Context, target NodeMode) error {
	logger := log.FromContext(ctx)

	// Phase 1: check current mode and unload under lock.
	m.mu.Lock()
	if m.mode == target {
		logger.Info("Already in target mode", "mode", target)
		m.mu.Unlock()
		return nil
	}
	previousMode := m.mode

	// Unload whatever is currently running (all models).
	all := make([]*LoadedModel, 0, len(m.models))
	for _, lm := range m.models {
		all = append(all, lm)
	}

	m.mode = target
	// A mode switch starts a fresh crash-supervision history.
	m.gamingRestartAttempt = 0
	m.mu.Unlock()

	for _, lm := range all {
		logger.Info("Unloading model for mode switch", "model", lm.Name)
		if err := m.unloadActive(ctx, lm); err != nil {
			m.mu.Lock()
			m.mode = previousMode
			m.mu.Unlock()
			return fmt.Errorf("unloading model %q during mode switch: %w", lm.Name, err)
		}
	}

	// Phase 2: if gaming mode, call Load() without holding the lock.
	// Load() acquires m.mu internally for its own state management.
	if target == ModeGaming {
		err := m.loadGamingBackend(ctx)
		if err != nil {
			// Phase 3: revert mode under lock on failure.
			m.mu.Lock()
			m.mode = ModeInference
			m.mu.Unlock()
			m.setModeGauge(ModeInference)
			return fmt.Errorf("failed to start gaming mode: %w", err)
		}
	}

	// Reflect the new mode and (re)configure the idle guard. Any prior guard is
	// cancelled first so entering inference stops it and re-entering gaming
	// restarts it cleanly.
	m.setModeGauge(target)
	m.stopGamingIdleGuard()
	if target == ModeGaming && m.gamingIdleTimeout > 0 {
		m.startGamingIdleGuard()
	}

	logger.Info("Mode switch complete", "mode", target)
	return nil
}

// setModeGauge sets NodeModeGauge to 1 for the active mode and 0 for the other.
func (m *Manager) setModeGauge(active NodeMode) {
	NodeModeGauge.WithLabelValues(string(ModeInference)).Set(boolToFloat(active == ModeInference))
	NodeModeGauge.WithLabelValues(string(ModeGaming)).Set(boolToFloat(active == ModeGaming))
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// stopGamingIdleGuard cancels a running idle-guard goroutine (no-op if none).
func (m *Manager) stopGamingIdleGuard() {
	m.mu.Lock()
	cancel := m.gamingIdleCancel
	m.gamingIdleCancel = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// startGamingIdleGuard launches the opt-in idle-revert goroutine.
func (m *Manager) startGamingIdleGuard() {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.gamingIdleCancel = cancel
	m.mu.Unlock()
	go m.gamingIdleLoop(ctx)
}

// gamingIdleLoop reverts the node to inference after gamingIdleTimeout with no
// connected Moonlight client. It ticks at a fraction of the timeout and resets
// the idle clock whenever a client is active.
func (m *Manager) gamingIdleLoop(ctx context.Context) {
	logger := log.FromContext(ctx)
	interval := m.gamingIdleTimeout / 4
	if interval < 15*time.Second {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	idleSince := m.nowFn()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.Mode() != ModeGaming {
				return
			}
			active := m.gamingClientActive != nil && m.gamingClientActive()
			revert, newIdleSince := gamingShouldRevert(active, idleSince, m.nowFn(), m.gamingIdleTimeout)
			idleSince = newIdleSince
			if revert {
				logger.Info("Gaming idle timeout reached with no client; reverting to inference",
					"idleTimeout", m.gamingIdleTimeout)
				GamingIdleRevertsTotal.Inc()
				if err := m.SetMode(context.Background(), ModeInference); err != nil {
					logger.Error(err, "Idle auto-revert to inference failed")
				}
				return
			}
		}
	}
}

// gamingShouldRevert is the pure idle-guard decision. A live client resets the
// idle clock (never revert); with the guard disabled (timeout <= 0) it never
// reverts; otherwise it reverts once the no-client span reaches the timeout.
func gamingShouldRevert(clientActive bool, idleSince, now time.Time, timeout time.Duration) (revert bool, newIdleSince time.Time) {
	if clientActive {
		return false, now
	}
	if timeout <= 0 {
		return false, idleSince
	}
	return now.Sub(idleSince) >= timeout, idleSince
}

// sunshineClientActive reports whether a Moonlight client currently holds a
// connection to Sunshine, by scanning /proc/net/tcp{,6} for ESTABLISHED sockets
// on the Moonlight control (47989) or RTSP (48010) ports. Best-effort: if no
// /proc file can be read it returns true (assume active) so a broken probe never
// auto-reverts a live gaming session.
func (m *Manager) sunshineClientActive() bool {
	const stateEstablished = "01" // TCP_ESTABLISHED in /proc/net/tcp
	watched := map[int64]bool{
		int64(backend.PortSunshine): true, // 47989 Moonlight HTTP control
		48010:                       true, // RTSP
	}
	readAny := false
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		readAny = true
		for i, line := range strings.Split(string(data), "\n") {
			if i == 0 { // header row
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 4 || fields[3] != stateEstablished {
				continue
			}
			_, portHex, ok := strings.Cut(fields[1], ":") // local_address = HEXIP:HEXPORT
			if !ok {
				continue
			}
			if p, err := strconv.ParseInt(portHex, 16, 32); err == nil && watched[p] {
				return true
			}
		}
	}
	if !readAny {
		return true // could not probe; do not auto-revert on probe failure
	}
	return false
}

// RuntimeStatus is the serializable status of the runtime manager.
type RuntimeStatus struct {
	GPUVendor  string `json:"gpuVendor"`
	GPUArch    string `json:"gpuArch"`
	Mode       string `json:"mode"`
	MultiModel bool   `json:"multiModel"`
	// ActiveModel is a representative loaded model (back-compat with the
	// single-slot API). Prefer ActiveModels for the full set.
	ActiveModel *ModelSummary `json:"activeModel,omitempty"`
	// ActiveModels lists every loaded model (one entry in single-slot mode).
	ActiveModels []ModelSummary `json:"activeModels,omitempty"`
}

// ModelSummary is a serializable view of a loaded model.
type ModelSummary struct {
	Name            string     `json:"name"`
	Backend         string     `json:"backend"`
	Model           string     `json:"model"`
	State           string     `json:"state"`
	Port            int32      `json:"port"`
	PID             int        `json:"pid,omitempty"`
	LoadedAt        time.Time  `json:"loadedAt,omitempty"`
	Error           string     `json:"error,omitempty"`
	ActiveRequests  int64      `json:"activeRequests,omitempty"`
	Draining        bool       `json:"draining,omitempty"`
	LastRequestTime *time.Time `json:"lastRequestTime,omitempty"`
	// Generation and SpecHash echo the load request that started this
	// instance (zero/empty for requests from controllers that predate them).
	Generation int64  `json:"generation,omitempty"`
	SpecHash   string `json:"specHash,omitempty"`
	// LoadingSubstage and LoadingMessage report load progress read from the
	// backend's output (LoadSubstage* values); set only while Loading.
	LoadingSubstage string `json:"loadingSubstage,omitempty"`
	LoadingMessage  string `json:"loadingMessage,omitempty"`
	// KVCacheTokens and KVMaxConcurrency are the vLLM boot profile (0/empty
	// until both lines were printed).
	KVCacheTokens    int64  `json:"kvCacheTokens,omitempty"`
	KVMaxConcurrency string `json:"kvMaxConcurrency,omitempty"`
}

// monitorProcess waits for the subprocess to exit and updates state. When the
// gaming backend exits while the node is still in gaming mode, it also kicks
// off supervised restarts — otherwise the runtime keeps reporting mode=gaming
// with no Sunshine process, the GamingSession reconciler's idempotent
// SetMode(gaming) no-ops, and the session stays dead until a pod restart.
func (m *Manager) monitorProcess(ctx context.Context, loaded *LoadedModel) {
	logger := log.FromContext(ctx).WithValues("model", loaded.Name)

	err := loaded.cmd.Wait()
	loaded.ingress.close()
	loaded.done <- err
	close(loaded.done)

	m.mu.Lock()
	// Only update if this exact instance is still loaded (survives reload races).
	cur, ok := m.models[loaded.Name]
	unexpected := ok && cur == loaded && loaded.State != ModelStateStopping
	// The gaming backend has no work-complete exit: while the node is in gaming
	// mode ANY exit — even status 0 — is a crash that must be supervised.
	gamingCrash := unexpected && loaded.Name == gamingModelName && m.mode == ModeGaming
	if unexpected && (err != nil || gamingCrash) {
		logger.Error(err, "Backend subprocess crashed")
		loaded.observeSwapLocked("failed") // no-op if it already reached Ready
		loaded.State = ModelStateFailed
		if err != nil {
			loaded.Error = err.Error()
		} else {
			loaded.Error = "backend exited unexpectedly"
		}
		BackendSubprocessCrashesTotal.WithLabelValues(loaded.Name, loaded.Backend).Inc()
		setModelStateMetric(loaded.Name, loaded.Backend, "Failed")
	}
	var delay time.Duration
	var attempt int
	if gamingCrash {
		delay = m.nextGamingRestartDelayLocked(loaded.LoadedAt)
		attempt = m.gamingRestartAttempt
	}
	m.mu.Unlock()

	if gamingCrash {
		logger.Info("Gaming backend crashed while node is in gaming mode; scheduling supervised restart",
			"attempt", attempt, "delay", delay)
		go m.superviseGamingRestart(ctx, delay)
	}
}

// loadGamingBackend (re)loads the configured gaming backend subprocess. It is
// the default gamingRestart hook; tests stub gamingRestart to observe
// supervised restarts without a real Sunshine binary.
func (m *Manager) loadGamingBackend(ctx context.Context) error {
	return m.Load(ctx, gamingModelName, LoadRequest{Backend: m.gamingBackend})
}

// nextGamingRestartDelayLocked advances the crash counter and returns the
// backoff delay before the next restart attempt. A crash after
// gamingStableUptime of runtime resets the counter, so an occasional crash
// restarts fast while a tight crash loop backs off toward gamingRestartMax.
// Caller must hold m.mu.
func (m *Manager) nextGamingRestartDelayLocked(loadedAt time.Time) time.Duration {
	if !loadedAt.IsZero() && m.nowFn().Sub(loadedAt) >= gamingStableUptime {
		m.gamingRestartAttempt = 0
	}
	m.gamingRestartAttempt++
	delay := m.gamingRestartMax
	if n := m.gamingRestartAttempt - 1; n < 16 {
		if d := m.gamingRestartBase << n; d < delay {
			delay = d
		}
	}
	return delay
}

// superviseGamingRestart restarts the crashed gaming backend after delay and
// keeps retrying with growing backoff while the node stays in gaming mode. It
// exits once a restart succeeds (a later crash schedules a fresh supervisor
// via monitorProcess) or the node leaves gaming mode.
func (m *Manager) superviseGamingRestart(ctx context.Context, delay time.Duration) {
	logger := log.FromContext(ctx).WithValues("model", gamingModelName, "backend", m.gamingBackend)
	for {
		time.Sleep(delay)
		if m.Mode() != ModeGaming {
			logger.Info("Node left gaming mode; stopping gaming backend restart supervision")
			return
		}
		m.mu.RLock()
		attempt := m.gamingRestartAttempt
		m.mu.RUnlock()
		logger.Info("Restarting crashed gaming backend", "attempt", attempt)
		if err := m.gamingRestart(ctx); err != nil {
			GamingBackendRestartsTotal.WithLabelValues("error").Inc()
			logger.Error(err, "Gaming backend restart failed; backing off", "attempt", attempt)
			m.mu.Lock()
			delay = m.nextGamingRestartDelayLocked(time.Time{})
			m.mu.Unlock()
			continue
		}
		GamingBackendRestartsTotal.WithLabelValues("ok").Inc()
		// The mode may have flipped while the restart was in flight; don't leave
		// an orphaned gaming subprocess on an inference node.
		if m.Mode() != ModeGaming {
			logger.Info("Mode changed during gaming backend restart; unloading")
			_ = m.Unload(context.Background(), gamingModelName)
		}
		return
	}
}

// GamingClientsActive reports whether a Moonlight client currently holds an
// established connection to the gaming backend. ok is false outside gaming
// mode (the mode API then omits the field, so the controller reports
// StreamActive=Unknown rather than a guess). Best-effort like the idle guard:
// a broken probe reads as "active", which also keeps controller-side idle
// expiry from killing a live session on probe failure.
func (m *Manager) GamingClientsActive() (active bool, ok bool) {
	if m.Mode() != ModeGaming {
		return false, false
	}
	m.mu.RLock()
	probe := m.gamingClientActive
	m.mu.RUnlock()
	if probe == nil {
		return false, false
	}
	return probe(), true
}

// GamingDegraded reports whether the node is in gaming mode but its backend
// subprocess is not running (crashed and awaiting a supervised restart). The
// mode API surfaces this so the GamingSession controller can reflect a broken
// session (phase Degraded) instead of Active.
func (m *Manager) GamingDegraded() (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.mode != ModeGaming {
		return false, ""
	}
	lm, ok := m.models[gamingModelName]
	if !ok {
		return true, "gaming backend not loaded"
	}
	if lm.State == ModelStateFailed {
		detail := fmt.Sprintf("gaming backend crashed; supervised restart attempt %d pending", m.gamingRestartAttempt)
		if lm.Error != "" {
			detail += ": " + lm.Error
		}
		return true, detail
	}
	return false, ""
}

// stillLoadingLocked reports whether loaded is still the registered instance for
// its name and is in the Loading state. Caller must hold m.mu.
func (m *Manager) stillLoadingLocked(loaded *LoadedModel) bool {
	cur, ok := m.models[loaded.Name]
	return ok && cur == loaded && loaded.State == ModelStateLoading
}

// healthCheckLoop polls the backend's health endpoint until ready or cancelled.
func (m *Manager) healthCheckLoop(ctx context.Context, loaded *LoadedModel, b backend.Backend, port int32, startupTimeout time.Duration) {
	name := loaded.Name
	logger := log.FromContext(ctx).WithValues("model", name, "startupTimeout", startupTimeout)

	probe := b.ReadinessProbe()
	checkHealth, probeDesc := healthProbeFor(probe, port)
	if checkHealth == nil {
		// No usable probe (neither HTTP GET nor TCP socket) — mark ready after
		// startup timeout. This is the only path that reports Ready without
		// verifying the backend actually serves, so it is reserved for backends
		// that genuinely expose no probe.
		select {
		case <-time.After(startupTimeout):
		case <-ctx.Done():
			return
		}
		m.mu.Lock()
		if m.stillLoadingLocked(loaded) {
			loaded.State = ModelStateReady
			loaded.observeSwapLocked("ready")
			setModelStateMetric(name, loaded.Backend, "Ready")
			logger.Info("Model marked ready (no probe defined, startup timeout elapsed)")
		}
		m.mu.Unlock()
		loaded.progress.stop()
		return
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	startupDeadline := time.After(startupTimeout)

	for {
		select {
		case <-ctx.Done():
			return
		case <-startupDeadline:
			m.mu.Lock()
			if m.stillLoadingLocked(loaded) {
				loaded.State = ModelStateFailed
				loaded.observeSwapLocked("failed")
				loaded.Error = "startup timeout exceeded"
				logger.Error(nil, "Backend startup timeout exceeded")
				setModelStateMetric(name, loaded.Backend, "Failed")
			}
			m.mu.Unlock()
			loaded.progress.stop()
			return
		case <-ticker.C:
			if checkHealth() {
				m.mu.Lock()
				if m.stillLoadingLocked(loaded) {
					loaded.State = ModelStateReady
					logger.Info("Model is ready", "probe", probeDesc)
					setModelStateMetric(name, loaded.Backend, "Ready")
					ModelLoadDurationSeconds.WithLabelValues(name, loaded.Backend).Observe(time.Since(loaded.LoadedAt).Seconds())
					loaded.observeSwapLocked("ready")
				}
				m.mu.Unlock()
				loaded.progress.stop()

				// Continue monitoring for ongoing health.
				m.continuousHealthCheck(ctx, loaded, checkHealth)
				return
			}
		}
	}
}

// healthProbeFor builds a health-check closure for the backend's readiness
// probe. It dials the runtime-managed port (the port the subprocess was
// actually launched on) rather than the probe's declared port, which reflects
// the default Deployment port and not the runtime subprocess port — mirroring
// the HTTP path's long-standing behaviour. It returns nil when the probe
// defines neither an HTTP GET nor a TCP socket check, signalling the caller to
// fall back to a startup-timer that marks the model ready unconditionally.
func healthProbeFor(probe *corev1.Probe, port int32) (func() bool, string) {
	if probe == nil {
		return nil, ""
	}
	if probe.HTTPGet != nil {
		url := fmt.Sprintf("http://127.0.0.1:%d%s", port, probe.HTTPGet.Path)
		return func() bool { return checkHTTPHealth(url) }, "http " + url
	}
	if probe.TCPSocket != nil {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		return func() bool { return checkTCPHealth(addr) }, "tcp " + addr
	}
	return nil, ""
}

func runtimeBackendPort(b backend.Backend, spec *backend.ModelSpec) int32 {
	port := pkgrt.RuntimePortForBackend(b)
	if spec != nil {
		if spec.Config == nil {
			spec.Config = make(map[string]any, 1)
		}
		if configured := spec.ConfigInt("port", 0); configured > 0 {
			port = int32(configured)
		} else if b != nil && b.Port() == pkgrt.RuntimeAPIPort {
			spec.Config["port"] = float64(port)
		}
	}
	return port
}

// continuousHealthCheck monitors a ready model and marks it failed if unhealthy.
// checkHealth is the same probe closure used during startup (HTTP or TCP).
func (m *Manager) continuousHealthCheck(ctx context.Context, loaded *LoadedModel, checkHealth func() bool) {
	name := loaded.Name
	logger := log.FromContext(ctx).WithValues("model", name)
	ticker := time.NewTicker(m.healthCheckInterval)
	defer ticker.Stop()

	failures := 0
	const maxFailures = 3
	detector := backend.StallDetector{Window: loaded.stallWindow}
	var lastScrape time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if loaded.Backend == backend.NameVLLM && time.Since(lastScrape) >= 30*time.Second {
				if !lastScrape.IsZero() && time.Since(lastScrape) > 90*time.Second {
					detector.Reset()
				}
				lastScrape = time.Now()
				if m.checkEngineStall(ctx, loaded, &detector, lastScrape) {
					return
				}
			}
			if checkHealth() {
				failures = 0
				continue
			}

			failures++
			if failures >= maxFailures {
				m.mu.Lock()
				if cur, ok := m.models[name]; ok && cur == loaded && loaded.State == ModelStateReady {
					loaded.State = ModelStateFailed
					loaded.Error = "health check failed"
					logger.Error(nil, "Backend health check failed, marking model as failed")
					HealthCheckFailuresTotal.WithLabelValues(name, loaded.Backend).Inc()
					setModelStateMetric(name, loaded.Backend, "Failed")
				}
				m.mu.Unlock()
				return
			}
		}
	}
}

// checkEngineStall applies an observation only to the still-current model.
func (m *Manager) checkEngineStall(ctx context.Context, loaded *LoadedModel, detector *backend.StallDetector, now time.Time) bool {
	sample, err := backend.ReadVLLMStallSample(ctx, fmt.Sprintf("http://127.0.0.1:%d/metrics", modelBackendPort(loaded)))
	if err != nil {
		detector.Reset()
		return false
	}
	if !detector.Observe(sample, now) {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur := m.models[loaded.Name]; cur == loaded && loaded.State == ModelStateReady {
		loaded.State = ModelStateFailed
		loaded.Error = "engine stalled: " + sample.String()
		ModelEngineStalled.WithLabelValues(loaded.Name, runtimeMetricsNamespace()).Set(1)
		setModelStateMetric(loaded.Name, loaded.Backend, "Failed")
	}
	return true
}

func runtimeMetricsNamespace() string {
	if namespace := os.Getenv("POD_NAMESPACE"); namespace != "" {
		return namespace
	}
	return "flexinfer-system"
}

// resolveExecutable preserves explicit command paths when they exist, but
// lets runtime images move backend binaries to PATH-compatible locations.
func resolveExecutable(executable string) (string, bool) {
	if executable == "" {
		return executable, false
	}
	if !strings.ContainsRune(executable, os.PathSeparator) {
		return executable, false
	}
	if _, err := os.Stat(executable); err == nil {
		return executable, true
	}
	base := filepath.Base(executable)
	if base == "" || base == "." || base == string(os.PathSeparator) {
		return executable, false
	}
	resolved, err := exec.LookPath(base)
	if err != nil {
		return executable, false
	}
	return resolved, true
}

// inferCommand maps a backend name to its executable and any required
// default arguments (e.g. Python module or script path). These are only
// used when the backend's Command() returns nil, meaning the standalone
// Dockerfile handles invocation via ENTRYPOINT/CMD.
func inferCommand(backendName string) (string, []string) {
	switch backendName {
	case backend.NameVLLM, backend.NameVLLMOmni:
		return "python", []string{"-m", "vllm.entrypoints.openai.api_server"}
	case backend.NameDiffusers:
		return "python", []string{"/opt/flexinfer/server-diffusers.py"}
	case backend.NameSNAC:
		return "python", []string{"/opt/flexinfer/server-snac.py"}
	case backend.NameLlamaCpp:
		return "llama-server", nil
	case backend.NameOllama:
		return "ollama", nil
	case backend.NameSteam:
		return "steam", nil
	default:
		return backendName, nil
	}
}

func overlayEnvVars(base []corev1.EnvVar, overlay []corev1.EnvVar) []corev1.EnvVar {
	if len(overlay) == 0 {
		return base
	}

	merged := make([]corev1.EnvVar, 0, len(base)+len(overlay))
	indexByName := make(map[string]int, len(base)+len(overlay))

	for _, env := range base {
		indexByName[env.Name] = len(merged)
		merged = append(merged, env)
	}

	for _, env := range overlay {
		if idx, ok := indexByName[env.Name]; ok {
			merged[idx] = env
			continue
		}
		indexByName[env.Name] = len(merged)
		merged = append(merged, env)
	}

	return merged
}

// maxBackendLogLine caps a single streamed subprocess line. Loki's default
// max_line_size is 256KB and rejects longer lines (reason=line_too_long); keep
// headroom for the structured wrapper fields.
const maxBackendLogLine = 200 * 1024

// errBackendReported is the sentinel error attached when a backend log line is
// classified as an error, so it renders at error level (queryable by level and
// picked up by the log alert rules) without inventing a fake Go error value.
var errBackendReported = errors.New("backend reported an error")

// backendLogNoise are high-frequency, low-signal lines emitted by streamed
// backends — chiefly the headless Sunshine gaming host. Under that volume the
// log pipeline falls behind and Loki drops the late (real) events as
// too_far_behind, which is exactly how the gaming crash lines were lost. Matched
// lines are demoted to V(1)/debug: suppressed at the default info level so they
// don't drown the signal, still recoverable with -zap-log-level debug.
var backendLogNoise = []string{
	"exceeds DATA_SHARDS_MAX",           // Sunshine FEC-overflow flood
	"Could not resolve keysym",          // Xwayland keymap warnings at session start
	"Errors from xkbcomp are not fatal", // Xwayland keymap compile noise
	"failed to start server on 'unix",   // pipewire/pulse re-init on supervised restart
	"pending linkable(s) not activated", // wireplumber startup churn
}

// backendLineSeverity classifies a raw backend log line by the severity markers
// common to the backends we run (Sunshine "Error:"/"Warning:", wlroots/pipewire
// "[ERROR]"/"[E][", plus generic crash markers) so real failures leave the flat
// info stream — filterable by level and able to trip the Loki error/segfault
// alert rules.
func backendLineSeverity(line string) string {
	switch {
	case strings.Contains(line, "Error:"),
		strings.Contains(line, "[ERROR]"),
		strings.Contains(line, "[E]["),
		strings.Contains(line, "Frame capture failed"),
		strings.Contains(line, "panic:"),
		strings.Contains(line, "segfault"),
		strings.Contains(line, "Segmentation fault"):
		return "error"
	case strings.Contains(line, "Warning:"),
		strings.Contains(line, "[WARN"),
		strings.Contains(line, "[W]["):
		return "warn"
	default:
		return "info"
	}
}

func backendLineIsNoise(line string) bool {
	for _, n := range backendLogNoise {
		if strings.Contains(line, n) {
			return true
		}
	}
	return false
}

// streamLogs reads a backend's stdout/stderr line-by-line and re-emits each line
// through the structured logger, classifying severity, demoting known flood
// noise to debug, and truncating over-long lines so downstream ingestion (Loki)
// preserves the real events instead of dropping them under load. When observe
// is set it also receives every output segment as it arrives, including each
// '\r' redraw of a progress bar, so the load tracker sees weight-loading
// progress live; logging still happens per '\n'-terminated line.
func streamLogs(r io.ReadCloser, logger logr.Logger, stream string, observe func(string)) {
	scanner := bufio.NewScanner(r)
	// Allow reading up to 1MB lines (some backends produce verbose JSON output),
	// but truncate before logging so we stay under Loki's max_line_size.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	scanner.Split(scanLinesOrCR)
	var pending strings.Builder
	for scanner.Scan() {
		segment := scanner.Text()
		redraw := strings.HasSuffix(segment, "\r")
		segment = strings.TrimRight(segment, "\r\n")
		if observe != nil {
			if trimmed := strings.TrimSpace(segment); trimmed != "" {
				observe(trimmed)
			}
		}
		if redraw {
			// Keep the redraws for the line's log entry, bounded by what
			// logging would keep anyway.
			if pending.Len() <= maxBackendLogLine {
				pending.WriteString(segment)
				pending.WriteByte('\r')
			}
			continue
		}
		raw := segment
		if pending.Len() > 0 {
			pending.WriteString(segment)
			raw = pending.String()
			pending.Reset()
		}
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if len(line) > maxBackendLogLine {
			line = line[:maxBackendLogLine] + "…[truncated]"
		}
		if backendLineIsNoise(line) {
			logger.V(1).Info(line, "stream", stream)
			continue
		}
		switch backendLineSeverity(line) {
		case "error":
			logger.Error(errBackendReported, line, "stream", stream)
		case "warn":
			logger.Info(line, "stream", stream, "backend_severity", "warn")
		default:
			logger.Info(line, "stream", stream)
		}
	}
}
