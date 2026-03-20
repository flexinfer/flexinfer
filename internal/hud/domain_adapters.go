// domain_adapters.go provides exported method adapters so that *App satisfies
// the domain handler interfaces (fleet.AppHandlers, spawn.AppHandlers, etc.).
//
// Each method is a thin forwarder to the corresponding unexported handler.
// This enables the domain packages to call App methods without exporting the
// original handler signatures or changing the existing code.
package hud

import (
	"net/http"

	"github.com/crb2nu/loom/internal/hud/domain"
	coorddomain "github.com/crb2nu/loom/internal/hud/domain/coordinator"
	"github.com/crb2nu/loom/internal/hud/domain/fleet"
	"github.com/crb2nu/loom/internal/hud/domain/mobile"
	"github.com/crb2nu/loom/internal/hud/domain/sandbox"
	"github.com/crb2nu/loom/internal/hud/domain/spawn"
)

// initDomainRegistry creates and populates the domain registry. Called from
// Run() and from test helpers.
func (a *App) initDomainRegistry() {
	a.domainRegistry = domain.NewRegistry()
	a.domainRegistry.Register(fleet.New(a))
	a.domainRegistry.Register(spawn.New(a))
	a.domainRegistry.Register(mobile.New(a))
	a.domainRegistry.Register(coorddomain.New(a))
	a.domainRegistry.Register(sandbox.New(a))
}

// --- Fleet domain adapters ---

func (a *App) HandleAgentSessionStart(w http.ResponseWriter, r *http.Request) {
	a.handleAgentSessionStart(w, r)
}

func (a *App) HandleAgentSessionEnd(w http.ResponseWriter, r *http.Request) {
	a.handleAgentSessionEnd(w, r)
}

func (a *App) HandleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	a.handleAgentHeartbeat(w, r)
}

func (a *App) HandleAgentSession(w http.ResponseWriter, r *http.Request) {
	a.handleAgentSession(w, r)
}

func (a *App) HandleAgentSessionList(w http.ResponseWriter, r *http.Request) {
	a.handleAgentSessionList(w, r)
}

func (a *App) HandleAgentSessionPrune(w http.ResponseWriter, r *http.Request) {
	a.handleAgentSessionPrune(w, r)
}

func (a *App) HandleAgentSessionDetail(w http.ResponseWriter, r *http.Request) {
	a.handleAgentSessionDetail(w, r)
}

func (a *App) HandleAgentContextAdd(w http.ResponseWriter, r *http.Request) {
	a.handleAgentContextAdd(w, r)
}

func (a *App) HandleAgentContextInspect(w http.ResponseWriter, r *http.Request) {
	a.handleAgentContextInspect(w, r)
}

func (a *App) HandleKnowledge(w http.ResponseWriter, r *http.Request) {
	a.handleKnowledge(w, r)
}

func (a *App) HandleAgentTaskUpdate(w http.ResponseWriter, r *http.Request) {
	a.handleAgentTaskUpdate(w, r)
}

func (a *App) HandleAgentWorkflowDefine(w http.ResponseWriter, r *http.Request) {
	a.handleAgentWorkflowDefine(w, r)
}

func (a *App) HandleAgentWorkflowDefinitions(w http.ResponseWriter, r *http.Request) {
	a.handleAgentWorkflowDefinitions(w, r)
}

func (a *App) HandleAgentNudge(w http.ResponseWriter, r *http.Request) {
	a.handleAgentNudge(w, r)
}

func (a *App) HandleAgentNudgeQueue(w http.ResponseWriter, r *http.Request) {
	a.handleAgentNudgeQueue(w, r)
}

func (a *App) HandleAgentNudgeQueuePolicy(w http.ResponseWriter, r *http.Request) {
	a.handleAgentNudgeQueuePolicy(w, r)
}

func (a *App) HandleAgentNudgeQueuePolicyUpdate(w http.ResponseWriter, r *http.Request) {
	a.handleAgentNudgeQueuePolicyUpdate(w, r)
}

