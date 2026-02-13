// Package monitor provides aggregation monitors that poll the bridge layer
// and maintain cached snapshots of fleet, health, workflow, and memory state
// for the HUD HTTP API.
package monitor

import (
	"log/slog"
	"sync"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
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

	// Metadata
	UpdatedAt time.Time `json:"updated_at"`
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
type FleetMonitor struct {
	client *bridge.DaemonClient
	agent  *bridge.AgentBridge
	logger *slog.Logger

	mu          sync.RWMutex
	snapshot    FleetSnapshot
	lastRefresh time.Time // debounce: skip Refresh() if <2s since last

	// Handoff notification dedup: tracks handoff IDs already notified.
	notifiedHandoffs map[string]bool

	// KPI counters — daily aggregate metrics.
	kpis KPICounters

	// Previous snapshot for diff-based notifications.
	prevFileClaims []bridge.FileClaimInfo
	prevApprovals  int

	onRefresh func(FleetSnapshot)

	stopCh   chan struct{}
	stopOnce sync.Once
}

// OnRefresh registers a callback that fires after each successful refresh
// with the new snapshot. Used to broadcast data via SSE.
func (m *FleetMonitor) OnRefresh(fn func(FleetSnapshot)) {
	m.onRefresh = fn
}

// NewFleetMonitor creates a FleetMonitor backed by the given client and agent bridge.
func NewFleetMonitor(client *bridge.DaemonClient, agent *bridge.AgentBridge, logger *slog.Logger) *FleetMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &FleetMonitor{
		client:           client,
		agent:            agent,
		logger:           logger.With("component", "fleet-monitor"),
		notifiedHandoffs: make(map[string]bool),
		stopCh:           make(chan struct{}),
	}
}

// Start begins the background polling goroutine at the given interval.
func (m *FleetMonitor) Start(interval time.Duration) {
	// Do an initial refresh immediately.
	if err := m.Refresh(); err != nil {
		m.logger.Warn("initial fleet refresh failed", "error", err)
	}

	go m.pollLoop(interval)
}

// Stop signals the background goroutine to exit. It is safe to call multiple times.
func (m *FleetMonitor) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
}

// Snapshot returns the current aggregated fleet snapshot.
func (m *FleetMonitor) Snapshot() FleetSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

// KPIs returns the current daily KPI counters.
func (m *FleetMonitor) KPIs() KPICounters {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.kpis
}

