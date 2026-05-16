package hud

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/crb2nu/loom/internal/devbox/backend"
	"github.com/crb2nu/loom/internal/devbox/detect"
	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/spawn"
)

// Pinned CLI versions for reproducible agent container builds.
const (
	claudeCodeVersion = "1.0.33"
	codexVersion      = "0.130.0"
	geminiVersion     = "0.37.1"
)

// SpawnStatus is a type alias for spawn.Status, preserving the existing HUD API.
type SpawnStatus = spawn.Status

// SpawnStatus constants — aliases to spawn package constants.
const (
	SpawnStatusCreating  = spawn.StatusPending
	SpawnStatusBuilding  = spawn.StatusBuilding
	SpawnStatusRunning   = spawn.StatusRunning
	SpawnStatusCompleted = spawn.StatusCompleted
	SpawnStatusFailed    = spawn.StatusFailed
	SpawnStatusStopped   = spawn.StatusStopped
)

// SpawnRequest is a type alias for spawn.Request.
type SpawnRequest = spawn.Request

// SpawnState is a type alias for spawn.State.
type SpawnState = spawn.State

// SpawnOrchestrator manages the full lifecycle of headless agent spawns.
// It delegates state management to a spawn.Controller, keeping the HUD layer
// focused on shuttle concerns (build, deploy, exec, SSE, metrics).
type SpawnOrchestrator struct {
	backend     backend.Backend
	agentBridge *bridge.AgentBridge
	sseHub      *SSEHub
	tracer      trace.Tracer
	metrics     *HUDMetrics
	logger      *slog.Logger
	ctrl        *spawn.K8sController

	// Limits.
	maxConcurrent  int
	buildSlots     chan struct{}
	defaultTimeout time.Duration
	defaultMemory  int
	defaultCPUs    float64

	// workspaceRoot is the local path to the workspace mount (for project detection).
	workspaceRoot string
	// projects lists available project names for the spawn picker.
	projects []string

	// telemetry holds live SpawnTelemetryAccumulators for running spawns.
	// map[spawnID]*bridge.SpawnTelemetryAccumulator
	telemetry sync.Map

	// autoHandoff is the F5/Slice C1 trigger hook. Nil-safe: if unset,
	// the budget watcher skips auto-handoff evaluation. Set via
	// SetAutoHandoffHook from the orchestrator's wiring layer so this
	// file does not need to import pkg/agentcontext.
	autoHandoff AutoHandoffHook
}

// AutoHandoffHook is the minimal surface the budget watcher needs to
// evaluate + create an auto-handoff draft. Implemented by the
// agentcontext wiring layer so internal/hud/spawn.go stays
// dependency-light. All methods must be nil-safe at the call site.
type AutoHandoffHook interface {
	// Observe returns true if the trigger gate fires for this
	// (sessionKey, reason) pair at `now`.
	Observe(sessionKey, reason string, now time.Time) bool
	// Create drafts a handoff tagged source="auto". Errors are
	// logged by callers; Create must not panic on missing context.
	Create(ctx context.Context, sessionKey, sourceAgent, targetAgent, reason string, details map[string]any) error
	// Config exposes the live thresholds for inline breach evaluation.
	Config() AutoHandoffThresholds
}

// AutoHandoffThresholds is the subset of AutoHandoffConfig the watcher
// needs. Mirroring it here keeps this file independent of the
// agentcontext package.
type AutoHandoffThresholds struct {
	Enabled         bool
	InputTokenHigh  int
	CostUSDHigh     float64
	StalledDuration time.Duration
}

// SetAutoHandoffHook installs the auto-handoff trigger. Calling with a
// nil hook disables the feature without restructuring the orchestrator.
func (o *SpawnOrchestrator) SetAutoHandoffHook(h AutoHandoffHook) {
	o.autoHandoff = h
}

// streamExecCapable is satisfied by *backend.K8sBackend. It provides the
// low-level K8s client/config needed by backend.StreamExec.
type streamExecCapable interface {
	Clientset() kubernetes.Interface
	RestConfig() *rest.Config
	Namespace() string
	NFSFlush() bool
}

// SpawnOrchestratorConfig holds configuration for the spawn orchestrator.
type SpawnOrchestratorConfig struct {
	MaxConcurrent       int
	MaxConcurrentBuilds int
	DefaultTimeout      time.Duration
	DefaultMemory       int // MB
	DefaultCPUs         float64
	WorkspaceRoot       string   // local path to workspace mount (for project detection)
	Projects            []string // available projects for spawn picker (from SPAWN_PROJECTS env)
}

// DefaultSpawnConfig returns sensible defaults.
func DefaultSpawnConfig() SpawnOrchestratorConfig {
	wsRoot := "/workspace"
	if home, err := os.UserHomeDir(); err == nil {
		wsRoot = home + "/workspace"
	}
	return SpawnOrchestratorConfig{
		MaxConcurrent:       3,
		MaxConcurrentBuilds: 1,
		DefaultTimeout:      60 * time.Minute,
		DefaultMemory:       4096,
		DefaultCPUs:         2.0,
		WorkspaceRoot:       wsRoot,
	}
}

// NewSpawnOrchestrator creates a new spawn orchestrator. It initialises a
// spawn.K8sController backed by a FileStore for persistence and wires it
// into the HUD shuttle layer.
func NewSpawnOrchestrator(
	b backend.Backend,
	agentBridge *bridge.AgentBridge,
	sseHub *SSEHub,
	tracer trace.Tracer,
	metrics *HUDMetrics,
	logger *slog.Logger,
	cfg SpawnOrchestratorConfig,
) *SpawnOrchestrator {
	wsRoot := cfg.WorkspaceRoot
	if wsRoot == "" {
		wsRoot = "/workspace"
	}

	spawnLogger := logger.With("component", "spawn")

	// Initialize persistent spawn store. In Kubernetes, use a ConfigMap
	// in the spawn namespace so HUD rollouts preserve accepted/in-flight
	// spawns. Local/dev backends keep the legacy FileStore.
	var store spawn.Store
	if k8s, ok := b.(streamExecCapable); ok && k8s.Clientset() != nil && k8s.Namespace() != "" {
		store = spawn.NewK8sConfigMapStore(k8s.Clientset(), k8s.Namespace(), "loom-spawn-state")
		spawnLogger.Info("using kubernetes spawn state store", "namespace", k8s.Namespace(), "configmap", "loom-spawn-state")
	} else {
		storeDir := spawn.DefaultStoreDir()
		if fs, err := spawn.NewFileStore(storeDir); err != nil {
			spawnLogger.Warn("failed to create spawn store, state will not be persisted",
				"dir", storeDir, "error", err)
		} else {
			store = fs
		}
	}

	// Create a K8sController. We pass a nil kubernetes.Interface because the
	// orchestrator uses the devbox backend (not raw K8s client) for pod
	// management. The controller still provides state tracking, reconciliation
	// hooks, and persistence. A future iteration can inject a real K8s client
	// when the spawn backend exposes it.
	ctrl := spawn.NewK8sController(nil, "", store, spawnLogger)

	// Recover persisted state from the store on startup.
	if err := ctrl.RecoverFromStore(context.Background()); err != nil {
		spawnLogger.Warn("failed to recover spawn state from store", "error", err)
	}

	o := &SpawnOrchestrator{
		backend:        b,
		agentBridge:    agentBridge,
		sseHub:         sseHub,
		tracer:         tracer,
		metrics:        metrics,
		logger:         spawnLogger,
		ctrl:           ctrl,
		maxConcurrent:  cfg.MaxConcurrent,
		buildSlots:     newBuildSlots(cfg.MaxConcurrentBuilds),
		defaultTimeout: cfg.DefaultTimeout,
		defaultMemory:  cfg.DefaultMemory,
		defaultCPUs:    cfg.DefaultCPUs,
		workspaceRoot:  wsRoot,
		projects:       cfg.Projects,
	}
	// Wire the controller's terminal-cleanup hook so Reconcile reaps the
	// pod + presence + agent session for any spawn it observes in a
	// terminal state without CleanupAt set. This covers:
	//   - pods that exit naturally between failSpawn/completeSpawn ticks
	//   - pods abandoned across an operator restart
	//   - terminal-state spawns whose pod is still running (orphans that
	//     drain namespace quota — the failure mode that triggered this fix).
	ctrl.SetTerminalHook(o.reapTerminalSpawn)
	o.resumePreRuntimeSpawns()
	return o
}

