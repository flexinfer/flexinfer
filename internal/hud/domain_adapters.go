// domain_adapters.go provides exported method adapters so that *App satisfies
// the Deps interfaces for all domain packages (fleet, spawn, coordinator,
// sandbox, mobile, graph, workflow, memory, handoff).
package hud

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/domain"
	domainalerting "github.com/crb2nu/loom/internal/hud/domain/alerting"
	"github.com/crb2nu/loom/internal/hud/domain/codebase"
	domainctx "github.com/crb2nu/loom/internal/hud/domain/context"
	coorddomain "github.com/crb2nu/loom/internal/hud/domain/coordinator"
	"github.com/crb2nu/loom/internal/hud/domain/fleet"
	"github.com/crb2nu/loom/internal/hud/domain/graph"
	"github.com/crb2nu/loom/internal/hud/domain/handoff"
	"github.com/crb2nu/loom/internal/hud/domain/memory"
	domainmerge "github.com/crb2nu/loom/internal/hud/domain/merge"
	"github.com/crb2nu/loom/internal/hud/domain/mobile"
	"github.com/crb2nu/loom/internal/hud/domain/sandbox"
	domainshuttle "github.com/crb2nu/loom/internal/hud/domain/shuttle"
	domainspawn "github.com/crb2nu/loom/internal/hud/domain/spawn"
	domainweaver "github.com/crb2nu/loom/internal/hud/domain/weaver"
	domainwebhook "github.com/crb2nu/loom/internal/hud/domain/webhook"
	"github.com/crb2nu/loom/internal/hud/domain/workflow"
	"github.com/crb2nu/loom/internal/hud/monitor"
	"github.com/crb2nu/loom/pkg/projectmeta"
)

// initDomainRegistry creates and populates the domain registry. Called from
// Run() and from test helpers.
func (a *App) initDomainRegistry() {
	a.domainRegistry = domain.NewRegistry()
	a.domainRegistry.Register(fleet.New(&fleetDepsAdapter{app: a}))
	a.domainRegistry.Register(domainspawn.New(&spawnDepsAdapter{app: a}))
	a.domainRegistry.Register(mobile.New(a))
	a.domainRegistry.Register(coorddomain.New(a))
	a.domainRegistry.Register(sandbox.New(a))
	a.domainRegistry.Register(graph.New(&graphDepsAdapter{app: a}))
	a.domainRegistry.Register(workflow.New(&workflowDepsAdapter{app: a}))
	a.domainRegistry.Register(memory.New(&memoryDepsAdapter{app: a}))
	a.domainRegistry.Register(handoff.New(&handoffDepsAdapter{app: a}))
	a.domainRegistry.Register(domainmerge.New(&mergeDepsAdapter{app: a}))
	a.domainRegistry.Register(domainshuttle.New(&shuttleDepsAdapter{app: a}))
	a.domainRegistry.Register(domainctx.New(&ctxDepsAdapter{app: a}))
	a.domainRegistry.Register(codebase.New(&codebaseDepsAdapter{app: a}))
	a.domainRegistry.Register(domainalerting.New(&alertingDepsAdapter{app: a}))
	a.domainRegistry.Register(domainweaver.New(&weaverDepsAdapter{app: a}))
	a.domainRegistry.Register(domainwebhook.New(&webhookDepsAdapter{app: a}))
}

// --- Shared Deps methods (used by multiple domains) ---

func (a *App) Agent() *bridge.AgentBridge { return a.agent }
func (a *App) Logger() *slog.Logger       { return a.logger }

func (a *App) Monitors() mobile.Monitors {
	return mobile.Monitors{
		Fleet:    a.fleetMonitor,
		Health:   a.healthMonitor,
		Memory:   a.memoryMonitor,
		Workflow: a.workflowMonitor,
		Sandbox:  a.sandboxMonitor,
		Cost:     a.costMonitor,
		Pipeline: a.pipelineMonitor,
	}
}