// IncrementKPI atomically increments a specific KPI counter.
func (m *FleetMonitor) IncrementKPI(field string, delta int) {
	m.mu.Lock()
	defer m.mu.Unlock()

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
// have active sessions — candidates for session reaping.
func (m *FleetMonitor) OfflineAgentsWithActiveSessions() []bridge.PresenceInfo {
	m.mu.RLock()
	snap := m.snapshot
	m.mu.RUnlock()

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
	m.mu.RLock()
	if time.Since(m.lastRefresh) < 2*time.Second {
		m.mu.RUnlock()
		m.logger.Debug("fleet refresh debounced")
		return nil
	}
	m.mu.RUnlock()

	snap := FleetSnapshot{
		UpdatedAt: time.Now(),
	}

	// Fetch daemon status.
	if status, err := m.client.Status(); err != nil {
		m.logger.Warn("fleet: failed to fetch daemon status", "error", err)
	} else {
		snap.DaemonRunning = status.Running
		snap.ServerCount = status.Servers
		snap.ActiveConns = status.ActiveConns
		snap.Processes = status.Processes
	}

	// Fetch agent sessions.
	if sessions, err := m.agent.Sessions(); err != nil {
		m.logger.Warn("fleet: failed to fetch sessions", "error", err)
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
		m.logger.Warn("fleet: failed to fetch tasks", "error", err)
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
		m.logger.Warn("fleet: failed to fetch memory stats", "error", err)
	} else {
		snap.MemoryTotalItems = memStats.TotalItems
		snap.MemoryTotalTokens = memStats.TotalTokens
	}

	// Fetch graph stats.
	if graphStats, err := m.agent.GraphStats(); err != nil {
		m.logger.Warn("fleet: failed to fetch graph stats", "error", err)
	} else {
		snap.EntityCount = graphStats.EntityCount
		snap.RelationCount = graphStats.RelationCount
	}

	// Fetch workflow list.
	if workflows, err := m.agent.WorkflowList(); err != nil {
		m.logger.Warn("fleet: failed to fetch workflows", "error", err)
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
		m.logger.Warn("fleet: failed to fetch presence", "error", err)
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
		m.logger.Warn("fleet: failed to fetch file claims", "error", err)
	} else {
		snap.FileClaims = claims
	}

	// Fetch worktree assignments.
	if worktrees, err := m.agent.WorktreeList("", "active"); err != nil {
		m.logger.Warn("fleet: failed to fetch worktrees", "error", err)
	} else {
		snap.Worktrees = worktrees
		snap.ActiveWorktrees = len(worktrees)
	}

	// --- KPI daily counter reset ---
	m.mu.Lock()
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
	m.mu.Unlock()

	// --- Proactive notifications: conflict detection ---
	newConflicts := conflictCount
	m.mu.RLock()
	prevConflicts, _ := detectConflicts(m.prevFileClaims)
	m.mu.RUnlock()
	if newConflicts > prevConflicts {
		go func() {
			if err := notify.NotifyConflict(newConflicts); err != nil {
				m.logger.Debug("conflict notification failed", "error", err)
			}
		}()
	}

	// --- Proactive notifications: pending approvals ---
	if snap.PendingApprovals > 0 {
		m.mu.RLock()
		prevApprovals := m.prevApprovals
		m.mu.RUnlock()
		if snap.PendingApprovals > prevApprovals {
			go func() {
				if err := notify.NotifyApproval(snap.PendingApprovals); err != nil {
					m.logger.Debug("approval notification failed", "error", err)
				}
			}()
		}
	}

	// Check for new handoffs and send desktop notifications.
	if handoffs, err := m.agent.HandoffList(); err != nil {
		m.logger.Debug("fleet: failed to fetch handoffs for notification", "error", err)
	} else {
		m.mu.Lock()
		for _, h := range handoffs {
			if h.Status == "pending" && !m.notifiedHandoffs[h.ID] {
				m.notifiedHandoffs[h.ID] = true
				go func(from, to, summary string) {
					if err := notify.NotifyHandoff(from, to, summary); err != nil {
						m.logger.Debug("handoff notification failed", "error", err)
					}
				}(h.FromAgent, h.ToAgent, h.Summary)
			}
		}
		m.mu.Unlock()
	}

	// Commit the snapshot atomically and save previous state for diff-based notifications.
	m.mu.Lock()
	m.prevFileClaims = m.snapshot.FileClaims
	m.prevApprovals = m.snapshot.PendingApprovals
	m.snapshot = snap
	m.lastRefresh = time.Now()
	m.mu.Unlock()

	// Notify listeners (e.g., SSE hub) with the fresh snapshot.
	if m.onRefresh != nil {
		m.onRefresh(snap)
	}

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
		case <-m.stopCh:
			m.logger.Debug("fleet monitor stopped")
			return
		case <-ticker.C:
			if err := m.Refresh(); err != nil {
				consecutiveErrors++
				if consecutiveErrors <= 3 {
					m.logger.Warn("fleet refresh error", "error", err)
				}
				// Back off: skip next N-1 ticks (up to 4 skips = 5x interval).
				skipTicks := min(consecutiveErrors-1, 4)
				for range skipTicks {
					select {
					case <-m.stopCh:
						return
					case <-ticker.C:
					}
				}
			} else {
				if consecutiveErrors > 0 {
					m.logger.Info("fleet refresh recovered", "after_errors", consecutiveErrors)
				}
				consecutiveErrors = 0
			}
		}
	}
}