func newBuildSlots(maxConcurrentBuilds int) chan struct{} {
	if maxConcurrentBuilds <= 0 {
		maxConcurrentBuilds = 1
	}
	return make(chan struct{}, maxConcurrentBuilds)
}

func (o *SpawnOrchestrator) acquireBuildSlot(ctx context.Context) (func(), error) {
	if o.buildSlots == nil {
		return func() {}, nil
	}
	select {
	case o.buildSlots <- struct{}{}:
		return func() { <-o.buildSlots }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for spawn build slot: %w", ctx.Err())
	}
}

// Controller returns the underlying spawn.K8sController for callers that need
// direct access (e.g., to start a reconcile loop).
func (o *SpawnOrchestrator) Controller() *spawn.K8sController {
	return o.ctrl
}

// RecoverSpawns delegates recovery to the spawn controller. Previously this
// blindly marked non-terminal spawns as failed ("stale after HUD restart").
// Now the controller recovers from the store and a subsequent Reconcile call
// will check actual pod status — fixing the stale-after-restart bug.
func (o *SpawnOrchestrator) RecoverSpawns() {
	if err := o.ctrl.RecoverFromStore(context.Background()); err != nil {
		o.logger.Warn("failed to recover spawns", "error", err)
	}
	o.resumePreRuntimeSpawns()
}

func (o *SpawnOrchestrator) resumePreRuntimeSpawns() {
	if o == nil || o.ctrl == nil || o.backend == nil {
		return
	}
	for _, state := range o.ctrl.List() {
		if state == nil || state.PodName != "" || !isPreRuntimeSpawnStatus(state.Status) {
			continue
		}
		if state.Request.TaskDescription == "" || state.Request.Project == "" {
			continue
		}
		spawnID := state.SpawnID
		req := state.Request
		o.logger.Info("resuming pre-runtime spawn after HUD restart",
			"spawn_id", spawnID,
			"status", state.Status,
			"agent_type", req.AgentType,
			"project", req.Project,
		)
		go o.runSpawn(spawnID, req)
	}
}

func isPreRuntimeSpawnStatus(status spawn.Status) bool {
	switch status {
	case spawn.StatusPending, spawn.StatusBuilding:
		return true
	default:
		return false
	}
}

// Spawn starts a new headless agent. Returns the spawn ID immediately (202).
// The actual spawn runs asynchronously in a goroutine.
func (o *SpawnOrchestrator) Spawn(ctx context.Context, req SpawnRequest) (string, error) {
	// Apply defaults.
	if req.MemoryMB <= 0 {
		req.MemoryMB = o.defaultMemory
	}
	if req.CPUs <= 0 {
		req.CPUs = o.defaultCPUs
	}
	if req.TimeoutMinutes <= 0 {
		req.TimeoutMinutes = int(o.defaultTimeout.Minutes())
	}
	if req.BaseBranch == "" {
		req.BaseBranch = "main"
	}
	if req.Namespace == "" {
		req.Namespace = req.Project + "/spawn"
	}

	if existing := o.existingActiveSpawnForRequest(req); existing != "" {
		o.logger.Info("returning existing active spawn for idempotent request",
			"spawn_id", existing,
			"run_id", req.Metadata["LOOM_MILLS_RUN_ID"],
			"stage", req.Metadata["LOOM_MILLS_STAGE"],
		)
		return existing, nil
	}

	// Check concurrent limit.
	if o.ctrl.ActiveCount() >= o.maxConcurrent {
		return "", fmt.Errorf("max concurrent spawns reached (%d)", o.maxConcurrent)
	}

	// Delegate validation and ID generation to the controller.
	spawnID, err := o.ctrl.Spawn(ctx, req)
	if err != nil {
		return "", err
	}

	if o.metrics != nil {
		o.metrics.AgentSpawnTotal.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("agent_type", req.AgentType),
				attribute.String("outcome", "initiated"),
			),
		)
		o.metrics.SpawnedAgentActive.Add(ctx, 1)
	}

	// Run spawn flow asynchronously.
	go o.runSpawn(spawnID, req)

	return spawnID, nil
}

func (o *SpawnOrchestrator) existingActiveSpawnForRequest(req SpawnRequest) string {
	if o == nil || o.ctrl == nil {
		return ""
	}
	runID := firstNonEmptySpawnTag(req.Metadata["LOOM_MILLS_RUN_ID"], req.Metadata["loom_mills_run_id"])
	stage := firstNonEmptySpawnTag(req.Metadata["LOOM_MILLS_STAGE"], req.Metadata["loom_mills_stage"])
	if runID == "" || stage == "" {
		return ""
	}
	for _, state := range o.ctrl.List() {
		if state == nil || spawn.IsTerminal(state.Status) {
			continue
		}
		meta := state.Request.Metadata
		if firstNonEmptySpawnTag(meta["LOOM_MILLS_RUN_ID"], meta["loom_mills_run_id"]) != runID {
			continue
		}
		if firstNonEmptySpawnTag(meta["LOOM_MILLS_STAGE"], meta["loom_mills_stage"]) != stage {
			continue
		}
		if req.Project != "" && state.Request.Project != "" && req.Project != state.Request.Project {
			continue
		}
		if req.Branch != "" && state.Request.Branch != "" && req.Branch != state.Request.Branch {
			continue
		}
		return state.SpawnID
	}
	return ""
}

