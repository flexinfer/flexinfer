// domain_adapters.go provides exported method adapters so that *App satisfies
// the Deps interfaces for all domain packages (fleet, spawn, coordinator,
// sandbox, mobile, graph, workflow, memory, handoff).
package hud

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/domain"
	coorddomain "github.com/crb2nu/loom/internal/hud/domain/coordinator"
	"github.com/crb2nu/loom/internal/hud/domain/fleet"
	"github.com/crb2nu/loom/internal/hud/domain/graph"
	"github.com/crb2nu/loom/internal/hud/domain/handoff"
	"github.com/crb2nu/loom/internal/hud/domain/memory"
	"github.com/crb2nu/loom/internal/hud/domain/mobile"
	"github.com/crb2nu/loom/internal/hud/domain/sandbox"
	domainspawn "github.com/crb2nu/loom/internal/hud/domain/spawn"
	"github.com/crb2nu/loom/internal/hud/domain/workflow"
	"github.com/crb2nu/loom/internal/hud/monitor"
	pkgspawn "github.com/crb2nu/loom/internal/spawn"
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

	project := namespace
	if i := strings.Index(namespace, "/"); i > 0 {
		project = namespace[:i]
	}
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

// --- Mobile adapter helpers ---

// mobileEventLogAdapter wraps *EventLog to satisfy mobile.EventLogOps,
// converting hud.TimelineEntry to mobile.TimelineEntry.
type mobileEventLogAdapter struct {
	log *EventLog
}

func (e *mobileEventLogAdapter) All(limit int) []mobile.TimelineEntry {
	entries := e.log.All(limit)
	result := make([]mobile.TimelineEntry, len(entries))
	for i, entry := range entries {
		result[i] = mobile.TimelineEntry{
			Timestamp: entry.Timestamp,
			EventType: entry.EventType,
			AgentID:   entry.AgentID,
			AgentType: entry.AgentType,
			Data:      entry.Data,
		}
	}
	return result
}

// mobileSpawnerAdapter wraps *SpawnOrchestrator to satisfy mobile.SpawnerOps.
type mobileSpawnerAdapter struct {
	s *SpawnOrchestrator
}

func (sa *mobileSpawnerAdapter) Spawn(ctx context.Context, req pkgspawn.Request) (string, error) {
	return sa.s.Spawn(ctx, req)
}

func (sa *mobileSpawnerAdapter) GetSpawn(spawnID string) (*pkgspawn.State, bool) {
	return sa.s.GetSpawn(spawnID)
}

func (sa *mobileSpawnerAdapter) ListSpawns() []*pkgspawn.State {
	return sa.s.ListSpawns()
}

func (sa *mobileSpawnerAdapter) StopSpawn(ctx context.Context, spawnID string) error {
	return sa.s.StopSpawn(ctx, spawnID)
}

func (sa *mobileSpawnerAdapter) Projects() []string {
	return sa.s.Projects()
}

// --- Spawn domain Deps adapter ---

// spawnDepsAdapter wraps *App to satisfy domainspawn.Deps. A separate adapter
// is needed because *App.Spawner() returns mobile.SpawnerOps (for the mobile
// domain), while spawn.Deps requires spawn.SpawnerOps. Both interfaces have
// identical method sets but are distinct Go types.
type spawnDepsAdapter struct {
	app *App
}

func (s *spawnDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	s.app.WriteJSON(w, status, v)
}

func (s *spawnDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	s.app.WriteError(w, status, msg, err)
}

func (s *spawnDepsAdapter) RequireAdminToken(w http.ResponseWriter, r *http.Request) bool {
	return s.app.RequireAdminToken(w, r)
}

