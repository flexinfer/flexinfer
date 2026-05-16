// Package monitor provides aggregation monitors that poll the bridge layer
// and maintain cached snapshots of fleet, health, workflow, and memory state
// for the HUD HTTP API.
package monitor

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/coordination"
	"github.com/crb2nu/loom/internal/hud/fleetview"
	"github.com/crb2nu/loom/internal/hud/notify"
	"github.com/crb2nu/loom/internal/visibility/contracts/presence"
	"github.com/crb2nu/loom/internal/visibility/contracts/status"
)

// FleetSnapshot is the aggregated fleet state served to the frontend.
// It combines daemon status, agent sessions, tasks, memory, graph,
// and workflow summaries into a single JSON-serializable struct.
type FleetSnapshot struct {
	// Daemon status
	DaemonRunning bool     `json:"daemon_running"`
	ServerCount   int      `json:"server_count"`
	ActiveConns   int      `json:"active_conns"`
	Processes     []string `json:"processes"`

	// Agent sessions
	Sessions       []bridge.SessionInfo `json:"sessions"`
	ActiveSessions int                  `json:"active_sessions"`
	TotalSessions  int                  `json:"total_sessions"`
	// StaleSessions counts active sessions whose joined presence has not
	// heartbeated past fleetStaleSessionReapAfter. These were dropped from
	// Sessions for the current snapshot and a background reaper has been
	// dispatched to call agent_session_end on them.
	StaleSessions int `json:"stale_sessions"`

	// Task summary
	TotalTasks   int               `json:"total_tasks"`
	PendingTasks int               `json:"pending_tasks"`
	ActiveTasks  int               `json:"active_tasks"`
	BlockedTasks int               `json:"blocked_tasks"`
	Tasks        []bridge.TaskInfo `json:"tasks"`

	// Token summary (across all sessions)
	TotalTokens int `json:"total_tokens"`

	// Memory summary
	MemoryTotalItems  int `json:"memory_total_items"`
	MemoryTotalTokens int `json:"memory_total_tokens"`

	// Graph summary
	EntityCount   int `json:"entity_count"`
	RelationCount int `json:"relation_count"`

	// Workflow summary
	RunningWorkflows int `json:"running_workflows"`
	PendingApprovals int `json:"pending_approvals"`

	// Agent presence
	ActiveAgents    int                     `json:"active_agents"`
	IdleAgents      int                     `json:"idle_agents"`
	OfflineAgents   int                     `json:"offline_agents"`
	OrphanAgents    int                     `json:"orphan_agents"`
	Agents          []presence.PresenceInfo `json:"agents"`
	FileClaims      []bridge.FileClaimInfo  `json:"file_claims"`
	ActiveWorktrees int                     `json:"active_worktrees"`
	Worktrees       []bridge.WorktreeInfo   `json:"worktrees"`
	Coordination    coordination.Snapshot   `json:"coordination"`

	// Spawned agents (K8s pods)
	Spawns []SpawnInfo `json:"spawns"`

	// Metadata
	UpdatedAt time.Time `json:"updated_at"`
}

// SpawnInfo is a flat representation of a spawned agent for fleet aggregation.
type SpawnInfo struct {
	SpawnID   string `json:"spawn_id"`
	AgentID   string `json:"agent_id"`
	PodName   string `json:"pod_name"`
	Status    string `json:"status"`
	Project   string `json:"project"`
	Branch    string `json:"branch"`
	Task      string `json:"task_description"`
	AgentType string `json:"agent_type"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at,omitempty"`
	Error     string `json:"error,omitempty"`

	// Telemetry summary (populated from SpawnTelemetry when available).
	TurnCount       int     `json:"turn_count"`
	TotalCostUSD    float64 `json:"total_cost_usd"`
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	ToolCallCount   int     `json:"tool_call_count"`
	FileChangeCount int     `json:"file_change_count"`
	StopReason      string  `json:"stop_reason,omitempty"`
	LastMessage     string  `json:"last_message,omitempty"`
}

