// mcp-devbox is an MCP server providing persistent, project-aware container
// sandboxes for AI coding agents. Each project gets an auto-built container
// with the right runtimes and dependencies.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
)

var version = "0.2.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-devbox", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-devbox")

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

	backendType := env.String("DEVBOX_BACKEND", "docker")

	// K8s backend defaults to 2h idle timeout (sleeping pods are nearly free).
	defaultIdleTimeout := 30 * 60 * time.Second // 30m for Docker
	if backendType == "k8s" || backendType == "kubernetes" {
		defaultIdleTimeout = 2 * time.Hour
	}

	mgr, err := newManager(ctx, logger, managerConfig{
		workspaceRoot:      workspaceRoot,
		cacheDir:           cacheDir,
		backendType:        backendType,
		registry:           env.String("DEVBOX_REGISTRY", "registry.harbor.lan"),
		imagePrefix:        env.String("DEVBOX_IMAGE_PREFIX", "mcp/devbox"),
		maxTailLines:       env.Int("DEVBOX_MAX_TAIL_LINES", 20),
		idleTimeout:        env.Duration("DEVBOX_IDLE_TIMEOUT", defaultIdleTimeout),
		defaultCPU:         env.Float("DEVBOX_DEFAULT_CPU", 0.5),
		defaultMemMB:       env.Int("DEVBOX_DEFAULT_MEMORY_MB", 512),
		kubeconfig:         env.String("DEVBOX_KUBECONFIG", ""),
		k8sNamespace:       env.String("DEVBOX_K8S_NAMESPACE", "devbox"),
		storageClass:       env.String("DEVBOX_K8S_STORAGE_CLASS", "longhorn"),
		k8sWorkspacePVC:    env.String("DEVBOX_K8S_WORKSPACE_PVC", "devbox-workspace-nfs"),
		k8sImagePullSecret: env.String("DEVBOX_K8S_IMAGE_PULL_SECRET", "harbor-creds"),
		builderImage:       builderImage(),
		buildCachePVC:      env.String("DEVBOX_K8S_BUILD_CACHE_PVC", ""),
	})
	if err != nil {
		return fmt.Errorf("init manager: %w", err)
	}

	// Initialize metrics
	mgr.metrics = newMetrics()

	// Initialize event emitter (optional — enabled via DEVBOX_HUD_ADDR)
	mgr.events = newEventEmitter(env.String("DEVBOX_HUD_ADDR", ""), logger)

	// Initialize async exec registry
	mgr.asyncExecs = newAsyncRegistry()
	go mgr.asyncExecs.cleanupLoop(ctx)

	server := mcp.NewServer("mcp-devbox", version)
	server.SetInstructions("Project-aware dev sandbox executor. Builds isolated containers with auto-detected runtimes and deps. " +
		"Tools: devbox_exec, devbox_build, devbox_status, devbox_stop, devbox_detect, " +
		"devbox_read_file, devbox_write_file, devbox_exec_async, devbox_exec_poll, " +
		"devbox_metrics, devbox_summary")

	registerTools(server, mgr, tracer)

	// Reconcile stale state entries (pods evicted, node reboots)
	mgr.reconcileState(ctx)

	// Start idle reaper
	go mgr.reapLoop(ctx)

	// Cleanup on shutdown
	defer mgr.shutdownAll(context.Background())

	return server.Run(ctx)
}

// builderImage returns the builder image, checking DEVBOX_BUILDER_IMAGE first,
// then falling back to the deprecated DEVBOX_KANIKO_IMAGE for backward compatibility.
func builderImage() string {
	if img := env.String("DEVBOX_BUILDER_IMAGE", ""); img != "" {
		return img
	}
	return env.String("DEVBOX_KANIKO_IMAGE", "")
}