func (a *App) HandleAgentDispatch(w http.ResponseWriter, r *http.Request) {
	a.handleAgentDispatch(w, r)
}

func (a *App) HandleClaimRelease(w http.ResponseWriter, r *http.Request) {
	a.handleClaimRelease(w, r)
}

// --- Spawn domain adapters ---

func (a *App) HandleAgentSpawn(w http.ResponseWriter, r *http.Request) {
	a.handleAgentSpawn(w, r)
}

func (a *App) HandleAgentSpawnList(w http.ResponseWriter, r *http.Request) {
	a.handleAgentSpawnList(w, r)
}

func (a *App) HandleAgentSpawnConfig(w http.ResponseWriter, r *http.Request) {
	a.handleAgentSpawnConfig(w, r)
}

func (a *App) HandleAgentSpawnDetail(w http.ResponseWriter, r *http.Request) {
	a.handleAgentSpawnDetail(w, r)
}

func (a *App) HandleAgentSpawnStop(w http.ResponseWriter, r *http.Request) {
	a.handleAgentSpawnStop(w, r)
}

// --- Mobile domain adapters ---

func (a *App) HandleMobilePing(w http.ResponseWriter, r *http.Request) {
	a.handleMobilePing(w, r)
}

func (a *App) HandleMobileDashboard(w http.ResponseWriter, r *http.Request) {
	a.handleMobileDashboard(w, r)
}

func (a *App) HandleMobileControlPlane(w http.ResponseWriter, r *http.Request) {
	a.handleMobileControlPlane(w, r)
}

func (a *App) HandleMobileSessions(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSessions(w, r)
}

func (a *App) HandleMobileSessionDetail(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSessionDetail(w, r)
}

func (a *App) HandleMobileSessionEvents(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSessionEvents(w, r)
}

func (a *App) HandleMobileTasks(w http.ResponseWriter, r *http.Request) {
	a.handleMobileTasks(w, r)
}

func (a *App) HandleMobileWorkflows(w http.ResponseWriter, r *http.Request) {
	a.handleMobileWorkflows(w, r)
}

func (a *App) HandleMobileWorkflowDetail(w http.ResponseWriter, r *http.Request) {
	a.handleMobileWorkflowDetail(w, r)
}

func (a *App) HandleMobilePresence(w http.ResponseWriter, r *http.Request) {
	a.handleMobilePresence(w, r)
}

func (a *App) HandleMobileAgents(w http.ResponseWriter, r *http.Request) {
	a.handleMobileAgents(w, r)
}

func (a *App) HandleMobileMemoryStats(w http.ResponseWriter, r *http.Request) {
	a.handleMobileMemoryStats(w, r)
}

func (a *App) HandleMobileMemoryItems(w http.ResponseWriter, r *http.Request) {
	a.handleMobileMemoryItems(w, r)
}

func (a *App) HandleMobileStream(w http.ResponseWriter, r *http.Request) {
	a.handleMobileStream(w, r)
}

func (a *App) HandleMobileTopology(w http.ResponseWriter, r *http.Request) {
	a.handleMobileTopology(w, r)
}

func (a *App) HandleMobileGraphStats(w http.ResponseWriter, r *http.Request) {
	a.handleMobileGraphStats(w, r)
}

func (a *App) HandleMobileGraphEntities(w http.ResponseWriter, r *http.Request) {
	a.handleMobileGraphEntities(w, r)
}

func (a *App) HandleMobileGraphPath(w http.ResponseWriter, r *http.Request) {
	a.handleMobileGraphPath(w, r)
}

func (a *App) HandleMobileReasoningChains(w http.ResponseWriter, r *http.Request) {
	a.handleMobileReasoningChains(w, r)
}

func (a *App) HandleMobileReasoningChainDetail(w http.ResponseWriter, r *http.Request) {
	a.handleMobileReasoningChainDetail(w, r)
}

func (a *App) HandleMobileEventsStream(w http.ResponseWriter, r *http.Request) {
	a.handleMobileEventsStream(w, r)
}

