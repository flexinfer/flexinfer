// domain_adapters_fleet.go provides fleet and nudge-queue domain adapters.
package hud

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/domain/fleet"
)

// --- Fleet domain Deps adapter ---

// fleetDepsAdapter wraps *App to satisfy fleet.Deps. A separate adapter is
// needed because fleet.NudgeQueue() returns fleet.NudgeQueueOps (bridge DTOs),
// while *App holds a *NudgeQueue with hud-local types.
type fleetDepsAdapter struct {
	app *App
}

func (f *fleetDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	f.app.WriteJSON(w, status, v)
}

func (f *fleetDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	f.app.WriteError(w, status, msg, err)
}

func (f *fleetDepsAdapter) RequireAdminToken(w http.ResponseWriter, r *http.Request) bool {
	return f.app.RequireAdminToken(w, r)
}

func (f *fleetDepsAdapter) Logger() *slog.Logger { return f.app.Logger() }

func (f *fleetDepsAdapter) Agent() *bridge.AgentBridge { return f.app.Agent() }

func (f *fleetDepsAdapter) FleetIncrementKPI(field string, delta int) {
	f.app.FleetIncrementKPI(field, delta)
}

func (f *fleetDepsAdapter) FleetRefresh() { f.app.FleetRefresh() }

func (f *fleetDepsAdapter) BroadcastAgentEvent(eventType string, payload any) {
	f.app.BroadcastAgentEvent(eventType, payload)
}

func (f *fleetDepsAdapter) OnSessionEnd(sessionID, agentID string) {
	f.app.OnSessionEnd(sessionID, agentID)
}

func (f *fleetDepsAdapter) MaybeAutoProvisionSandbox(namespace string) {
	f.app.MaybeAutoProvisionSandbox(namespace)
}

func (f *fleetDepsAdapter) MaybeSampleContextTelemetry(agentID, sessionID, agentType, reason string) {
	f.app.maybeSampleAgentContextTelemetry(agentID, sessionID, agentType, reason)
}

func (f *fleetDepsAdapter) NudgeQueue() fleet.NudgeQueueOps {
	return &fleetNudgeAdapter{q: f.app.nudgeQueue}
}

func (f *fleetDepsAdapter) PlanSessionEndSummary(params bridge.SessionEndParams) (bridge.SessionEndParams, bool) {
	return planSessionEndSummary(params, f.app.coordinator != nil)
}

func (f *fleetDepsAdapter) CacheGet(key string) (any, bool) { return f.app.CacheGet(key) }

func (f *fleetDepsAdapter) CacheSet(key string, value any, ttl time.Duration) {
	f.app.CacheSet(key, value, ttl)
}

// SpawnSnapshots returns a fleet-domain-local view of every spawn the
// orchestrator currently tracks (active + recently-completed). Returns nil
// when no orchestrator is configured (e.g. dev environments without the
// spawn loop) so the F8 economics handler can surface the
// "insufficient_data" status instead of bogus zeros.
func (f *fleetDepsAdapter) SpawnSnapshots() []fleet.SpawnEconomicsSnapshot {
	if f.app.spawner == nil {
		return nil
	}
	spawns := f.app.spawner.ListSpawns()
	out := make([]fleet.SpawnEconomicsSnapshot, 0, len(spawns))
	for _, s := range spawns {
		if s == nil {
			continue
		}
		snap := fleet.SpawnEconomicsSnapshot{StartedAt: s.StartedAt}
		if s.Telemetry != nil {
			snap.InputTokens = s.Telemetry.TokenUsage.InputTokens
			snap.OutputTokens = s.Telemetry.TokenUsage.OutputTokens
			snap.TotalCostUSD = s.Telemetry.TotalCostUSD
			snap.ToolCallCount = len(s.Telemetry.ToolCalls)
			snap.FileChangeCount = len(s.Telemetry.FileChanges)
		}
		out = append(out, snap)
	}
	return out
}

// WeaverMetrics reports weaver counter aggregates plus a reachability flag.
// The HUD App does not currently hold a weaver client (weaver lives in the
// daemon), so this returns (zero, false) and the F8 local-utilization ratio
// renders "weaver_metrics_unreachable" rather than fake data. Threading a
// weaver fetcher through here is a follow-up slice.
func (f *fleetDepsAdapter) WeaverMetrics() (fleet.WeaverMetricsView, bool) {
	return fleet.WeaverMetricsView{}, false
}

// fleetNudgeAdapter wraps *NudgeQueue to satisfy fleet.NudgeQueueOps,
// converting between hud-local types and bridge DTOs.
type fleetNudgeAdapter struct {
	q *NudgeQueue
}

func (n *fleetNudgeAdapter) QueueNudge(agentID, nudgeType, lane, content, fromAgent string) string {
	id := NewNudgeID(agentID)
	n.q.Add(agentID, NudgeEntry{
		ID:        id,
		Type:      nudgeType,
		Lane:      lane,
		Content:   content,
		FromAgent: fromAgent,
	})
	return id
}

func (n *fleetNudgeAdapter) Count(agentID string) int {
	return n.q.Count(agentID)
}

func (n *fleetNudgeAdapter) Drain(agentID string) []any {
	entries := n.q.Drain(agentID)
	if len(entries) == 0 {
		return nil
	}
	result := make([]any, len(entries))
	for i, e := range entries {
		result[i] = e
	}
	return result
}

func (n *fleetNudgeAdapter) StatusView(agentID string) bridge.NudgeQueueStatus {
	s := n.q.Status(agentID)
	return bridge.NudgeQueueStatus{
		Pending:      s.Pending,
		Dropped:      s.Dropped,
		ByLane:       s.ByLane,
		DebounceMs:   s.DebounceMs,
		Cap:          s.Cap,
		DropPolicy:   string(s.DropPolicy),
		LanePriority: s.LanePriority,
	}
}

func (n *fleetNudgeAdapter) PolicyView() bridge.NudgeQueuePolicy {
	cfg := n.q.Config()
	return bridge.NudgeQueuePolicy{
		DebounceMs:   int(cfg.Debounce / time.Millisecond),
		Cap:          cfg.Cap,
		DropPolicy:   string(cfg.DropPolicy),
		LanePriority: cfg.LanePriority,
	}
}

func (n *fleetNudgeAdapter) ApplyPolicy(mutation bridge.NudgeQueuePolicyMutation) (before, after bridge.NudgeQueuePolicy, err error) {
	before = n.PolicyView()
	update := NudgeQueuePolicyUpdate{
		DebounceMs:   mutation.DebounceMs,
		Cap:          mutation.Cap,
		DropPolicy:   mutation.DropPolicy,
		LanePriority: mutation.LanePriority,
	}
	afterCfg, err := n.q.UpdateConfig(update)
	if err != nil {
		return before, before, err
	}
	after = bridge.NudgeQueuePolicy{
		DebounceMs:   int(afterCfg.Debounce / time.Millisecond),
		Cap:          afterCfg.Cap,
		DropPolicy:   string(afterCfg.DropPolicy),
		LanePriority: afterCfg.LanePriority,
	}
	return before, after, nil
}
