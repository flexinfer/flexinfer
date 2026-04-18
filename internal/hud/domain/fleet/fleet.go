// Package fleet implements the fleet domain -- agent session lifecycle,
// context management, nudge queue, task updates, and knowledge handlers.
package fleet

import (
	"net/http"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// FleetDomain registers agent lifecycle, context, nudge, and task routes.
type FleetDomain struct {
	deps Deps
}

// New creates a new FleetDomain backed by the given Deps interface.
func New(deps Deps) *FleetDomain {
	return &FleetDomain{deps: deps}
}

// Name returns "fleet".
func (d *FleetDomain) Name() string { return "fleet" }

// RegisterRoutes wires the agent lifecycle endpoints to the ServeMux.
func (d *FleetDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	// Session lifecycle (CLI hooks call these).
	mux.HandleFunc("POST "+bridge.AgentSessionStartEndpoint, mw(d.handleAgentSessionStart))
	mux.HandleFunc("POST "+bridge.AgentSessionEndEndpoint, mw(d.handleAgentSessionEnd))
	mux.HandleFunc("POST /api/agent/heartbeat", mw(d.handleAgentHeartbeat))
	mux.HandleFunc("GET "+bridge.AgentSessionEndpoint, mw(d.handleAgentSession))
	mux.HandleFunc("POST "+bridge.AgentSessionListEndpoint, mw(d.handleAgentSessionList))
	mux.HandleFunc("POST "+bridge.AgentSessionPruneEndpoint, mw(d.handleAgentSessionPrune))
	mux.HandleFunc("GET /api/agent/session-detail", mw(d.handleAgentSessionDetail))

	// Context and knowledge.
	mux.HandleFunc("POST /api/agent/context/add", mw(d.handleAgentContextAdd))
	mux.HandleFunc("GET "+bridge.AgentContextInspectEndpoint, mw(d.handleAgentContextInspect))
	mux.HandleFunc("GET /api/knowledge", mw(d.handleKnowledge))

	// Task and workflow.
	mux.HandleFunc("POST /api/agent/task-sync", mw(d.handleAgentTaskSync))
	mux.HandleFunc("POST /api/agent/task-update", mw(d.handleAgentTaskUpdate))
	mux.HandleFunc("POST /api/agent/workflow-define", mw(d.handleAgentWorkflowDefine))
	mux.HandleFunc("GET /api/agent/workflow-definitions", mw(d.handleAgentWorkflowDefinitions))

	// Nudge queue.
	mux.HandleFunc("POST /api/agent/nudge", mw(d.handleAgentNudge))
	mux.HandleFunc("GET /api/agent/nudge-queue", mw(d.handleAgentNudgeQueue))
	mux.HandleFunc("GET "+bridge.AgentNudgeQueuePolicyPath, mw(d.handleAgentNudgeQueuePolicy))
	mux.HandleFunc("POST "+bridge.AgentNudgeQueuePolicyPath, mw(d.handleAgentNudgeQueuePolicyUpdate))

	// Dispatch and claims.
	mux.HandleFunc("POST "+bridge.AgentDispatchEndpoint, mw(d.handleAgentDispatch))
	mux.HandleFunc("DELETE /api/claims/{agent_id}/{file_path...}", mw(d.handleClaimRelease))

	// F9: live file-claim conflict overlay (SSE).
	mux.HandleFunc("GET /api/fleet/claims/stream", mw(d.handleClaimsStream))

	// F8 (D2): token-economics dashboard.
	mux.HandleFunc("GET /api/fleet/economics", mw(d.handleEconomics))
}
