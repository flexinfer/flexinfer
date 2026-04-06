package hud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/crb2nu/loom/internal/devbox/backend"
	"github.com/crb2nu/loom/internal/devbox/detect"
	"github.com/crb2nu/loom/internal/devbox/dockerfile"
	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/spawn"
)

// Pinned CLI versions for reproducible agent container builds.
const (
	claudeCodeVersion = "1.0.33"
	codexVersion      = "0.1.2025062000"
	geminiVersion     = "0.3.7"
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
	MaxConcurrent  int
	DefaultTimeout time.Duration
	DefaultMemory  int // MB
	DefaultCPUs    float64
	WorkspaceRoot  string   // local path to workspace mount (for project detection)
	Projects       []string // available projects for spawn picker (from SPAWN_PROJECTS env)
}

// DefaultSpawnConfig returns sensible defaults.
func DefaultSpawnConfig() SpawnOrchestratorConfig {
	wsRoot := "/workspace"
	if home, err := os.UserHomeDir(); err == nil {
		wsRoot = home + "/workspace"
	}
	return SpawnOrchestratorConfig{
		MaxConcurrent:  3,
		DefaultTimeout: 60 * time.Minute,
		DefaultMemory:  4096,
		DefaultCPUs:    2.0,
		WorkspaceRoot:  wsRoot,
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

	// Initialize persistent spawn store (FileStore for backward compat).
	var store spawn.Store
	storeDir := spawn.DefaultStoreDir()
	if fs, err := spawn.NewFileStore(storeDir); err != nil {
		spawnLogger.Warn("failed to create spawn store, state will not be persisted",
			"dir", storeDir, "error", err)
	} else {
		store = fs
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

	return &SpawnOrchestrator{
		backend:        b,
		agentBridge:    agentBridge,
		sseHub:         sseHub,
		tracer:         tracer,
		metrics:        metrics,
		logger:         spawnLogger,
		ctrl:           ctrl,
		maxConcurrent:  cfg.MaxConcurrent,
		defaultTimeout: cfg.DefaultTimeout,
		defaultMemory:  cfg.DefaultMemory,
		defaultCPUs:    cfg.DefaultCPUs,
		workspaceRoot:  wsRoot,
		projects:       cfg.Projects,
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

// newSpawnParser creates the appropriate JSONL parser for the given agent type.
// Returns nil for agent types that don't support structured telemetry.
func newSpawnParser(agentType string, sink SpawnEventSink, agentID string, broadcast SpawnEventBroadcaster, logger *slog.Logger) SpawnLineParser {
	switch agentType {
	case "claude-code":
		return NewClaudeJSONLParser(sink, agentID, broadcast, logger)
	case "codex":
		return NewCodexJSONLParser(sink, agentID, broadcast, logger)
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

	projectDir := o.workspaceRoot + "/" + req.Project

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
	buildTag := fmt.Sprintf("%s-spawn-%s", req.Project, spawnID[6:])
	buildResult, err := o.backend.Build(ctx, backend.BuildOpts{
		Tag:        buildTag,
		Dockerfile: df,
		ContextDir: projectDir,
	})
	buildSpan.End()
	if err != nil {
		o.failSpawn(ctx, state, fmt.Sprintf("image build failed: %v", err))
		return
	}

	// Step 2: Start K8s pod.
	o.logger.Info("build completed, starting pod", "spawn_id", spawnID, "image", buildResult.ImageTag)
	_, podSpan := o.tracer.Start(ctx, "agent.spawn.pod_create")
	startResult, err := o.backend.Start(ctx, backend.StartOpts{
		Name:     "spawn-" + spawnID,
		ImageTag: buildResult.ImageTag,
		WorkDir:  "/workspace/" + req.Project,
		Env: map[string]string{
			"AGENT_ID":  state.AgentID,
			"SPAWN_ID":  spawnID,
			"NAMESPACE": req.Namespace,
		},
		SecretEnv:    agentSecretEnvVars(req.AgentType),
		SecretMounts: agentSecretMounts(req.AgentType),
		MemoryMB:     req.MemoryMB,
		CPUs:         req.CPUs,
		Network:      true,
		AgentID:      state.AgentID,
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
	if err := o.injectAgentConfig(cfgCtx, startResult.ContainerID, req.AgentType, req.Project); err != nil {
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

	// Step 6: Execute agent CLI with real-time JSONL telemetry parsing.
	o.logger.Info("executing agent", "spawn_id", spawnID, "agent_type", req.AgentType, "pod", startResult.ContainerID)
	_, execSpan := o.tracer.Start(ctx, "agent.spawn.agent_exec")
	agentCmd := buildAgentCommand(req.AgentType, req.TaskDescription, state.AgentID)

	// Create telemetry accumulator and JSONL parser for real-time parsing.
	acc := bridge.NewSpawnTelemetryAccumulator()
	o.telemetry.Store(spawnID, acc)

	broadcaster := SpawnEventBroadcaster(func(eventType string, agentID string, data any) {
		o.broadcastTelemetryEvent(eventType, agentID, data)
	})
	parser := newSpawnParser(req.AgentType, acc, state.AgentID, broadcaster, o.logger)

	var execResult *backend.ExecResult
	var execErr error

	// Use streaming exec if backend supports it and we have a parser; fall back to buffered.
	if sec, ok := o.backend.(streamExecCapable); ok && parser != nil {
		execResult, execErr = backend.StreamExec(ctx,
			sec.Clientset(), sec.RestConfig(), sec.Namespace(), sec.NFSFlush(),
			backend.StreamExecOpts{
				ContainerID: startResult.ContainerID,
				Command:     agentCmd,
				WorkDir:     "/workspace/" + req.Project,
				TimeoutSec:  req.TimeoutMinutes * 60,
				OnLine: func(line []byte) {
					parser.HandleLine(line)
				},
			},
		)
	} else {
		// Fallback: buffered exec (no real-time telemetry).
		execResult, execErr = o.backend.Exec(ctx, backend.ExecOpts{
			ContainerID: startResult.ContainerID,
			Command:     agentCmd,
			WorkDir:     "/workspace/" + req.Project,
			TimeoutSec:  req.TimeoutMinutes * 60,
		})
	}
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

// generateDockerfile detects the project environment and generates a Dockerfile.
// Falls back to a minimal Go-based Dockerfile if detection fails.
// Appends agent CLI install lines so the spawned pod has the right agent binary.
func (o *SpawnOrchestrator) generateDockerfile(projectDir, agentType string) ([]byte, error) {
	var df []byte

	fp, err := detect.Fingerprint(projectDir)
	if err != nil {
		o.logger.Warn("project detection failed, using fallback Dockerfile", "dir", projectDir, "error", err)
		df = fallbackDockerfile()
	} else {
		generated, genErr := dockerfile.Generate(fp)
		if genErr != nil {
			o.logger.Warn("dockerfile generation failed, using fallback", "dir", projectDir, "error", genErr)
			df = fallbackDockerfile()
		} else {
			df = generated
		}
	}

	// Append agent CLI installation lines.
	if cliLines := agentCLIInstallLines(agentType); cliLines != "" {
		df = append(df, []byte("\n"+cliLines+"\n")...)
	}
	return df, nil
}

// fallbackDockerfile returns a minimal Dockerfile for projects where detection fails.
func fallbackDockerfile() []byte {
	return []byte(`FROM golang:1.24-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends \
    git make curl ca-certificates nodejs npm python3 python3-pip && \
    rm -rf /var/lib/apt/lists/*
WORKDIR /workspace
`)
}

// injectAgentConfig writes platform-specific config files into the pod.
// Uses Exec (stdout-only SPDY) instead of WriteFile (stdin SPDY) to avoid
// in-cluster SPDY stdin stream hangs observed on K3s.
func (o *SpawnOrchestrator) injectAgentConfig(ctx context.Context, containerID, agentType, project string) error {
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

	projectDir := "/workspace/" + project
	switch agentType {
	case "claude-code":
		// Claude Code reads project-level .claude/settings.json for permissions.
		// apiKeyHelper extracts the subscription accessToken from the mounted
		// OAuth JSON (synced from macOS Keychain), falling back to API key env var.
		// The OAuth file is mounted at /root/.claude.auth/oauth.json (separate from
		// .claude/ to avoid volume mount shadowing the injected settings.json).
		settings := `{"permissions":{"allow":["Bash","Read","Write","Edit","Glob","Grep"]},"apiKeyHelper":"python3 -c \"import json,sys; d=json.load(open('/root/.claude.auth/oauth.json')); print(d['claudeAiOauth']['accessToken'])\" 2>/dev/null || echo $ANTHROPIC_API_KEY"}`
		if err := writeCmd(projectDir+"/.claude", "settings.json", settings); err != nil {
			return fmt.Errorf("write claude settings: %w", err)
		}
	case "codex":
		// Full Codex config with sandbox and multi-agent features.
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
		if err := writeCmd("/root/.codex", "config.toml", config); err != nil {
			return fmt.Errorf("write codex config: %w", err)
		}
	case "gemini":
		// Gemini reads ~/.gemini/settings.json for permissions.
		settings := `{"permissions":{"allow_all":true}}`
		if err := writeCmd("/root/.gemini", "settings.json", settings); err != nil {
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
		return fmt.Sprintf(`claude -p %q --dangerously-skip-permissions --output-format stream-json --max-turns 50`, task)
	case "codex":
		// Wrap with EXIT trap so loom session-end fires even without a native hook.
		// The trap is best-effort: if the loom binary is not in the pod PATH,
		// stderr is suppressed via 2>/dev/null and the HUD-side completeSpawn /
		// failSpawn will still call EndSession as a fallback.
		return fmt.Sprintf(
			`trap 'loom agent session-end --agent-id %q --summarize --summary-async --quiet 2>/dev/null' EXIT; codex exec --full-auto --json %q`,
			agentID, task,
		)
	case "gemini":
		return fmt.Sprintf(`gemini -p %q --yolo`, task)
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

// failSpawn marks a spawn as failed, cleans up the K8s pod, and broadcasts the event.
func (o *SpawnOrchestrator) failSpawn(ctx context.Context, state *SpawnState, reason string) {
	// Attach partial telemetry snapshot (valuable for debugging failures).
	if accVal, ok := o.telemetry.LoadAndDelete(state.SpawnID); ok {
		acc := accVal.(*bridge.SpawnTelemetryAccumulator)
		snap := acc.Snapshot()
		state.Telemetry = &snap
	}

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

// ListSpawns returns all spawn states.
func (o *SpawnOrchestrator) ListSpawns() []*SpawnState {
	return o.ctrl.List()
}

// GetSpawn returns a specific spawn state.
func (o *SpawnOrchestrator) GetSpawn(spawnID string) (*SpawnState, bool) {
	return o.ctrl.Get(spawnID)
}

// Projects returns the configured project list for spawn pickers.
func (o *SpawnOrchestrator) Projects() []string { return o.projects }

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
}

// agentSecretEnvVars returns K8s secret env vars for the given agent type.
// These provide API key fallback authentication when subscription auth tokens
// aren't available.
func agentSecretEnvVars(agentType string) []backend.SecretEnvVar {
	const secretName = "agent-api-keys"
	switch agentType {
	case "claude-code":
		return []backend.SecretEnvVar{
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

// agentSecretMounts returns K8s secret volume mounts for subscription auth
// token files. These are mounted at the CLI's expected home-dir config paths
// so agents authenticate using existing subscription accounts.
func agentSecretMounts(agentType string) []backend.SecretMount {
	const secretName = "agent-auth-tokens"
	switch agentType {
	case "codex":
		// Codex CLI reads ~/.codex/auth.json for OAuth subscription tokens.
		return []backend.SecretMount{
			{
				SecretName: secretName,
				MountPath:  "/root/.codex",
				Items: []backend.SecretMountItem{
					{Key: "codex-auth-json", Path: "auth.json"},
				},
			},
		}
	case "gemini":
		// Gemini CLI reads ~/.gemini/oauth_creds.json and google_accounts.json.
		return []backend.SecretMount{
			{
				SecretName: secretName,
				MountPath:  "/root/.gemini",
				Items: []backend.SecretMountItem{
					{Key: "gemini-oauth-creds-json", Path: "oauth_creds.json"},
					{Key: "gemini-google-accounts-json", Path: "google_accounts.json"},
				},
			},
		}
	case "claude-code":
		// Claude Code subscription OAuth token (synced from macOS Keychain).
		// Mounted as /root/.claude/oauth.json; apiKeyHelper in settings.json
		// extracts the accessToken at runtime, falling back to ANTHROPIC_API_KEY.
		return []backend.SecretMount{
			{
				SecretName: secretName,
				MountPath:  "/root/.claude.auth",
				Items: []backend.SecretMountItem{
					{Key: "claude-oauth-json", Path: "oauth.json"},
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
	switch agentType {
	case "claude-code":
		return fmt.Sprintf("RUN npm install -g @anthropic-ai/claude-code@%s", claudeCodeVersion)
	case "codex":
		return fmt.Sprintf("RUN npm install -g @openai/codex@%s", codexVersion)
	case "gemini":
		return fmt.Sprintf("RUN npm install -g @google/gemini-cli@%s", geminiVersion)
	default:
		return ""
	}
}
