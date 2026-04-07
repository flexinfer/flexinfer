// Package spawn provides a standalone controller for managing headless agent
// spawn lifecycles. It extracts spawn orchestration out of the HUD package,
// adding K8s-native state reconciliation so pod labels become the source of
// truth instead of local JSON files.
package spawn

import (
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// Status tracks the lifecycle state of a spawned agent.
type Status string

const (
	StatusPending  Status = "creating"
	StatusBuilding Status = "building"
	StatusRunning  Status = "running"
	StatusStopped  Status = "stopped"
	StatusFailed   Status = "failed"
	StatusUnknown  Status = "unknown"

	// StatusCompleted indicates the agent finished its task successfully.
	StatusCompleted Status = "completed"
)

// Request contains the parameters for spawning a headless agent.
type Request struct {
	AgentType       string  `json:"agent_type"`       // "claude-code", "codex", "gemini"
	Namespace       string  `json:"namespace"`        // Agent context namespace.
	Branch          string  `json:"branch"`           // Git branch to work on.
	BaseBranch      string  `json:"base_branch"`      // Base branch for worktree.
	TaskDescription string  `json:"task_description"` // Task to execute.
	Project         string  `json:"project"`          // Project/repo name.
	MemoryMB        int     `json:"memory_mb"`        // Container memory limit.
	CPUs            float64 `json:"cpus"`             // Container CPU limit.
	TimeoutMinutes  int     `json:"timeout_minutes"`  // Max runtime before reap.
	// MaxCostUSD caps total spawn cost in USD. The budget watcher cancels the
	// exec when the accumulated cost meets or exceeds this value. 0 = unlimited.
	MaxCostUSD float64 `json:"max_cost_usd,omitempty"`
	// MaxTurns caps total agent turns. The budget watcher cancels the exec
	// when the accumulated turn count meets or exceeds this value. 0 = unlimited.
	MaxTurns int `json:"max_turns,omitempty"`
}

// State holds the state of a spawned agent.
type State struct {
	SpawnID   string                 `json:"spawn_id"`
	AgentID   string                 `json:"agent_id"`
	PodName   string                 `json:"pod_name"`
	Status    Status                 `json:"status"`
	Request   Request                `json:"request"`
	StartedAt time.Time              `json:"started_at"`
	EndedAt   *time.Time             `json:"ended_at,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Telemetry *bridge.SpawnTelemetry `json:"telemetry,omitempty"`
}

// IsTerminal returns true if the status represents a terminal spawn state.
func IsTerminal(status Status) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusStopped:
		return true
	default:
		return false
	}
}

// ManagedByLabel is the Kubernetes label used to identify pods managed by the
// spawn controller.
const ManagedByLabel = "app.kubernetes.io/managed-by"

// ManagedByValue is the label value applied to spawn-managed pods.
const ManagedByValue = "loom-spawn"

// SpawnIDLabel is the label key for the spawn ID on managed pods.
const SpawnIDLabel = "loom.dev/spawn-id"

// AgentIDLabel is the label key for the agent ID on managed pods.
const AgentIDLabel = "loom.dev/agent-id"
