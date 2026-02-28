package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/devbox/backend"
	"github.com/crb2nu/loom/internal/devbox/detect"
	"github.com/crb2nu/loom/internal/devbox/dockerfile"
	"github.com/crb2nu/loom/internal/devbox/state"
	"github.com/crb2nu/loom/pkg/validate"
)

func (m *manager) handleExec(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	command := v.Required("command")
	timeoutStr := v.String("timeout", "2m")
	maxLines := v.Int("max_lines", m.cfg.maxTailLines)
	agentID := v.String("agent_id", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("invalid timeout: %w", err)), nil
	}

	projectDir, projectName, err := m.resolveProject(project)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Per-project+agent lock prevents concurrent ensureRunning TOCTOU races
	key := storeKey(projectName, agentID)
	mu := m.projectLock(key)
	mu.Lock()
	containerID, err := m.ensureRunning(ctx, projectDir, projectName, agentID)
	mu.Unlock()
	if err != nil {
		if m.metrics != nil {
			m.metrics.errors.WithLabelValues("ensure_running").Inc()
		}
		return mcp.ErrorResult(fmt.Errorf("ensure sandbox: %w", err)), nil
	}

	// Parse extra env vars
	envVars := make(map[string]string)
	if envRaw, ok := args["env"].(map[string]any); ok {
		for k, val := range envRaw {
			if s, ok := val.(string); ok {
				envVars[k] = s
			}
		}
	}

	// Re-sync workspace before exec (tar-pipe mode) so uncommitted changes propagate.
	if err := m.syncIfNeeded(ctx, containerID, projectDir); err != nil {
		m.logger.Warn("pre-exec sync failed", "project", projectName, "error", err)
		// Non-fatal: exec may still work with stale files.
	}

	// Touch last used BEFORE exec so reaper doesn't kill during long-running commands
	_ = m.store.TouchLastUsed(key)
	m.incActiveExecs(key)
	defer m.decActiveExecs(key)

	m.totalExecs.Add(1)
	m.logger.Info("exec", "project", projectName, "agent", agentID, "command", command)

	start := time.Now()
	result, err := m.backend.Exec(ctx, backend.ExecOpts{
		ContainerID: containerID,
		Command:     command,
		WorkDir:     m.projectWorkDir(projectDir),
		Env:         envVars,
		TimeoutSec:  int(timeout.Seconds()),
		MaxLines:    maxLines,
	})
	execDuration := time.Since(start).Seconds()
	if err != nil {
		if m.metrics != nil {
			m.metrics.errors.WithLabelValues("exec").Inc()
		}
		return mcp.ErrorResult(fmt.Errorf("exec failed: %w", err)), nil
	}

	// Update last used after exec too
	_ = m.store.TouchLastUsed(key)

	// Record metrics
	if m.metrics != nil {
		exitStr := fmt.Sprintf("%d", result.ExitCode)
		m.metrics.execDuration.WithLabelValues(projectName, exitStr).Observe(execDuration)
		m.metrics.execTotal.WithLabelValues(projectName, exitStr).Inc()
	}

	// Emit event for HUD visibility
	if m.events != nil {
		m.events.Emit(ctx, "exec", projectName,
			fmt.Sprintf("exit=%d duration=%dms cmd=%s", result.ExitCode, result.DurationMs, command))
	}

	return mcp.JSONResult(result)
}

func (m *manager) handleBuild(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	force := v.Bool("force", false)
	_ = v.String("agent_id", "") // accepted but not used for build-only operations
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	projectDir, projectName, err := m.resolveProject(project)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	fp, err := detect.Fingerprint(projectDir)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("fingerprint: %w", err)), nil
	}

	tag := m.imageTag(projectName, fp.Hash)

	// Check if already built with same hash
	entry := m.store.Get(projectName)
	if !force && entry != nil && entry.FingerprintHash == fp.Hash {
		return mcp.JSONResult(map[string]any{
			"status":    "cached",
			"image":     tag,
			"languages": langNames(fp),
			"cached":    true,
			"hash":      fp.Hash[:7],
		})
	}

	m.totalBuilds.Add(1)
	m.logger.Info("building sandbox image", "project", projectName, "hash", fp.Hash[:7])

	dockerfileContent, err := dockerfile.Generate(fp)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("generate dockerfile: %w", err)), nil
	}

	start := time.Now()
	buildResult, err := m.backend.Build(ctx, backend.BuildOpts{
		Tag:        tag,
		Dockerfile: dockerfileContent,
		ContextDir: projectDir,
	})
	buildDuration := time.Since(start)
	if err != nil {
		if m.metrics != nil {
			m.metrics.builds.WithLabelValues(projectName, "failed").Inc()
			m.metrics.errors.WithLabelValues("build").Inc()
		}
		return mcp.ErrorResult(fmt.Errorf("build failed: %w", err)), nil
	}

	if m.metrics != nil {
		status := "built"
		if buildResult.Cached {
			status = "cached"
		}
		m.metrics.builds.WithLabelValues(projectName, status).Inc()
		m.metrics.buildDuration.WithLabelValues(projectName).Observe(buildDuration.Seconds())
	}

	now := time.Now()
	if err := m.store.Set(projectName, &state.Entry{
		ProjectDir:      projectDir,
		ImageTag:        tag,
		FingerprintHash: fp.Hash,
		Backend:         m.cfg.backendType,
		Status:          "ready",
		LastUsed:        now,
		CreatedAt:       now,
	}); err != nil {
		m.logger.Warn("failed to persist state", "error", err)
	}

	if m.events != nil {
		m.events.Emit(ctx, "build", projectName,
			fmt.Sprintf("image=%s cached=%v duration=%dms", buildResult.ImageTag, buildResult.Cached, buildDuration.Milliseconds()))
	}

	return mcp.JSONResult(map[string]any{
		"status":            "built",
		"image":             buildResult.ImageTag,
		"languages":         langNames(fp),
		"cached":            buildResult.Cached,
		"build_duration_ms": buildDuration.Milliseconds(),
		"hash":              fp.Hash[:7],
	})
}