func firstNonEmptySpawnTag(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// newSpawnParser creates the appropriate JSONL parser for the given agent type.
// Returns nil for agent types that don't support structured telemetry.
func newSpawnParser(agentType string, sink SpawnEventSink, agentID, spawnID string, broadcast SpawnEventBroadcaster, logger *slog.Logger) SpawnLineParser {
	switch agentType {
	case "claude-code":
		return NewClaudeJSONLParser(sink, agentID, spawnID, broadcast, logger)
	case "codex":
		return NewCodexJSONLParser(sink, agentID, spawnID, broadcast, logger)
	case "gemini":
		return NewGeminiJSONLParser(sink, agentID, spawnID, broadcast, logger)
	default:
		return nil
	}
}

// broadcastTelemetryEvent broadcasts a telemetry event via SSE.
func (o *SpawnOrchestrator) broadcastTelemetryEvent(eventType string, agentID string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	o.sseHub.Broadcast(bridge.SSEEvent{
		ID:        fmt.Sprintf("%s-%s-%d", eventType, agentID, time.Now().UnixMilli()),
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      payload,
	})
}

// runSpawn executes the full spawn lifecycle in a background goroutine.
func (o *SpawnOrchestrator) runSpawn(spawnID string, req SpawnRequest) {
	ctx, span := o.tracer.Start(context.Background(), "agent.spawn",
		trace.WithAttributes(
			attribute.String("agent.type", req.AgentType),
			attribute.String("project", req.Project),
			attribute.String("namespace", req.Namespace),
			attribute.String("spawn_id", spawnID),
		),
	)
	defer span.End()

	state, _ := o.ctrl.Get(spawnID)

	// Resolve which cluster auth path this spawn will use. Populated on
	// the state so HUD detail endpoints can surface "cluster_api_key" vs
	// "cluster_service_account" without introspecting the pod. In Slice 2a
	// the resolver returns a default based on agent type; Slice 2b layers
	// in cluster OAuth detection.
	state.AuthMode = resolveAuthMode(req.AgentType)
	o.ctrl.UpdateState(ctx, state)

	// Resolve the project name to an on-disk location plus the
	// workspace-relative path used inside the spawned pod. Bare names like
	// "loom-core" are searched under the standard buckets (services/, libs/,
	// ...) so monorepo repos resolve without explicit registration.
	projectDir, projectRel, resolveErr := resolveProjectPath(o.workspaceRoot, req.Project)
	if resolveErr != nil {
		o.failSpawn(ctx, state, fmt.Sprintf("project resolution failed: %v", resolveErr))
		return
	}
	podProjectDir := "/workspace/" + projectRel

	// Step 1: Detect project environment and generate Dockerfile.
	state.Status = SpawnStatusBuilding
	o.ctrl.UpdateState(ctx, state)
	o.broadcastSpawnEvent("agent.spawn.building", state)

	_, buildSpan := o.tracer.Start(ctx, "agent.spawn.image_build")

	df, dfErr := o.generateDockerfile(projectDir, req.AgentType)
	if dfErr != nil {
		buildSpan.End()
		o.failSpawn(ctx, state, fmt.Sprintf("dockerfile generation failed: %v", dfErr))
		return
	}

	// ContextDir is used by the K8s backend for filepath.Rel (string-only,
	// no local filesystem access needed). In git-clone mode the backend
	// derives the project name from the path and clones the repo.
	buildTag := agentRuntimeBuildTag(req.AgentType, df)
	releaseBuildSlot, slotErr := o.acquireBuildSlot(ctx)
	if slotErr != nil {
		buildSpan.End()
		o.failSpawn(ctx, state, fmt.Sprintf("image build queue failed: %v", slotErr))
		return
	}
	buildResult, err := o.backend.Build(ctx, backend.BuildOpts{
		Tag:            buildTag,
		Dockerfile:     df,
		ContextDir:     projectDir,
		PreferExisting: true,
	})
	releaseBuildSlot()
	buildSpan.End()
	if err != nil {
		o.failSpawn(ctx, state, fmt.Sprintf("image build failed: %v", err))
		return
	}

	// Step 2: Start K8s pod.
	o.logger.Info("build completed, starting pod", "spawn_id", spawnID, "image", buildResult.ImageTag)
	_, podSpan := o.tracer.Start(ctx, "agent.spawn.pod_create")
	env := map[string]string{
		"AGENT_ID":  state.AgentID,
		"SPAWN_ID":  spawnID,
		"NAMESPACE": req.Namespace,
	}
	if req.ParentSessionID != "" {
		env["LOOM_PARENT_SESSION_ID"] = req.ParentSessionID
	}
	// Gemini picks up service-account auth via this standard Google env
	// var, which the Google Auth Library reads to find the SA JSON file.
	// Harmless when the SA JSON isn't present — Gemini falls back to
	// GEMINI_API_KEY from cluster-agent-api-keys.
	if req.AgentType == "gemini" {
		env["GOOGLE_APPLICATION_CREDENTIALS"] = GeminiSAMountPath + "/" + GeminiSAFilename
	}
	startResult, err := o.backend.Start(ctx, backend.StartOpts{
		Name:              "spawn-" + spawnID,
		ImageTag:          buildResult.ImageTag,
		WorkDir:           podProjectDir,
		Env:               env,
		SecretEnv:         agentSecretEnvVars(req.AgentType),
		SecretMounts:      agentSecretMounts(req.AgentType),
		MemoryMB:          req.MemoryMB,
		CPUs:              req.CPUs,
		Network:           true,
		AgentID:           state.AgentID,
		ManagedByOverride: spawn.ManagedByValue,
		ExtraLabels: map[string]string{
			spawn.SpawnIDLabel: spawnID,
			spawn.AgentIDLabel: state.AgentID,
		},
	})
	podSpan.End()
	if err != nil {
		o.failSpawn(ctx, state, fmt.Sprintf("pod creation failed: %v", err))
		return
	}

	state.PodName = startResult.ContainerID
	o.ctrl.UpdateState(ctx, state)

	// Step 3: Inject pre-authed configs (with a short timeout to avoid hanging on SPDY issues).
	_, cfgSpan := o.tracer.Start(ctx, "agent.spawn.config_inject")
	cfgCtx, cfgCancel := context.WithTimeout(ctx, 30*time.Second)
	o.logger.Info("injecting agent config", "spawn_id", spawnID, "pod", startResult.ContainerID, "agent_type", req.AgentType)
	if err := o.injectAgentConfig(cfgCtx, startResult.ContainerID, req.AgentType, podProjectDir); err != nil {
		cfgCancel()
		cfgSpan.End()
		o.failSpawn(ctx, state, fmt.Sprintf("config injection failed: %v", err))
		return
	}
	cfgCancel()
	cfgSpan.End()

	// Step 4: Register agent session (before exec so the agent has session context).
	_, sessSpan := o.tracer.Start(ctx, "agent.spawn.session_register")
	o.agentBridge.StartSession(bridge.SessionStartParams{
		Namespace:   req.Namespace,
		AgentID:     state.AgentID,
		AgentType:   req.AgentType,
		Description: req.TaskDescription,
	})
	sessSpan.End()

	// Mark running and broadcast event.
	state.Status = SpawnStatusRunning
	o.ctrl.UpdateState(ctx, state)
	o.broadcastSpawnEvent("agent.spawn.running", state)

	// Step 5: Start heartbeat loop for spawn visibility.
	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	go o.runHeartbeatLoop(heartbeatCtx, state)

	// Step 6: Execute agent CLI (or SDK driver) with real-time JSONL telemetry parsing.
	o.logger.Info("executing agent",
		"spawn_id", spawnID,
		"agent_type", req.AgentType,
		"pod", startResult.ContainerID,
		"use_sdk_driver", req.UseSDKDriver,
		"multi_turn", req.MultiTurn,
	)
	_, execSpan := o.tracer.Start(ctx, "agent.spawn.agent_exec")

	// Choose between the legacy CLI path and the embedded loom-spawn-driver
	// Node.js sidecar. When UseSDKDriver is set we inject the bundled driver
	// into the pod and invoke it instead of the raw agent CLI.
	var agentCmd string
	if req.UseSDKDriver {
		injectCtx, injectCancel := context.WithTimeout(ctx, 30*time.Second)
		if err := o.injectSDKDriver(injectCtx, startResult.ContainerID); err != nil {
			injectCancel()
			execSpan.End()
			heartbeatCancel()
			o.failSpawn(ctx, state, fmt.Sprintf("inject spawn driver: %v", err))
			return
		}

		// In multi-turn mode pre-create the control file so the driver's
		// fs.watch fires immediately on the first REST-driven append. The
		// REST handlers (slice 8c) call injectControlMessage to push
		// `{type:"message"|"interrupt"|"shutdown"}` lines into this file.
		var controlFilePath string
		if req.MultiTurn {
			if err := o.injectControlFile(injectCtx, startResult.ContainerID, spawnID); err != nil {
				injectCancel()
				execSpan.End()
				heartbeatCancel()
				o.failSpawn(ctx, state, fmt.Sprintf("inject control file: %v", err))
				return
			}
			controlFilePath = controlFilePathForSpawn(spawnID)
		}
		injectCancel()

		agentCmd = buildSDKDriverCommand(
			req.AgentType,
			req.TaskDescription,
			state.AgentID,
			spawnID,
			podProjectDir,
			controlFilePath,
			req.MaxTurns,
			req.MaxCostUSD,
		)
	} else {
		agentCmd = buildAgentCommand(req.AgentType, req.TaskDescription, state.AgentID)
	}

	// Create telemetry accumulator and JSONL parser for real-time parsing.
	acc := bridge.NewSpawnTelemetryAccumulator()
	o.telemetry.Store(spawnID, acc)

	broadcaster := SpawnEventBroadcaster(func(eventType string, agentID string, data any) {
		o.broadcastTelemetryEvent(eventType, agentID, data)
	})
	parser := newSpawnParser(req.AgentType, acc, state.AgentID, spawnID, broadcaster, o.logger)

	var execResult *backend.ExecResult
	var execErr error

	// Cancellable exec context so the budget watcher can abort the run when
	// MaxCostUSD or MaxTurns is exceeded.
	execCtx, execCancel := context.WithCancel(ctx)

	// Budget watcher: polls the telemetry accumulator every 5s and cancels the
	// exec context when a configured budget is exceeded. The watcher terminates
	// when the exec returns via the done channel.
	watcherDone := make(chan struct{})
	if req.MaxCostUSD > 0 || req.MaxTurns > 0 {
		go o.runBudgetWatcher(execCtx, spawnID, req, acc, execCancel, watcherDone)
	} else {
		close(watcherDone)
	}

	// Use streaming exec if backend supports it and we have a parser; fall back to buffered.
	if sec, ok := o.backend.(streamExecCapable); ok && parser != nil {
		execResult, execErr = backend.StreamExec(execCtx,
			sec.Clientset(), sec.RestConfig(), sec.Namespace(), sec.NFSFlush(),
			backend.StreamExecOpts{
				ContainerID: startResult.ContainerID,
				Command:     agentCmd,
				WorkDir:     podProjectDir,
				TimeoutSec:  req.TimeoutMinutes * 60,
				OnLine: func(line []byte) {
					parser.HandleLine(line)
				},
			},
		)
	} else {
		// Fallback: buffered exec (no real-time telemetry).
		execResult, execErr = o.backend.Exec(execCtx, backend.ExecOpts{
			ContainerID: startResult.ContainerID,
			Command:     agentCmd,
			WorkDir:     podProjectDir,
			TimeoutSec:  req.TimeoutMinutes * 60,
		})
	}
	// Stop the watcher and release the exec context.
	if req.MaxCostUSD > 0 || req.MaxTurns > 0 {
		close(watcherDone)
	}
	execCancel()
	execSpan.End()
	heartbeatCancel()

	// Capture agent output as a context entry for session visibility.
	if execResult != nil && execResult.StdoutTail != "" {
		go func() {
			truncated := execResult.StdoutTail
			if len(truncated) > 8000 {
				truncated = truncated[:8000] + "\n... (truncated)"
			}
			_ = o.agentBridge.ContextAdd("", []map[string]any{{
				"entry_type": "finding",
				"title":      fmt.Sprintf("Agent output (%s)", state.SpawnID),
				"content":    truncated,
			}})
		}()
	}

	// Step 7: Finalize based on exec result.
	if execErr != nil {
		o.failSpawn(ctx, state, fmt.Sprintf("agent execution failed: %v", execErr))
		return
	}
	o.completeSpawn(ctx, state)
}

// generateDockerfile builds a lean agent runtime image. Spawned agents get
// project source through the runtime workspace init container, and quality
// gates run in later Mills stages, so the spawn image should not install
// project-specific lint/security toolchains during planning/implementation.
func (o *SpawnOrchestrator) generateDockerfile(projectDir, agentType string) ([]byte, error) {
	if _, err := detect.Fingerprint(projectDir); err != nil {
		o.logger.Warn("project detection failed; using generic agent runtime image", "dir", projectDir, "error", err)
	}
	df := agentRuntimeDockerfile()
	if cliLines := agentCLIInstallLines(agentType); cliLines != "" {
		df = append(df, []byte("\n"+cliLines+"\n")...)
	}
	// Switch to the non-root agent user *after* the CLI install layer so
	// `npm install -g` can write to /usr/local/lib/node_modules. The
	// runtime CMD in agentRuntimeDockerfile keeps the pod alive as the
	// agent user; injectAgentConfig + claude exec all run as uid 1000.
	df = append(df, []byte(agentRuntimeUserSuffix())...)
	return df, nil
}

// agentRuntimeDockerfile returns the shared base for spawned agent pods.
//
// Why the non-root user: the claude CLI refuses `--dangerously-skip-permissions`
// when the effective uid is 0 ("cannot be used with root/sudo privileges for
// security reasons"), and Mills launches every claude-code spawn with that
// flag. Spawned agents must run as a non-root user with a writable $HOME so
// claude can stash its `.claude.json` profile.
//
// The base stays as root so the agent CLI install layer
// (agentCLIInstallLines) can run `npm install -g`, which needs to write
// to /usr/local/lib/node_modules. The trailing USER agent + HOME switch is
// appended by generateDockerfile *after* the install layer.
func agentRuntimeDockerfile() []byte {
	return []byte(`FROM golang:1.25.10-alpine
RUN apk add --no-cache ca-certificates git make bash curl nodejs npm python3 \
 && adduser -D -u 1000 -h /home/agent agent \
 && mkdir -p /workspace \
 && chown -R agent:agent /workspace /home/agent
WORKDIR /workspace
CMD ["sleep", "infinity"]
`)
}

// agentRuntimeUserSuffix returns the trailing Dockerfile lines that flip
// the image from root to the agent user. It is appended *after* the agent
// CLI install layer (which needs root for `npm install -g`).
func agentRuntimeUserSuffix() string {
	return "USER agent\nENV HOME=/home/agent\n"
}

func agentRuntimeBuildTag(agentType string, dockerfile []byte) string {
	sum := sha256.Sum256(dockerfile)
	safeAgent := sanitizeImageTagComponent(agentType)
	if safeAgent == "" {
		safeAgent = "agent"
	}
	return fmt.Sprintf("spawn-runtime-%s:%x", safeAgent, sum[:8])
}

func sanitizeImageTagComponent(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// injectAgentConfig writes platform-specific config files into the pod.
// Uses Exec (stdout-only SPDY) instead of WriteFile (stdin SPDY) to avoid
// in-cluster SPDY stdin stream hangs observed on K3s.
//
// projectDir is the pod-internal absolute path to the project root (e.g.
// "/workspace/services/loom-core"), already resolved by the caller via
// resolveProjectPath so this function does not need to know about
// workspace bucket layouts.
func (o *SpawnOrchestrator) injectAgentConfig(ctx context.Context, containerID, agentType, projectDir string) error {
	writeCmd := func(dir, file, content string) error {
		encoded := base64.StdEncoding.EncodeToString([]byte(content))
		cmd := fmt.Sprintf("mkdir -p %s && echo '%s' | base64 -d > %s/%s", dir, encoded, dir, file)
		_, err := o.backend.Exec(ctx, backend.ExecOpts{
			ContainerID: containerID,
			Command:     cmd,
			TimeoutSec:  30,
		})
		return err
	}

	switch agentType {
	case "claude-code":
		// Claude Code reads project-level .claude/settings.json for permissions.
		// apiKeyHelper extracts the subscription accessToken from the
		// cluster-owned OAuth JSON mounted at /home/agent/.claude.auth/oauth.json
		// (sourced from cluster-agent-auth, NOT the developer's Mac). If
		// the mount is absent or the key is missing, python fails silently
		// and the helper falls back to ANTHROPIC_API_KEY from
		// cluster-agent-api-keys.
		settings := `{"permissions":{"allow":["Bash","Read","Write","Edit","Glob","Grep"]},"apiKeyHelper":"python3 -c \"import json,sys; d=json.load(open('` + AgentHomeDir + `/.claude.auth/oauth.json')); print(d['claudeAiOauth']['accessToken'])\" 2>/dev/null || echo $ANTHROPIC_API_KEY"}`
		if err := writeCmd(projectDir+"/.claude", "settings.json", settings); err != nil {
			return fmt.Errorf("write claude settings: %w", err)
		}
	case "codex":
		// Codex reads ~/.codex/config.toml for sandbox + multi-agent features
		// and ~/.codex/auth.json for OAuth (falling back to $OPENAI_API_KEY).
		// Because the auth.json is a read-only secret volume mount staged at
		// /home/agent/.codex.auth/, we symlink it into the writable
		// /home/agent/.codex/ directory so Codex CLI can read it at its
		// native path. The symlink transparently reflects kubelet-propagated
		// secret updates (e.g., refreshed OAuth tokens written by
		// mcp-auth-refresher).
		//
		// TODO(spawn): add MCP proxy section once the loom binary is available
		// in spawned pods. Currently agentCLIInstallLines only installs the
		// agent CLI via npm; adding a `COPY --from=loom-builder` stage or an
		// npm-published loom package would enable:
		//   [mcp_servers.loom]
		//   command = "loom"
		//   args = ["proxy", "--stdio"]
		config := `[agent]
approval = "auto-edit"

[sandbox]
mode = "workspace-write"
network_access = true

[features]
multi_agent = true
collaboration_modes = true
unified_exec = true
`
		if err := writeCmd(AgentHomeDir+"/.codex", "config.toml", config); err != nil {
			return fmt.Errorf("write codex config: %w", err)
		}
		// Best-effort symlink; pipe "true" at the end so the exec doesn't
		// fail when the auth mount is absent (API-key-only operators).
		linkCmd := "ln -sf " + AgentHomeDir + "/.codex.auth/auth.json " + AgentHomeDir + "/.codex/auth.json 2>/dev/null || true"
		if _, err := o.backend.Exec(ctx, backend.ExecOpts{
			ContainerID: containerID,
			Command:     linkCmd,
			TimeoutSec:  10,
		}); err != nil {
			return fmt.Errorf("link codex auth.json: %w", err)
		}
	case "gemini":
		// Gemini reads ~/.gemini/settings.json for permissions. The Google
		// Auth Library auto-detects GOOGLE_APPLICATION_CREDENTIALS; that env
		// var is set at pod-start time in runSpawn(), pointing at the
		// service-account JSON mounted from the cluster secret. If the SA
		// JSON key is absent, the file is missing and Gemini falls back to
		// GEMINI_API_KEY env.
		settings := `{"permissions":{"allow_all":true}}`
		if err := writeCmd(AgentHomeDir+"/.gemini", "settings.json", settings); err != nil {
			return fmt.Errorf("write gemini settings: %w", err)
		}
	}
	return nil
}

// buildAgentCommand constructs the CLI command to run the agent headlessly.
func buildAgentCommand(agentType, task, agentID string) string {
	switch agentType {
	case "claude-code":
		// stream-json emits one JSONL event per line for real-time telemetry parsing.
		// --verbose is mandatory: claude-code 1.x rejects `-p` + `--output-format
		// stream-json` without it ("Error: When using --print, --output-format=
		// stream-json requires --verbose"). Without --verbose the CLI prints
		// that one line and exits 0 *without making any API call*, which is
		// why every Mills spawn showed turn_count=0 / cost=$0 / file_changes=0.
		return fmt.Sprintf(`claude -p %q --dangerously-skip-permissions --output-format stream-json --verbose --max-turns 50`, task)
	case "codex":
		// Wrap with EXIT trap so loom session-end fires even without a native hook.
		// The trap is best-effort: if the loom binary is not in the pod PATH,
		// stderr is suppressed via 2>/dev/null and the HUD-side completeSpawn /
		// failSpawn will still call EndSession as a fallback.
		//
		// --full-auto was deprecated in @openai/codex 0.110+; the supported form
		// is --sandbox workspace-write. --skip-git-repo-check lets codex run in
		// the spawn pod's /workspace clone where there is no .git on first
		// boot (devbox backend clones into the working dir, not /).
		return fmt.Sprintf(
			`trap 'loom agent session-end --agent-id %q --summarize --summary-async --quiet 2>/dev/null' EXIT; codex exec --sandbox workspace-write --skip-git-repo-check --json %q`,
			agentID, task,
		)
	case "gemini":
		return fmt.Sprintf(`gemini -p %q --yolo --output-format stream-json`, task)
	default:
		return fmt.Sprintf(`echo "Unsupported agent type: %s"`, agentType)
	}
}

// StopSpawn stops a running spawned agent.
func (o *SpawnOrchestrator) StopSpawn(ctx context.Context, spawnID string) error {
	state, ok := o.ctrl.Get(spawnID)
	if !ok {
		return fmt.Errorf("spawn %s not found", spawnID)
	}

	// Stop via devbox backend (handles pod deletion + cleanup).
	if state.PodName != "" {
		if err := o.backend.Stop(ctx, state.PodName); err != nil {
			o.logger.Warn("failed to stop spawn pod", "spawn_id", spawnID, "error", err)
		}
	}

	state.Status = SpawnStatusStopped
	now := time.Now()
	state.EndedAt = &now
	o.ctrl.UpdateState(ctx, state)

	if o.metrics != nil {
		o.metrics.SpawnedAgentActive.Add(ctx, -1)
	}

	o.broadcastSpawnEvent("agent.spawn.stopped", state)

	// End the agent session on manual stop.
	go func() {
		summarize := false
		o.agentBridge.EndSession(bridge.SessionEndParams{AgentID: state.AgentID, Summarize: &summarize})
	}()
	return nil
}

// SendControlMessage appends a control command to a running multi-turn
// spawn's JSONL control file. The spawn-driver's tail loop picks up the new
// line within ~200ms (fs.watch + poll fallback) and dispatches it to the
// active SDK Query or Codex Thread.
//
// Errors are returned as wrapped sentinels so REST handlers can distinguish
// 404 (not found), 409 (not running), and 400 (not multi-turn / invalid
// command) from 5xx backend failures:
//
//   - spawn.ErrSpawnNotFound         → 404
//   - spawn.ErrSpawnNotRunning       → 409
//   - spawn.ErrSpawnNotMultiTurn     → 400
//   - spawn.ErrInvalidControlCommand → 400
//
// Any other error is a backend/exec failure and should surface as 500.
func (o *SpawnOrchestrator) SendControlMessage(ctx context.Context, spawnID string, cmd spawn.ControlCommand) error {
	if err := validateControlCommand(cmd); err != nil {
		return err
	}

	state, ok := o.ctrl.Get(spawnID)
	if !ok {
		return fmt.Errorf("%w: %s", spawn.ErrSpawnNotFound, spawnID)
	}

	if state.Status != SpawnStatusRunning {
		return fmt.Errorf("%w: %s is %s", spawn.ErrSpawnNotRunning, spawnID, state.Status)
	}

	if !state.Request.MultiTurn || !state.Request.UseSDKDriver {
		return fmt.Errorf("%w: %s was not spawned with multi_turn=true", spawn.ErrSpawnNotMultiTurn, spawnID)
	}

	if state.PodName == "" {
		return fmt.Errorf("spawn %s has no pod name; cannot inject control command", spawnID)
	}

	if err := o.injectControlMessage(ctx, state.PodName, spawnID, cmd); err != nil {
		return fmt.Errorf("inject control command for %s: %w", spawnID, err)
	}

	o.logger.Info("injected spawn control command",
		"spawn_id", spawnID,
		"agent_id", state.AgentID,
		"command_type", cmd.Type,
	)
	return nil
}

// validateControlCommand enforces the driver contract: Type must be one of
// the known discriminators and "message" requires non-empty Text.
func validateControlCommand(cmd spawn.ControlCommand) error {
	switch cmd.Type {
	case spawn.ControlCommandMessage:
		if strings.TrimSpace(cmd.Text) == "" {
			return fmt.Errorf("%w: message text is required", spawn.ErrInvalidControlCommand)
		}
		return nil
	case spawn.ControlCommandInterrupt, spawn.ControlCommandShutdown:
		return nil
	case "":
		return fmt.Errorf("%w: type is required", spawn.ErrInvalidControlCommand)
	default:
		return fmt.Errorf("%w: unknown type %q", spawn.ErrInvalidControlCommand, cmd.Type)
	}
}

// failSpawn marks a spawn as failed, cleans up the K8s pod, and broadcasts the event.
func (o *SpawnOrchestrator) failSpawn(ctx context.Context, state *SpawnState, reason string) {
	// Attach partial telemetry snapshot (valuable for debugging failures).
	if accVal, ok := o.telemetry.LoadAndDelete(state.SpawnID); ok {
		acc := accVal.(*bridge.SpawnTelemetryAccumulator)
		snap := acc.Snapshot()
		state.Telemetry = &snap
	}

	// Persist final telemetry summary to the agent-context session.
	o.persistTelemetrySummary(state, string(SpawnStatusFailed))

	state.Status = SpawnStatusFailed
	state.Error = reason
	now := time.Now()
	state.EndedAt = &now
	podName := state.PodName
	o.ctrl.UpdateState(ctx, state)

	// Clean up K8s pod if one was created.
	if podName != "" {
		if err := o.backend.Stop(ctx, podName); err != nil {
			o.logger.Warn("failed to clean up pod on spawn failure",
				"spawn_id", state.SpawnID, "pod", podName, "error", err)
		}
	}

	if o.metrics != nil {
		o.metrics.SpawnedAgentActive.Add(ctx, -1)
		o.metrics.AgentSpawnTotal.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("agent_type", state.Request.AgentType),
				attribute.String("outcome", "failed"),
			),
		)
	}

	// Record partial spawn telemetry metrics (still valuable for debugging failures).
	if o.metrics != nil && state.Telemetry != nil {
		o.recordSpawnTelemetryMetrics(ctx, state)
	}

	o.logger.Error("spawn failed", "spawn_id", state.SpawnID, "reason", reason)
	o.broadcastSpawnEvent("agent.spawn.failed", state)

	// Record failure and end the agent session.
	go func() {
		_ = o.agentBridge.ContextAdd("", []map[string]any{{
			"entry_type": "error",
			"title":      "Spawn failed: " + state.SpawnID,
			"content":    reason,
		}})
		summarize := false
		o.agentBridge.EndSession(bridge.SessionEndParams{AgentID: state.AgentID, Summarize: &summarize})
	}()
}