// SpawnLister provides active spawn states for fleet aggregation.
type SpawnLister interface {
	ListSpawnInfos() []SpawnInfo
}

// ConflictDetail describes a single file claimed by multiple agents.
type ConflictDetail struct {
	Path   string   `json:"path"`
	Agents []string `json:"agents"`
}

// KPICounters tracks daily aggregate metrics for the HUD dashboard.
type KPICounters struct {
	SessionsToday       int              `json:"sessions_today"`
	TokensToday         int              `json:"tokens_today"`
	TasksCompletedToday int              `json:"tasks_completed_today"`
	FileConflicts       int              `json:"file_conflicts"`
	ConflictDetails     []ConflictDetail `json:"conflict_details,omitempty"`

	// Internal tracking.
	resetDate string // YYYY-MM-DD of last reset
}

// fleetOrphanReapAfter is how long an orphan presence (heartbeating, no
// matching active session) is tolerated before the fleet monitor auto-
// deregisters it. 10 minutes is long enough that a genuine session-start
// retry window has closed, short enough that orphans don't accumulate
// across a workday.
const fleetOrphanReapAfter = 10 * time.Minute

// fleetOrphanReapCooldown prevents hammering the MCP server when a reap
// fails: a just-attempted agent is skipped for this long before the next
// refresh will queue it again.
const fleetOrphanReapCooldown = 2 * time.Minute

// fleetStaleSessionReapAfter is how long an "active" session may exist
// without a fresh heartbeat from its agent before the fleet monitor
// calls agent_session_end on it. Defense-in-depth against zombie
// sessions left behind when a vendor CLI is killed without firing its
// SessionEnd/Stop hook, or when a Mills spawn pod is deleted before
// reapTerminalSpawn completes the EndSession call. Matches
// fleetOrphanReapAfter so both reapers age out on the same horizon.
const fleetStaleSessionReapAfter = 10 * time.Minute

// fleetStaleSessionReapCooldown mirrors fleetOrphanReapCooldown — once
// agent_session_end has been attempted, skip retries for this long so
// a stuck MCP call doesn't flood the agent-context server on every 5s
// refresh.
const fleetStaleSessionReapCooldown = 2 * time.Minute

// FleetMonitor aggregates data from the daemon client and agent bridge
// into a FleetSnapshot. It runs a background goroutine that polls all
// data sources at a configurable interval.
//
// FleetMonitor embeds BaseMonitor for lifecycle management (stop, pollLoop,
// OnRefresh, Snapshot) but keeps its own complex Refresh implementation
// with KPI tracking, conflict detection, and notification logic.
type FleetMonitor struct {
	BaseMonitor[FleetSnapshot]
	client bridge.Caller
	agent  *bridge.AgentBridge
	spawns SpawnLister // optional -- nil when spawn orchestrator not configured

	lastRefresh time.Time // debounce: skip Refresh() if <2s since last
	refreshing  bool      // coalesce concurrent refreshes into a single in-flight run

	// Handoff notification dedup: tracks handoff IDs already notified.
	notifiedHandoffs map[string]bool

	// Orphan reap dedup: agent_id -> time of last reap attempt. Prevents
	// re-reaping the same agent on every 5s refresh while a single deregister
	// call is still in flight or recently failed.
	orphanReapedAt map[string]time.Time

	// Stale session reap dedup: session_id -> time of last
	// agent_session_end attempt. Same rationale as orphanReapedAt: a single
	// end call is enough; later refreshes should not pile on while the
	// previous attempt is in flight.
	staleSessionReapedAt map[string]time.Time

	// KPI counters -- daily aggregate metrics.
	kpis KPICounters

	// Previous snapshot for diff-based notifications.
	prevFileClaims []bridge.FileClaimInfo
	prevApprovals  int
}

