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
	"github.com/crb2nu/loom/internal/hud/alerting"
	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/coordinator"
	"github.com/crb2nu/loom/internal/hud/domain/memory"
	"github.com/crb2nu/loom/internal/hud/monitor"
	"github.com/crb2nu/loom/internal/hud/shuttle"
	"github.com/crb2nu/loom/pkg/codebase"
	"github.com/crb2nu/loom/pkg/mcpotel"
)

const (
	embeddedFleetSnapshotCacheKey    = "hud:embedded:fleet_snapshot"
	embeddedPipelineSnapshotCacheKey = "hud:embedded:pipeline_snapshot"
	embeddedSnapshotCacheTTL         = 10 * time.Minute
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
	app.agentContextMetrics = NewAgentContextMetrics()
	app.agentContextLatest = NewAgentContextLatestStore()

	return app, nil
}

// StartMonitors initializes and starts all background monitors and optional
// components (coordinator, spawn orchestrator, SSE hub, etc.).
func (a *App) StartMonitors(ctx context.Context) error {
	// Shared runtime wiring must exist before the first monitor refresh so the
	// embedded HUD can broadcast the initial snapshots instead of missing them.
	a.sseHub = NewSSEHub(a.logger)
	a.eventLog = NewEventLog(1000)

	// Background monitors.
	a.fleetMonitor = monitor.NewFleetMonitor(a.client, a.agent, a.logger)

	a.healthMonitor = monitor.NewHealthMonitor(a.client, a.logger)

	a.memoryMonitor = monitor.NewMemoryMonitor(a.agent, a.logger)

	a.workflowMonitor = monitor.NewWorkflowMonitor(a.agent, a.logger)

	a.streamMonitor = monitor.NewStreamMonitor(a.agent, a.logger)

	a.sandboxMonitor = monitor.NewSandboxMonitor(a.client, a.logger)

	a.costMonitor = monitor.NewCostMonitor(a.client, a.logger)

	pipelineProjects := a.config.PipelineProjects
	if pipelineProjects == "" {
		if detected := codebase.DetectPipelineProject(ctx, "."); detected != "" {
			pipelineProjects = detected
			a.logger.Info("auto-detected pipeline project", "project", detected)
		}
	}
	var projects []string
	if pipelineProjects != "" {
		for _, p := range strings.Split(pipelineProjects, ",") {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				projects = append(projects, trimmed)
			}
		}
	} else {
		// Fallback: ask the gitlab MCP server what projects the authenticated
		// user has access to. This makes "pipelines show up" work out of the
		// box in the cluster/hub deployment without requiring an exhaustive
		// hardcoded `HUD_PIPELINE_PROJECTS` env var to enumerate every repo.
		if discovered, err := a.agent.ListPipelineProjects(ctx, 5); err != nil {
			a.logger.Info(
				"pipeline project auto-discovery unavailable, skipping pipeline monitor",
				"reason", err.Error(),
			)
		} else if len(discovered) > 0 {
			projects = discovered
			a.logger.Info(
				"auto-discovered pipeline projects via gitlab MCP",
				"count", len(discovered),
			)
		}
	}
	if len(projects) > 0 {
		a.pipelineMonitor = monitor.NewPipelineMonitor(a.agent, projects, a.cache, a.logger)
	}

	a.contextHealthMonitor = monitor.NewContextHealthMonitor(a.agent, nil, a.logger)

	a.codebaseMonitor = monitor.NewCodebaseMonitor(a.agent, a.logger)

	// Shuttle engine + monitor.
	a.shuttleEngine = shuttle.NewEngine(a.logger)
	a.shuttleMonitor = shuttle.NewShuttleMonitor(a.shuttleEngine, a.agent, a.logger)

	// Alert engine + auto-fix.
	alertDispatcher := alerting.NewDispatcher(a.sseHub, nil, nil, nil, a.logger)
	a.alertEngine = alerting.NewAlertEngine(alertDispatcher, a.logger)

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

	// Start the polling loops only after callbacks are wired so the initial
	// refreshes are immediately visible to embedded/mobile clients.
	a.fleetMonitor.Start(15 * time.Second)
	a.healthMonitor.Start(5 * time.Second)
	a.memoryMonitor.Start(10 * time.Second)
	a.workflowMonitor.Start(5 * time.Second)
	a.streamMonitor.Start(5 * time.Second)
	a.sandboxMonitor.Start(10 * time.Second)
	a.costMonitor.Start(10 * time.Second)
	if a.pipelineMonitor != nil {
		a.pipelineMonitor.Start(10 * time.Second)
	}
	a.contextHealthMonitor.Start(5 * time.Second)
	a.codebaseMonitor.Start(30 * time.Second)
	a.shuttleMonitor.Start(3 * time.Second)

	// Wire pipeline monitor → alert engine callback.
	if a.pipelineMonitor != nil && a.alertEngine != nil {
		a.pipelineMonitor.OnRefresh(func(pipelines []bridge.PipelineInfo) {
			a.alertEngine.Evaluate(pipelines)
		})
	}

	a.logger.Info("background monitors started",
		"fleet", "15s", "health", "5s", "memory", "10s",
		"workflow", "5s", "stream", "5s", "sandbox", "10s", "cost", "10s",
		"context-health", "5s", "codebase", "30s", "shuttle", "3s")

	// Coordinator.
	if a.config.FlexInferURL != "" {
		a.initCoordinator()
	}

	// Domain registry.
	a.initDomainRegistry()

	return nil
}

