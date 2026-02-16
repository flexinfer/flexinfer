package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/crb2nu/loom/internal/devbox/backend"
	"github.com/crb2nu/loom/internal/devbox/detect"
	"github.com/crb2nu/loom/internal/devbox/dockerfile"
	"github.com/crb2nu/loom/internal/devbox/state"
)

type managerConfig struct {
	workspaceRoot string
	cacheDir      string
	backendType   string
	registry      string
	imagePrefix   string
	maxTailLines  int
	idleTimeout   time.Duration
	defaultCPU    float64
	defaultMemMB  int

	// K8s-specific
	kubeconfig         string
	k8sNamespace       string
	storageClass       string
	k8sWorkspacePVC    string
	k8sImagePullSecret string
	builderImage       string
}

type manager struct {
	cfg     managerConfig
	backend backend.Backend
	store   *state.Store
	logger  *slog.Logger
	metrics *metrics
	events  *eventEmitter

	// Async exec tracking
	asyncExecs *asyncRegistry

	// Per-project lifecycle lock prevents concurrent ensureRunning races (TOCTOU).
	projectMu sync.Map // map[string]*sync.Mutex

	// Active exec counter per project — reaper skips projects with active execs.
	activeExecs sync.Map // map[string]*atomic.Int32
}

// backendHealthTimeout bounds startup probing so MCP init is never blocked by a hung runtime.
var backendHealthTimeout = 3 * time.Second

func checkBackendHealth(ctx context.Context, logger *slog.Logger, health func(context.Context) error) {
	healthCtx, cancel := context.WithTimeout(ctx, backendHealthTimeout)
	defer cancel()

	if err := health(healthCtx); err != nil {
		logger.Warn("backend health check failed", "error", err)
	}
}

// projectLock returns (or creates) a per-project mutex for lifecycle serialization.
func (m *manager) projectLock(name string) *sync.Mutex {
	v, _ := m.projectMu.LoadOrStore(name, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// incActiveExecs increments the active exec counter for a project.
func (m *manager) incActiveExecs(name string) {
	v, _ := m.activeExecs.LoadOrStore(name, &atomic.Int32{})
	v.(*atomic.Int32).Add(1)
}

// decActiveExecs decrements the active exec counter for a project.
func (m *manager) decActiveExecs(name string) {
	if v, ok := m.activeExecs.Load(name); ok {
		v.(*atomic.Int32).Add(-1)
	}
}

// hasActiveExecs returns true if a project has exec calls in flight.
func (m *manager) hasActiveExecs(name string) bool {
	if v, ok := m.activeExecs.Load(name); ok {
		return v.(*atomic.Int32).Load() > 0
	}
	return false
}

// sanitizeContainerName ensures the name contains only Docker-safe characters.
func sanitizeContainerName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}

// validateMountPath ensures a host path is under an allowed directory.
func (m *manager) validateMountPath(hostPath string) error {
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return fmt.Errorf("invalid mount path %q: %w", hostPath, err)
	}
	home, _ := os.UserHomeDir()
	allowed := []string{
		m.cfg.workspaceRoot,
		filepath.Join(home, ".cache"),
		filepath.Join(home, ".local"),
	}
	for _, prefix := range allowed {
		if strings.HasPrefix(abs, prefix+string(filepath.Separator)) || abs == prefix {
			return nil
		}
	}
	return fmt.Errorf("mount path %q not under allowed directories (%s)", hostPath, strings.Join(allowed, ", "))
}

func newManager(ctx context.Context, logger *slog.Logger, cfg managerConfig) (*manager, error) {
	var b backend.Backend
	switch cfg.backendType {
	case "docker":
		db, err := backend.NewDockerBackend()
		if err != nil {
			return nil, err
		}
		b = db
	case "k8s", "kubernetes":
		kb, err := backend.NewK8sBackend(backend.K8sBackendConfig{
			Kubeconfig:      cfg.kubeconfig,
			Namespace:       cfg.k8sNamespace,
			Registry:        cfg.registry,
			WorkspacePVC:    cfg.k8sWorkspacePVC,
			ImagePullSecret: cfg.k8sImagePullSecret,
			WorkspaceRoot:   cfg.workspaceRoot,
			BuilderImage:    cfg.builderImage,
		})
		if err != nil {
			return nil, err
		}
		b = kb
	default:
		return nil, fmt.Errorf("unsupported backend: %s (use 'docker' or 'k8s')", cfg.backendType)
	}

	checkBackendHealth(ctx, logger, b.Health)

	store, err := state.NewStore(cfg.cacheDir)
	if err != nil {
		return nil, fmt.Errorf("init state store: %w", err)
	}

	return &manager{cfg: cfg, backend: b, store: store, logger: logger}, nil
}

