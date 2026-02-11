package main

import (
	"context"
	"fmt"
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

	containerID, err := m.ensureRunning(ctx, projectDir, projectName)
	if err != nil {
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

	m.logger.Info("exec", "project", projectName, "command", command)

	result, err := m.backend.Exec(ctx, backend.ExecOpts{
		ContainerID: containerID,
		Command:     command,
		Env:         envVars,
		TimeoutSec:  int(timeout.Seconds()),
		MaxLines:    maxLines,
	})
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("exec failed: %w", err)), nil
	}

	_ = m.store.TouchLastUsed(projectName)

	return mcp.JSONResult(result)
}

func (m *manager) handleBuild(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	force := v.Bool("force", false)
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
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("build failed: %w", err)), nil
	}
	buildDuration := time.Since(start).Milliseconds()

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

	return mcp.JSONResult(map[string]any{
		"status":            "built",
		"image":             buildResult.ImageTag,
		"languages":         langNames(fp),
		"cached":            buildResult.Cached,
		"build_duration_ms": buildDuration,
		"hash":              fp.Hash[:7],
	})
}

func (m *manager) handleStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.String("project", "")

	entries := m.store.List()
	sandboxes := make([]map[string]any, 0)

	for name, entry := range entries {
		if project != "" && name != project {
			continue
		}

		info := map[string]any{
			"project":   name,
			"status":    entry.Status,
			"image":     entry.ImageTag,
			"backend":   entry.Backend,
			"last_used": entry.LastUsed.Format(time.RFC3339),
		}

		if entry.Status == "running" {
			status, err := m.backend.Status(ctx, m.containerName(name))
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
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	_, projectName, err := m.resolveProject(project)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	containerID := m.containerName(projectName)
	if err := m.backend.Stop(ctx, containerID); err != nil {
		return mcp.ErrorResult(fmt.Errorf("stop failed: %w", err)), nil
	}

	entry := m.store.Get(projectName)
	if entry != nil {
		entry.Status = "stopped"
		_ = m.store.Set(projectName, entry)
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