// completeSpawn marks a spawn as completed.
func (o *SpawnOrchestrator) completeSpawn(ctx context.Context, state *SpawnState) {
	// Attach final telemetry snapshot if available.
	if accVal, ok := o.telemetry.LoadAndDelete(state.SpawnID); ok {
		acc := accVal.(*bridge.SpawnTelemetryAccumulator)
		snap := acc.Snapshot()
		state.Telemetry = &snap
	}

	// Persist final telemetry summary to the agent-context session.
	o.persistTelemetrySummary(state, string(SpawnStatusCompleted))

	state.Status = SpawnStatusCompleted
	now := time.Now()
	state.EndedAt = &now
	o.ctrl.UpdateState(ctx, state)

	if o.metrics != nil {
		o.metrics.SpawnedAgentActive.Add(ctx, -1)
		o.metrics.AgentSpawnTotal.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("agent_type", state.Request.AgentType),
				attribute.String("outcome", "completed"),
			),
		)
	}

	// Record spawn telemetry metrics.
	if o.metrics != nil && state.Telemetry != nil {
		o.recordSpawnTelemetryMetrics(ctx, state)
	}

	o.logger.Info("spawn completed", "spawn_id", state.SpawnID)
	o.broadcastSpawnEvent("agent.spawn.completed", state)

	// End the agent session with summarization.
	go func() {
		summarize := true
		o.agentBridge.EndSession(bridge.SessionEndParams{
			AgentID:   state.AgentID,
			Summarize: &summarize,
		})
	}()
}