// resolveProject finds the absolute path for a project name.
// Searches: workspace/<name>, workspace/services/<name>, workspace/libs/<name>, workspace/platform/<name>
func (m *manager) resolveProject(project string) (string, string, error) {
	// If it's already an absolute path
	if filepath.IsAbs(project) {
		if info, err := os.Stat(project); err == nil && info.IsDir() {
			return project, filepath.Base(project), nil
		}
		return "", "", fmt.Errorf("project directory not found: %s", project)
	}

	candidates := []string{
		filepath.Join(m.cfg.workspaceRoot, project),
		filepath.Join(m.cfg.workspaceRoot, "services", project),
		filepath.Join(m.cfg.workspaceRoot, "libs", project),
		filepath.Join(m.cfg.workspaceRoot, "platform", project),
	}

	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			name := filepath.Base(path)
			return path, name, nil
		}
	}

	return "", "", fmt.Errorf("project '%s' not found under %s", project, m.cfg.workspaceRoot)
}

// imageTag returns the Docker image tag for a project fingerprint.
func (m *manager) imageTag(projectName, hash string) string {
	return fmt.Sprintf("%s/%s:%s", m.cfg.imagePrefix, sanitizeContainerName(projectName), hash[:7])
}

// containerName returns the Docker container name for a project.
func (m *manager) containerName(projectName string) string {
	return "devbox-" + sanitizeContainerName(projectName)
}

// ensureRunning ensures a sandbox is built and running for a project.
// Returns the container ID.
func (m *manager) ensureRunning(ctx context.Context, projectDir, projectName, agentID string) (string, error) {
	// Fingerprint the project
	fp, err := detect.Fingerprint(projectDir)
	if err != nil {
		return "", fmt.Errorf("fingerprint: %w", err)
	}

	entry := m.store.Get(projectName)
	tag := m.imageTag(projectName, fp.Hash)
	containerID := m.containerName(projectName)

	// Fast path: container exists with matching hash
	if entry != nil && entry.FingerprintHash == fp.Hash && entry.Status == "running" {
		status, err := m.backend.Status(ctx, containerID)
		if err == nil && status.Running {
			return containerID, nil
		}
	}

	// Warm resume: paused container with matching hash — unpause instead of rebuild
	if entry != nil && entry.FingerprintHash == fp.Hash && entry.Status == "paused" {
		if err := m.backend.Resume(ctx, containerID); err == nil {
			entry.Status = "running"
			entry.LastUsed = time.Now()
			_ = m.store.Set(projectName, entry)
			m.logger.Info("resumed paused sandbox", "project", projectName)
			return containerID, nil
		}
		// Resume failed — fall through to rebuild
		m.logger.Warn("resume failed, rebuilding", "project", projectName)
	}

	// Stale or missing: rebuild if hash changed
	if entry == nil || entry.FingerprintHash != fp.Hash {
		m.logger.Info("building sandbox image", "project", projectName, "hash", fp.Hash[:7])

		dockerfileContent, err := dockerfile.Generate(fp)
		if err != nil {
			return "", fmt.Errorf("generate dockerfile: %w", err)
		}

		_, err = m.backend.Build(ctx, backend.BuildOpts{
			Tag:        tag,
			Dockerfile: dockerfileContent,
			ContextDir: projectDir,
		})
		if err != nil {
			return "", fmt.Errorf("build image: %w", err)
		}
	}

	// Stop existing container if running
	_ = m.backend.Stop(ctx, containerID)

	// Start new container
	mounts := m.buildMounts(projectDir)

	memMB := m.cfg.defaultMemMB
	cpu := m.cfg.defaultCPU
	network := true
	if fp.Overrides != nil {
		if fp.Overrides.Limits != nil {
			if fp.Overrides.Limits.MemoryMB > 0 {
				memMB = fp.Overrides.Limits.MemoryMB
			}
			if fp.Overrides.Limits.CPU > 0 {
				cpu = fp.Overrides.Limits.CPU
			}
		}
		if fp.Overrides.Network != nil {
			network = *fp.Overrides.Network
		}
		for _, extra := range fp.Overrides.Mounts {
			if err := m.validateMountPath(extra.Host); err != nil {
				return "", fmt.Errorf("invalid override mount: %w", err)
			}
			mounts = append(mounts, backend.Mount{
				Host:      extra.Host,
				Container: extra.Container,
				ReadOnly:  extra.ReadOnly,
			})
		}
	}

	workDir := m.projectWorkDir(projectDir)
	m.logger.Info("starting sandbox", "project", projectName, "image", tag, "workdir", workDir)
	result, err := m.backend.Start(ctx, backend.StartOpts{
		Name:     containerID,
		ImageTag: tag,
		WorkDir:  workDir,
		Mounts:   mounts,
		Env:      fp.EnvVars,
		MemoryMB: memMB,
		CPUs:     cpu,
		Network:  network,
		AgentID:  agentID,
	})
	if err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}

	// Persist state
	now := time.Now()
	if err := m.store.Set(projectName, &state.Entry{
		ProjectDir:      projectDir,
		ContainerID:     result.ContainerID,
		ImageTag:        tag,
		FingerprintHash: fp.Hash,
		Backend:         m.cfg.backendType,
		Status:          "running",
		LastUsed:        now,
		CreatedAt:       now,
	}); err != nil {
		m.logger.Warn("failed to persist state", "error", err)
	}

	return m.containerName(projectName), nil
}