// NewFleetMonitor creates a FleetMonitor backed by the given caller and agent bridge.
func NewFleetMonitor(client bridge.Caller, agent *bridge.AgentBridge, logger *slog.Logger) *FleetMonitor {
	m := &FleetMonitor{
		client:               client,
		agent:                agent,
		notifiedHandoffs:     make(map[string]bool),
		orphanReapedAt:       make(map[string]time.Time),
		staleSessionReapedAt: make(map[string]time.Time),
	}
	m.InitBase(logger, nil, "fleet-monitor")
	return m
}

// reapOrphans deregisters presence for agents that have been orphan
// (heartbeating but with no matching active session) past
// fleetOrphanReapAfter. Runs in a goroutine so the fleet refresh path
// stays non-blocking; each candidate is gated by a per-agent cooldown so
// a stuck MCP call doesn't flood retries. On success the presence row
// disappears on the next refresh.
func (m *FleetMonitor) reapOrphans(agentIDs []string) {
	now := time.Now()
	for _, agentID := range agentIDs {
		if m.agent == nil {
			return
		}
		m.Lock()
		last, seen := m.orphanReapedAt[agentID]
		if seen && now.Sub(last) < fleetOrphanReapCooldown {
			m.Unlock()
			continue
		}
		m.orphanReapedAt[agentID] = now
		m.Unlock()

		if err := m.agent.PresenceDeregister(agentID); err != nil {
			m.Logger.Warn("fleet: orphan reap failed",
				"agent_id", agentID, "error", err)
			continue
		}
		m.Logger.Info("fleet: reaped orphan presence",
			"agent_id", agentID,
			"reap_after_seconds", int(fleetOrphanReapAfter.Seconds()))
	}
}

// staleSessionRef captures the data needed to end a single stale
// session. We keep heartbeat age around for log breadcrumbs.
type staleSessionRef struct {
	SessionID           string
	AgentID             string
	HeartbeatAgeSeconds int
}

// reapStaleSessions ends sessions whose joined presence has not
// heartbeated past fleetStaleSessionReapAfter. Runs in a goroutine so
// the fleet refresh path stays non-blocking; each candidate is gated
// by a per-session cooldown so a stuck MCP call doesn't flood retries.
// On success the session disappears from agent_session_list on the
// next refresh; we also drop it from the current snapshot so the UI
// count is correct immediately.
func (m *FleetMonitor) reapStaleSessions(refs []staleSessionRef) {
	now := time.Now()
	for _, ref := range refs {
		if m.agent == nil {
			return
		}
		if ref.SessionID == "" {
			continue
		}
		m.Lock()
		last, seen := m.staleSessionReapedAt[ref.SessionID]
		if seen && now.Sub(last) < fleetStaleSessionReapCooldown {
			m.Unlock()
			continue
		}
		m.staleSessionReapedAt[ref.SessionID] = now
		m.Unlock()

		// Suppress the summarization roundtrip — a stale session's
		// agent is already gone, so there is no meaningful working
		// context to harvest and we don't want to pay the recall
		// budget on a zombie.
		summarize := false
		ended, err := m.agent.EndSession(bridge.SessionEndParams{
			SessionID: ref.SessionID,
			AgentID:   ref.AgentID,
			Summarize: &summarize,
		})
		if err != nil {
			m.Logger.Warn("fleet: stale session reap failed",
				"session_id", ref.SessionID,
				"agent_id", ref.AgentID,
				"heartbeat_age_seconds", ref.HeartbeatAgeSeconds,
				"error", err)
			continue
		}
		if !ended {
			// Session was already gone on the MCP side (race with a
			// SessionEnd hook that finally fired). Nothing to log.
			continue
		}
		m.Logger.Info("fleet: reaped stale session",
			"session_id", ref.SessionID,
			"agent_id", ref.AgentID,
			"heartbeat_age_seconds", ref.HeartbeatAgeSeconds,
			"reap_after_seconds", int(fleetStaleSessionReapAfter.Seconds()))
	}
}

