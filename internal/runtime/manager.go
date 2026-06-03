// Package runtime implements the flexinfer-runtime process manager.
// It manages backend subprocesses (vLLM, llamacpp, ollama, diffusers)
// on a single GPU node, enabling near-instant model swapping without
// pod scheduling overhead.
package runtime

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
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
}

// LoadedModel tracks a running backend subprocess and its model.
type LoadedModel struct {
	Name      string             `json:"name"`
	Backend   string             `json:"backend"`
	Model     string             `json:"model"`
	State     ModelState         `json:"state"`
	Port      int32              `json:"port"`
	PID       int                `json:"pid,omitempty"`
	LoadedAt  time.Time          `json:"loadedAt,omitempty"`
	Error     string             `json:"error,omitempty"`
	HealthURL string             `json:"-"`
	cmd       *exec.Cmd          `json:"-"`
	cancel    context.CancelFunc `json:"-"`
	done      chan error         `json:"-"`
}

// NodeMode represents the operating mode of a GPU node.
type NodeMode string

const (
	ModeInference NodeMode = "inference"
	ModeGaming    NodeMode = "gaming"
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

	// gpuVendor and gpuArch describe the GPU on this node.
	gpuVendor backend.GPUVendor
	gpuArch   string

	// shutdownTimeout is how long to wait for graceful subprocess exit.
	shutdownTimeout time.Duration

	// healthCheckInterval controls how often the subprocess is probed.
	healthCheckInterval time.Duration

	// modelBasePath is the root path where models are mounted.
	modelBasePath string
}

// defaultVRAMHeadroomMB is the free-VRAM margin kept before admitting a new
// concurrent model in multi-model mode.
const defaultVRAMHeadroomMB int64 = 1024

