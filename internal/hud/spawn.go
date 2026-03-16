package hud

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/internal/devbox/backend"
	"github.com/crb2nu/loom/internal/devbox/detect"
	"github.com/crb2nu/loom/internal/devbox/dockerfile"
	"github.com/crb2nu/loom/internal/hud/bridge"
)

// SpawnStatus tracks the lifecycle state of a spawned agent.
type SpawnStatus string

const (
	SpawnStatusCreating  SpawnStatus = "creating"
	SpawnStatusBuilding  SpawnStatus = "building"
	SpawnStatusRunning   SpawnStatus = "running"
	SpawnStatusCompleted SpawnStatus = "completed"
	SpawnStatusFailed    SpawnStatus = "failed"
	SpawnStatusStopped   SpawnStatus = "stopped"
)

// SpawnRequest contains the parameters for spawning a headless agent.
type SpawnRequest struct {
	AgentType       string  `json:"agent_type"`       // "claude-code", "codex", "gemini"
	Namespace       string  `json:"namespace"`        // Agent context namespace.
	Branch          string  `json:"branch"`           // Git branch to work on.
	BaseBranch      string  `json:"base_branch"`      // Base branch for worktree.
	TaskDescription string  `json:"task_description"` // Task to execute.
	Project         string  `json:"project"`          // Project/repo name.
	MemoryMB        int     `json:"memory_mb"`        // Container memory limit.
	CPUs            float64 `json:"cpus"`             // Container CPU limit.
	TimeoutMinutes  int     `json:"timeout_minutes"`  // Max runtime before reap.
}

// SpawnState holds the state of a spawned agent.
type SpawnState struct {
	SpawnID   string       `json:"spawn_id"`
	AgentID   string       `json:"agent_id"`
	PodName   string       `json:"pod_name"`
	Status    SpawnStatus  `json:"status"`
	Request   SpawnRequest `json:"request"`
	StartedAt time.Time    `json:"started_at"`
	EndedAt   *time.Time   `json:"ended_at,omitempty"`
	Error     string       `json:"error,omitempty"`
}

// SpawnOrchestrator manages the full lifecycle of headless agent spawns.
type SpawnOrchestrator struct {
	backend     backend.Backend
	agentBridge *bridge.AgentBridge
	sseHub      *SSEHub
	tracer      trace.Tracer
	metrics     *HUDMetrics
	logger      *slog.Logger

	mu     sync.RWMutex
	spawns map[string]*SpawnState

	// Limits.
	maxConcurrent  int
	defaultTimeout time.Duration
	defaultMemory  int
	defaultCPUs    float64

	// workspaceRoot is the local path to the workspace mount (for project detection).
	workspaceRoot string
	// projects lists available project names for the spawn picker.
	projects []string
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

// NewSpawnOrchestrator creates a new spawn orchestrator.
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
	return &SpawnOrchestrator{
		backend:        b,
		agentBridge:    agentBridge,
		sseHub:         sseHub,
		tracer:         tracer,
		metrics:        metrics,
		logger:         logger.With("component", "spawn"),
		spawns:         make(map[string]*SpawnState),
		maxConcurrent:  cfg.MaxConcurrent,
		defaultTimeout: cfg.DefaultTimeout,
		defaultMemory:  cfg.DefaultMemory,
		defaultCPUs:    cfg.DefaultCPUs,
		workspaceRoot:  wsRoot,
		projects:       cfg.Projects,
	}
}

