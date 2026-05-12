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
	kubeconfig          string
	k8sNamespace        string
	storageClass        string
	k8sWorkspacePVC     string
	k8sImagePullSecret  string
	builderImage        string
	gitCloneImage       string
	buildCPURequest     string
	buildCPULimit       string
	buildMemoryRequest  string
	buildMemoryLimit    string
	buildAvoidNodes     string
	maxConcurrentBuilds int

	// NFS cache flush before each exec (default true for K8s backend)
	nfsFlush bool

	// Git-clone mode: populate workspace via git clone instead of NFS PVC
	gitBaseURL string // base git URL (e.g., "https://gitlab.blevins.dev/homelab")
	gitSecret  string // K8s secret name with git token (key: "token")

	// Tar-pipe sync: stream local files into pods via SPDY exec
	syncMode     string   // "tar-pipe", "git-clone", "nfs"
	syncExcludes []string // additional exclude patterns
	maxSyncSize  int64    // max uncompressed tar bytes

	// Warm pool: pre-provision pods for these projects on startup
	warmProjects []string
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

	// Cumulative counters for HUD summary.
	startedAt   time.Time
	totalExecs  atomic.Int64
	totalBuilds atomic.Int64

	// asyncWg tracks running async goroutines for graceful shutdown.
	asyncWg sync.WaitGroup
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
// Resolves symlinks before validation so symlinked paths are correctly matched.
func (m *manager) validateMountPath(hostPath string) error {
	resolved, err := filepath.EvalSymlinks(hostPath)
	if err != nil {
		// Path doesn't exist yet — fall back to Abs only.
		resolved = hostPath
	}
	abs, err := filepath.Abs(resolved)
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
			Kubeconfig:          cfg.kubeconfig,
			Namespace:           cfg.k8sNamespace,
			Registry:            cfg.registry,
			WorkspacePVC:        cfg.k8sWorkspacePVC,
			ImagePullSecret:     cfg.k8sImagePullSecret,
			WorkspaceRoot:       cfg.workspaceRoot,
			BuilderImage:        cfg.builderImage,
			GitCloneImage:       cfg.gitCloneImage,
			NFSFlush:            cfg.nfsFlush,
			GitBaseURL:          cfg.gitBaseURL,
			GitSecret:           cfg.gitSecret,
			BuildCPURequest:     cfg.buildCPURequest,
			BuildCPULimit:       cfg.buildCPULimit,
			BuildMemoryRequest:  cfg.buildMemoryRequest,
			BuildMemoryLimit:    cfg.buildMemoryLimit,
			BuildAvoidNodes:     cfg.buildAvoidNodes,
			MaxConcurrentBuilds: cfg.maxConcurrentBuilds,
			SyncMode:            cfg.syncMode,
			SyncExcludes:        cfg.syncExcludes,
			MaxSyncSize:         cfg.maxSyncSize,
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

// containerName returns the Docker container/pod name for a project.
// When agentID is provided, the pod name includes a truncated agent suffix
// for per-agent isolation: devbox-<project>-<agent>.
func (m *manager) containerName(projectName, agentID string) string {
	base := "devbox-" + sanitizeContainerName(projectName)
	if agentID != "" {
		id := sanitizeContainerName(agentID)
		if len(id) > 12 {
			id = id[:12]
		}
		return base + "-" + id
	}
	return base
}

// storeKey returns the state store key for a project+agent combination.
// When agentID is set, returns "project/agentID" for per-agent state isolation.
func storeKey(projectName, agentID string) string {
	if agentID != "" {
		return projectName + "/" + agentID
	}
	return projectName
}

// ensureRunning ensures a sandbox is built and running for a project.
// Returns the container ID. When agentID is provided, each agent gets
// its own isolated pod for the project.
func (m *manager) ensureRunning(ctx context.Context, projectDir, projectName, agentID string) (string, error) {
	// Fingerprint the project
	fp, err := detect.Fingerprint(projectDir)
	if err != nil {
		return "", fmt.Errorf("fingerprint: %w", err)
	}

	key := storeKey(projectName, agentID)
	entry := m.store.Get(key)
	tag := m.imageTag(projectName, fp.Hash)
	containerID := m.containerName(projectName, agentID)

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
			_ = m.store.Set(key, entry)
			m.logger.Info("resumed paused sandbox", "project", projectName, "agent", agentID)
			return containerID, nil
		}
		// Resume failed — fall through to rebuild
		m.logger.Warn("resume failed, rebuilding", "project", projectName, "agent", agentID)
	}

	// Stopped container with matching hash — try to restart without rebuild.
	// For K8s backend, Start() reuses existing running pods or creates new ones.
	if entry != nil && entry.FingerprintHash == fp.Hash && entry.Status == "stopped" {
		m.logger.Info("restarting stopped sandbox (hash match)", "project", projectName, "agent", agentID)
		// Skip build, go straight to Start below
	} else if entry == nil || entry.FingerprintHash != fp.Hash {
		// Stale or missing: rebuild if hash changed
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
	m.logger.Info("starting sandbox", "project", projectName, "agent", agentID, "image", tag, "workdir", workDir)
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

	// Tar-pipe sync: stream local source into the pod after it starts.
	if err := m.syncIfNeeded(ctx, containerID, projectDir); err != nil {
		return "", fmt.Errorf("sync workspace: %w", err)
	}

	// Persist state
	now := time.Now()
	if err := m.store.Set(key, &state.Entry{
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

	return containerID, nil
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

// syncIfNeeded performs tar-pipe workspace sync if configured.
// It discovers the project's sibling deps (Go replace directives) and streams
// all source files into the pod via SPDY exec.
func (m *manager) syncIfNeeded(ctx context.Context, containerID, projectDir string) error {
	if m.cfg.syncMode != "tar-pipe" {
		return nil
	}

	kb, ok := m.backend.(*backend.K8sBackend)
	if !ok {
		return nil // tar-pipe only works with K8s backend
	}

	dirs, err := backend.DiscoverDeps(projectDir, m.cfg.workspaceRoot)
	if err != nil {
		return fmt.Errorf("discover deps: %w", err)
	}

	m.logger.Info("syncing workspace", "project", filepath.Base(projectDir),
		"dirs", len(dirs), "container", containerID)

	return kb.SyncWorkspace(ctx, containerID, dirs, m.cfg.syncExcludes, m.cfg.maxSyncSize) //nolint:staticcheck // intentionally deprecated, migrating to sandbox.Controller
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

// reapLoop periodically stops idle containers, prunes stale state, and cleans up build pods.
func (m *manager) reapLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reapIdle(ctx)
			m.reapCompletedBuilds(ctx)
			m.store.PruneOlderThan(7 * 24 * time.Hour)
		}
	}
}

// reapCompletedBuilds cleans up completed build pods and orphaned ConfigMaps.
func (m *manager) reapCompletedBuilds(ctx context.Context) {
	cleaned, err := m.backend.CleanupBuilds(ctx, 1*time.Hour)
	if err != nil {
		m.logger.Warn("build cleanup failed", "error", err)
		return
	}
	if cleaned > 0 {
		m.logger.Info("cleaned up build resources", "count", cleaned)
	}
}

// isK8sBackend returns true if the backend is Kubernetes-based.
func (m *manager) isK8sBackend() bool {
	return m.cfg.backendType == "k8s" || m.cfg.backendType == "kubernetes"
}

// isWarmProject returns true if the project is in the warm pool list.
func (m *manager) isWarmProject(projectName string) bool {
	for _, p := range m.cfg.warmProjects {
		if p == projectName {
			return true
		}
	}
	return false
}

// warmPool pre-provisions pods for configured warm projects.
// Runs on startup and re-warms every 30 minutes.
func (m *manager) warmPool(ctx context.Context) {
	m.warmOnce(ctx)

	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.warmOnce(ctx)
		}
	}
}

