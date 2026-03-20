// Package fleet implements the fleet domain -- agent session lifecycle,
// context management, nudge queue, task updates, and knowledge handlers.
//
// The FleetDomain delegates to the HUD App via the AppHandlers interface,
// keeping handler implementations in their original files while owning
// route registration.
package fleet

import (
	"net/http"
)

// AppHandlers exposes the subset of *App methods that fleet routes need.
// This interface decouples route wiring from the concrete App struct.
type AppHandlers interface {
	// Session lifecycle.
	HandleAgentSessionStart(w http.ResponseWriter, r *http.Request)
	HandleAgentSessionEnd(w http.ResponseWriter, r *http.Request)
	HandleAgentHeartbeat(w http.ResponseWriter, r *http.Request)
	HandleAgentSession(w http.ResponseWriter, r *http.Request)
	HandleAgentSessionList(w http.ResponseWriter, r *http.Request)
	HandleAgentSessionPrune(w http.ResponseWriter, r *http.Request)
	HandleAgentSessionDetail(w http.ResponseWriter, r *http.Request)

	// Context and knowledge.
	HandleAgentContextAdd(w http.ResponseWriter, r *http.Request)
	HandleAgentContextInspect(w http.ResponseWriter, r *http.Request)
	HandleKnowledge(w http.ResponseWriter, r *http.Request)

	// Task and workflow.
	HandleAgentTaskUpdate(w http.ResponseWriter, r *http.Request)
	HandleAgentWorkflowDefine(w http.ResponseWriter, r *http.Request)
	HandleAgentWorkflowDefinitions(w http.ResponseWriter, r *http.Request)

	// Nudge queue.
	HandleAgentNudge(w http.ResponseWriter, r *http.Request)
	HandleAgentNudgeQueue(w http.ResponseWriter, r *http.Request)
	HandleAgentNudgeQueuePolicy(w http.ResponseWriter, r *http.Request)
	HandleAgentNudgeQueuePolicyUpdate(w http.ResponseWriter, r *http.Request)

	// Dispatch and claims.
	HandleAgentDispatch(w http.ResponseWriter, r *http.Request)
	HandleClaimRelease(w http.ResponseWriter, r *http.Request)
}

// FleetDomain registers agent lifecycle, context, nudge, and task routes.
type FleetDomain struct {
	app AppHandlers
}

// New creates a new FleetDomain backed by the given handler interface.
func New(app AppHandlers) *FleetDomain {
	return &FleetDomain{app: app}
}

// Name returns "fleet".
func (d *FleetDomain) Name() string { return "fleet" }

// RegisterRoutes wires the agent lifecycle endpoints to the ServeMux.
func (d *FleetDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	// Session lifecycle (CLI hooks call these).
	mux.HandleFunc("POST /api/agent/session-start", mw(d.app.HandleAgentSessionStart))
	mux.HandleFunc("POST /api/agent/session-end", mw(d.app.HandleAgentSessionEnd))
	mux.HandleFunc("POST /api/agent/heartbeat", mw(d.app.HandleAgentHeartbeat))
	mux.HandleFunc("GET /api/agent/session", mw(d.app.HandleAgentSession))
	mux.HandleFunc("POST /api/agent/session-list", mw(d.app.HandleAgentSessionList))
	mux.HandleFunc("POST /api/agent/session-prune", mw(d.app.HandleAgentSessionPrune))
	mux.HandleFunc("GET /api/agent/session-detail", mw(d.app.HandleAgentSessionDetail))

	// Context and knowledge.
	mux.HandleFunc("POST /api/agent/context/add", mw(d.app.HandleAgentContextAdd))
	mux.HandleFunc("GET /api/agent/context-inspect", mw(d.app.HandleAgentContextInspect))
	mux.HandleFunc("GET /api/knowledge", mw(d.app.HandleKnowledge))

	// Task and workflow.
	mux.HandleFunc("POST /api/agent/task-update", mw(d.app.HandleAgentTaskUpdate))
	mux.HandleFunc("POST /api/agent/workflow-define", mw(d.app.HandleAgentWorkflowDefine))
	mux.HandleFunc("GET /api/agent/workflow-definitions", mw(d.app.HandleAgentWorkflowDefinitions))

	// Nudge queue.
	mux.HandleFunc("POST /api/agent/nudge", mw(d.app.HandleAgentNudge))
	mux.HandleFunc("GET /api/agent/nudge-queue", mw(d.app.HandleAgentNudgeQueue))
	mux.HandleFunc("GET /api/agent/nudge-queue-policy", mw(d.app.HandleAgentNudgeQueuePolicy))
	mux.HandleFunc("POST /api/agent/nudge-queue-policy", mw(d.app.HandleAgentNudgeQueuePolicyUpdate))

	// Dispatch and claims.
	mux.HandleFunc("POST /api/agent/dispatch", mw(d.app.HandleAgentDispatch))
	mux.HandleFunc("DELETE /api/claims/{agent_id}/{file_path...}", mw(d.app.HandleClaimRelease))
}
