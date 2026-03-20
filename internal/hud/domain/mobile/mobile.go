// Package mobile implements the mobile domain -- companion app REST API
// (all /api/mobile/v1/* endpoints).
package mobile

import (
	"context"
	"net/http"
)

// AppHandlers exposes the subset of *App methods that mobile routes need.
type AppHandlers interface {
	HandleMobilePing(w http.ResponseWriter, r *http.Request)
	HandleMobileDashboard(w http.ResponseWriter, r *http.Request)
	HandleMobileControlPlane(w http.ResponseWriter, r *http.Request)
	HandleMobileSessions(w http.ResponseWriter, r *http.Request)
	HandleMobileSessionDetail(w http.ResponseWriter, r *http.Request)
	HandleMobileSessionEvents(w http.ResponseWriter, r *http.Request)
	HandleMobileTasks(w http.ResponseWriter, r *http.Request)
	HandleMobileWorkflows(w http.ResponseWriter, r *http.Request)
	HandleMobileWorkflowDetail(w http.ResponseWriter, r *http.Request)
	HandleMobilePresence(w http.ResponseWriter, r *http.Request)
	HandleMobileAgents(w http.ResponseWriter, r *http.Request)
	HandleMobileMemoryStats(w http.ResponseWriter, r *http.Request)
	HandleMobileMemoryItems(w http.ResponseWriter, r *http.Request)
	HandleMobileStream(w http.ResponseWriter, r *http.Request)
	HandleMobileTopology(w http.ResponseWriter, r *http.Request)
	HandleMobileGraphStats(w http.ResponseWriter, r *http.Request)
	HandleMobileGraphEntities(w http.ResponseWriter, r *http.Request)
	HandleMobileGraphPath(w http.ResponseWriter, r *http.Request)
	HandleMobileReasoningChains(w http.ResponseWriter, r *http.Request)
	HandleMobileReasoningChainDetail(w http.ResponseWriter, r *http.Request)
	HandleMobileEventsStream(w http.ResponseWriter, r *http.Request)
	HandleMobileSessionCreate(w http.ResponseWriter, r *http.Request)
	HandleMobileSessionEnd(w http.ResponseWriter, r *http.Request)
	HandleMobileAudit(w http.ResponseWriter, r *http.Request)
	HandleMobileAlertsPolicy(w http.ResponseWriter, r *http.Request)
	HandleMobilePushRegister(w http.ResponseWriter, r *http.Request)
	HandleMobilePushUnregister(w http.ResponseWriter, r *http.Request)
	HandleMobileAdminRevoke(w http.ResponseWriter, r *http.Request)
	HandleMobileSandbox(w http.ResponseWriter, r *http.Request)
	HandleMobileSandboxStart(w http.ResponseWriter, r *http.Request)
	HandleMobileSandboxStop(w http.ResponseWriter, r *http.Request)
	HandleMobilePipelines(w http.ResponseWriter, r *http.Request)
	HandleMobileWorkflowApprove(w http.ResponseWriter, r *http.Request)
	HandleMobileWorkflowReject(w http.ResponseWriter, r *http.Request)
	HandleMobileHandoffs(w http.ResponseWriter, r *http.Request)
	HandleMobileSpawnAgent(w http.ResponseWriter, r *http.Request)
	HandleMobileSpawnList(w http.ResponseWriter, r *http.Request)
	HandleMobileSpawnConfig(w http.ResponseWriter, r *http.Request)
	HandleMobileSpawnDetail(w http.ResponseWriter, r *http.Request)
	HandleMobileSpawnStop(w http.ResponseWriter, r *http.Request)
	HandleMobileSpawnStream(w http.ResponseWriter, r *http.Request)
}

// MobileDomain registers the companion mobile API endpoints.
type MobileDomain struct {
	app AppHandlers
}

// New creates a new MobileDomain backed by the given handler interface.
func New(app AppHandlers) *MobileDomain {
	return &MobileDomain{app: app}
}

// Name returns "mobile".
func (d *MobileDomain) Name() string { return "mobile" }