// Ready reports whether the monitor has been fully initialized.
func (m *FleetMonitor) Ready() bool {
	return m != nil && m.stopCh != nil && m.Logger != nil
}

// SetSpawnLister injects a spawn source for fleet aggregation.
// Call after both FleetMonitor and SpawnOrchestrator are initialized.
func (m *FleetMonitor) SetSpawnLister(sl SpawnLister) {
	m.spawns = sl
}

// Start begins the background polling goroutine at the given interval.
func (m *FleetMonitor) Start(interval time.Duration) {
	m.StartLoop(interval, m.Refresh)
}

// KPIs returns the current daily KPI counters.
func (m *FleetMonitor) KPIs() KPICounters {
	m.RLock()
	defer m.RUnlock()
	return m.kpis
}

// IncrementKPI atomically increments a specific KPI counter.
func (m *FleetMonitor) IncrementKPI(field string, delta int) {
	m.Lock()
	defer m.Unlock()

	// Auto-reset on day change.
	today := time.Now().Format("2006-01-02")
	if m.kpis.resetDate != today {
		m.kpis = KPICounters{resetDate: today}
	}

	switch field {
	case "sessions":
		m.kpis.SessionsToday += delta
	case "tokens":
		m.kpis.TokensToday += delta
	case "tasks_completed":
		m.kpis.TasksCompletedToday += delta
	}
}

// OfflineAgentsWithActiveSessions returns agents that are offline but still
// have active sessions -- candidates for session reaping.
func (m *FleetMonitor) OfflineAgentsWithActiveSessions() []presence.PresenceInfo {
	m.RLock()
	snap := m.GetSnapshot()
	m.RUnlock()

	// Build set of agents with active sessions.
	activeSessionAgents := make(map[string]bool)
	for _, s := range snap.Sessions {
		if s.Status == "active" {
			activeSessionAgents[s.AgentID] = true
		}
	}

	var result []presence.PresenceInfo
	for _, a := range snap.Agents {
		if a.Status == "offline" && activeSessionAgents[a.AgentID] {
			result = append(result, a)
		}
	}
	return result
}

// Refresh forces an immediate refresh of all data sources and updates
// the snapshot. Each sub-fetch is independent; errors are logged but
// do not prevent other fetches from completing.
func (m *FleetMonitor) Refresh() error {
	return m.refresh(false)
}

// RefreshForce refreshes immediately, bypassing the short debounce window.
// This is used by embedded HUD startup/reload hooks so a transient empty
// snapshot does not linger until the next polling tick.
func (m *FleetMonitor) RefreshForce() error {
	return m.refresh(true)
}

