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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/flexinfer/flexinfer/backend"
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
}

// NodeMode represents the operating mode of a GPU node.
type NodeMode string

const (
	ModeInference NodeMode = "inference"
	ModeGaming    NodeMode = "gaming"
)

// Manager controls backend subprocess lifecycle on a GPU node.
// Only one backend subprocess runs at a time (single-GPU constraint).
type Manager struct {
	mu sync.RWMutex

	// active is the currently loaded model (nil if none).
	active *LoadedModel

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

// ManagerConfig holds configuration for creating a Manager.
type ManagerConfig struct {
	GPUVendor           backend.GPUVendor
	GPUArch             string
	ShutdownTimeout     time.Duration
	HealthCheckInterval time.Duration
	ModelBasePath       string
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
	return &Manager{
		mode:                ModeInference,
		gpuVendor:           cfg.GPUVendor,
		gpuArch:             cfg.GPUArch,
		shutdownTimeout:     cfg.ShutdownTimeout,
		healthCheckInterval: cfg.HealthCheckInterval,
		modelBasePath:       cfg.ModelBasePath,
	}
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

	m.mu.Lock()
	defer m.mu.Unlock()

	// Idempotency: if the same model+backend is already loaded and healthy,
	// skip the unload/reload cycle.  This prevents the controller and proxy
	// from fighting over the same model.
	if m.active != nil && m.active.Name == name && m.active.Backend == req.Backend &&
		(m.active.State == ModelStateReady || m.active.State == ModelStateLoading) {
		logger.Info("Model already loaded, skipping reload",
			"state", string(m.active.State))
		return nil
	}

	// Unload any active model first (different model or failed state).
	if m.active != nil {
		logger.Info("Unloading active model before loading new one", "active", m.active.Name)
		if err := m.unloadLocked(ctx); err != nil {
			logger.Error(err, "Failed to unload active model, forcing")
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
		Port:    b.Port(),
		cmd:     cmd,
		cancel:  cancel,
	}

	m.active = loaded

	// Clear any previous model state metrics.
	ModelActiveState.Reset()
	ModelActiveState.WithLabelValues(name, req.Backend, "Loading").Set(1)

	logger.Info("Starting backend subprocess",
		"executable", executable,
		"args", execArgs,
		"port", b.Port(),
	)

	if err := cmd.Start(); err != nil {
		loaded.State = ModelStateFailed
		loaded.Error = err.Error()
		cancel()
		ModelLoadsTotal.WithLabelValues(req.Backend, "error").Inc()
		ModelActiveState.Reset()
		ModelActiveState.WithLabelValues(name, req.Backend, "Failed").Set(1)
		return fmt.Errorf("failed to start backend: %w", err)
	}

	loaded.PID = cmd.Process.Pid
	loaded.LoadedAt = time.Now()

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
	go m.monitorProcess(subCtx, name, cmd)

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
	go m.healthCheckLoop(subCtx, name, b, startupTimeout)

	return nil
}

// Unload stops the active model's backend subprocess.
func (m *Manager) Unload(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active == nil || m.active.Name != name {
		return fmt.Errorf("model %q is not loaded", name)
	}

	return m.unloadLocked(ctx)
}

// unloadLocked stops the active subprocess. Caller must hold m.mu.
func (m *Manager) unloadLocked(ctx context.Context) error {
	if m.active == nil {
		return nil
	}

	logger := log.FromContext(ctx).WithValues("model", m.active.Name)
	m.active.State = ModelStateStopping

	// Send SIGTERM for graceful shutdown.
	if m.active.cmd != nil && m.active.cmd.Process != nil {
		logger.Info("Sending SIGTERM to backend", "pid", m.active.PID)
		_ = m.active.cmd.Process.Signal(syscall.SIGTERM)

		// Wait for graceful exit with timeout. The done channel signals
		// that cmd.Wait() has returned and the process has been reaped.
		// We must not call Kill() after Wait() returns, because the PID
		// may have been recycled by the OS.
		done := make(chan error, 1)
		go func() { done <- m.active.cmd.Wait() }()

		select {
		case <-time.After(m.shutdownTimeout):
			// Only kill if the process has not yet exited. We use a
			// non-blocking check on done to avoid a race with PID reuse:
			// if Wait() already returned, the PID is reaped and could be
			// reassigned to an unrelated process.
			select {
			case err := <-done:
				// Process already exited before we could kill it.
				if err != nil {
					logger.Info("Backend exited during shutdown timeout", "error", err)
				}
			default:
				logger.Info("Shutdown timeout exceeded, sending SIGKILL", "pid", m.active.PID)
				if err := m.active.cmd.Process.Kill(); err != nil {
					logger.Info("Kill returned error (process may have already exited)", "error", err)
				}
				<-done // Wait for the process to be reaped after kill.
			}
		case err := <-done:
			if err != nil {
				logger.Info("Backend exited", "error", err)
			}
		}
	}

	if m.active.cancel != nil {
		m.active.cancel()
	}

	ModelUnloadsTotal.WithLabelValues(m.active.Backend, "requested").Inc()
	ModelActiveState.Reset()
	m.active = nil
	return nil
}

// Active returns the currently loaded model, or nil.
func (m *Manager) Active() *LoadedModel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// Status returns summary information about the runtime.
func (m *Manager) Status() RuntimeStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s := RuntimeStatus{
		GPUVendor: string(m.gpuVendor),
		GPUArch:   m.gpuArch,
		Mode:      string(m.mode),
	}

	if m.active != nil {
		s.ActiveModel = &ModelSummary{
			Name:     m.active.Name,
			Backend:  m.active.Backend,
			Model:    m.active.Model,
			State:    string(m.active.State),
			Port:     m.active.Port,
			PID:      m.active.PID,
			LoadedAt: m.active.LoadedAt,
			Error:    m.active.Error,
		}
	}

	return s
}

// Shutdown gracefully stops any active subprocess.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.unloadLocked(ctx)
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

	// Unload whatever is currently running.
	if m.active != nil {
		logger.Info("Unloading active model for mode switch", "active", m.active.Name)
		if err := m.unloadLocked(ctx); err != nil {
			logger.Error(err, "Failed to unload during mode switch")
		}
	}

	m.mode = target
	m.mu.Unlock()

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
	GPUVendor   string        `json:"gpuVendor"`
	GPUArch     string        `json:"gpuArch"`
	Mode        string        `json:"mode"`
	ActiveModel *ModelSummary `json:"activeModel,omitempty"`
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
func (m *Manager) monitorProcess(ctx context.Context, name string, cmd *exec.Cmd) {
	logger := log.FromContext(ctx).WithValues("model", name)

	err := cmd.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Only update if this is still the active model.
	if m.active != nil && m.active.Name == name {
		if err != nil && m.active.State != ModelStateStopping {
			logger.Error(err, "Backend subprocess crashed")
			m.active.State = ModelStateFailed
			m.active.Error = err.Error()
			BackendSubprocessCrashesTotal.WithLabelValues(name, m.active.Backend).Inc()
			ModelActiveState.Reset()
			ModelActiveState.WithLabelValues(name, m.active.Backend, "Failed").Set(1)
		}
	}
}

// healthCheckLoop polls the backend's health endpoint until ready or cancelled.
func (m *Manager) healthCheckLoop(ctx context.Context, name string, b backend.Backend, startupTimeout time.Duration) {
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
		if m.active != nil && m.active.Name == name && m.active.State == ModelStateLoading {
			m.active.State = ModelStateReady
			logger.Info("Model marked ready (no probe defined, startup timeout elapsed)")
		}
		m.mu.Unlock()
		return
	}

	healthPath := probe.HTTPGet.Path
	port := b.Port()
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
			if m.active != nil && m.active.Name == name && m.active.State == ModelStateLoading {
				m.active.State = ModelStateFailed
				m.active.Error = "startup timeout exceeded"
				logger.Error(nil, "Backend startup timeout exceeded")
				ModelActiveState.Reset()
				ModelActiveState.WithLabelValues(name, m.active.Backend, "Failed").Set(1)
			}
			m.mu.Unlock()
			return
		case <-ticker.C:
			if checkHTTPHealth(healthURL) {
				m.mu.Lock()
				if m.active != nil && m.active.Name == name && m.active.State == ModelStateLoading {
					m.active.State = ModelStateReady
					logger.Info("Model is ready", "healthURL", healthURL)
					ModelActiveState.Reset()
					ModelActiveState.WithLabelValues(name, m.active.Backend, "Ready").Set(1)
					ModelLoadDurationSeconds.WithLabelValues(name, m.active.Backend).Observe(time.Since(m.active.LoadedAt).Seconds())
				}
				m.mu.Unlock()

				// Continue monitoring for ongoing health.
				m.continuousHealthCheck(ctx, name, healthURL)
				return
			}
		}
	}
}

// continuousHealthCheck monitors a ready model and marks it failed if unhealthy.
func (m *Manager) continuousHealthCheck(ctx context.Context, name, healthURL string) {
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
				if m.active != nil && m.active.Name == name && m.active.State == ModelStateReady {
					m.active.State = ModelStateFailed
					m.active.Error = "health check failed"
					logger.Error(nil, "Backend health check failed, marking model as failed")
					HealthCheckFailuresTotal.WithLabelValues(name, m.active.Backend).Inc()
					ModelActiveState.Reset()
					ModelActiveState.WithLabelValues(name, m.active.Backend, "Failed").Set(1)
				}
				m.mu.Unlock()
				return
			}
		}
	}
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