// ManagerConfig holds configuration for creating a Manager.
type ManagerConfig struct {
	GPUVendor           backend.GPUVendor
	GPUArch             string
	ShutdownTimeout     time.Duration
	HealthCheckInterval time.Duration
	ModelBasePath       string

	// MultiModel enables concurrent VRAM-bounded subprocesses (default false =
	// single-slot, behavior identical to the pre-multi-model runtime).
	MultiModel bool
	// VRAMHeadroomMB overrides the default free-VRAM admission margin.
	VRAMHeadroomMB int64
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
	return &Manager{
		models:              make(map[string]*LoadedModel),
		multiModel:          cfg.MultiModel,
		vramHeadroomMB:      cfg.VRAMHeadroomMB,
		mode:                ModeInference,
		gpuVendor:           cfg.GPUVendor,
		gpuArch:             cfg.GPUArch,
		shutdownTimeout:     cfg.ShutdownTimeout,
		healthCheckInterval: cfg.HealthCheckInterval,
		modelBasePath:       cfg.ModelBasePath,
	}
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

// canAdmitLocked reports whether a new model with the given VRAM estimate fits
// alongside the currently loaded models. It is fail-open: when GPU telemetry is
// unavailable (free==0) it admits and lets the backend's own OOM handling apply.
// Caller need not hold m.mu (QueryGPU shells out); callers pass estimateMB=0 to
// mean "unknown estimate" (admission then only enforces the headroom margin).
func (m *Manager) canAdmit(estimateMB int64) (bool, string) {
	info := QueryGPU(string(m.gpuVendor), m.gpuArch)
	if info.VRAMFreeMB <= 0 {
		// No telemetry — fail open.
		return true, "vram telemetry unavailable; admitting (fail-open)"
	}
	if info.VRAMFreeMB-estimateMB < m.vramHeadroomMB {
		return false, fmt.Sprintf("insufficient VRAM: free=%dMB estimate=%dMB headroom=%dMB",
			info.VRAMFreeMB, estimateMB, m.vramHeadroomMB)
	}
	return true, fmt.Sprintf("admit: free=%dMB estimate=%dMB headroom=%dMB",
		info.VRAMFreeMB, estimateMB, m.vramHeadroomMB)
}

// Load starts a backend subprocess for the named model. If another model
// is already active, it is unloaded first (serialize load/unload).
func (m *Manager) Load(ctx context.Context, name string, req LoadRequest) error {
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
	// from fighting over the same model.
	m.mu.Lock()
	if existing, ok := m.models[name]; ok && existing.Backend == req.Backend &&
		(existing.State == ModelStateReady || existing.State == ModelStateLoading) {
		state := string(existing.State)
		m.mu.Unlock()
		logger.Info("Model already loaded, skipping reload",
			"state", state)
		return nil
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
	m.mu.Unlock()
	for _, lm := range toUnload {
		logger.Info("Unloading model before loading new one", "unloading", lm.Name, "loading", name)
		if err := m.unloadActive(ctx, lm); err != nil {
			logger.Error(err, "Failed to unload model, forcing", "model", lm.Name)
		}
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
		var estimateMB int64
		if v := spec.ConfigInt("vramEstimateMB", 0); v > 0 {
			estimateMB = int64(v)
		}
		if ok, reason := m.canAdmit(estimateMB); !ok {
			return fmt.Errorf("cannot load %q: %s", name, reason)
		} else {
			logger.Info("VRAM admission passed", "model", name, "reason", reason, "port", backendPort)
		}
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

	loaded := &LoadedModel{
		Name:    name,
		Backend: req.Backend,
		Model:   req.Model,
		State:   ModelStateLoading,
		Port:    backendPort,
		cmd:     cmd,
		cancel:  cancel,
		done:    make(chan error, 1),
	}

	m.mu.Lock()
	m.models[name] = loaded
	m.mu.Unlock()

	setModelStateMetric(name, req.Backend, "Loading")

	logger.Info("Starting backend subprocess",
		"executable", executable,
		"args", execArgs,
		"port", backendPort,
	)

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		loaded.State = ModelStateFailed
		loaded.Error = err.Error()
		if cur, ok := m.models[name]; ok && cur == loaded {
			delete(m.models, name)
		}
		m.mu.Unlock()
		cancel()
		close(loaded.done)
		ModelLoadsTotal.WithLabelValues(req.Backend, "error").Inc()
		setModelStateMetric(name, req.Backend, "Failed")
		return fmt.Errorf("failed to start backend: %w", err)
	}

	m.mu.Lock()
	loaded.PID = cmd.Process.Pid
	loaded.LoadedAt = time.Now()
	m.mu.Unlock()

	ModelLoadsTotal.WithLabelValues(req.Backend, "ok").Inc()

	// Stream subprocess output to logger. Only start goroutines if pipes
	// were created successfully — a nil reader would panic in bufio.NewScanner.
	if stdout != nil {
		go streamLogs(stdout, logger, "stdout")
	}
	if stderr != nil {
		go streamLogs(stderr, logger, "stderr")
	}

	// Monitor subprocess exit.
	go m.monitorProcess(subCtx, loaded)

	// Start health checking in background.
	// Allow the load request config to override the backend's default startup timeout
	// so the proxy/controller can pass the model's coldStartTimeout.
	startupTimeout := b.StartupTimeout()
	if v, ok := req.Config["startupTimeoutSeconds"]; ok {
		switch t := v.(type) {
		case float64:
			startupTimeout = time.Duration(t) * time.Second
		case string:
			if d, err := time.ParseDuration(t); err == nil {
				startupTimeout = d
			}
		}
	}
	go m.healthCheckLoop(subCtx, loaded, b, backendPort, startupTimeout)

	return nil
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
	active.State = ModelStateStopping
	m.mu.Unlock()

	// Send SIGTERM for graceful shutdown.
	if active.cmd != nil && active.cmd.Process != nil {
		logger.Info("Sending SIGTERM to backend", "pid", active.PID)
		_ = active.cmd.Process.Signal(syscall.SIGTERM)

		select {
		case <-time.After(m.shutdownTimeout):
			// Only kill if the process has not yet exited. We use a
			// non-blocking check on done to avoid a race with PID reuse:
			// if Wait() already returned, the PID is reaped and could be
			// reassigned to an unrelated process.
			select {
			case err := <-active.done:
				// Process already exited before we could kill it.
				if err != nil {
					logger.Info("Backend exited during shutdown timeout", "error", err)
				}
			default:
				logger.Info("Shutdown timeout exceeded, sending SIGKILL", "pid", active.PID)
				if err := active.cmd.Process.Kill(); err != nil {
					logger.Info("Kill returned error (process may have already exited)", "error", err)
				}
				<-active.done // Wait for the process to be reaped after kill.
			}
		case err := <-active.done:
			if err != nil {
				logger.Info("Backend exited", "error", err)
			}
		}
	}

	if active.cancel != nil {
		active.cancel()
	}

	ModelUnloadsTotal.WithLabelValues(active.Backend, "requested").Inc()
	clearModelStateMetric(active.Name, active.Backend)
	m.mu.Lock()
	if cur, ok := m.models[active.Name]; ok && cur == active {
		delete(m.models, active.Name)
	}
	m.mu.Unlock()
	return nil
}

// Active returns a representative loaded model, or nil. Prefers a Ready model.
// For the full set (multi-model mode) use Status().ActiveModels.
func (m *Manager) Active() *LoadedModel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.primaryLocked()
}

func summarize(lm *LoadedModel) ModelSummary {
	return ModelSummary{
		Name:     lm.Name,
		Backend:  lm.Backend,
		Model:    lm.Model,
		State:    string(lm.State),
		Port:     lm.Port,
		PID:      lm.PID,
		LoadedAt: lm.LoadedAt,
		Error:    lm.Error,
	}
}

// Status returns summary information about the runtime. ActiveModel is a
// representative model (back-compat); ActiveModels lists all loaded models.
func (m *Manager) Status() RuntimeStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s := RuntimeStatus{
		GPUVendor: string(m.gpuVendor),
		GPUArch:   m.gpuArch,
		Mode:      string(m.mode),
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
	for _, lm := range all {
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
// Gaming mode unloads any active model and starts the steam backend.
// Inference mode unloads steam and leaves the node available for models.
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

	// Unload whatever is currently running (all models).
	all := make([]*LoadedModel, 0, len(m.models))
	for _, lm := range m.models {
		all = append(all, lm)
	}

	m.mode = target
	m.mu.Unlock()

	for _, lm := range all {
		logger.Info("Unloading model for mode switch", "model", lm.Name)
		if err := m.unloadActive(ctx, lm); err != nil {
			logger.Error(err, "Failed to unload during mode switch", "model", lm.Name)
		}
	}

	// Phase 2: if gaming mode, call Load() without holding the lock.
	// Load() acquires m.mu internally for its own state management.
	if target == ModeGaming {
		err := m.Load(ctx, "__gaming__", LoadRequest{
			Backend: "steam",
		})
		if err != nil {
			// Phase 3: revert mode under lock on failure.
			m.mu.Lock()
			m.mode = ModeInference
			m.mu.Unlock()
			return fmt.Errorf("failed to start gaming mode: %w", err)
		}
	}

	logger.Info("Mode switch complete", "mode", target)
	return nil
}

// RuntimeStatus is the serializable status of the runtime manager.
type RuntimeStatus struct {
	GPUVendor string `json:"gpuVendor"`
	GPUArch   string `json:"gpuArch"`
	Mode      string `json:"mode"`
	// ActiveModel is a representative loaded model (back-compat with the
	// single-slot API). Prefer ActiveModels for the full set.
	ActiveModel *ModelSummary `json:"activeModel,omitempty"`
	// ActiveModels lists every loaded model (one entry in single-slot mode).
	ActiveModels []ModelSummary `json:"activeModels,omitempty"`
}

// ModelSummary is a serializable view of a loaded model.
type ModelSummary struct {
	Name     string    `json:"name"`
	Backend  string    `json:"backend"`
	Model    string    `json:"model"`
	State    string    `json:"state"`
	Port     int32     `json:"port"`
	PID      int       `json:"pid,omitempty"`
	LoadedAt time.Time `json:"loadedAt,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// monitorProcess waits for the subprocess to exit and updates state.
func (m *Manager) monitorProcess(ctx context.Context, loaded *LoadedModel) {
	logger := log.FromContext(ctx).WithValues("model", loaded.Name)

	err := loaded.cmd.Wait()
	loaded.done <- err
	close(loaded.done)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Only update if this exact instance is still loaded (survives reload races).
	if cur, ok := m.models[loaded.Name]; ok && cur == loaded {
		if err != nil && loaded.State != ModelStateStopping {
			logger.Error(err, "Backend subprocess crashed")
			loaded.State = ModelStateFailed
			loaded.Error = err.Error()
			BackendSubprocessCrashesTotal.WithLabelValues(loaded.Name, loaded.Backend).Inc()
			setModelStateMetric(loaded.Name, loaded.Backend, "Failed")
		}
	}
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
	if probe == nil || probe.HTTPGet == nil {
		// No HTTP probe defined — mark ready after startup timeout.
		select {
		case <-time.After(startupTimeout):
		case <-ctx.Done():
			return
		}
		m.mu.Lock()
		if m.stillLoadingLocked(loaded) {
			loaded.State = ModelStateReady
			setModelStateMetric(name, loaded.Backend, "Ready")
			logger.Info("Model marked ready (no probe defined, startup timeout elapsed)")
		}
		m.mu.Unlock()
		return
	}

	healthPath := probe.HTTPGet.Path
	healthURL := fmt.Sprintf("http://127.0.0.1:%d%s", port, healthPath)

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
				loaded.Error = "startup timeout exceeded"
				logger.Error(nil, "Backend startup timeout exceeded")
				setModelStateMetric(name, loaded.Backend, "Failed")
			}
			m.mu.Unlock()
			return
		case <-ticker.C:
			if checkHTTPHealth(healthURL) {
				m.mu.Lock()
				if m.stillLoadingLocked(loaded) {
					loaded.State = ModelStateReady
					logger.Info("Model is ready", "healthURL", healthURL)
					setModelStateMetric(name, loaded.Backend, "Ready")
					ModelLoadDurationSeconds.WithLabelValues(name, loaded.Backend).Observe(time.Since(loaded.LoadedAt).Seconds())
				}
				m.mu.Unlock()

				// Continue monitoring for ongoing health.
				m.continuousHealthCheck(ctx, loaded, healthURL)
				return
			}
		}
	}
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
func (m *Manager) continuousHealthCheck(ctx context.Context, loaded *LoadedModel, healthURL string) {
	name := loaded.Name
	logger := log.FromContext(ctx).WithValues("model", name)
	ticker := time.NewTicker(m.healthCheckInterval)
	defer ticker.Stop()

	failures := 0
	const maxFailures = 3

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if checkHTTPHealth(healthURL) {
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

// streamLogs reads from r line-by-line and logs each line.
func streamLogs(r io.ReadCloser, logger interface {
	Info(msg string, keysAndValues ...any)
}, stream string) {
	scanner := bufio.NewScanner(r)
	// Allow up to 1MB lines (some backends produce verbose JSON output).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			logger.Info(line, "stream", stream)
		}
	}
}
