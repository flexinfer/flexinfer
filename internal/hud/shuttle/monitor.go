package shuttle

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/monitor"
)

// ShuttleMonitor polls the bridge and engine to maintain a cached
// ShuttleSnapshot for the HUD API.
type ShuttleMonitor struct {
	monitor.BaseMonitor[ShuttleSnapshot]
	engine *Engine
	bridge Bridge
}

// NewShuttleMonitor creates a monitor backed by the given engine and bridge.
func NewShuttleMonitor(engine *Engine, br Bridge, logger *slog.Logger) *ShuttleMonitor {
	m := &ShuttleMonitor{
		engine: engine,
		bridge: br,
	}
	m.InitBase(logger, nil, "shuttle-monitor")
	return m
}

// Start begins the background polling goroutine at the given interval.
func (m *ShuttleMonitor) Start(interval time.Duration) {
	m.BaseMonitor.Start(interval, m.refresh)
}

// refresh builds a fresh ShuttleSnapshot from bridge data.
func (m *ShuttleMonitor) refresh(_ context.Context) (ShuttleSnapshot, error) {
	sessions, err := m.bridge.Sessions()
	if err != nil {
		m.Logger.Warn("shuttle: failed to fetch sessions", "error", err)
		return ShuttleSnapshot{}, err
	}

	tasks, err := m.bridge.AllTasks()
	if err != nil {
		m.Logger.Warn("shuttle: failed to fetch tasks", "error", err)
		return ShuttleSnapshot{}, err
	}

	presence, err := m.bridge.PresenceList(true)
	if err != nil {
		m.Logger.Warn("shuttle: failed to fetch presence", "error", err)
		return ShuttleSnapshot{}, err
	}

	capacities := m.engine.BuildCapacities(sessions, tasks, presence)
	recommendations := m.engine.BuildRecommendations(tasks, capacities)

	// Count pending tasks.
	pendingTasks := 0
	for _, task := range tasks {
		if strings.EqualFold(strings.TrimSpace(task.Status), "pending") {
			pendingTasks++
		}
	}

	// Count active agents.
	activeAgents := 0
	for _, cap := range capacities {
		if strings.EqualFold(cap.Status, "active") || strings.EqualFold(cap.Status, "idle") {
			activeAgents++
		}
	}

	// Compute system load as average utilization.
	var systemLoad float64
	if len(capacities) > 0 {
		var total float64
		for _, cap := range capacities {
			total += cap.Utilization
		}
		systemLoad = total / float64(len(capacities))
	}

	snapshot := ShuttleSnapshot{
		Capacities:      capacities,
		Recommendations: recommendations,
		PendingTasks:    pendingTasks,
		ActiveAgents:    activeAgents,
		SystemLoad:      systemLoad,
		UpdatedAt:       time.Now(),
	}

	// Ensure slices are never nil for JSON serialization.
	if snapshot.Capacities == nil {
		snapshot.Capacities = []CapacityInfo{}
	}
	if snapshot.Recommendations == nil {
		snapshot.Recommendations = []DispatchRecommendation{}
	}

	return snapshot, nil
}

// Refresh forces an immediate refresh. Exposed for external callers.
func (m *ShuttleMonitor) Refresh() error {
	snap, err := m.refresh(context.Background())
	if err != nil {
		return err
	}
	m.Update(snap)
	return nil
}
