// Package monitor provides aggregation monitors that poll the bridge layer
// and maintain cached snapshots of fleet, health, workflow, and memory state
// for the HUD HTTP API.
package monitor

import (
	"log/slog"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/coordination"
	"github.com/crb2nu/loom/internal/hud/notify"
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
	ActiveAgents    int                    `json:"active_agents"`
	IdleAgents      int                    `json:"idle_agents"`
	OfflineAgents   int                    `json:"offline_agents"`
	Agents          []bridge.PresenceInfo  `json:"agents"`
	FileClaims      []bridge.FileClaimInfo `json:"file_claims"`
	ActiveWorktrees int                    `json:"active_worktrees"`
	Worktrees       []bridge.WorktreeInfo  `json:"worktrees"`
	Coordination    coordination.Snapshot  `json:"coordination"`

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

// FleetMonitor aggregates data from the daemon client and agent bridge
// into a FleetSnapshot. It runs a background goroutine that polls all
// data sources at a configurable interval.
//
// FleetMonitor embeds BaseMonitor for lifecycle management (stop, pollLoop,
// OnRefresh, Snapshot) but keeps its own complex Refresh implementation
// with KPI tracking, conflict detection, and notification logic.
type FleetMonitor struct {
	BaseMonitor[FleetSnapshot]
	client *bridge.DaemonClient
	agent  *bridge.AgentBridge
	spawns SpawnLister // optional -- nil when spawn orchestrator not configured

	lastRefresh time.Time // debounce: skip Refresh() if <2s since last

	// Handoff notification dedup: tracks handoff IDs already notified.
	notifiedHandoffs map[string]bool

	// KPI counters -- daily aggregate metrics.
	kpis KPICounters

	// Previous snapshot for diff-based notifications.
	prevFileClaims []bridge.FileClaimInfo
	prevApprovals  int
}

// NewFleetMonitor creates a FleetMonitor backed by the given client and agent bridge.
func NewFleetMonitor(client *bridge.DaemonClient, agent *bridge.AgentBridge, logger *slog.Logger) *FleetMonitor {
	m := &FleetMonitor{
		client:           client,
		agent:            agent,
		notifiedHandoffs: make(map[string]bool),
	}
	m.InitBase(logger, nil, "fleet-monitor")
	return m
}

// SetSpawnLister injects a spawn source for fleet aggregation.
// Call after both FleetMonitor and SpawnOrchestrator are initialized.
func (m *FleetMonitor) SetSpawnLister(sl SpawnLister) {
	m.spawns = sl
}

// Start begins the background polling goroutine at the given interval.
func (m *FleetMonitor) Start(interval time.Duration) {
	m.StartManual()
	// Run initial refresh asynchronously so HUD/TUI startup is non-blocking
	// when downstream services are slow or unavailable.
	go func() {
		if err := m.Refresh(); err != nil {
			m.Logger.Warn("initial fleet refresh failed", "error", err)
		}
	}()

	go m.pollLoop(interval)
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
func (m *FleetMonitor) OfflineAgentsWithActiveSessions() []bridge.PresenceInfo {
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

	var result []bridge.PresenceInfo
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
	// Debounce: skip if less than 2s since last refresh to prevent stampede
	// when multiple handlers fire go Refresh() concurrently.
	m.RLock()
	if time.Since(m.lastRefresh) < 2*time.Second {
		m.RUnlock()
		m.Logger.Debug("fleet refresh debounced")
		return nil
	}
	m.RUnlock()

	snap := FleetSnapshot{
		UpdatedAt: time.Now(),
	}

	// Fetch daemon status.
	if status, err := m.client.Status(); err != nil {
		m.Logger.Warn("fleet: failed to fetch daemon status", "error", err)
	} else {
		snap.DaemonRunning = status.Running
		snap.ServerCount = status.Servers
		snap.ActiveConns = status.ActiveConns
		snap.Processes = status.Processes
	}

	// Fetch agent sessions.
	if sessions, err := m.agent.Sessions(); err != nil {
		m.Logger.Warn("fleet: failed to fetch sessions", "error", err)
	} else {
		snap.Sessions = sessions
		snap.TotalSessions = len(sessions)
		for _, s := range sessions {
			if s.Status == "active" {
				snap.ActiveSessions++
			}
			snap.TotalTokens += s.TotalTokens
		}
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

	// Fetch agent presence.
	if agents, err := m.agent.PresenceList(true); err != nil {
		m.Logger.Warn("fleet: failed to fetch presence", "error", err)
	} else {
		snap.Agents = agents
		for _, a := range agents {
			switch a.Status {
			case "active":
				snap.ActiveAgents++
			case "idle":
				snap.IdleAgents++
			case "offline":
				snap.OfflineAgents++
			}
		}
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

// pollLoop runs Refresh on a ticker until stopCh is closed.
// On consecutive errors, it backs off by skipping ticker ticks.
func (m *FleetMonitor) pollLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	consecutiveErrors := 0
	for {
		select {
		case <-m.StopCh():
			m.Logger.Debug("fleet monitor stopped")
			return
		case <-ticker.C:
			if err := m.Refresh(); err != nil {
				consecutiveErrors++
				if consecutiveErrors <= 3 {
					m.Logger.Warn("fleet refresh error", "error", err)
				}
				// Back off: skip next N-1 ticks (up to 4 skips = 5x interval).
				skipTicks := min(consecutiveErrors-1, 4)
				for range skipTicks {
					select {
					case <-m.StopCh():
						return
					case <-ticker.C:
					}
				}
			} else {
				if consecutiveErrors > 0 {
					m.Logger.Info("fleet refresh recovered", "after_errors", consecutiveErrors)
				}
				consecutiveErrors = 0
			}
		}
	}
}