func (a *App) MobileConfig() mobile.MobileConfig {
	return mobile.MobileConfig{
		OperatorToken:  a.config.MobileOperatorToken,
		OperatorScopes: a.config.MobileOperatorScopes,
		PushEnabled:    a.config.MobilePushEnabled,
	}
}

func (a *App) SSEHub() mobile.SSEHubOps { return a.sseHub }

func (a *App) RateLimiter() mobile.RateLimiterOps {
	if a.mobileRateLimiter == nil {
		return nil
	}
	return a.mobileRateLimiter
}

func (a *App) RevocationList() mobile.RevocationListOps {
	if a.mobileRevocationList == nil {
		return nil
	}
	return a.mobileRevocationList
}

func (a *App) DeviceTokens() mobile.DeviceTokenStoreOps {
	if a.deviceTokenStore == nil {
		return nil
	}
	return a.deviceTokenStore
}

func (a *App) EventLog() mobile.EventLogOps {
	if a.eventLog == nil {
		return nil
	}
	return &mobileEventLogAdapter{log: a.eventLog}
}

func (a *App) Spawner() mobile.SpawnerOps {
	if a.spawner == nil {
		return nil
	}
	return &mobileSpawnerAdapter{s: a.spawner}
}

func (a *App) BroadcastAgentEvent(eventType string, payload any) {
	a.broadcastAgentEvent(eventType, payload)
}

func (a *App) MaybeAutoProvisionSandbox(namespace string) {
	cached, ok := a.cache.Get("sandbox_policy")
	if !ok {
		return
	}
	policy, ok := cached.(map[string]any)
	if !ok {
		return
	}
	autoProvision, _ := policy["auto_provision"].(bool)
	if !autoProvision {
		return
	}

	project := projectmeta.FromNamespace(namespace)
	if project == "" {
		return
	}

	detectResult, err := a.client.CallTool("devbox_detect", map[string]any{"project": project})
	if err != nil {
		a.logger.Debug("sandbox auto-provision: detect failed", "project", project, "error", err)
		return
	}
	detect, err := bridge.ParseToolResultMap(detectResult)
	if err != nil {
		return
	}
	if detect["fingerprint_hash"] == nil || detect["fingerprint_hash"] == "" {
		return
	}

	_, err = a.client.CallTool("devbox_build", map[string]any{"project": project})
	if err != nil {
		a.logger.Debug("sandbox auto-provision: build failed", "project", project, "error", err)
		return
	}
	a.logger.Info("sandbox auto-provisioned", "project", project)
}

func (a *App) FetchRBACConfig() bridge.RBACConfigResult { return a.fetchRBACConfig() }
func (a *App) FetchOTelStatus() bridge.OTelStatusResult { return a.fetchOTelStatus() }

func (a *App) DoSandboxStart(project, agentID string) (map[string]any, error) {
	return a.doSandboxStart(project, agentID)
}

func (a *App) DoSandboxStop(project string) error {
	return a.doSandboxStop(project)
}

func (a *App) DoSandboxExecAsync(project, command, timeout, agentID string) (map[string]any, error) {
	return a.doSandboxExecAsync(project, command, timeout, agentID)
}

func (a *App) DoSandboxExecPoll(execID string) (map[string]any, error) {
	return a.doSandboxExecPoll(execID)
}

func (a *App) DoSandboxStatus(project string) ([]map[string]any, error) {
	return a.doSandboxStatus(project)
}

func (a *App) WriteJSON(w http.ResponseWriter, status int, v any) {
	a.writeJSON(w, status, v)
}

func (a *App) HandleSSE(w http.ResponseWriter, r *http.Request) {
	a.handleSSE(w, r)
}

