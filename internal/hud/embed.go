package hud

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	loomcache "github.com/crb2nu/loom/internal/cache"
	"github.com/crb2nu/loom/internal/devbox/backend"
	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/coordinator"
	"github.com/crb2nu/loom/internal/hud/domain/memory"
	"github.com/crb2nu/loom/internal/hud/monitor"
	"github.com/crb2nu/loom/pkg/mcpotel"
)

// NewApp creates a HUD App with the given caller and configuration.
// The caller can be a bridge.DaemonClient (standalone mode) or
// bridge.LocalCaller (embedded in daemon). Call StartMonitors to begin
// background polling, and RegisterRoutes to mount HTTP handlers.
func NewApp(cfg Config, caller bridge.Caller, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default().With("component", "hud")
	}

	agent := bridge.NewAgentBridge(caller)

	cacheCfg := loomcache.LoadConfigFromEnv()
	appCache := loomcache.New(cacheCfg, logger)

	app := &App{
		config:               cfg,
		client:               caller,
		agent:                agent,
		cache:                appCache,
		cacheBackend:         cacheCfg.Backend,
		logger:               logger,
		nudgeQueue:           NewNudgeQueue(),
		mobileRevocationList: NewMobileTokenRevocationList(),
		deviceTokenStore:     NewDeviceTokenStore(),
		mobileRateLimiter: NewMobileRateLimiter(MobileRateLimitConfig{
			MutationPerMinute: cfg.MobileRateLimitMutation,
			ReadPerMinute:     cfg.MobileRateLimitRead,
		}),
	}

	// Initialize OTel tracer.
	tp, otelShutdown, err := mcpotel.InitTracer(context.Background(), "loom-hud", logger)
	if err != nil {
		logger.Warn("otel tracer init failed, continuing without tracing", "error", err)
	} else if otelShutdown != nil {
		// Store shutdown for StopMonitors to call.
		app.otelShutdown = otelShutdown
	}
	app.tracer = mcpotel.Tracer(tp, "loom-hud")
	app.metrics = NewHUDMetrics()

	return app, nil
}

// StartMonitors initializes and starts all background monitors and optional
// components (coordinator, spawn orchestrator, SSE hub, etc.).
func (a *App) StartMonitors(ctx context.Context) error {
	// Background monitors.
	a.fleetMonitor = monitor.NewFleetMonitor(a.client, a.agent, a.logger)
	a.fleetMonitor.Start(15 * time.Second)

	a.healthMonitor = monitor.NewHealthMonitor(a.client, a.logger)
	a.healthMonitor.Start(5 * time.Second)

	a.memoryMonitor = monitor.NewMemoryMonitor(a.agent, a.logger)
	a.memoryMonitor.Start(10 * time.Second)

	a.workflowMonitor = monitor.NewWorkflowMonitor(a.agent, a.logger)
	a.workflowMonitor.Start(5 * time.Second)

	a.streamMonitor = monitor.NewStreamMonitor(a.agent, a.logger)
	a.streamMonitor.Start(5 * time.Second)

	a.sandboxMonitor = monitor.NewSandboxMonitor(a.client, a.logger)
	a.sandboxMonitor.Start(10 * time.Second)

	a.costMonitor = monitor.NewCostMonitor(a.client, a.logger)
	a.costMonitor.Start(10 * time.Second)

	if a.config.PipelineProjects != "" {
		projects := strings.Split(a.config.PipelineProjects, ",")
		a.pipelineMonitor = monitor.NewPipelineMonitor(a.agent, projects, a.logger)
		a.pipelineMonitor.Start(10 * time.Second)
	}

	a.logger.Info("background monitors started",
		"fleet", "15s", "health", "5s", "memory", "10s",
		"workflow", "5s", "stream", "5s", "sandbox", "10s", "cost", "10s")

	// Bootstrap workflow definitions.
	a.bootstrapWorkflowDefinitions()

	// SSE hub.
	a.sseHub = NewSSEHub(a.logger)

	// Timeline event log.
	a.eventLog = NewEventLog(1000)

	// Spawn orchestrator.
	if a.config.SpawnEnabled {
		if err := a.initSpawnOrchestrator(ctx); err != nil {
			a.logger.Error("spawn backend init failed", "error", err)
		}
	}

	// Session reaper.
	go a.sessionReaper(ctx)

	// Push token reaper.
	if a.config.MobilePushEnabled {
		go a.pushTokenReaper(ctx)
	}

	// APNs push bridge.
	if a.config.MobilePushEnabled && a.config.APNsKeyPath != "" {
		a.initPushBridge()
	}

	// Wire monitor → SSE broadcast callbacks.
	a.wireMonitorCallbacks()

	// Coordinator.
	if a.config.FlexInferURL != "" {
		a.initCoordinator()
	}

	// Domain registry.
	a.initDomainRegistry()

	return nil
}

