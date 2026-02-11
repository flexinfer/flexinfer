package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
}

type manager struct {
	cfg     managerConfig
	backend backend.Backend
	store   *state.Store
	logger  *slog.Logger
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
	default:
		return nil, fmt.Errorf("unsupported backend: %s", cfg.backendType)
	}

	if err := b.Health(ctx); err != nil {
		logger.Warn("backend health check failed", "error", err)
	}

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
	return fmt.Sprintf("%s/%s:%s", m.cfg.imagePrefix, projectName, hash[:7])
}

// containerName returns the Docker container name for a project.
func (m *manager) containerName(projectName string) string {
	return "devbox-" + projectName
}

// ensureRunning ensures a sandbox is built and running for a project.
// Returns the container ID.
func (m *manager) ensureRunning(ctx context.Context, projectDir, projectName string) (string, error) {
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
			mounts = append(mounts, backend.Mount{
				Host:      extra.Host,
				Container: extra.Container,
				ReadOnly:  extra.ReadOnly,
			})
		}
	}

	m.logger.Info("starting sandbox", "project", projectName, "image", tag)
	result, err := m.backend.Start(ctx, backend.StartOpts{
		Name:     containerID,
		ImageTag: tag,
		Mounts:   mounts,
		Env:      fp.EnvVars,
		MemoryMB: memMB,
		CPUs:     cpu,
		Network:  network,
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

// buildMounts creates the standard bind mounts for a sandbox.
func (m *manager) buildMounts(projectDir string) []backend.Mount {
	home, _ := os.UserHomeDir()
	mounts := []backend.Mount{
		{Host: projectDir, Container: "/workspace"},
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

// reapIdle stops containers that have been idle beyond the timeout.
func (m *manager) reapIdle(ctx context.Context) {
	idle := m.store.IdleEntries(m.cfg.idleTimeout)
	for name, entry := range idle {
		m.logger.Info("reaping idle sandbox", "project", name,
			"idle_since", entry.LastUsed.Format(time.RFC3339))

		containerName := m.containerName(name)
		if err := m.backend.Stop(ctx, containerName); err != nil {
			m.logger.Warn("failed to stop idle sandbox", "project", name, "error", err)
			continue
		}

		entry.Status = "stopped"
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