// Spawn starts a new headless agent. Returns the spawn ID immediately (202).
// The actual spawn runs asynchronously in a goroutine.
func (o *SpawnOrchestrator) Spawn(ctx context.Context, req SpawnRequest) (string, error) {
	// Validate request.
	if req.AgentType == "" {
		req.AgentType = "claude-code"
	}
	switch req.AgentType {
	case "claude-code", "codex", "gemini":
		// ok
	default:
		return "", fmt.Errorf("unsupported agent type: %s", req.AgentType)
	}
	if req.TaskDescription == "" {
		return "", fmt.Errorf("task_description is required")
	}
	if req.Project == "" {
		return "", fmt.Errorf("project is required")
	}

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
	if o.activeCount() >= o.maxConcurrent {
		return "", fmt.Errorf("max concurrent spawns reached (%d)", o.maxConcurrent)
	}

	spawnID := newSpawnID()
	agentID := fmt.Sprintf("spawn-%s-%s", req.AgentType, spawnID[6:])

	state := &SpawnState{
		SpawnID:   spawnID,
		AgentID:   agentID,
		Status:    SpawnStatusCreating,
		Request:   req,
		StartedAt: time.Now(),
	}

	o.mu.Lock()
	o.spawns[spawnID] = state
	o.mu.Unlock()

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

	o.mu.RLock()
	state := o.spawns[spawnID]
	o.mu.RUnlock()

	projectDir := o.workspaceRoot + "/" + req.Project

	// Step 1: Detect project environment and generate Dockerfile.
	o.mu.Lock()
	state.Status = SpawnStatusBuilding
	o.mu.Unlock()
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

	o.mu.Lock()
	state.PodName = startResult.ContainerID
	o.mu.Unlock()

	// Step 3: Inject pre-authed configs (with a short timeout to avoid hanging on SPDY issues).
	_, cfgSpan := o.tracer.Start(ctx, "agent.spawn.config_inject")
	cfgCtx, cfgCancel := context.WithTimeout(ctx, 30*time.Second)
	o.logger.Info("injecting agent config", "spawn_id", spawnID, "pod", startResult.ContainerID, "agent_type", req.AgentType)
	if err := o.injectAgentConfig(cfgCtx, startResult.ContainerID, req.AgentType); err != nil {
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
	o.mu.Lock()
	state.Status = SpawnStatusRunning
	o.mu.Unlock()
	o.broadcastSpawnEvent("agent.spawn.running", state)

	// Step 5: Execute agent CLI (blocking until agent exits or timeout).
	o.logger.Info("executing agent", "spawn_id", spawnID, "agent_type", req.AgentType, "pod", startResult.ContainerID)
	_, execSpan := o.tracer.Start(ctx, "agent.spawn.agent_exec")
	agentCmd := buildAgentCommand(req.AgentType, req.TaskDescription, state.AgentID)
	_, execErr := o.backend.Exec(ctx, backend.ExecOpts{
		ContainerID: startResult.ContainerID,
		Command:     agentCmd,
		WorkDir:     "/workspace/" + req.Project,
		TimeoutSec:  req.TimeoutMinutes * 60,
	})
	execSpan.End()

	// Step 6: Finalize based on exec result.
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
    git make curl ca-certificates nodejs npm && \
    rm -rf /var/lib/apt/lists/*
WORKDIR /workspace
`)
}

// injectAgentConfig writes platform-specific config files into the pod.
// Uses Exec (stdout-only SPDY) instead of WriteFile (stdin SPDY) to avoid
// in-cluster SPDY stdin stream hangs observed on K3s.
func (o *SpawnOrchestrator) injectAgentConfig(ctx context.Context, containerID, agentType string) error {
	writeCmd := func(dir, file, content string) error {
		cmd := fmt.Sprintf("mkdir -p %s && cat > %s/%s << 'AGENTCFG'\n%s\nAGENTCFG", dir, dir, file, content)
		_, err := o.backend.Exec(ctx, backend.ExecOpts{
			ContainerID: containerID,
			Command:     cmd,
			TimeoutSec:  30,
		})
		return err
	}

	switch agentType {
	case "claude-code":
		settings := `{"permissions":{"allow":["Bash","Read","Write","Edit","Glob","Grep"]}}`
		if err := writeCmd("/workspace/.claude", "settings.json", settings); err != nil {
			return fmt.Errorf("write claude settings: %w", err)
		}
	case "codex":
		config := "[agent]\napproval = \"auto-edit\"\n"
		if err := writeCmd("/workspace/.codex", "config.toml", config); err != nil {
			return fmt.Errorf("write codex config: %w", err)
		}
	case "gemini":
		settings := `{"permissions":{"allow_all":true}}`
		if err := writeCmd("/workspace/.gemini", "settings.json", settings); err != nil {
			return fmt.Errorf("write gemini settings: %w", err)
		}
	}
	return nil
}

// buildAgentCommand constructs the CLI command to run the agent headlessly.
func buildAgentCommand(agentType, task, agentID string) string {
	switch agentType {
	case "claude-code":
		return fmt.Sprintf(`claude --headless --task %q --agent-id %q`, task, agentID)
	case "codex":
		return fmt.Sprintf(`codex --approval auto-edit %q`, task)
	case "gemini":
		return fmt.Sprintf(`gemini --headless %q`, task)
	default:
		return fmt.Sprintf(`echo "Unsupported agent type: %s"`, agentType)
	}
}

// newSpawnID generates a unique spawn ID using crypto/rand.
func newSpawnID() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("spawn-%d", time.Now().UnixNano())
	}
	return "spawn-" + hex.EncodeToString(buf[:])
}

// StopSpawn stops a running spawned agent.
func (o *SpawnOrchestrator) StopSpawn(ctx context.Context, spawnID string) error {
	o.mu.Lock()
	state, ok := o.spawns[spawnID]
	if !ok {
		o.mu.Unlock()
		return fmt.Errorf("spawn %s not found", spawnID)
	}
	o.mu.Unlock()

	if state.PodName != "" {
		if err := o.backend.Stop(ctx, state.PodName); err != nil {
			o.logger.Warn("failed to stop spawn pod", "spawn_id", spawnID, "error", err)
		}
	}

	o.mu.Lock()
	state.Status = SpawnStatusStopped
	now := time.Now()
	state.EndedAt = &now
	o.mu.Unlock()

	if o.metrics != nil {
		o.metrics.SpawnedAgentActive.Add(ctx, -1)
	}

	o.broadcastSpawnEvent("agent.spawn.stopped", state)
	return nil
}

// failSpawn marks a spawn as failed and broadcasts the event.
func (o *SpawnOrchestrator) failSpawn(ctx context.Context, state *SpawnState, reason string) {
	o.mu.Lock()
	state.Status = SpawnStatusFailed
	state.Error = reason
	now := time.Now()
	state.EndedAt = &now
	o.mu.Unlock()

	if o.metrics != nil {
		o.metrics.SpawnedAgentActive.Add(ctx, -1)
		o.metrics.AgentSpawnTotal.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("agent_type", state.Request.AgentType),
				attribute.String("outcome", "failed"),
			),
		)
	}

	o.logger.Error("spawn failed", "spawn_id", state.SpawnID, "reason", reason)
	o.broadcastSpawnEvent("agent.spawn.failed", state)
}

// completeSpawn marks a spawn as completed.
func (o *SpawnOrchestrator) completeSpawn(ctx context.Context, state *SpawnState) {
	o.mu.Lock()
	state.Status = SpawnStatusCompleted
	now := time.Now()
	state.EndedAt = &now
	o.mu.Unlock()

	if o.metrics != nil {
		o.metrics.SpawnedAgentActive.Add(ctx, -1)
		o.metrics.AgentSpawnTotal.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("agent_type", state.Request.AgentType),
				attribute.String("outcome", "completed"),
			),
		)
	}

	o.logger.Info("spawn completed", "spawn_id", state.SpawnID)
	o.broadcastSpawnEvent("agent.spawn.completed", state)
}

// ListSpawns returns all spawn states.
func (o *SpawnOrchestrator) ListSpawns() []*SpawnState {
	o.mu.RLock()
	defer o.mu.RUnlock()
	result := make([]*SpawnState, 0, len(o.spawns))
	for _, s := range o.spawns {
		result = append(result, s)
	}
	return result
}

// GetSpawn returns a specific spawn state.
func (o *SpawnOrchestrator) GetSpawn(spawnID string) (*SpawnState, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	s, ok := o.spawns[spawnID]
	return s, ok
}

// activeCount returns the number of spawns in creating or running state.
func (o *SpawnOrchestrator) activeCount() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	count := 0
	for _, s := range o.spawns {
		if s.Status == SpawnStatusCreating || s.Status == SpawnStatusBuilding || s.Status == SpawnStatusRunning {
			count++
		}
	}
	return count
}

// Projects returns the configured project list for spawn pickers.
func (o *SpawnOrchestrator) Projects() []string { return o.projects }

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
	default:
		// Claude Code uses ANTHROPIC_API_KEY env var (no auth file on disk).
		return nil
	}
}

// agentCLIInstallLines returns Dockerfile RUN lines to install the agent CLI.
func agentCLIInstallLines(agentType string) string {
	switch agentType {
	case "claude-code":
		return "RUN npm install -g @anthropic-ai/claude-code@latest 2>/dev/null || true"
	case "codex":
		return "RUN npm install -g @openai/codex@latest 2>/dev/null || true"
	case "gemini":
		return "RUN npm install -g @anthropic-ai/claude-code@latest 2>/dev/null || true\nRUN pip install google-genai 2>/dev/null || true"
	default:
		return ""
	}
}