// reapTerminalSpawn is the K8sController.TerminalHook invocation: it
// releases the cluster + agent-context resources for a terminal spawn
// so neither stale pods nor stale presence rows accumulate.
//
// The hook is intentionally tolerant of partial failure — backend.Stop
// returns nil for an already-gone pod; PresenceDeregister returns nil
// for an already-deregistered agent. Whatever can be cleaned up is.
// Errors are logged but do not block further cleanup steps so a flake
// on one resource cannot starve the others.
func (o *SpawnOrchestrator) reapTerminalSpawn(ctx context.Context, state spawn.State) {
	o.logger.Info("reaping terminal spawn",
		"spawn_id", state.SpawnID,
		"agent_id", state.AgentID,
		"pod", state.PodName,
		"status", state.Status,
	)

	if state.PodName != "" && o.backend != nil {
		if err := o.backend.Stop(ctx, state.PodName); err != nil {
			o.logger.Warn("reap: failed to stop pod",
				"spawn_id", state.SpawnID, "pod", state.PodName, "error", err)
		}
	}

	if state.AgentID != "" && o.agentBridge != nil {
		if err := o.agentBridge.PresenceDeregister(state.AgentID); err != nil {
			o.logger.Warn("reap: failed to deregister presence",
				"spawn_id", state.SpawnID, "agent_id", state.AgentID, "error", err)
		}
		// EndSession is idempotent and safe to call even when the
		// session already ended in failSpawn/completeSpawn. It guards
		// against the operator restart path where the prior process
		// died before reaching its EndSession call.
		summarize := false
		o.agentBridge.EndSession(bridge.SessionEndParams{
			AgentID:   state.AgentID,
			Summarize: &summarize,
		})
	}
}

