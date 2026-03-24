package orchestration

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/monitor"
)

// OrchestrationMonitor polls the bridge and engine to maintain a cached
// OrchestrationSnapshot for the HUD API.
type OrchestrationMonitor struct {
	monitor.BaseMonitor[OrchestrationSnapshot]
	engine *Engine
	bridge Bridge
}

// NewOrchestrationMonitor creates a monitor backed by the given engine and bridge.
func NewOrchestrationMonitor(engine *Engine, br Bridge, logger *slog.Logger) *OrchestrationMonitor {
	m := &OrchestrationMonitor{
		engine: engine,
		bridge: br,
	}
	m.InitBase(logger, nil, "orchestration-monitor")
	return m
}

// Start begins the background polling goroutine at the given interval.
func (m *OrchestrationMonitor) Start(interval time.Duration) {
	m.BaseMonitor.Start(interval, m.refresh)
}

// refresh builds a fresh OrchestrationSnapshot from bridge data.
func (m *OrchestrationMonitor) refresh(_ context.Context) (OrchestrationSnapshot, error) {
	sessions, err := m.bridge.Sessions()
	if err != nil {
		m.Logger.Warn("orchestration: failed to fetch sessions", "error", err)
		return OrchestrationSnapshot{}, err
	}

	tasks, err := m.bridge.AllTasks()
	if err != nil {
		m.Logger.Warn("orchestration: failed to fetch tasks", "error", err)
		return OrchestrationSnapshot{}, err
	}

	presence, err := m.bridge.PresenceList(true)
	if err != nil {
		m.Logger.Warn("orchestration: failed to fetch presence", "error", err)
		return OrchestrationSnapshot{}, err
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

	snapshot := OrchestrationSnapshot{
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
func (m *OrchestrationMonitor) Refresh() error {
	snap, err := m.refresh(context.Background())
	if err != nil {
		return err
	}
	m.Update(snap)
	return nil
}
