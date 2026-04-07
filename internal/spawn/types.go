// Package spawn provides a standalone controller for managing headless agent
// spawn lifecycles. It extracts spawn orchestration out of the HUD package,
// adding K8s-native state reconciliation so pod labels become the source of
// truth instead of local JSON files.
package spawn

import (
	"errors"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// ControlCommand is the wire format the spawn-driver consumes over its JSONL
// control file. The Go orchestrator serializes one of these per line. The
// REST layer (admin + mobile) accepts the same shape as its request body so
// web and mobile clients can push follow-up turns or cancellations into a
// long-lived multi-turn spawn.
//
// Type discriminates payload semantics:
//
//   - "message"   : push a follow-up user turn (Text required)
//   - "interrupt" : abort the in-flight generation (no payload)
//   - "shutdown"  : graceful exit after the current turn completes
type ControlCommand struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Control command type discriminators. Keep in sync with the driver-side
// ControlCommand union in tools/spawn-driver/src/control-file.ts.
const (
	ControlCommandMessage   = "message"
	ControlCommandInterrupt = "interrupt"
	ControlCommandShutdown  = "shutdown"
)

// Control plane sentinel errors. Handlers map these to HTTP status codes so
// the HUD web UI and mobile client can surface precise failure reasons.
var (
	// ErrSpawnNotFound indicates the spawn ID is unknown to the controller.
	ErrSpawnNotFound = errors.New("spawn not found")
	// ErrSpawnNotRunning indicates the spawn exists but is in a terminal
	// state (completed/failed/stopped) and cannot receive control commands.
	ErrSpawnNotRunning = errors.New("spawn is not running")
	// ErrSpawnNotMultiTurn indicates the spawn was created without the
	// multi_turn flag and therefore has no control file to append to.
	ErrSpawnNotMultiTurn = errors.New("spawn is not multi-turn")
	// ErrInvalidControlCommand indicates the command failed validation
	// (missing type, empty message text, or unknown type).
	ErrInvalidControlCommand = errors.New("invalid control command")
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
	// UseSDKDriver routes the spawn through the embedded loom-spawn-driver
	// Node.js sidecar instead of invoking the agent CLI directly. The driver
	// is injected into the pod via injectSDKDriver and emits parser-compatible
	// JSONL on stdout. Slice 7a/7b ship a hand-written stub bundle; Slice 7c
	// will swap in a real SDK-backed bundle. Defaults to false (legacy CLI path).
	UseSDKDriver bool `json:"use_sdk_driver,omitempty"`
	// MultiTurn enables long-lived conversational mode for the spawn driver.
	// When set, the orchestrator pre-creates an empty JSONL control file in
	// the pod and passes its path to the driver via --control-file. The
	// driver tails the file for `{type:"message"|"interrupt"|"shutdown"}`
	// commands so the HUD/mobile REST endpoints (slice 8c) can push
	// follow-up turns and cancellations into a running session. Requires
	// UseSDKDriver=true; ignored on the legacy CLI path. Defaults to false
	// for full backwards compatibility with single-shot spawns.
	MultiTurn bool `json:"multi_turn,omitempty"`
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