// RefreshMonitors forces a best-effort refresh of the embedded HUD snapshots.
// Embedded daemon mode does not have the standalone HUD's SSE event consumer,
// so explicit refreshes are needed after startup and daemon reloads to avoid
// serving stale or empty cached state until the next polling tick.
func (a *App) RefreshMonitors() {
	if a.fleetMonitor != nil && a.fleetMonitor.Ready() {
		if err := a.fleetMonitor.RefreshForce(); err != nil {
			a.logger.Warn("embedded refresh: fleet refresh failed", "error", err)
		} else if fleetSnapshotLooksEmpty(a.fleetMonitor.Snapshot()) {
			// If the first refresh raced startup/reload, give the daemon a brief
			// moment to settle and try once more before we fall back to polling.
			time.Sleep(500 * time.Millisecond)
			if err := a.fleetMonitor.RefreshForce(); err != nil {
				a.logger.Warn("embedded refresh retry: fleet refresh failed", "error", err)
			}
		}
		if snap := a.fleetMonitor.Snapshot(); !fleetSnapshotLooksEmpty(snap) {
			a.storeCachedSnapshot(embeddedFleetSnapshotCacheKey, snap, embeddedSnapshotCacheTTL)
		} else if cached, ok := a.loadCachedSnapshot(embeddedFleetSnapshotCacheKey); ok {
			a.logger.Info("embedded refresh: restored cached fleet snapshot")
			a.fleetMonitor.Update(cached)
		}
	}
	if a.healthMonitor != nil {
		if err := a.healthMonitor.Refresh(); err != nil {
			a.logger.Warn("embedded refresh: health refresh failed", "error", err)
		}
	}
	if a.memoryMonitor != nil {
		if err := a.memoryMonitor.Refresh(); err != nil {
			a.logger.Warn("embedded refresh: memory refresh failed", "error", err)
		}
	}
	if a.workflowMonitor != nil {
		if err := a.workflowMonitor.Refresh(); err != nil {
			a.logger.Warn("embedded refresh: workflow refresh failed", "error", err)
		}
	}
	if a.streamMonitor != nil {
		if err := a.streamMonitor.Refresh(); err != nil {
			a.logger.Warn("embedded refresh: stream refresh failed", "error", err)
		}
	}
	if a.sandboxMonitor != nil {
		if err := a.sandboxMonitor.Refresh(); err != nil {
			a.logger.Warn("embedded refresh: sandbox refresh failed", "error", err)
		}
	}
	if a.costMonitor != nil {
		if err := a.costMonitor.Refresh(); err != nil {
			a.logger.Warn("embedded refresh: cost refresh failed", "error", err)
		}
	}
	if a.pipelineMonitor != nil && a.pipelineMonitor.Ready() {
		if refreshPipelineMonitor(a.pipelineMonitor, a.logger) {
			pipelines := a.pipelineMonitor.Pipelines()
			a.storeCachedSnapshot(embeddedPipelineSnapshotCacheKey, pipelines, embeddedSnapshotCacheTTL)
		} else if cached, ok := a.loadCachedPipelineSnapshot(); ok {
			a.logger.Info("embedded refresh: restored cached pipeline snapshot")
			a.pipelineMonitor.Update(cached)
		}
	}
	if a.contextHealthMonitor != nil {
		if err := a.contextHealthMonitor.Refresh(); err != nil {
			a.logger.Warn("embedded refresh: context-health refresh failed", "error", err)
		}
	}
	if a.codebaseMonitor != nil {
		if err := a.codebaseMonitor.Refresh(); err != nil {
			a.logger.Warn("embedded refresh: codebase refresh failed", "error", err)
		}
	}
	if a.shuttleMonitor != nil {
		if err := a.shuttleMonitor.Refresh(); err != nil {
			a.logger.Warn("embedded refresh: shuttle refresh failed", "error", err)
		}
	}
}

