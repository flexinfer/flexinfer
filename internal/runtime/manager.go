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

// Manager controls backend subprocess lifecycle on a GPU node.
// Only one backend subprocess runs at a time (single-GPU constraint).
type Manager struct {
	mu sync.RWMutex

	// active is the currently loaded model (nil if none).
	active *LoadedModel

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
		return fmt.Errorf("unknown backend %q", req.Backend)
	}

	if !b.SupportsGPUVendor(m.gpuVendor) {
		return fmt.Errorf("backend %q does not support GPU vendor %q", req.Backend, m.gpuVendor)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Unload any active model first.
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

	// Determine the executable.
	var executable string
	var execArgs []string
	if len(command) > 0 {
		executable = command[0]
		execArgs = append(command[1:], args...)
	} else {
		// Infer executable from backend name.
		executable = inferExecutable(b.Name())
		execArgs = args
	}

	subCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(subCtx, executable, execArgs...)

	// Inherit current process env, then overlay backend-specific vars.
	cmd.Env = os.Environ()
	for _, e := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", e.Name, e.Value))
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

	// Stream subprocess output to logger.
	go streamLogs(stdout, logger, "stdout")
	go streamLogs(stderr, logger, "stderr")

	// Monitor subprocess exit.
	go m.monitorProcess(subCtx, name, cmd)

	// Start health checking in background.
	go m.healthCheckLoop(subCtx, name, b)

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

// RuntimeStatus is the serializable status of the runtime manager.
type RuntimeStatus struct {
	GPUVendor   string        `json:"gpuVendor"`
	GPUArch     string        `json:"gpuArch"`
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
func (m *Manager) healthCheckLoop(ctx context.Context, name string, b backend.Backend) {
	logger := log.FromContext(ctx).WithValues("model", name)

	probe := b.ReadinessProbe()
	if probe == nil || probe.HTTPGet == nil {
		// No HTTP probe defined — mark ready after startup timeout.
		select {
		case <-time.After(b.StartupTimeout()):
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

	startupDeadline := time.After(b.StartupTimeout())

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

// inferExecutable maps backend name to an executable path.
func inferExecutable(backendName string) string {
	switch backendName {
	case "vllm":
		return "python"
	case "llamacpp":
		return "llama-server"
	case "ollama":
		return "ollama"
	case "diffusers":
		return "python"
	case "comfyui":
		return "python"
	default:
		return backendName
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