func (m *manager) warmOnce(ctx context.Context) {
	for _, project := range m.cfg.warmProjects {
		projectDir, projectName, err := m.resolveProject(project)
		if err != nil {
			m.logger.Warn("warm pool: project not found", "project", project, "error", err)
			continue
		}

		key := storeKey(projectName, "")
		mu := m.projectLock(key)
		mu.Lock()
		_, err = m.ensureRunning(ctx, projectDir, projectName, "")
		mu.Unlock()
		if err != nil {
			m.logger.Warn("warm pool: failed to warm project", "project", projectName, "error", err)
		} else {
			m.logger.Info("warm pool: project ready", "project", projectName)
		}
	}
}

// parseStoreKey splits a compound store key ("project/agent") into its parts.
func parseStoreKey(key string) (projectName, agentID string) {
	if idx := strings.Index(key, "/"); idx >= 0 {
		return key[:idx], key[idx+1:]
	}
	return key, ""
}

// reapIdle pauses containers that have been idle beyond the timeout.
// Paused containers can be resumed instantly (~5ms) vs cold start (~2-5s).
// Falls back to stop if pause is not supported by the backend.
//
// K8s-aware: sleeping K8s pods use ~0 CPU. On first idle timeout, just log
// "keeping warm". Hard-reap (stop) only after 2× idle timeout.
func (m *manager) reapIdle(ctx context.Context) {
	idle := m.store.IdleEntries(m.cfg.idleTimeout)
	for key, entry := range idle {
		// Skip entries with active exec calls
		if m.hasActiveExecs(key) {
			continue
		}

		projectName, agentID := parseStoreKey(key)

		// Skip warm-pool projects — the warm pool goroutine will just recreate them.
		if agentID == "" && m.isWarmProject(projectName) {
			continue
		}

		containerName := m.containerName(projectName, agentID)

		// K8s-aware: keep pods warm on first idle, hard-reap at 2× timeout.
		if m.isK8sBackend() {
			idleDuration := time.Since(entry.LastUsed)
			if idleDuration < 2*m.cfg.idleTimeout {
				m.logger.Debug("keeping K8s pod warm", "key", key,
					"idle_since", entry.LastUsed.Format(time.RFC3339))
				continue
			}
			// Exceeded 2× timeout — hard-reap.
			m.logger.Info("hard-reaping idle K8s pod", "key", key,
				"idle_since", entry.LastUsed.Format(time.RFC3339))
			if err := m.backend.Stop(ctx, containerName); err != nil {
				m.logger.Warn("failed to stop idle sandbox", "key", key, "error", err)
				continue
			}
			entry.Status = "stopped"
		} else {
			m.logger.Info("pausing idle sandbox", "key", key,
				"idle_since", entry.LastUsed.Format(time.RFC3339))

			// Try pause first (instant resume); fall back to stop
			if err := m.backend.Pause(ctx, containerName); err != nil {
				// Pause not supported — fall back to stop
				if err := m.backend.Stop(ctx, containerName); err != nil {
					m.logger.Warn("failed to stop idle sandbox", "key", key, "error", err)
					continue
				}
				entry.Status = "stopped"
			} else {
				entry.Status = "paused"
			}
		}

		if m.metrics != nil {
			m.metrics.idleReaps.WithLabelValues(projectName).Inc()
		}
		if err := m.store.Set(key, entry); err != nil {
			m.logger.Warn("failed to update state", "key", key, "error", err)
		}
	}
}

