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
	sharedrt "github.com/flexinfer/flexinfer/pkg/runtime"
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
	Backend   string                 `json:"backend"`
	Model     string                 `json:"model"`
	ModelPath string                 `json:"modelPath,omitempty"`
	Config    map[string]interface{} `json:"config,omitempty"`
	Env       []sharedrt.EnvVar      `json:"env,omitempty"`
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
	Launch    *LaunchPlan        `json:"launch,omitempty"`
	HealthURL string             `json:"-"`
	cmd       *exec.Cmd          `json:"-"`
	cancel    context.CancelFunc `json:"-"`
}

// LaunchPlan captures the resolved subprocess configuration for a loaded model.
type LaunchPlan struct {
	Executable            string            `json:"executable"`
	Args                  []string          `json:"args,omitempty"`
	Env                   []sharedrt.EnvVar `json:"env,omitempty"`
	ModelPath             string            `json:"modelPath,omitempty"`
	StartupTimeoutSeconds int               `json:"startupTimeoutSeconds,omitempty"`
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

	if b.Name() == "comfyui" {
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
	if m.gpuVendor == backend.GPUVendorAMD {
		env = append(env, backend.ROCmEnvVars(m.gpuArch)...)
		env = append(env, backend.DeviceIsolationEnvVars(spec)...)
	}

	// In the runtime, all models share /models without SubPath mounts.
	// Set LOCAL_MODEL_PATH so backends that use env-based model discovery
	// (e.g. diffusers server-diffusers.py) find the correct subdirectory.
	if modelPath != "" {
		env = append(env, corev1.EnvVar{Name: "LOCAL_MODEL_PATH", Value: modelPath})
	}

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
		cmd.Env = upsertCommandEnv(cmd.Env, e.Name, e.Value)
	}
	for _, e := range req.Env {
		cmd.Env = upsertCommandEnv(cmd.Env, e.Name, e.Value)
	}

	// Pipe stdout/stderr to structured logging.
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	loaded := &LoadedModel{
		Name:    name,
		Backend: req.Backend,
		Model:   req.Model,
		State:   ModelStateLoading,
		Port:    b.Port(),
		Launch: &LaunchPlan{
			Executable:            executable,
			Args:                  append([]string(nil), execArgs...),
			Env:                   mergeLaunchEnv(env, req.Env),
			ModelPath:             modelPath,
			StartupTimeoutSeconds: int(startupTimeout.Seconds()),
		},
		cmd:    cmd,
		cancel: cancel,
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

	// Stream subprocess output to logger.
	go streamLogs(stdout, logger, "stdout")
	go streamLogs(stderr, logger, "stderr")

	// Monitor subprocess exit.
	go m.monitorProcess(subCtx, name, cmd)

	// Start health checking in background.
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

		// Wait for graceful exit with timeout.
		done := make(chan error, 1)
		go func() { done <- m.active.cmd.Wait() }()

		select {
		case <-time.After(m.shutdownTimeout):
			logger.Info("Shutdown timeout exceeded, sending SIGKILL", "pid", m.active.PID)
			_ = m.active.cmd.Process.Kill()
			<-done
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
			Launch:   m.active.Launch,
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
func (m *Manager) SetMode(ctx context.Context, target NodeMode) error {
	logger := log.FromContext(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.mode == target {
		logger.Info("Already in target mode", "mode", target)
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

	if target == ModeGaming {
		// Load steam backend. Release lock briefly to reuse Load()
		// which acquires the lock itself.
		m.mu.Unlock()
		err := m.Load(ctx, "__gaming__", LoadRequest{
			Backend: "steam",
		})
		m.mu.Lock()
		if err != nil {
			m.mode = ModeInference // revert on failure
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
	Name     string      `json:"name"`
	Backend  string      `json:"backend"`
	Model    string      `json:"model"`
	State    string      `json:"state"`
	Port     int32       `json:"port"`
	PID      int         `json:"pid,omitempty"`
	LoadedAt time.Time   `json:"loadedAt,omitempty"`
	Error    string      `json:"error,omitempty"`
	Launch   *LaunchPlan `json:"launch,omitempty"`
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
	case "vllm", "vllm-omni":
		return "python", []string{"-m", "vllm.entrypoints.openai.api_server"}
	case "diffusers":
		return "python", []string{"/opt/flexinfer/server-diffusers.py"}
	case "llamacpp":
		return "llama-server", nil
	case "ollama":
		return "ollama", nil
	case "steam":
		return "steam", nil
	default:
		return backendName, nil
	}
}

// streamLogs reads from r line-by-line and logs each line.
func streamLogs(r io.ReadCloser, logger interface {
	Info(msg string, keysAndValues ...interface{})
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

func upsertCommandEnv(env []string, key, value string) []string {
	prefix := key + "="
	entry := prefix + value
	for i := range env {
		if strings.HasPrefix(env[i], prefix) {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

func mergeLaunchEnv(base []corev1.EnvVar, overlay []sharedrt.EnvVar) []sharedrt.EnvVar {
	merged := make([]sharedrt.EnvVar, 0, len(base)+len(overlay))
	for _, item := range base {
		merged = appendOrReplaceLaunchEnv(merged, sharedrt.EnvVar{Name: item.Name, Value: item.Value})
	}
	for _, item := range overlay {
		merged = appendOrReplaceLaunchEnv(merged, item)
	}
	return merged
}

func appendOrReplaceLaunchEnv(env []sharedrt.EnvVar, item sharedrt.EnvVar) []sharedrt.EnvVar {
	for i := range env {
		if env[i].Name == item.Name {
			env[i].Value = item.Value
			return env
		}
	}
	return append(env, item)
}
