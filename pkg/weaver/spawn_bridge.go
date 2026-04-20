// Package weaver — spawn_bridge.go defines the integration seam between
// weaver's subagent dispatch and the headless-agent spawn system.
//
// The Router itself does not import internal/spawn or internal/hud; instead
// it consumes the SpawnBridge interface. The concrete DaemonSpawnBridge is
// implemented in internal/daemon/ (Slice 4 of the session+auth+weaver plan)
// and satisfied by the SpawnOrchestrator, keeping pkg/weaver importable
// from standalone tools (e.g. cmd/mcp-weaver) that don't link the HUD.
package weaver

import (
	"context"
	"errors"
	"fmt"
)

// ErrSpawnBridgeNotConfigured is returned when a SubAgent declares a
// non-flexinfer Backend but the Router has no SpawnBridge wired in. It is
// a structured, stable error so daemon handlers can map it to a clear
// operator-facing message without string matching.
var ErrSpawnBridgeNotConfigured = errors.New("weaver: spawn bridge not configured for non-flexinfer backend")

// SpawnBridge dispatches a weaver SubAgent to a real headless-agent pod
// (Claude Code / Codex / Gemini), waits for a terminal state, and returns
// a BridgeResult shaped for folding into a weaver DomainResult.
//
// Implementations must:
//   - Translate SubAgent.SpawnOverrides + the incoming query into a
//     spawn.Request whose AgentType matches SubAgent.Backend.
//   - Propagate parentSessionID into the spawn's env (LOOM_PARENT_SESSION_ID)
//     so downstream CLI hooks can join to the caller's proxy session.
//   - Record weaverQueryID and the source domain on the spawn's metadata so
//     the HUD can render "spawn X came from weaver query Y".
//   - Honor ctx cancellation: on ctx.Done before terminal state, cancel the
//     underlying spawn and return ctx.Err().
type SpawnBridge interface {
	Dispatch(ctx context.Context, agent SubAgent, query, parentSessionID, weaverQueryID string) (BridgeResult, error)
}

// BridgeResult is the shape the Router folds into a DomainResult when a
// subagent runs on a real headless-agent pod instead of FlexInfer. It
// intentionally mirrors only the fields weaver needs — the bridge
// implementation retains full SpawnState access for its own telemetry.
type BridgeResult struct {
	SpawnID      string  `json:"spawn_id"`
	LastMessage  string  `json:"last_message"`
	ToolCalls    int     `json:"tool_calls"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	StopReason   string  `json:"stop_reason"`
	Tokens       int     `json:"tokens"`
}

// NoopSpawnBridge is the default SpawnBridge installed when a Router is
// built without an explicit bridge. It always returns
// ErrSpawnBridgeNotConfigured so non-flexinfer subagents fail fast with
// an actionable error. Standalone tools that never need real-agent
// dispatch (e.g. cmd/mcp-weaver in isolation) can leave this default.
type NoopSpawnBridge struct{}

// Dispatch on NoopSpawnBridge always returns ErrSpawnBridgeNotConfigured.
func (NoopSpawnBridge) Dispatch(_ context.Context, agent SubAgent, _, _, _ string) (BridgeResult, error) {
	return BridgeResult{}, fmt.Errorf("%w: domain %q uses backend %q",
		ErrSpawnBridgeNotConfigured, agent.Name, agent.Backend)
}