func (s *spawnDepsAdapter) Spawner() domainspawn.SpawnerOps {
	if s.app.spawner == nil {
		return nil
	}
	return s.app.spawner
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

// --- Graph domain Deps adapter ---

// graphDepsAdapter wraps *App to satisfy graph.Deps.
type graphDepsAdapter struct {
	app *App
}

func (g *graphDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	g.app.WriteJSON(w, status, v)
}

func (g *graphDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	g.app.WriteError(w, status, msg, err)
}

func (g *graphDepsAdapter) Logger() *slog.Logger { return g.app.Logger() }

func (g *graphDepsAdapter) Agent() *bridge.AgentBridge { return g.app.Agent() }

func (g *graphDepsAdapter) CacheGet(key string) (any, bool) { return g.app.CacheGet(key) }

func (g *graphDepsAdapter) CacheSet(key string, value any, ttl time.Duration) {
	g.app.CacheSet(key, value, ttl)
}

// --- Workflow domain Deps adapter ---

type workflowDepsAdapter struct {
	app *App
}

func (w *workflowDepsAdapter) WriteJSON(wr http.ResponseWriter, status int, v any) {
	w.app.WriteJSON(wr, status, v)
}

func (w *workflowDepsAdapter) WriteError(wr http.ResponseWriter, status int, msg string, err error) {
	w.app.WriteError(wr, status, msg, err)
}

func (w *workflowDepsAdapter) Logger() *slog.Logger { return w.app.Logger() }

func (w *workflowDepsAdapter) BroadcastAgentEvent(eventType string, payload any) {
	w.app.BroadcastAgentEvent(eventType, payload)
}

func (w *workflowDepsAdapter) WorkflowMonitor() workflow.WorkflowMonitorOps {
	return &workflowMonitorAdapter{mon: w.app.workflowMonitor}
}

// workflowMonitorAdapter converts between monitor types and workflow domain types.
type workflowMonitorAdapter struct {
	mon *monitor.WorkflowMonitor
}

func (a *workflowMonitorAdapter) Workflows() []workflow.WorkflowSummary {
	infos := a.mon.Workflows()
	out := make([]workflow.WorkflowSummary, len(infos))
	for i, wf := range infos {
		out[i] = workflow.WorkflowSummary{
			ID:          wf.ID,
			Name:        wf.Name,
			Status:      wf.Status,
			CurrentStep: wf.CurrentStep,
			CreatedAt:   wf.CreatedAt,
			Progress:    wf.Progress,
			Error:       wf.Error,
		}
	}
	return out
}

func (a *workflowMonitorAdapter) Detail(id string) (*workflow.WorkflowDetail, error) {
	detail, err := a.mon.Detail(id)
	if err != nil {
		return nil, err
	}
	steps := make([]workflow.WorkflowStep, len(detail.Steps))
	for i, s := range detail.Steps {
		steps[i] = workflow.WorkflowStep{
			ID:     s.ID,
			Name:   s.Name,
			Status: s.Status,
			Type:   s.Type,
		}
	}
	events := make([]workflow.WorkflowEvent, len(detail.Events))
	for i, e := range detail.Events {
		events[i] = workflow.WorkflowEvent{
			ID:        e.ID,
			EventType: e.EventType,
			Timestamp: e.Timestamp,
			StepID:    e.StepID,
			Details:   e.Details,
		}
	}
	return &workflow.WorkflowDetail{
		ID:          detail.ID,
		Name:        detail.Name,
		Status:      detail.Status,
		CurrentStep: detail.CurrentStep,
		Progress:    detail.Progress,
		CreatedAt:   detail.CreatedAt,
		StartedAt:   detail.StartedAt,
		CompletedAt: detail.CompletedAt,
		Error:       detail.Error,
		Steps:       steps,
		Events:      events,
	}, nil
}

func (a *workflowMonitorAdapter) ApproveStep(workflowID, stepID string) error {
	return a.mon.ApproveStep(workflowID, stepID)
}

func (a *workflowMonitorAdapter) RejectStep(workflowID, stepID string) error {
	return a.mon.RejectStep(workflowID, stepID)
}

func (a *workflowMonitorAdapter) CancelWorkflow(id string) error {
	return a.mon.CancelWorkflow(id)
}

func (a *workflowMonitorAdapter) Refresh() {
	_ = a.mon.Refresh()
}

// --- Memory domain Deps adapter ---

type memoryDepsAdapter struct {
	app *App
}

func (m *memoryDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	m.app.WriteJSON(w, status, v)
}

func (m *memoryDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	m.app.WriteError(w, status, msg, err)
}

func (m *memoryDepsAdapter) Logger() *slog.Logger { return m.app.Logger() }

func (m *memoryDepsAdapter) Agent() *bridge.AgentBridge { return m.app.Agent() }

func (m *memoryDepsAdapter) BroadcastAgentEvent(eventType string, payload any) {
	m.app.BroadcastAgentEvent(eventType, payload)
}

func (m *memoryDepsAdapter) MemoryMonitor() memory.MemoryMonitorOps {
	return m.app.memoryMonitor
}

// --- Handoff domain Deps adapter ---

type handoffDepsAdapter struct {
	app *App
}

func (h *handoffDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	h.app.WriteJSON(w, status, v)
}

func (h *handoffDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	h.app.WriteError(w, status, msg, err)
}

func (h *handoffDepsAdapter) Logger() *slog.Logger { return h.app.Logger() }

func (h *handoffDepsAdapter) Agent() *bridge.AgentBridge { return h.app.Agent() }

func (h *handoffDepsAdapter) BroadcastAgentEvent(eventType string, payload any) {
	h.app.BroadcastAgentEvent(eventType, payload)
}