func (m *FleetMonitor) refresh(force bool) error {
	prev := m.Snapshot()

	// Coalesce concurrent refreshes so heartbeat-triggered refresh storms do not
	// pile up overlapping agent-context calls while one refresh is already busy.
	m.Lock()
	if m.refreshing {
		m.Unlock()
		m.Logger.Debug("fleet refresh skipped; refresh already in flight")
		return nil
	}

	// Debounce: skip if less than 2s since last refresh to prevent stampede
	// when multiple handlers fire go Refresh() concurrently.
	if !force && time.Since(m.lastRefresh) < 2*time.Second {
		m.Unlock()
		m.Logger.Debug("fleet refresh debounced")
		return nil
	}
	m.refreshing = true
	m.Unlock()
	defer func() {
		m.Lock()
		m.refreshing = false
		m.Unlock()
	}()

	snap := FleetSnapshot{
		UpdatedAt: time.Now(),
	}

	// Fetch daemon status.
	if raw, err := m.client.Call("loom/status", nil); err != nil {
		m.Logger.Warn("fleet: failed to fetch daemon status", "error", err)
	} else {
		var daemonStatus status.DaemonRPCStatus
		if err := json.Unmarshal(raw, &daemonStatus); err != nil {
			m.Logger.Warn("fleet: failed to unmarshal daemon status", "error", err)
		} else {
			snap.DaemonRunning = daemonStatus.Running
			snap.ServerCount = daemonStatus.Servers
			snap.ActiveConns = daemonStatus.ActiveConns
			snap.Processes = daemonStatus.Processes
		}
	}

	// Fetch agent sessions. Session counts and totals are recomputed after
	// the fleetview.Join below so the stale-session filter can drop zombie
	// sessions (no fresh heartbeat) from the snapshot in a single pass.
	if sessions, err := m.agent.Sessions(); err != nil {
		m.Logger.Warn("fleet: failed to fetch sessions", "error", err)
	} else {
		snap.Sessions = sessions
	}

	// Fetch all tasks.
	if tasks, err := m.agent.AllTasks(); err != nil {
		m.Logger.Warn("fleet: failed to fetch tasks", "error", err)
	} else {
		snap.Tasks = tasks
		snap.TotalTasks = len(tasks)
		for _, t := range tasks {
			switch t.Status {
			case "pending":
				snap.PendingTasks++
			case "active", "in_progress":
				snap.ActiveTasks++
			case "blocked":
				snap.BlockedTasks++
			}
		}
	}

	// Fetch memory stats.
	if memStats, err := m.agent.MemoryStats(); err != nil {
		m.Logger.Warn("fleet: failed to fetch memory stats", "error", err)
	} else {
		snap.MemoryTotalItems = memStats.TotalItems
		snap.MemoryTotalTokens = memStats.TotalTokens
	}

	// Fetch graph stats.
	if graphStats, err := m.agent.GraphStats(); err != nil {
		m.Logger.Warn("fleet: failed to fetch graph stats", "error", err)
	} else {
		snap.EntityCount = graphStats.EntityCount
		snap.RelationCount = graphStats.RelationCount
	}

	// Fetch workflow list.
	if workflows, err := m.agent.WorkflowList(); err != nil {
		m.Logger.Warn("fleet: failed to fetch workflows", "error", err)
	} else {
		for _, w := range workflows {
			switch w.Status {
			case "running":
				snap.RunningWorkflows++
			case "waiting_approval":
				snap.PendingApprovals++
			}
		}
	}

	// Fetch agent presence. We keep the raw slice around so the
	// stale-session filter below can compute heartbeat ages without
	// having to call fleetview.Join twice (the first join would already
	// have synthesized session-only rows for sessions we're about to
	// drop, which would then leak back through a second join).
	var rawAgents []presence.PresenceInfo
	if agents, err := m.agent.PresenceList(true); err != nil {
		m.Logger.Warn("fleet: failed to fetch presence", "error", err)
	} else {
		rawAgents = agents
	}

	// Stale-session filter: identify sessions whose backing agent has
	// not heartbeated past fleetStaleSessionReapAfter, drop them from
	// the snapshot, and dispatch a background reaper to call
	// agent_session_end on each. Heartbeat lookup precedence:
	//   1. raw presence keyed by session_id (most precise),
	//   2. raw presence keyed by agent_id,
	//   3. session.StartedAt as a fallback when no presence row exists
	//      OR the presence row never reported a heartbeat — in either
	//      case the session has been alive for its entire life without
	//      any liveness signal, so its own age is the correct
	//      staleness clock.
	//
	// Presence rows with an empty LastHeartbeat are skipped here. Before
	// this guard, fleetview.AgeSeconds clamped empty/unparseable values
	// to 0, which seeded the maps with age=0 and made every such session
	// look freshly heartbeated forever — visible as spawn-* rows stuck
	// in "active" with HEARTBEAT="---" long after the spawn pod died.
	heartbeatBySession := make(map[string]int, len(rawAgents))
	heartbeatByAgent := make(map[string]int, len(rawAgents))
	for _, p := range rawAgents {
		if strings.TrimSpace(p.LastHeartbeat) == "" {
			continue
		}
		age := fleetview.AgeSeconds(p.LastHeartbeat, snap.UpdatedAt)
		if p.SessionID != "" {
			heartbeatBySession[p.SessionID] = age
		}
		if p.AgentID != "" {
			// Prefer the freshest heartbeat we can find for this
			// agent when multiple presence rows exist (shouldn't
			// happen in practice, but the MCP server doesn't enforce
			// uniqueness for our purposes).
			if existing, ok := heartbeatByAgent[p.AgentID]; !ok || age < existing {
				heartbeatByAgent[p.AgentID] = age
			}
		}
	}
	reapThresholdSeconds := int(fleetStaleSessionReapAfter.Seconds())
	liveSessions := snap.Sessions[:0]
	var staleSessions []staleSessionRef
	for _, s := range snap.Sessions {
		isActive := s.Status == "active"
		age, ok := heartbeatBySession[s.ID]
		if !ok {
			age, ok = heartbeatByAgent[s.AgentID]
		}
		if !ok {
			age = fleetview.AgeSeconds(s.StartedAt, snap.UpdatedAt)
		}
		if isActive && age >= reapThresholdSeconds {
			snap.StaleSessions++
			staleSessions = append(staleSessions, staleSessionRef{
				SessionID:           s.ID,
				AgentID:             s.AgentID,
				HeartbeatAgeSeconds: age,
			})
			continue
		}
		liveSessions = append(liveSessions, s)
		if isActive {
			snap.ActiveSessions++
		}
		snap.TotalTokens += s.TotalTokens
	}
	snap.Sessions = liveSessions
	snap.TotalSessions = len(snap.Sessions)
	if len(staleSessions) > 0 {
		go m.reapStaleSessions(staleSessions)
	}

	// Join raw presence against the filtered session list. Sessions
	// reaped above don't produce synthetic session-only agent rows here,
	// and presence rows that previously matched a now-stale session
	// revert to presence-only (and may be picked up by the orphan
	// reaper on a later refresh if they keep heartbeating without
	// re-establishing a session).
	snap.Agents = fleetview.Join(rawAgents, snap.Sessions, snap.UpdatedAt)

	var reapCandidates []string
	for _, a := range snap.Agents {
		switch a.Status {
		case "active":
			snap.ActiveAgents++
		case "idle":
			snap.IdleAgents++
		case "offline":
			snap.OfflineAgents++
		}
		if a.IsOrphan {
			snap.OrphanAgents++
			if a.OrphanAgeSeconds >= int(fleetOrphanReapAfter.Seconds()) {
				reapCandidates = append(reapCandidates, a.AgentID)
			}
		}
	}
	// Auto-deregister orphans past the reap threshold. Fire-and-forget so
	// the fleet refresh path stays non-blocking; failures are retried on
	// the next refresh. See fleetOrphanReapAfter rationale.
	if len(reapCandidates) > 0 {
		go m.reapOrphans(reapCandidates)
	}

	// Fetch file claims.
	if claims, err := m.agent.FileClaimList(""); err != nil {
		m.Logger.Warn("fleet: failed to fetch file claims", "error", err)
	} else {
		snap.FileClaims = claims
	}

	// Fetch worktree assignments.
	if worktrees, err := m.agent.WorktreeList("", "active"); err != nil {
		m.Logger.Warn("fleet: failed to fetch worktrees", "error", err)
	} else {
		snap.Worktrees = worktrees
		snap.ActiveWorktrees = len(worktrees)
	}

	// Fetch active spawns.
	if m.spawns != nil {
		snap.Spawns = m.spawns.ListSpawnInfos()
	}

	snap.Coordination = coordination.Build(
		snap.Sessions,
		snap.Tasks,
		snap.Agents,
		snap.FileClaims,
		snap.Worktrees,
	)

	// --- KPI daily counter reset ---
	m.Lock()
	today := time.Now().Format("2006-01-02")
	if m.kpis.resetDate != today {
		m.kpis = KPICounters{resetDate: today}
	}
	// Update token counter from current snapshot.
	m.kpis.TokensToday = snap.TotalTokens
	// Update file conflicts count and details.
	conflictCount, conflictDetails := detectConflicts(snap.FileClaims)
	m.kpis.FileConflicts = conflictCount
	m.kpis.ConflictDetails = conflictDetails
	m.Unlock()

	// --- Proactive notifications: conflict detection ---
	newConflicts := conflictCount
	m.RLock()
	prevConflicts, _ := detectConflicts(m.prevFileClaims)
	m.RUnlock()
	if newConflicts > prevConflicts {
		go func() {
			if err := notify.NotifyConflict(newConflicts); err != nil {
				m.Logger.Debug("conflict notification failed", "error", err)
			}
		}()
	}

	// Intentionally skip desktop notifications for pending approvals. Workflow
	// churn makes them too noisy in practice, and the HUD/app surfaces the state
	// directly.

	// Check for new handoffs and send desktop notifications.
	if handoffs, err := m.agent.HandoffList(); err != nil {
		m.Logger.Debug("fleet: failed to fetch handoffs for notification", "error", err)
	} else {
		m.Lock()
		for _, h := range handoffs {
			if h.Status == "pending" && !m.notifiedHandoffs[h.ID] {
				m.notifiedHandoffs[h.ID] = true
				go func(from, to, summary string) {
					if err := notify.NotifyHandoff(from, to, summary); err != nil {
						m.Logger.Debug("handoff notification failed", "error", err)
					}
				}(h.FromAgent, h.ToAgent, h.Summary)
			}
		}
		m.Unlock()
	}

	// Commit the snapshot atomically and save previous state for diff-based notifications.
	m.Lock()
	m.prevFileClaims = m.GetSnapshot().FileClaims
	m.prevApprovals = m.GetSnapshot().PendingApprovals
	if fleetSnapshotLooksEmpty(snap) && !fleetSnapshotLooksEmpty(prev) {
		m.Logger.Info("fleet refresh returned empty; preserving previous snapshot")
		snap = prev
	}
	m.SetSnapshot(snap)
	m.lastRefresh = time.Now()
	m.Unlock()

	// Notify listeners (e.g., SSE hub) with the fresh snapshot.
	m.FireOnRefresh(snap)

	return nil
}

