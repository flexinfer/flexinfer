// Package mobile implements the mobile domain -- companion app REST API
// (all /api/mobile/v1/* endpoints).
package mobile

import (
	"net/http"
)

// MobileDomain registers the companion mobile API endpoints.
type MobileDomain struct {
	deps Deps
}

// New creates a new MobileDomain backed by the given Deps implementation.
func New(deps Deps) *MobileDomain {
	return &MobileDomain{deps: deps}
}

// Name returns "mobile".
func (d *MobileDomain) Name() string { return "mobile" }

// RegisterRoutes wires all /api/mobile/v1/* endpoints to the ServeMux.
func (d *MobileDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	// Read endpoints.
	mux.HandleFunc("GET /api/mobile/v1/ping", mw(d.handleMobilePing))
	mux.HandleFunc("GET /api/mobile/v1/dashboard", mw(d.handleMobileDashboard))
	mux.HandleFunc("GET /api/mobile/v1/control-plane", mw(d.handleMobileControlPlane))
	mux.HandleFunc("GET /api/mobile/v1/sessions", mw(d.handleMobileSessions))
	mux.HandleFunc("GET /api/mobile/v1/sessions/{session_id}", mw(d.handleMobileSessionDetail))
	mux.HandleFunc("GET /api/mobile/v1/sessions/{session_id}/events", mw(d.handleMobileSessionEvents))
	mux.HandleFunc("GET /api/mobile/v1/sessions/{session_id}/activity", mw(d.handleMobileSessionActivity))
	mux.HandleFunc("GET /api/mobile/v1/tasks", mw(d.handleMobileTasks))
	mux.HandleFunc("GET /api/mobile/v1/workflows", mw(d.handleMobileWorkflows))
	mux.HandleFunc("GET /api/mobile/v1/workflows/{workflow_id}", mw(d.handleMobileWorkflowDetail))
	mux.HandleFunc("GET /api/mobile/v1/presence", mw(d.handleMobilePresence))
	mux.HandleFunc("GET /api/mobile/v1/agents", mw(d.handleMobileAgents))
	mux.HandleFunc("GET /api/mobile/v1/namespaces", mw(d.handleMobileNamespaces))
	mux.HandleFunc("GET /api/mobile/v1/memory/stats", mw(d.handleMobileMemoryStats))
	mux.HandleFunc("GET /api/mobile/v1/memory/items", mw(d.handleMobileMemoryItems))
	mux.HandleFunc("GET /api/mobile/v1/stream", mw(d.handleMobileStream))
	mux.HandleFunc("GET /api/mobile/v1/topology", mw(d.handleMobileTopology))
	mux.HandleFunc("GET /api/mobile/v1/graph/stats", mw(d.handleMobileGraphStats))
	mux.HandleFunc("GET /api/mobile/v1/graph/entities", mw(d.handleMobileGraphEntities))
	mux.HandleFunc("GET /api/mobile/v1/graph/path", mw(d.handleMobileGraphPath))
	mux.HandleFunc("GET /api/mobile/v1/reasoning/chains", mw(d.handleMobileReasoningChains))
	mux.HandleFunc("GET /api/mobile/v1/reasoning/chains/{chain_id}", mw(d.handleMobileReasoningChainDetail))
	mux.HandleFunc("GET /api/mobile/v1/events/stream", mw(d.handleMobileEventsStream))
	mux.HandleFunc("GET /api/mobile/v1/audit", mw(d.handleMobileAudit))
	mux.HandleFunc("GET /api/mobile/v1/alerts/policy", mw(d.handleMobileAlertsPolicy))
	mux.HandleFunc("GET /api/mobile/v1/sandbox", mw(d.handleMobileSandbox))
	mux.HandleFunc("GET /api/mobile/v1/pipelines", mw(d.handleMobilePipelines))
	mux.HandleFunc("GET /api/mobile/v1/handoffs", mw(d.handleMobileHandoffs))
	mux.HandleFunc("GET /api/mobile/v1/agent/spawns", mw(d.handleMobileSpawnList))
	mux.HandleFunc("GET /api/mobile/v1/agent/spawn/config", mw(d.handleMobileSpawnConfig))
	mux.HandleFunc("GET /api/mobile/v1/agent/spawn/{spawn_id}", mw(d.handleMobileSpawnDetail))
	mux.HandleFunc("GET /api/mobile/v1/agent/spawn/{spawn_id}/stream", mw(d.handleMobileSpawnStream))
	mux.HandleFunc("GET /api/mobile/v1/agent/spawn/{spawn_id}/telemetry", mw(d.handleMobileSpawnTelemetry))
	mux.HandleFunc("GET /api/mobile/v1/agent/spawn/{spawn_id}/telemetry/tools", mw(d.HandleGetSpawnTelemetryTools))
	mux.HandleFunc("GET /api/mobile/v1/agent/spawn/{spawn_id}/telemetry/files", mw(d.HandleGetSpawnTelemetryFiles))
	mux.HandleFunc("GET /api/mobile/v1/agent/spawn/{spawn_id}/telemetry/errors", mw(d.HandleGetSpawnTelemetryErrors))

	// Mutation endpoints.
	mux.HandleFunc("POST /api/mobile/v1/sessions", mw(d.handleMobileSessionCreate))
	mux.HandleFunc("POST /api/mobile/v1/sessions/{session_id}/end", mw(d.handleMobileSessionEnd))
	mux.HandleFunc("POST /api/mobile/v1/push/register", mw(d.handleMobilePushRegister))
	mux.HandleFunc("POST /api/mobile/v1/push/unregister", mw(d.handleMobilePushUnregister))
	mux.HandleFunc("POST /api/mobile/v1/admin/revoke", mw(d.handleMobileAdminRevoke))
	mux.HandleFunc("POST /api/mobile/v1/sandbox/start", mw(d.handleMobileSandboxStart))
	mux.HandleFunc("POST /api/mobile/v1/sandbox/stop", mw(d.handleMobileSandboxStop))
	mux.HandleFunc("POST /api/mobile/v1/workflows/{workflow_id}/approve", mw(d.handleMobileWorkflowApprove))
	mux.HandleFunc("POST /api/mobile/v1/workflows/{workflow_id}/reject", mw(d.handleMobileWorkflowReject))
	mux.HandleFunc("POST /api/mobile/v1/agent/spawn", mw(d.handleMobileSpawnAgent))
	mux.HandleFunc("POST /api/mobile/v1/agent/spawn/{spawn_id}/stop", mw(d.handleMobileSpawnStop))
}