func (a *App) ComputeTopology(snap monitor.FleetSnapshot) mobile.TopologyGraph {
	hudGraph := computeTopology(snap, a)
	nodes := make([]mobile.TopologyNode, len(hudGraph.Nodes))
	for i, n := range hudGraph.Nodes {
		nodes[i] = mobile.TopologyNode{
			AgentID:     n.AgentID,
			Status:      n.Status,
			AgentType:   n.AgentType,
			CurrentTask: n.CurrentTask,
			Branch:      n.Branch,
			PRUrl:       n.PRUrl,
			Namespace:   n.Namespace,
		}
	}
	edges := make([]mobile.TopologyEdge, len(hudGraph.Edges))
	for i, e := range hudGraph.Edges {
		edges[i] = mobile.TopologyEdge{
			Source:   e.Source,
			Target:   e.Target,
			EdgeType: e.EdgeType,
			Weight:   e.Weight,
			Label:    e.Label,
			Status:   e.Status,
		}
	}
	clusters := make([]mobile.TopologyCluster, len(hudGraph.Clusters))
	for i, c := range hudGraph.Clusters {
		clusters[i] = mobile.TopologyCluster{
			Project:  c.Project,
			AgentIDs: c.AgentIDs,
		}
	}
	return mobile.TopologyGraph{Nodes: nodes, Edges: edges, Clusters: clusters}
}

func (a *App) PlanSessionEndSummary(params bridge.SessionEndParams) (bridge.SessionEndParams, bool) {
	return planSessionEndSummary(params, a.coordinator != nil)
}

func (a *App) OnSessionEnd(sessionID, agentID string) {
	if a.coordinator != nil {
		go a.coordinator.OnSessionEnd(sessionID, agentID)
	}
}

// planSessionEndSummary decides whether the coordinator should own
// summarization (instead of the agent-context MCP server) for a session-end
// call. When the coordinator is enabled and the caller has not explicitly
// disabled summarization, it returns modified params with summarize=false
// (so agent-context skips its own summary) and coordinatorOwnsSummary=true
// so the caller can delegate summarization to the coordinator.
func planSessionEndSummary(params bridge.SessionEndParams, coordinatorEnabled bool) (bridge.SessionEndParams, bool) {
	shouldSummarize := params.Summarize == nil || *params.Summarize
	if !coordinatorEnabled || !shouldSummarize {
		return params, false
	}

	planned := params
	summarize := false
	planned.Summarize = &summarize
	planned.SummaryAsync = false
	return planned, true
}

func (a *App) MemoryStatsPayload(stats *bridge.MemoryStatsResult) map[string]any {
	return memory.StatsPayload(stats)
}

func (a *App) FleetIncrementKPI(field string, delta int) {
	a.fleetMonitor.IncrementKPI(field, delta)
}

func (a *App) FleetRefresh() {
	a.fleetMonitor.Refresh()
}

func (a *App) RequireAdminToken(w http.ResponseWriter, r *http.Request) bool {
	return a.requireAdminToken(w, r)
}

// --- Coordinator domain Deps implementation ---

// Coordinator returns the coordinator operations, or nil if not enabled.
func (a *App) Coordinator() coorddomain.CoordinatorOps {
	if a.coordinator == nil {
		return nil
	}
	return a.coordinator
}

// CoordinatorMetrics returns the coordinator metrics, or nil if not enabled.
func (a *App) CoordinatorMetrics() coorddomain.MetricsOps {
	if a.coordinatorMetrics == nil {
		return nil
	}
	return a.coordinatorMetrics
}

// WriteError writes a JSON error response (exported for domain packages).
func (a *App) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	a.writeError(w, status, msg, err)
}

// --- Sandbox domain Deps implementation ---

func (a *App) SandboxSnapshot() map[string]any {
	return a.sandboxMonitor.Snapshot()
}

func (a *App) CacheGet(key string) (any, bool) {
	return a.cache.Get(key)
}

func (a *App) CacheSet(key string, value any, ttl time.Duration) {
	a.cache.Set(key, value, ttl)
}
