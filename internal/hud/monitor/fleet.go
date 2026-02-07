// Package monitor provides aggregation monitors that poll the bridge layer
// and maintain cached snapshots of fleet, health, workflow, and memory state
// for the HUD HTTP API.
package monitor

import (
	"log/slog"
	"sync"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
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
	TotalTasks   int `json:"total_tasks"`
	PendingTasks int `json:"pending_tasks"`
	ActiveTasks  int `json:"active_tasks"`
	BlockedTasks int `json:"blocked_tasks"`

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

	// Metadata
	UpdatedAt time.Time `json:"updated_at"`
}

// FleetMonitor aggregates data from the daemon client and agent bridge
// into a FleetSnapshot. It runs a background goroutine that polls all
// data sources at a configurable interval.
type FleetMonitor struct {
	client *bridge.DaemonClient
	agent  *bridge.AgentBridge
	logger *slog.Logger

	mu       sync.RWMutex
	snapshot FleetSnapshot

	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewFleetMonitor creates a FleetMonitor backed by the given client and agent bridge.
func NewFleetMonitor(client *bridge.DaemonClient, agent *bridge.AgentBridge, logger *slog.Logger) *FleetMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &FleetMonitor{
		client: client,
		agent:  agent,
		logger: logger.With("component", "fleet-monitor"),
		stopCh: make(chan struct{}),
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

// Refresh forces an immediate refresh of all data sources and updates
// the snapshot. Each sub-fetch is independent; errors are logged but
// do not prevent other fetches from completing.
func (m *FleetMonitor) Refresh() error {
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

	// Commit the snapshot atomically.
	m.mu.Lock()
	m.snapshot = snap
	m.mu.Unlock()

	return nil
}

// pollLoop runs Refresh on a ticker until stopCh is closed.
func (m *FleetMonitor) pollLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			m.logger.Debug("fleet monitor stopped")
			return
		case <-ticker.C:
			if err := m.Refresh(); err != nil {
				m.logger.Warn("fleet refresh error", "error", err)
			}
		}
	}
}