// detectConflicts counts the number of files claimed by multiple agents
// and returns up to 5 conflict details.
func detectConflicts(claims []bridge.FileClaimInfo) (int, []ConflictDetail) {
	fileAgents := make(map[string]map[string]bool)
	for _, c := range claims {
		if fileAgents[c.FilePath] == nil {
			fileAgents[c.FilePath] = make(map[string]bool)
		}
		fileAgents[c.FilePath][c.AgentID] = true
	}
	conflicts := 0
	var details []ConflictDetail
	for path, agents := range fileAgents {
		if len(agents) > 1 {
			conflicts++
			if len(details) < 5 {
				agentList := make([]string, 0, len(agents))
				for a := range agents {
					agentList = append(agentList, a)
				}
				details = append(details, ConflictDetail{Path: path, Agents: agentList})
			}
		}
	}
	return conflicts, details
}

func fleetSnapshotLooksEmpty(s FleetSnapshot) bool {
	return len(s.Agents) == 0 &&
		len(s.Tasks) == 0 &&
		len(s.Sessions) == 0 &&
		len(s.FileClaims) == 0 &&
		len(s.Worktrees) == 0 &&
		len(s.Spawns) == 0 &&
		s.ActiveSessions == 0 &&
		s.TotalSessions == 0 &&
		s.TotalTasks == 0
}
