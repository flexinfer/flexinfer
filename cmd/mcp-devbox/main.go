// mcp-devbox is an MCP server providing persistent, project-aware container
// sandboxes for AI coding agents. Each project gets an auto-built container
// with the right runtimes and dependencies.
package main

import (
	"context"
	"fmt"
	"os"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
)

var version = "0.1.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	logger.Info("starting server", "name", "mcp-devbox", "version", version)

	workspaceRoot := env.String("DEVBOX_WORKSPACE_ROOT", "")
	if workspaceRoot == "" {
		home, _ := os.UserHomeDir()
		workspaceRoot = home + "/workspace"
	}

	cacheDir := env.String("DEVBOX_CACHE_DIR", "")
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = home + "/.cache/loom/devbox"
	}

	mgr, err := newManager(ctx, logger, managerConfig{
		workspaceRoot: workspaceRoot,
		cacheDir:      cacheDir,
		backendType:   env.String("DEVBOX_BACKEND", "docker"),
		registry:      env.String("DEVBOX_REGISTRY", "registry.harbor.lan"),
		imagePrefix:   env.String("DEVBOX_IMAGE_PREFIX", "mcp/devbox"),
		maxTailLines:  env.Int("DEVBOX_MAX_TAIL_LINES", 20),
		idleTimeout:   env.Duration("DEVBOX_IDLE_TIMEOUT", 30*60*1e9), // 30m
		defaultCPU:    2.0,
		defaultMemMB:  env.Int("DEVBOX_DEFAULT_MEMORY_MB", 1024),
	})
	if err != nil {
		return fmt.Errorf("init manager: %w", err)
	}

	server := mcp.NewServer("mcp-devbox", version)
	server.SetInstructions("Project-aware dev sandbox executor. Builds isolated containers with auto-detected runtimes and deps. Tools: devbox_exec, devbox_build, devbox_status, devbox_stop, devbox_detect")

	registerTools(server, mgr)

	// Start idle reaper
	go mgr.reapLoop(ctx)

	// Cleanup on shutdown
	defer mgr.shutdownAll(context.Background())

	return server.Run(ctx)
}
