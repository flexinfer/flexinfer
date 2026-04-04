package shuttle

import (
	"testing"
	"time"
)

func TestNewShuttleMonitor(t *testing.T) {
	engine := NewEngine(nil)
	mon := NewShuttleMonitor(engine, nil, nil)

	if mon == nil {
		t.Fatal("expected non-nil monitor")
	}

	// Snapshot should be a zero value before any refresh.
	snap := mon.Snapshot()
	if snap.ActiveAgents != 0 {
		t.Errorf("expected 0 active agents, got %d", snap.ActiveAgents)
	}
	if snap.PendingTasks != 0 {
		t.Errorf("expected 0 pending tasks, got %d", snap.PendingTasks)
	}
	if len(snap.Capacities) != 0 {
		t.Errorf("expected 0 capacities, got %d", len(snap.Capacities))
	}
}

func TestShuttleSnapshot_Serialization(t *testing.T) {
	snap := ShuttleSnapshot{
		Capacities: []CapacityInfo{
			{AgentID: "a1", ActiveTasks: 2, AvailableSlots: 3},
		},
		Recommendations: []DispatchRecommendation{
			{TaskID: "t1", RecommendedAgent: "a1", Score: 0.85},
		},
		PendingTasks: 5,
		ActiveAgents: 2,
		UpdatedAt:    time.Now(),
	}

	if len(snap.Capacities) != 1 {
		t.Errorf("expected 1 capacity entry, got %d", len(snap.Capacities))
	}
	if snap.Capacities[0].AgentID != "a1" {
		t.Errorf("expected agent a1, got %s", snap.Capacities[0].AgentID)
	}
	if snap.PendingTasks != 5 {
		t.Errorf("expected 5 pending tasks, got %d", snap.PendingTasks)
	}
}