// reconcileState checks actual pod/container status on startup and corrects
// stale entries (e.g., pods evicted during daemon downtime, node reboots).
func (m *manager) reconcileState(ctx context.Context) {
	entries := m.store.List()
	for key, entry := range entries {
		if entry.Status != "running" && entry.Status != "paused" {
			continue
		}
		projectName, agentID := parseStoreKey(key)
		containerName := m.containerName(projectName, agentID)
		status, err := m.backend.Status(ctx, containerName)
		if err != nil {
			m.logger.Warn("reconcile: failed to check status", "key", key, "error", err)
			entry.Status = "stopped"
			_ = m.store.Set(key, entry)
			continue
		}
		if !status.Running {
			m.logger.Info("reconcile: marking stale entry as stopped",
				"key", key, "actual_status", status.Status)
			entry.Status = "stopped"
			_ = m.store.Set(key, entry)
		}
	}
}

// shutdownAll stops all managed containers gracefully.
// It cancels running async execs and waits for goroutines to finish
// before stopping containers.
func (m *manager) shutdownAll(ctx context.Context) {
	// Cancel all running async execs so goroutines exit promptly.
	if m.asyncExecs != nil {
		m.asyncExecs.mu.RLock()
		for _, ae := range m.asyncExecs.execs {
			if ae.Status == "running" && ae.cancel != nil {
				ae.cancel()
			}
		}
		m.asyncExecs.mu.RUnlock()
	}

	// Wait for all async goroutines to complete.
	m.asyncWg.Wait()

	entries := m.store.List()
	for key, entry := range entries {
		if entry.Status == "running" {
			projectName, agentID := parseStoreKey(key)
			containerName := m.containerName(projectName, agentID)
			m.logger.Info("shutting down sandbox", "key", key)
			if err := m.backend.Stop(ctx, containerName); err != nil {
				m.logger.Warn("failed to stop sandbox on shutdown", "key", key, "error", err)
			}
			entry.Status = "stopped"
			_ = m.store.Set(key, entry)
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