func fleetSnapshotLooksEmpty(s monitor.FleetSnapshot) bool {
	return len(s.Agents) == 0 &&
		len(s.Tasks) == 0 &&
		len(s.Sessions) == 0 &&
		len(s.FileClaims) == 0 &&
		len(s.Worktrees) == 0 &&
		len(s.Spawns) == 0 &&
		s.ActiveSessions == 0 &&
		s.TotalSessions == 0 &&
		s.TotalTasks == 0
}

type pipelineMonitorRefresher interface {
	Ready() bool
	Refresh() error
	Pipelines() []bridge.PipelineInfo
	Projects() []string
}

func refreshPipelineMonitor(mon pipelineMonitorRefresher, logger *slog.Logger) bool {
	if mon == nil || !mon.Ready() {
		return false
	}
	if err := mon.Refresh(); err != nil {
		logger.Warn("embedded refresh: pipeline refresh failed", "error", err)
		return false
	}
	if len(mon.Pipelines()) == 0 && len(mon.Projects()) > 0 {
		time.Sleep(500 * time.Millisecond)
		if err := mon.Refresh(); err != nil {
			logger.Warn("embedded refresh retry: pipeline refresh failed", "error", err)
		}
	}
	return len(mon.Pipelines()) > 0
}

func (a *App) loadCachedSnapshot(key string) (monitor.FleetSnapshot, bool) {
	var snap monitor.FleetSnapshot
	if a.cache == nil {
		return snap, false
	}
	cached, ok := a.cache.Get(key)
	if !ok || cached == nil {
		return snap, false
	}
	raw, err := json.Marshal(cached)
	if err != nil {
		return snap, false
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		return snap, false
	}
	return snap, true
}

func (a *App) loadCachedPipelineSnapshot() ([]bridge.PipelineInfo, bool) {
	if a.cache == nil {
		return nil, false
	}
	cached, ok := a.cache.Get(embeddedPipelineSnapshotCacheKey)
	if !ok || cached == nil {
		return nil, false
	}
	raw, err := json.Marshal(cached)
	if err != nil {
		return nil, false
	}
	var pipelines []bridge.PipelineInfo
	if err := json.Unmarshal(raw, &pipelines); err != nil {
		return nil, false
	}
	return pipelines, true
}

func (a *App) storeCachedSnapshot(key string, value any, ttl time.Duration) {
	if a.cache == nil {
		return
	}
	a.cache.Set(key, value, ttl)
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
	if a.contextHealthMonitor != nil {
		a.contextHealthMonitor.Stop()
	}
	if a.codebaseMonitor != nil {
		a.codebaseMonitor.Stop()
	}
	if a.shuttleMonitor != nil {
		a.shuttleMonitor.Stop()
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
	}, a.tracer, a.metrics, a.logger).WithTokenStore(a.deviceTokenStore)

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
	a.workflowMonitor.OnNewApproval(func(workflows []bridge.WorkflowInfo) {
		now := time.Now()
		for _, w := range workflows {
			data, err := json.Marshal(map[string]any{
				"workflow_id":  w.ID,
				"name":         w.Name,
				"current_step": w.CurrentStep,
			})
			if err != nil {
				continue
			}
			a.sseHub.Broadcast(bridge.SSEEvent{
				ID:        fmt.Sprintf("hud-workflow-approval-%s-%d", w.ID, now.UnixMilli()),
				Type:      "hud.workflow.waiting_approval",
				Timestamp: now,
				Data:      data,
			})
		}
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
	if a.contextHealthMonitor != nil {
		a.contextHealthMonitor.OnRefresh(func(snap monitor.ContextHealthSnapshot) {
			data, err := json.Marshal(snap)
			if err != nil {
				return
			}
			a.sseHub.Broadcast(bridge.SSEEvent{
				ID:        fmt.Sprintf("hud-context-health-%d", time.Now().UnixMilli()),
				Type:      "hud.context_health",
				Timestamp: time.Now(),
				Data:      data,
			})
		})
	}
	if a.codebaseMonitor != nil {
		a.codebaseMonitor.OnRefresh(func(snap monitor.CodebaseSnapshot) {
			data, err := json.Marshal(snap)
			if err != nil {
				return
			}
			a.sseHub.Broadcast(bridge.SSEEvent{
				ID:        fmt.Sprintf("hud-codebase-%d", time.Now().UnixMilli()),
				Type:      "hud.codebase",
				Timestamp: time.Now(),
				Data:      data,
			})
		})
	}
	if a.shuttleMonitor != nil {
		a.shuttleMonitor.OnRefresh(func(snap shuttle.ShuttleSnapshot) {
			data, err := json.Marshal(snap)
			if err != nil {
				return
			}
			a.sseHub.Broadcast(bridge.SSEEvent{
				ID:        fmt.Sprintf("hud-shuttle-%d", time.Now().UnixMilli()),
				Type:      "hud.weavertion",
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
