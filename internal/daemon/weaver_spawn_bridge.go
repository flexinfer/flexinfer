// Package daemon — weaver_spawn_bridge.go implements weaver.SpawnBridge by
// delegating to the HUD's SpawnOrchestrator. This lives in the daemon
// package (rather than pkg/weaver) so pkg/weaver stays free of
// internal/hud and internal/spawn imports, keeping it usable from
// standalone tools like cmd/mcp-weaver.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/crb2nu/loom/internal/spawn"
	"github.com/crb2nu/loom/pkg/weaver"
)

// spawnDispatcher is the narrow surface of *hud.SpawnOrchestrator the
// bridge uses. Narrowing lets tests substitute a fake without pulling
// in the real backend machinery.
type spawnDispatcher interface {
	Spawn(ctx context.Context, req spawn.Request) (string, error)
	Wait(ctx context.Context, spawnID string) (*spawn.State, error)
}

// DaemonSpawnBridge adapts the HUD's SpawnOrchestrator to
// weaver.SpawnBridge. When weaver dispatches a SubAgent whose Backend is
// non-flexinfer, the bridge creates a real headless-agent pod, waits for
// it to terminate, and folds the resulting state into a
// weaver.BridgeResult.
type DaemonSpawnBridge struct {
	orch           spawnDispatcher
	logger         *slog.Logger
	defaultTimeout time.Duration
}

// NewDaemonSpawnBridge returns a bridge bound to the given spawn
// dispatcher. defaultTimeout is used as the Wait deadline when the
// SubAgent's SpawnOverrides does not specify one; 0 means "no Wait
// deadline beyond the caller's ctx".
func NewDaemonSpawnBridge(orch spawnDispatcher, logger *slog.Logger, defaultTimeout time.Duration) *DaemonSpawnBridge {
	if logger == nil {
		logger = slog.Default()
	}
	return &DaemonSpawnBridge{
		orch:           orch,
		logger:         logger.With("component", "weaver-spawn-bridge"),
		defaultTimeout: defaultTimeout,
	}
}

// Dispatch creates a spawn.Request from the SubAgent + query, starts
// the spawn, waits for terminal state, and returns a BridgeResult.
func (b *DaemonSpawnBridge) Dispatch(
	ctx context.Context,
	agent weaver.SubAgent,
	query, parentSessionID, weaverQueryID string,
) (weaver.BridgeResult, error) {
	if b.orch == nil {
		return weaver.BridgeResult{}, fmt.Errorf("weaver spawn bridge: no spawn dispatcher configured")
	}
	if agent.IsFlexInferBackend() {
		return weaver.BridgeResult{}, fmt.Errorf("weaver spawn bridge: cannot dispatch flexinfer backend (%q) — this is a weaver router bug", agent.Name)
	}

	req := buildSpawnRequestFromSubAgent(agent, query, parentSessionID, weaverQueryID)

	if req.Project == "" {
		return weaver.BridgeResult{}, fmt.Errorf("weaver spawn bridge: domain %q requires spawn.project; set it in the domain's SpawnOverrides", agent.Name)
	}

	spawnCtx := ctx
	var cancel context.CancelFunc
	if b.defaultTimeout > 0 && deadlineFits(ctx, b.defaultTimeout) {
		spawnCtx, cancel = context.WithTimeout(ctx, b.defaultTimeout)
		defer cancel()
	}

	spawnID, err := b.orch.Spawn(spawnCtx, req)
	if err != nil {
		return weaver.BridgeResult{}, fmt.Errorf("spawn create: %w", err)
	}

	b.logger.Debug("weaver dispatched to spawn",
		"domain", agent.Name,
		"backend", agent.Backend,
		"spawn_id", spawnID,
		"weaver_query_id", weaverQueryID,
		"parent_session_id", parentSessionID,
	)

	state, err := b.orch.Wait(spawnCtx, spawnID)
	if err != nil {
		return weaver.BridgeResult{SpawnID: spawnID}, fmt.Errorf("spawn wait: %w", err)
	}

	return bridgeResultFromState(spawnID, state), nil
}

// buildSpawnRequestFromSubAgent translates a weaver.SubAgent + query into
// a spawn.Request, folding SpawnOverrides on top. Nil overrides are
// treated as defaults.
func buildSpawnRequestFromSubAgent(
	agent weaver.SubAgent,
	query, parentSessionID, weaverQueryID string,
) spawn.Request {
	ov := agent.SpawnOverrides
	if ov == nil {
		ov = &weaver.SpawnOverrides{}
	}

	timeoutMinutes := 0
	if ov.Timeout > 0 {
		timeoutMinutes = int(ov.Timeout.Minutes())
		if timeoutMinutes < 1 {
			timeoutMinutes = 1
		}
	}

	metadata := map[string]string{
		"weaver_query_id": weaverQueryID,
		"weaver_domain":   agent.Name,
	}

	return spawn.Request{
		AgentType:       agent.Backend,
		TaskDescription: query,
		Project:         ov.Project,
		TimeoutMinutes:  timeoutMinutes,
		MaxCostUSD:      ov.MaxCostUSD,
		MaxTurns:        ov.MaxTurns,
		UseSDKDriver:    ov.UseSDKDriver,
		ParentSessionID: parentSessionID,
		Metadata:        metadata,
	}
}

// bridgeResultFromState projects terminal spawn state into the shape
// weaver uses for DomainResult. Telemetry may be nil on failed spawns;
// we tolerate that and return a result with just SpawnID + StopReason
// so the DomainResult surfaces the failure context.
func bridgeResultFromState(spawnID string, state *spawn.State) weaver.BridgeResult {
	out := weaver.BridgeResult{
		SpawnID:    spawnID,
		StopReason: string(state.Status),
	}
	if state.Telemetry == nil {
		return out
	}
	out.LastMessage = state.Telemetry.LastMessage
	out.ToolCalls = len(state.Telemetry.ToolCalls)
	out.TotalCostUSD = state.Telemetry.TotalCostUSD
	out.Tokens = state.Telemetry.TokenUsage.InputTokens + state.Telemetry.TokenUsage.OutputTokens
	if state.Telemetry.StopReason != "" {
		out.StopReason = state.Telemetry.StopReason
	}
	return out
}

// deadlineFits reports whether wrapping ctx in a child with the given
// relative timeout would actually constrain the deadline, rather than
// expire later than the parent (which would be a no-op). Caller uses this
// to avoid pointless WithTimeout wrapping.
func deadlineFits(parent context.Context, d time.Duration) bool {
	dl, ok := parent.Deadline()
	if !ok {
		return true // parent has no deadline, child will
	}
	return time.Until(dl) > d
}