func (a *App) HandleMobileSessionCreate(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSessionCreate(w, r)
}

func (a *App) HandleMobileSessionEnd(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSessionEnd(w, r)
}

func (a *App) HandleMobileAudit(w http.ResponseWriter, r *http.Request) {
	a.handleMobileAudit(w, r)
}

func (a *App) HandleMobileAlertsPolicy(w http.ResponseWriter, r *http.Request) {
	a.handleMobileAlertsPolicy(w, r)
}

func (a *App) HandleMobilePushRegister(w http.ResponseWriter, r *http.Request) {
	a.handleMobilePushRegister(w, r)
}

func (a *App) HandleMobilePushUnregister(w http.ResponseWriter, r *http.Request) {
	a.handleMobilePushUnregister(w, r)
}

func (a *App) HandleMobileAdminRevoke(w http.ResponseWriter, r *http.Request) {
	a.handleMobileAdminRevoke(w, r)
}

func (a *App) HandleMobileSandbox(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSandbox(w, r)
}

func (a *App) HandleMobileSandboxStart(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSandboxStart(w, r)
}

func (a *App) HandleMobileSandboxStop(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSandboxStop(w, r)
}

func (a *App) HandleMobilePipelines(w http.ResponseWriter, r *http.Request) {
	a.handleMobilePipelines(w, r)
}

func (a *App) HandleMobileWorkflowApprove(w http.ResponseWriter, r *http.Request) {
	a.handleMobileWorkflowApprove(w, r)
}

func (a *App) HandleMobileWorkflowReject(w http.ResponseWriter, r *http.Request) {
	a.handleMobileWorkflowReject(w, r)
}

func (a *App) HandleMobileHandoffs(w http.ResponseWriter, r *http.Request) {
	a.handleMobileHandoffs(w, r)
}

func (a *App) HandleMobileSpawnAgent(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSpawnAgent(w, r)
}

func (a *App) HandleMobileSpawnList(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSpawnList(w, r)
}

func (a *App) HandleMobileSpawnConfig(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSpawnConfig(w, r)
}

func (a *App) HandleMobileSpawnDetail(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSpawnDetail(w, r)
}

func (a *App) HandleMobileSpawnStop(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSpawnStop(w, r)
}

func (a *App) HandleMobileSpawnStream(w http.ResponseWriter, r *http.Request) {
	a.handleMobileSpawnStream(w, r)
}

// --- Coordinator domain adapters ---

func (a *App) HandleCoordinatorStatus(w http.ResponseWriter, r *http.Request) {
	a.handleCoordinatorStatus(w, r)
}

func (a *App) HandleCoordinatorSummarize(w http.ResponseWriter, r *http.Request) {
	a.handleCoordinatorSummarize(w, r)
}

func (a *App) HandleCoordinatorCompress(w http.ResponseWriter, r *http.Request) {
	a.handleCoordinatorCompress(w, r)
}

func (a *App) HandleCoordinatorPlan(w http.ResponseWriter, r *http.Request) {
	a.handleCoordinatorPlan(w, r)
}

// CoordinatorMetricsHandler returns the coordinator Prometheus metrics handler,
// or nil if the coordinator is not enabled.
func (a *App) CoordinatorMetricsHandler() http.Handler {
	if a.coordinatorMetrics == nil {
		return nil
	}
	return a.coordinatorMetrics.Handler()
}

// --- Sandbox domain adapters ---

func (a *App) HandleSandbox(w http.ResponseWriter, r *http.Request) {
	a.handleSandbox(w, r)
}

func (a *App) HandleSandboxPolicy(w http.ResponseWriter, r *http.Request) {
	a.handleSandboxPolicy(w, r)
}

func (a *App) HandleSandboxStart(w http.ResponseWriter, r *http.Request) {
	a.handleSandboxStart(w, r)
}

func (a *App) HandleSandboxStop(w http.ResponseWriter, r *http.Request) {
	a.handleSandboxStop(w, r)
}
