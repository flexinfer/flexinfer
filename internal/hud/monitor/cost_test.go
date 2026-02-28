package monitor

import (
	"testing"
)

func TestCostSnapshotDefault(t *testing.T) {
	// A zero-value CostSnapshot should be safe to use.
	var snap CostSnapshot
	if snap.Enabled {
		t.Error("expected default snapshot to have Enabled=false")
	}
	if snap.TotalCalls != 0 {
		t.Errorf("expected TotalCalls=0, got %d", snap.TotalCalls)
	}
	if snap.ByAgent != nil {
		t.Error("expected nil ByAgent")
	}
	if snap.ByServer != nil {
		t.Error("expected nil ByServer")
	}
}

func TestCostMonitorSnapshotWithoutStart(t *testing.T) {
	// CostMonitor should return zero snapshot when never started.
	m := NewCostMonitor(nil, nil)
	snap := m.Snapshot()
	if snap.Enabled {
		t.Error("expected snapshot.Enabled=false before any refresh")
	}
	if snap.TotalCalls != 0 {
		t.Errorf("expected TotalCalls=0, got %d", snap.TotalCalls)
	}
}

func TestCostMonitorStopIdempotent(t *testing.T) {
	m := NewCostMonitor(nil, nil)
	// Stop should be safe to call multiple times.
	m.Stop()
	m.Stop()
}
