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
