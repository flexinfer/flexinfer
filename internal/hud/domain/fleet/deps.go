package fleet

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// Deps defines the dependencies the fleet domain needs from the host App.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	RequireAdminToken(w http.ResponseWriter, r *http.Request) bool
	Logger() *slog.Logger
	Agent() *bridge.AgentBridge
	FleetIncrementKPI(field string, delta int)
	FleetRefresh()
	BroadcastAgentEvent(eventType string, payload any)
	OnSessionEnd(sessionID, agentID string)
	MaybeAutoProvisionSandbox(namespace string)
	MaybeSampleContextTelemetry(agentID, sessionID, agentType, reason string)
	NudgeQueue() NudgeQueueOps
	CacheGet(key string) (any, bool)
	CacheSet(key string, value any, ttl time.Duration)
	PlanSessionEndSummary(params bridge.SessionEndParams) (bridge.SessionEndParams, bool)

	// SpawnSnapshots returns a slice of finalized/in-flight spawn snapshots
	// used by the F8 token-economics handler to aggregate frontier costs and
	// token counts. Returns nil when no spawn orchestrator is configured;
	// callers must tolerate a nil/empty slice.
	SpawnSnapshots() []SpawnEconomicsSnapshot

	// WeaverMetrics returns aggregated weaver metrics for the F8
	// token-economics card. The returned bool indicates whether the weaver
	// metrics endpoint is reachable; when false, the local-utilization ratio
	// surfaces "weaver_metrics_unreachable" rather than faking data.
	WeaverMetrics() (WeaverMetricsView, bool)
}

// SpawnEconomicsSnapshot is a slim, fleet-domain-local view of a spawn's
// telemetry sufficient to derive the F8 economics ratios. Decouples the fleet
// domain from the concrete SpawnOrchestrator/SpawnInfo types so adapters can
// supply test doubles without dragging the spawn package into this domain.
type SpawnEconomicsSnapshot struct {
	StartedAt       time.Time
	InputTokens     int
	OutputTokens    int
	TotalCostUSD    float64
	ToolCallCount   int
	FileChangeCount int
}

// WeaverMetricsView is the slim weaver counters bundle used by the F8
// economics handler. All fields are best-effort: zero values are treated as
// "no data" by the ratio math, while the reachable flag (returned alongside)
// switches the local-utilization ratio between "insufficient_data" and
// "weaver_metrics_unreachable" so the UI can distinguish "weaver is off"
// from "weaver is on but quiet".
type WeaverMetricsView struct {
	TotalQueries int
	TotalTokens  int
}

// NudgeQueueOps abstracts the nudge queue, returning bridge DTOs to avoid
// importing hud-local types.
type NudgeQueueOps interface {
	QueueNudge(agentID, nudgeType, lane, content, fromAgent string) string
	Count(agentID string) int
	Drain(agentID string) []any
	StatusView(agentID string) bridge.NudgeQueueStatus
	PolicyView() bridge.NudgeQueuePolicy
	ApplyPolicy(mutation bridge.NudgeQueuePolicyMutation) (before, after bridge.NudgeQueuePolicy, err error)
}