// StopMonitors stops all background monitors and releases resources.
func (a *App) StopMonitors() {
	if a.fleetMonitor != nil {
		a.fleetMonitor.Stop()
	}
	if a.healthMonitor != nil {
		a.healthMonitor.Stop()
	}
	if a.memoryMonitor != nil {
		a.memoryMonitor.Stop()
	}
	if a.workflowMonitor != nil {
		a.workflowMonitor.Stop()
	}
	if a.streamMonitor != nil {
		a.streamMonitor.Stop()
	}
	if a.sandboxMonitor != nil {
		a.sandboxMonitor.Stop()
	}
	if a.costMonitor != nil {
		a.costMonitor.Stop()
	}
	if a.pipelineMonitor != nil {
		a.pipelineMonitor.Stop()
	}
	if a.coordinator != nil {
		a.coordinator.Stop()
	}
	if a.cache != nil {
		a.cache.Close()
	}
	if a.otelShutdown != nil {
		a.otelShutdown(context.Background())
	}
}

// RegisterRoutes mounts all HUD HTTP routes on the given ServeMux.
// This is the same set of routes used in standalone mode.
func (a *App) RegisterRoutes(mux *http.ServeMux) {
	a.registerRoutes(mux)
}

// initSpawnOrchestrator sets up the spawn backend and orchestrator.
func (a *App) initSpawnOrchestrator(ctx context.Context) error {
	cfg := a.config
	spawnBackend, err := backend.NewK8sBackend(backend.K8sBackendConfig{
		Kubeconfig: cfg.SpawnKubeconfig,
		Namespace:  cfg.SpawnNamespace,
		Registry:   cfg.SpawnRegistry,
		SyncMode:   cfg.SpawnSyncMode,
		GitBaseURL: cfg.SpawnGitBaseURL,
		GitSecret:  cfg.SpawnGitSecret,
	})
	if err != nil {
		return err
	}

	spawnCfg := DefaultSpawnConfig()
	if cfg.SpawnProjects != "" {
		spawnCfg.Projects = strings.Split(cfg.SpawnProjects, ",")
	}
	a.spawner = NewSpawnOrchestrator(
		spawnBackend, a.agent, a.sseHub, a.tracer, a.metrics, a.logger,
		spawnCfg,
	)
	a.fleetMonitor.SetSpawnLister(spawnAdapter{a.spawner})

	ctrl := a.spawner.Controller()
	ctrl.SetK8sClient(spawnBackend.Clientset(), spawnBackend.Namespace())
	ctrl.StartReconcileLoop(ctx, 30*time.Second)

	a.logger.Info("spawn orchestrator enabled",
		"namespace", cfg.SpawnNamespace, "registry", cfg.SpawnRegistry,
		"sync_mode", cfg.SpawnSyncMode, "projects", len(spawnCfg.Projects))
	return nil
}

// initPushBridge sets up the APNs push notification bridge.
func (a *App) initPushBridge() {
	cfg := a.config
	apnsSender := NewAPNsSender(APNsSenderConfig{
		KeyPath: cfg.APNsKeyPath,
		KeyID:   cfg.APNsKeyID,
		TeamID:  cfg.APNsTeamID,
		Topic:   cfg.APNsTopic,
		Sandbox: cfg.APNsSandbox,
	}, a.tracer, a.metrics, a.logger)

	a.pushBridge = NewPushEventBridge(
		apnsSender, a.deviceTokenStore, a.tracer, a.metrics, a.logger,
	)
	a.logger.Info("APNs push bridge enabled", "topic", cfg.APNsTopic, "sandbox", cfg.APNsSandbox)
}