// projectWorkDir returns the working directory inside the container for a project.
// If the project is under workspaceRoot, we mount the root and use a subdirectory.
// Otherwise, we mount the project directly at /workspace.
func (m *manager) projectWorkDir(projectDir string) string {
	rel, err := filepath.Rel(m.cfg.workspaceRoot, projectDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "/workspace"
	}
	return filepath.Join("/workspace", rel)
}

// buildMounts creates the standard bind mounts for a sandbox.
// For K8s backend, returns empty slice — NFS PVC handles workspace mounting.
func (m *manager) buildMounts(projectDir string) []backend.Mount {
	// K8s backend uses NFS PVC for workspace; host mounts are not available on cluster nodes.
	if m.cfg.backendType == "k8s" || m.cfg.backendType == "kubernetes" {
		return nil
	}

	home, _ := os.UserHomeDir()

	// Mount workspace root so sibling projects (Go replace directives) are accessible
	mountHost := m.cfg.workspaceRoot
	rel, err := filepath.Rel(m.cfg.workspaceRoot, projectDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		// Project is outside workspace root — mount project directly
		mountHost = projectDir
	}

	mounts := []backend.Mount{
		{Host: mountHost, Container: "/workspace"},
	}

	// Shared caches (only mount if they exist on host)
	caches := []struct {
		host      string
		container string
	}{
		{filepath.Join(home, ".cache", "go", "mod"), "/go/pkg/mod"},
		{filepath.Join(home, ".cache", "pip"), "/root/.cache/pip"},
		{filepath.Join(home, ".local", "share", "pnpm", "store"), "/root/.local/share/pnpm/store"},
	}

	for _, c := range caches {
		if info, err := os.Stat(c.host); err == nil && info.IsDir() {
			mounts = append(mounts, backend.Mount{Host: c.host, Container: c.container})
		}
	}

	return mounts
}

// reapLoop periodically stops idle containers.
func (m *manager) reapLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reapIdle(ctx)
		}
	}
}

// reapIdle pauses containers that have been idle beyond the timeout.
// Paused containers can be resumed instantly (~5ms) vs cold start (~2-5s).
// Falls back to stop if pause is not supported by the backend.
func (m *manager) reapIdle(ctx context.Context) {
	idle := m.store.IdleEntries(m.cfg.idleTimeout)
	for name, entry := range idle {
		// Skip projects with active exec calls
		if m.hasActiveExecs(name) {
			continue
		}

		m.logger.Info("pausing idle sandbox", "project", name,
			"idle_since", entry.LastUsed.Format(time.RFC3339))

		containerName := m.containerName(name)

		// Try pause first (instant resume); fall back to stop
		if err := m.backend.Pause(ctx, containerName); err != nil {
			// Pause not supported — fall back to stop
			if err := m.backend.Stop(ctx, containerName); err != nil {
				m.logger.Warn("failed to stop idle sandbox", "project", name, "error", err)
				continue
			}
			entry.Status = "stopped"
		} else {
			entry.Status = "paused"
		}

		if m.metrics != nil {
			m.metrics.idleReaps.WithLabelValues(name).Inc()
		}
		if err := m.store.Set(name, entry); err != nil {
			m.logger.Warn("failed to update state", "project", name, "error", err)
		}
	}
}

// shutdownAll stops all managed containers gracefully.
func (m *manager) shutdownAll(ctx context.Context) {
	entries := m.store.List()
	for name, entry := range entries {
		if entry.Status == "running" {
			containerName := m.containerName(name)
			m.logger.Info("shutting down sandbox", "project", name)
			if err := m.backend.Stop(ctx, containerName); err != nil {
				m.logger.Warn("failed to stop sandbox on shutdown", "project", name, "error", err)
			}
			entry.Status = "stopped"
			_ = m.store.Set(name, entry)
		}
	}
}

// langNames returns a comma-separated string of detected language names.
func langNames(fp *detect.EnvFingerprint) string {
	names := make([]string, len(fp.Languages))
	for i, l := range fp.Languages {
		names[i] = l.Language
	}
	return strings.Join(names, ", ")
}