// recordSpawnTelemetryMetrics records OTel metrics from the telemetry snapshot
// attached to a terminal spawn state. Called from completeSpawn and failSpawn.
func (o *SpawnOrchestrator) recordSpawnTelemetryMetrics(ctx context.Context, state *SpawnState) {
	tel := state.Telemetry
	attrs := metric.WithAttributes(attribute.String("agent_type", state.Request.AgentType))

	o.metrics.SpawnTokensTotal.Add(ctx, int64(tel.TokenUsage.InputTokens),
		attrs, metric.WithAttributes(attribute.String("direction", "input")))
	o.metrics.SpawnTokensTotal.Add(ctx, int64(tel.TokenUsage.OutputTokens),
		attrs, metric.WithAttributes(attribute.String("direction", "output")))

	if tel.TotalCostUSD > 0 {
		o.metrics.SpawnCostTotal.Add(ctx, tel.TotalCostUSD, attrs)
	}
	o.metrics.SpawnTurnsTotal.Add(ctx, int64(tel.TurnCount), attrs)
	o.metrics.SpawnToolCallsTotal.Add(ctx, int64(len(tel.ToolCalls)), attrs)

	for _, fc := range tel.FileChanges {
		o.metrics.SpawnFileChangesTotal.Add(ctx, 1,
			attrs, metric.WithAttributes(attribute.String("kind", fc.Kind)))
	}
	for _, e := range tel.Errors {
		o.metrics.SpawnErrorsTotal.Add(ctx, 1,
			attrs, metric.WithAttributes(attribute.String("error_type", e.Type)))
	}
}

// persistTelemetrySummary writes a structured telemetry summary to the
// agent-context session associated with this spawn. Called from completeSpawn
// and failSpawn after the telemetry snapshot has been attached to state.
//
// Uses a short background context (not the spawn context) because failSpawn
// may be invoked on a canceled or timed-out parent context. Errors from
// ContextAdd are logged but do not fail the spawn transition.
func (o *SpawnOrchestrator) persistTelemetrySummary(state *SpawnState, status string) {
	if o.agentBridge == nil || state == nil {
		return
	}
	if state.Request.Namespace == "" {
		return
	}
	if state.Telemetry == nil {
		return
	}

	tel := state.Telemetry
	summary := map[string]any{
		"spawn_id":       state.SpawnID,
		"agent_id":       state.AgentID,
		"agent_type":     state.Request.AgentType,
		"status":         status,
		"total_cost_usd": tel.TotalCostUSD,
		"turn_count":     tel.TurnCount,
		"stop_reason":    tel.StopReason,
		"tool_count":     len(tel.ToolCalls),
		"file_count":     len(tel.FileChanges),
		"error_count":    len(tel.Errors),
		"token_usage":    tel.TokenUsage,
		"last_message":   tel.LastMessage,
	}
	content, err := json.Marshal(summary)
	if err != nil {
		o.logger.Warn("failed to marshal spawn telemetry summary",
			"spawn_id", state.SpawnID, "error", err)
		return
	}

	entry := map[string]any{
		"entry_type": "decision",
		"title":      fmt.Sprintf("Spawn %s: %s", state.SpawnID, status),
		"content":    string(content),
		"metadata": map[string]any{
			"spawn_id":   state.SpawnID,
			"agent_id":   state.AgentID,
			"agent_type": state.Request.AgentType,
			"namespace":  state.Request.Namespace,
			"status":     status,
		},
	}

	// Use a short, independent timeout — the spawn context may already be
	// canceled on error paths. ContextAdd itself doesn't accept a context,
	// so we run it in a goroutine with a timeout guard to avoid blocking
	// terminal state transitions on a slow MCP bridge.
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	go func() {
		defer cancel()
		errCh := make(chan error, 1)
		go func() {
			errCh <- o.agentBridge.ContextAdd("", []map[string]any{entry})
		}()
		select {
		case err := <-errCh:
			if err != nil {
				o.logger.Warn("failed to persist spawn telemetry summary",
					"spawn_id", state.SpawnID, "error", err)
			}
		case <-persistCtx.Done():
			o.logger.Warn("spawn telemetry summary persist timed out",
				"spawn_id", state.SpawnID)
		}
	}()
}

// GetSpawnTelemetry returns a snapshot of the current telemetry for a spawn.
// For running spawns, it reads from the live accumulator. For completed/failed
// spawns, telemetry is attached to the State directly.
func (o *SpawnOrchestrator) GetSpawnTelemetry(spawnID string) (*bridge.SpawnTelemetry, bool) {
	// Check live accumulator first (spawn still running).
	if accVal, ok := o.telemetry.Load(spawnID); ok {
		acc := accVal.(*bridge.SpawnTelemetryAccumulator)
		snap := acc.Snapshot()
		return &snap, true
	}
	// Fall back to completed state's attached telemetry.
	if state, ok := o.ctrl.Get(spawnID); ok && state.Telemetry != nil {
		return state.Telemetry, true
	}
	return nil, false
}

// DeleteSpawn removes a terminal spawn from the controller and persistent store.
func (o *SpawnOrchestrator) DeleteSpawn(ctx context.Context, spawnID string) error {
	return o.ctrl.Delete(ctx, spawnID)
}

// ListSpawns returns all spawn states.
func (o *SpawnOrchestrator) ListSpawns() []*SpawnState {
	return o.ctrl.List()
}