// RegisterRoutes wires all /api/mobile/v1/* endpoints to the ServeMux.
func (d *MobileDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	// Read endpoints.
	mux.HandleFunc("GET /api/mobile/v1/ping", mw(d.app.HandleMobilePing))
	mux.HandleFunc("GET /api/mobile/v1/dashboard", mw(d.app.HandleMobileDashboard))
	mux.HandleFunc("GET /api/mobile/v1/control-plane", mw(d.app.HandleMobileControlPlane))
	mux.HandleFunc("GET /api/mobile/v1/sessions", mw(d.app.HandleMobileSessions))
	mux.HandleFunc("GET /api/mobile/v1/sessions/{session_id}", mw(d.app.HandleMobileSessionDetail))
	mux.HandleFunc("GET /api/mobile/v1/sessions/{session_id}/events", mw(d.app.HandleMobileSessionEvents))
	mux.HandleFunc("GET /api/mobile/v1/tasks", mw(d.app.HandleMobileTasks))
	mux.HandleFunc("GET /api/mobile/v1/workflows", mw(d.app.HandleMobileWorkflows))
	mux.HandleFunc("GET /api/mobile/v1/workflows/{workflow_id}", mw(d.app.HandleMobileWorkflowDetail))
	mux.HandleFunc("GET /api/mobile/v1/presence", mw(d.app.HandleMobilePresence))
	mux.HandleFunc("GET /api/mobile/v1/agents", mw(d.app.HandleMobileAgents))
	mux.HandleFunc("GET /api/mobile/v1/memory/stats", mw(d.app.HandleMobileMemoryStats))
	mux.HandleFunc("GET /api/mobile/v1/memory/items", mw(d.app.HandleMobileMemoryItems))
	mux.HandleFunc("GET /api/mobile/v1/stream", mw(d.app.HandleMobileStream))
	mux.HandleFunc("GET /api/mobile/v1/topology", mw(d.app.HandleMobileTopology))
	mux.HandleFunc("GET /api/mobile/v1/graph/stats", mw(d.app.HandleMobileGraphStats))
	mux.HandleFunc("GET /api/mobile/v1/graph/entities", mw(d.app.HandleMobileGraphEntities))
	mux.HandleFunc("GET /api/mobile/v1/graph/path", mw(d.app.HandleMobileGraphPath))
	mux.HandleFunc("GET /api/mobile/v1/reasoning/chains", mw(d.app.HandleMobileReasoningChains))
	mux.HandleFunc("GET /api/mobile/v1/reasoning/chains/{chain_id}", mw(d.app.HandleMobileReasoningChainDetail))
	mux.HandleFunc("GET /api/mobile/v1/events/stream", mw(d.app.HandleMobileEventsStream))
	mux.HandleFunc("GET /api/mobile/v1/audit", mw(d.app.HandleMobileAudit))
	mux.HandleFunc("GET /api/mobile/v1/alerts/policy", mw(d.app.HandleMobileAlertsPolicy))
	mux.HandleFunc("GET /api/mobile/v1/sandbox", mw(d.app.HandleMobileSandbox))
	mux.HandleFunc("GET /api/mobile/v1/pipelines", mw(d.app.HandleMobilePipelines))
	mux.HandleFunc("GET /api/mobile/v1/handoffs", mw(d.app.HandleMobileHandoffs))
	mux.HandleFunc("GET /api/mobile/v1/agent/spawns", mw(d.app.HandleMobileSpawnList))
	mux.HandleFunc("GET /api/mobile/v1/agent/spawn/config", mw(d.app.HandleMobileSpawnConfig))
	mux.HandleFunc("GET /api/mobile/v1/agent/spawn/{spawn_id}", mw(d.app.HandleMobileSpawnDetail))
	mux.HandleFunc("GET /api/mobile/v1/agent/spawn/{spawn_id}/stream", mw(d.app.HandleMobileSpawnStream))

	// Mutation endpoints.
	mux.HandleFunc("POST /api/mobile/v1/sessions", mw(d.app.HandleMobileSessionCreate))
	mux.HandleFunc("POST /api/mobile/v1/sessions/{session_id}/end", mw(d.app.HandleMobileSessionEnd))
	mux.HandleFunc("POST /api/mobile/v1/push/register", mw(d.app.HandleMobilePushRegister))
	mux.HandleFunc("POST /api/mobile/v1/push/unregister", mw(d.app.HandleMobilePushUnregister))
	mux.HandleFunc("POST /api/mobile/v1/admin/revoke", mw(d.app.HandleMobileAdminRevoke))
	mux.HandleFunc("POST /api/mobile/v1/sandbox/start", mw(d.app.HandleMobileSandboxStart))
	mux.HandleFunc("POST /api/mobile/v1/sandbox/stop", mw(d.app.HandleMobileSandboxStop))
	mux.HandleFunc("POST /api/mobile/v1/workflows/{workflow_id}/approve", mw(d.app.HandleMobileWorkflowApprove))
	mux.HandleFunc("POST /api/mobile/v1/workflows/{workflow_id}/reject", mw(d.app.HandleMobileWorkflowReject))
	mux.HandleFunc("POST /api/mobile/v1/agent/spawn", mw(d.app.HandleMobileSpawnAgent))
	mux.HandleFunc("POST /api/mobile/v1/agent/spawn/{spawn_id}/stop", mw(d.app.HandleMobileSpawnStop))
}

// Start is a no-op; mobile lifecycle resources are managed by *App.
func (d *MobileDomain) Start(_ context.Context) error { return nil }

// Stop is a no-op.
func (d *MobileDomain) Stop() error { return nil }