// wireMonitorCallbacks registers OnRefresh callbacks that broadcast monitor
// snapshots to browser clients via the SSE hub.
func (a *App) wireMonitorCallbacks() {
	// Optional webhook pusher.
	var fleetWebhook *FleetWebhook
	if a.config.WebhookURL != "" {
		fleetWebhook = NewFleetWebhook(a.config.WebhookURL, a.config.WebhookToken, a.config.WebhookResolve, a.logger)
		a.logger.Info("fleet webhook enabled", "url", a.config.WebhookURL)
	}

	a.fleetMonitor.OnRefresh(func(snap monitor.FleetSnapshot) {
		data, err := json.Marshal(snap)
		if err == nil {
			a.sseHub.Broadcast(bridge.SSEEvent{
				ID:        fmt.Sprintf("hud-fleet-%d", time.Now().UnixMilli()),
				Type:      "hud.fleet",
				Timestamp: time.Now(),
				Data:      data,
			})
		}
		if fleetWebhook != nil {
			go fleetWebhook.Push(snap)
		}
	})
	a.healthMonitor.OnRefresh(func(servers []monitor.ServerHealthEntry) {
		data, err := json.Marshal(map[string]any{"servers": servers})
		if err != nil {
			return
		}
		a.sseHub.Broadcast(bridge.SSEEvent{
			ID:        fmt.Sprintf("hud-health-%d", time.Now().UnixMilli()),
			Type:      "hud.health",
			Timestamp: time.Now(),
			Data:      data,
		})
	})
	a.memoryMonitor.OnRefresh(func(stats *bridge.MemoryStatsResult) {
		data, err := json.Marshal(memory.StatsPayload(stats))
		if err != nil {
			return
		}
		a.sseHub.Broadcast(bridge.SSEEvent{
			ID:        fmt.Sprintf("hud-memory-%d", time.Now().UnixMilli()),
			Type:      "hud.memory",
			Timestamp: time.Now(),
			Data:      data,
		})
	})
	a.workflowMonitor.OnRefresh(func(workflows []bridge.WorkflowInfo) {
		data, err := json.Marshal(map[string]any{"workflows": workflows})
		if err != nil {
			return
		}
		a.sseHub.Broadcast(bridge.SSEEvent{
			ID:        fmt.Sprintf("hud-workflows-%d", time.Now().UnixMilli()),
			Type:      "hud.workflows",
			Timestamp: time.Now(),
			Data:      data,
		})
	})
	a.streamMonitor.OnRefresh(func(entries []monitor.StreamEntry) {
		data, err := json.Marshal(map[string]any{"entries": entries})
		if err != nil {
			return
		}
		a.sseHub.Broadcast(bridge.SSEEvent{
			ID:        fmt.Sprintf("hud-stream-%d", time.Now().UnixMilli()),
			Type:      "hud.stream",
			Timestamp: time.Now(),
			Data:      data,
		})
	})
	a.sandboxMonitor.OnRefresh(func(snap map[string]any) {
		snap["available"] = true
		data, err := json.Marshal(snap)
		if err != nil {
			return
		}
		a.sseHub.Broadcast(bridge.SSEEvent{
			ID:        fmt.Sprintf("hud-sandbox-%d", time.Now().UnixMilli()),
			Type:      "hud.sandbox",
			Timestamp: time.Now(),
			Data:      data,
		})
	})
	a.costMonitor.OnRefresh(func(snap monitor.CostSnapshot) {
		data, err := json.Marshal(snap)
		if err != nil {
			return
		}
		a.sseHub.Broadcast(bridge.SSEEvent{
			ID:        fmt.Sprintf("hud-cost-%d", time.Now().UnixMilli()),
			Type:      "hud.cost",
			Timestamp: time.Now(),
			Data:      data,
		})
	})
	if a.pipelineMonitor != nil {
		a.pipelineMonitor.OnRefresh(func(pipelines []bridge.PipelineInfo) {
			data, err := json.Marshal(map[string]any{"pipelines": pipelines})
			if err != nil {
				return
			}
			a.sseHub.Broadcast(bridge.SSEEvent{
				ID:        fmt.Sprintf("hud-pipeline-%d", time.Now().UnixMilli()),
				Type:      "hud.pipeline",
				Timestamp: time.Now(),
				Data:      data,
			})
		})
	}
}

// initCoordinator sets up the LLM-powered coordinator.
func (a *App) initCoordinator() {
	cfg := a.config
	coordCfg := coordinator.ConfigFromEnv()
	coordCfg.FlexInferURL = cfg.FlexInferURL
	if cfg.FlexInferKey != "" {
		coordCfg.FlexInferKey = cfg.FlexInferKey
	}
	if cfg.CoordinatorModel != "" {
		coordCfg.DefaultModel = cfg.CoordinatorModel
	}

	if err := coordCfg.Validate(); err != nil {
		a.logger.Error("coordinator config invalid", "error", err)
		return
	}

	c := coordinator.NewCoordinator(coordCfg, a.agent, a.sseHub, a.logger)
	if c == nil {
		return
	}
	m := coordinator.NewMetrics()
	c.SetMetrics(m)
	if err := c.Start(); err != nil {
		a.logger.Warn("coordinator: failed to start, continuing without it", "error", err)
		return
	}
	a.coordinator = c
	a.coordinatorMetrics = m
	a.logger.Info("coordinator started", "url", cfg.FlexInferURL, "model", coordCfg.DefaultModel)
}