// GetSpawn returns a specific spawn state.
func (o *SpawnOrchestrator) GetSpawn(spawnID string) (*SpawnState, bool) {
	return o.ctrl.Get(spawnID)
}

// Wait blocks until the given spawn reaches a terminal state
// (completed / failed / stopped) or ctx is canceled. Returns the terminal
// SpawnState on success; ctx.Err() on cancellation; an error if the spawn
// ID does not exist.
//
// Implemented via polling (spawn.IsTerminal) every waitPollInterval rather
// than subscribing to the SSE hub because the hub's fan-out shape doesn't
// let a single waiter filter by spawn_id without delivery contention with
// browser clients. Polling is cheap — spawn state lookups are O(1) map
// reads — and spawn lifecycles are measured in minutes, so 500ms poll
// granularity is invisible to callers.
func (o *SpawnOrchestrator) Wait(ctx context.Context, spawnID string) (*SpawnState, error) {
	if _, ok := o.ctrl.Get(spawnID); !ok {
		return nil, fmt.Errorf("spawn %s not found", spawnID)
	}

	ticker := time.NewTicker(waitPollInterval)
	defer ticker.Stop()
	for {
		state, ok := o.ctrl.Get(spawnID)
		if !ok {
			return nil, fmt.Errorf("spawn %s disappeared while waiting", spawnID)
		}
		if spawn.IsTerminal(state.Status) {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// waitPollInterval is the cadence at which Wait() re-checks terminal
// state. Tuned for the minute-scale spawn lifecycle; low enough that
// Wait returns quickly after completion, high enough to keep polling
// overhead near zero.
const waitPollInterval = 500 * time.Millisecond

// Projects returns the configured project list for spawn pickers.
func (o *SpawnOrchestrator) Projects() []string { return o.projects }

// NewSpawnOrchestratorForTest builds a minimal SpawnOrchestrator backed by
// the given controller. Intended for external-package tests that need
// ListSpawns / GetSpawn / Wait but can't construct a full orchestrator
// because backend/sseHub/etc. fields are unexported. Do not use in
// production code paths.
func NewSpawnOrchestratorForTest(ctrl *spawn.K8sController) *SpawnOrchestrator {
	return &SpawnOrchestrator{ctrl: ctrl, logger: slog.Default()}
}

// runBudgetWatcher polls the spawn telemetry accumulator at a fixed interval
// and cancels the exec context when the configured cost or turn budget is
// exceeded. It records a structured error on the accumulator ("max_budget" or
// "max_turns") so the persisted telemetry captures the reason. The watcher
// exits when its own ctx is canceled (exec returned / parent canceled) or when
// done is closed by the caller after the exec returns.
func (o *SpawnOrchestrator) runBudgetWatcher(
	ctx context.Context,
	spawnID string,
	req SpawnRequest,
	acc *bridge.SpawnTelemetryAccumulator,
	cancelExec context.CancelFunc,
	done <-chan struct{},
) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			snap := acc.Snapshot()
			if req.MaxCostUSD > 0 && snap.TotalCostUSD >= req.MaxCostUSD {
				msg := fmt.Sprintf("spawn %s cost budget exceeded: $%.4f >= $%.4f",
					spawnID, snap.TotalCostUSD, req.MaxCostUSD)
				acc.AddError("max_budget", msg)
				o.logger.Warn("spawn budget exceeded, canceling exec",
					"spawn_id", spawnID, "cost_usd", snap.TotalCostUSD, "max_cost_usd", req.MaxCostUSD)
				cancelExec()
				return
			}
			if req.MaxTurns > 0 && snap.TurnCount >= req.MaxTurns {
				msg := fmt.Sprintf("spawn %s turn budget exceeded: %d >= %d",
					spawnID, snap.TurnCount, req.MaxTurns)
				acc.AddError("max_turns", msg)
				o.logger.Warn("spawn turn budget exceeded, canceling exec",
					"spawn_id", spawnID, "turns", snap.TurnCount, "max_turns", req.MaxTurns)
				cancelExec()
				return
			}
			// F5 / Slice C1: auto-handoff triggers. Nil-safe — skipped when the
			// hook is not installed or not enabled.
			o.evalAutoHandoff(ctx, spawnID, req, snap)
		}
	}
}

// evalAutoHandoff checks the current telemetry snapshot against the
// configured auto-handoff thresholds and, on a gate fire, creates a
// draft handoff. Additive, ≤25 lines.
func (o *SpawnOrchestrator) evalAutoHandoff(ctx context.Context, spawnID string, req SpawnRequest, snap bridge.SpawnTelemetry) {
	hook := o.autoHandoff
	if hook == nil {
		return
	}
	cfg := hook.Config()
	if !cfg.Enabled {
		return
	}
	var reason string
	switch {
	case cfg.InputTokenHigh > 0 && snap.TokenUsage.InputTokens >= cfg.InputTokenHigh:
		reason = "input_tokens"
	case cfg.CostUSDHigh > 0 && snap.TotalCostUSD >= cfg.CostUSDHigh:
		reason = "cost"
	}
	if !hook.Observe(spawnID, reason, time.Now()) {
		return
	}
	details := map[string]any{"input_tokens": snap.TokenUsage.InputTokens, "cost_usd": snap.TotalCostUSD, "turns": snap.TurnCount}
	if err := hook.Create(ctx, spawnID, req.AgentType, req.AgentType, reason, details); err != nil {
		o.logger.Warn("auto-handoff create failed", "spawn_id", spawnID, "reason", reason, "error", err)
	}
}

// runHeartbeatLoop sends periodic heartbeats for a spawned agent while it's running.
func (o *SpawnOrchestrator) runHeartbeatLoop(ctx context.Context, state *SpawnState) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if state.Status != SpawnStatusRunning {
				return
			}
			_, _ = o.agentBridge.PresenceHeartbeat(state.AgentID, bridge.PresenceHeartbeatParams{
				Status:      "active",
				CurrentTask: state.Request.TaskDescription,
				Branch:      state.Request.Branch,
			})
		}
	}
}

// broadcastSpawnEvent sends a spawn lifecycle event to the SSE hub.
// When the spawn was initiated by the weaver router (metadata carries
// weaver_query_id), also emits a one-shot agent.spawn.weaver_parent
// event on first broadcast so HUD clients can wire the "came from
// weaver query X" badge without polling the spawn detail endpoint.
func (o *SpawnOrchestrator) broadcastSpawnEvent(eventType string, state *SpawnState) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	o.sseHub.Broadcast(bridge.SSEEvent{
		ID:        fmt.Sprintf("%s-%s-%d", eventType, state.SpawnID, time.Now().UnixMilli()),
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	})

	// First lifecycle broadcast for a weaver-originated spawn also emits
	// a sidecar event carrying just the correlation keys. "agent.spawn.building"
	// is the earliest lifecycle event; later transitions don't re-broadcast
	// to avoid spamming the hub.
	if eventType == "agent.spawn.building" {
		o.broadcastWeaverParentIfApplicable(state)
	}
}