func (m *manager) handleStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.String("project", "")

	entries := m.store.List()
	sandboxes := make([]map[string]any, 0)

	for key, entry := range entries {
		projectName, agentID := parseStoreKey(key)
		if project != "" && projectName != project {
			continue
		}

		info := map[string]any{
			"project":   projectName,
			"status":    entry.Status,
			"image":     entry.ImageTag,
			"backend":   entry.Backend,
			"last_used": entry.LastUsed.Format(time.RFC3339),
		}
		if agentID != "" {
			info["agent_id"] = agentID
		}

		if entry.Status == "running" {
			containerName := m.containerName(projectName, agentID)
			status, err := m.backend.Status(ctx, containerName)
			if err == nil {
				info["running"] = status.Running
				if status.Running {
					info["uptime"] = time.Since(entry.CreatedAt).Truncate(time.Second).String()
				}
			}
		}

		if entry.Error != "" {
			info["error"] = entry.Error
		}

		sandboxes = append(sandboxes, info)
	}

	return mcp.JSONResult(map[string]any{"sandboxes": sandboxes})
}

func (m *manager) handleStop(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	agentID := v.String("agent_id", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	_, projectName, err := m.resolveProject(project)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	key := storeKey(projectName, agentID)
	containerID := m.containerName(projectName, agentID)
	if err := m.backend.Stop(ctx, containerID); err != nil {
		return mcp.ErrorResult(fmt.Errorf("stop failed: %w", err)), nil
	}

	entry := m.store.Get(key)
	if entry != nil {
		entry.Status = "stopped"
		_ = m.store.Set(key, entry)
	}

	return mcp.JSONResult(map[string]any{"stopped": true, "project": projectName})
}

func (m *manager) handleDetect(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	projectDir, _, err := m.resolveProject(project)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	fp, err := detect.Fingerprint(projectDir)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("fingerprint: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"fingerprint": fp,
	})
}

func (m *manager) handleReadFile(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	path := v.Required("path")
	maxLines := v.Int("max_lines", 200)
	offset := v.Int("offset", 0)
	agentID := v.String("agent_id", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	projectDir, projectName, err := m.resolveProject(project)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	key := storeKey(projectName, agentID)
	mu := m.projectLock(key)
	mu.Lock()
	containerID, err := m.ensureRunning(ctx, projectDir, projectName, agentID)
	mu.Unlock()
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure sandbox: %w", err)), nil
	}

	_ = m.store.TouchLastUsed(key)

	// Re-sync before reading (tar-pipe mode) so we read the latest local files.
	if err := m.syncIfNeeded(ctx, containerID, projectDir); err != nil {
		m.logger.Warn("pre-read sync failed", "project", projectName, "error", err)
	}

	// Resolve path relative to project workdir
	filePath := path
	if !filepath.IsAbs(path) {
		filePath = filepath.Join(m.projectWorkDir(projectDir), path)
	}

	content, err := m.backend.ReadFile(ctx, containerID, filePath)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("read file: %w", err)), nil
	}

	// Apply line offset and limit
	lines := strings.Split(string(content), "\n")
	totalLines := len(lines)
	if offset > 0 && offset < totalLines {
		lines = lines[offset:]
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
	}

	return mcp.JSONResult(map[string]any{
		"path":        path,
		"content":     strings.Join(lines, "\n"),
		"total_lines": totalLines,
		"truncated":   totalLines > (offset + maxLines),
	})
}

func (m *manager) handleWriteFile(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	path := v.Required("path")
	content := v.Required("content")
	mode := v.String("mode", "0644")
	agentID := v.String("agent_id", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	projectDir, projectName, err := m.resolveProject(project)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	key := storeKey(projectName, agentID)
	mu := m.projectLock(key)
	mu.Lock()
	containerID, err := m.ensureRunning(ctx, projectDir, projectName, agentID)
	mu.Unlock()
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure sandbox: %w", err)), nil
	}

	_ = m.store.TouchLastUsed(key)

	filePath := path
	if !filepath.IsAbs(path) {
		filePath = filepath.Join(m.projectWorkDir(projectDir), path)
	}

	if err := m.backend.WriteFile(ctx, containerID, filePath, []byte(content), mode); err != nil {
		return mcp.ErrorResult(fmt.Errorf("write file: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"written": true,
		"path":    path,
		"bytes":   len(content),
	})
}