// broadcastWeaverParentIfApplicable emits agent.spawn.weaver_parent when
// the spawn's metadata carries weaver_query_id. No-op otherwise.
func (o *SpawnOrchestrator) broadcastWeaverParentIfApplicable(state *SpawnState) {
	queryID := state.Request.Metadata["weaver_query_id"]
	if queryID == "" {
		return
	}
	payload := map[string]string{
		"spawn_id":        state.SpawnID,
		"weaver_query_id": queryID,
		"weaver_domain":   state.Request.Metadata["weaver_domain"],
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	o.sseHub.Broadcast(bridge.SSEEvent{
		ID:        fmt.Sprintf("agent.spawn.weaver_parent-%s-%d", state.SpawnID, time.Now().UnixMilli()),
		Type:      "agent.spawn.weaver_parent",
		Timestamp: time.Now(),
		Data:      data,
	})
}

// Cluster-scoped secret names. These hold credentials tied to the cluster
// identity, decoupled from any developer's Mac Keychain. See
// .loom/87-product-spec-session-spawning-weaver-2026-04-19.md §AUTH.
const (
	// ClusterAgentAPIKeysSecret holds vendor API keys (ANTHROPIC_API_KEY,
	// OPENAI_API_KEY, GEMINI_API_KEY) and the Gemini service-account JSON,
	// all scoped to the cluster's identity.
	ClusterAgentAPIKeysSecret = "cluster-agent-api-keys"

	// ClusterAgentAuthSecret holds cluster-owned OAuth tokens for agents
	// that support subscription auth (Claude, Codex). Populated by
	// `loom auth cluster-login`; refreshed in-cluster by mcp-auth-refresher.
	// Unused in Slice 2a (API-key only); Slice 2b adds the OAuth mounts.
	ClusterAgentAuthSecret = "cluster-agent-auth"

	// GeminiSAKeyName is the Secret key that holds the full Google service
	// account JSON for Gemini. When present it is mounted as a file at
	// GeminiSAMountPath/sa.json so Gemini CLI can pick it up via the
	// standard GOOGLE_APPLICATION_CREDENTIALS env var.
	GeminiSAKeyName   = "GOOGLE_APPLICATION_CREDENTIALS_JSON"
	GeminiSAMountPath = "/home/agent/.gcp"
	GeminiSAFilename  = "sa.json"

	// AgentHomeDir is the writable HOME for spawned agents. The runtime
	// image creates user `agent` (uid 1000) with this as its home; secret
	// mounts and CLI state files all live under here so the non-root
	// process can traverse the path. Stay in sync with
	// agentRuntimeDockerfile().
	AgentHomeDir = "/home/agent"
)

// resolveAuthMode returns the cluster-credential path the spawn will use.
// Describes the *configured* auth path — the actual runtime fallback
// (e.g., OAuth file absent → API-key env) is reflected in pod telemetry,
// not here.
//
//   - claude-code, codex → cluster_oauth (both agents mount from
//     cluster-agent-auth with OAuth-or-API-key fallback; an empty cluster
//     secret silently degrades to API-key mode via env fallback)
//   - gemini             → cluster_service_account (SA JSON mount from
//     cluster-agent-api-keys; falls through to GEMINI_API_KEY env if the
//     SA JSON key is absent)
//
// When the cluster-agent-auth secret is empty, Claude and Codex at runtime
// effectively use cluster_api_key via $ANTHROPIC_API_KEY/$OPENAI_API_KEY.
// Reporting cluster_oauth here reflects operator intent; a follow-up slice
// can add pod-side AuthMode reporting for the runtime-actual value.
func resolveAuthMode(agentType string) spawn.AuthMode {
	switch agentType {
	case "gemini":
		return spawn.AuthModeClusterServiceAccount
	case "claude-code", "codex":
		return spawn.AuthModeClusterOAuth
	default:
		return ""
	}
}

// agentSecretEnvVars returns K8s secret env vars for the given agent type.
// Sources credentials from the cluster-scoped secret (ClusterAgentAPIKeysSecret)
// so pods never read the developer's Mac Keychain state.
func agentSecretEnvVars(agentType string) []backend.SecretEnvVar {
	secretName := ClusterAgentAPIKeysSecret
	switch agentType {
	case "claude-code":
		// CLAUDE_CODE_OAUTH_TOKEN is the officially documented headless auth path
		// (https://code.claude.com/docs/en/authentication, "Long-Lived Authentication
		// Token" section). Operators generate a 1-year token via `claude setup-token`
		// on a machine with an active Pro/Max/Team subscription, then set it on the
		// cluster-agent-auth secret under the claude-oauth-token key. When present,
		// Claude CLI prefers it over ANTHROPIC_API_KEY. The SecretEnvVar is Optional
		// so pods start cleanly when the key is absent; agent then falls back to
		// ANTHROPIC_API_KEY from cluster-agent-api-keys.
		return []backend.SecretEnvVar{
			{Name: "CLAUDE_CODE_OAUTH_TOKEN", SecretName: ClusterAgentAuthSecret, SecretKey: "claude-oauth-token"},
			{Name: "ANTHROPIC_API_KEY", SecretName: secretName, SecretKey: "ANTHROPIC_API_KEY"},
		}
	case "codex":
		return []backend.SecretEnvVar{
			{Name: "OPENAI_API_KEY", SecretName: secretName, SecretKey: "OPENAI_API_KEY"},
			{Name: "CODEX_API_KEY", SecretName: secretName, SecretKey: "OPENAI_API_KEY"},
		}
	case "gemini":
		return []backend.SecretEnvVar{
			{Name: "GEMINI_API_KEY", SecretName: secretName, SecretKey: "GEMINI_API_KEY"},
			{Name: "GOOGLE_API_KEY", SecretName: secretName, SecretKey: "GEMINI_API_KEY"},
		}
	default:
		return nil
	}
}

// agentSecretMounts returns K8s secret volume mounts for credential files
// that the agent CLI reads from disk. All sources come from cluster-scoped
// secrets; no developer-Mac state is ever mounted.
//
//   - Claude: mounts cluster-agent-auth's claude-oauth-json at
//     /root/.claude.auth/oauth.json when populated. Read-only; pod-side
//     refresh is the job of mcp-auth-refresher (Slice 2b.2). At runtime
//     the apiKeyHelper injected into .claude/settings.json prefers this
//     OAuth accessToken and falls back to $ANTHROPIC_API_KEY from
//     cluster-agent-api-keys.
//   - Codex: mounts cluster-agent-auth's codex-auth-json at
//     /root/.codex/auth.json when populated. Codex CLI reads this file
//     natively and falls back to $OPENAI_API_KEY.
//   - Gemini: mounts the service-account JSON from cluster-agent-api-keys
//     at /root/.gcp/sa.json. GOOGLE_APPLICATION_CREDENTIALS env pointing
//     at the file is set by runSpawn.
//
// All mounts are k8s-Optional (see buildPodSpec): a missing secret or key
// results in an absent file, not a pod-start failure. The agent CLIs
// handle missing OAuth files by falling back to env-var API keys.
func agentSecretMounts(agentType string) []backend.SecretMount {
	switch agentType {
	case "claude-code":
		return []backend.SecretMount{
			{
				SecretName: ClusterAgentAuthSecret,
				MountPath:  AgentHomeDir + "/.claude.auth",
				Items: []backend.SecretMountItem{
					{Key: "claude-oauth-json", Path: "oauth.json"},
				},
			},
		}
	case "codex":
		// Stage the OAuth file at /home/agent/.codex.auth/ (NOT
		// /home/agent/.codex/) so the injected config.toml and the
		// symlink to auth.json can coexist in /home/agent/.codex/
		// without the secret-volume mount shadowing injectAgentConfig's
		// writes. injectAgentConfig creates /home/agent/.codex/auth.json
		// as a symlink to the staging mount so kubelet-propagated secret
		// updates reach the CLI transparently.
		return []backend.SecretMount{
			{
				SecretName: ClusterAgentAuthSecret,
				MountPath:  AgentHomeDir + "/.codex.auth",
				Items: []backend.SecretMountItem{
					{Key: "codex-auth-json", Path: "auth.json"},
				},
			},
		}
	case "gemini":
		return []backend.SecretMount{
			{
				SecretName: ClusterAgentAPIKeysSecret,
				MountPath:  GeminiSAMountPath,
				Items: []backend.SecretMountItem{
					{Key: GeminiSAKeyName, Path: GeminiSAFilename},
				},
			},
		}
	default:
		return nil
	}
}

// agentCLIInstallLines returns Dockerfile RUN lines to install the agent CLI
// with pinned versions for reproducible builds.
func agentCLIInstallLines(agentType string) string {
	const ensureNPM = `RUN if ! command -v npm >/dev/null 2>&1; then \
    if command -v apk >/dev/null 2>&1; then \
      apk add --no-cache nodejs npm; \
    elif command -v apt-get >/dev/null 2>&1; then \
      apt-get update && apt-get install -y --no-install-recommends nodejs npm && rm -rf /var/lib/apt/lists/*; \
    else \
      echo "npm is required to install agent CLI" >&2; exit 1; \
    fi; \
  fi`

	switch agentType {
	case "claude-code":
		return fmt.Sprintf("%s\nRUN npm install -g @anthropic-ai/claude-code@%s", ensureNPM, claudeCodeVersion)
	case "codex":
		return fmt.Sprintf("%s\nRUN npm install -g @openai/codex@%s", ensureNPM, codexVersion)
	case "gemini":
		return fmt.Sprintf("%s\nRUN npm install -g @google/gemini-cli@%s", ensureNPM, geminiVersion)
	default:
		return ""
	}
}
