package hud

import (
	"testing"
)

func TestNewHUDMetrics_SpawnTelemetryCounters(t *testing.T) {
	m := NewHUDMetrics()
	if m == nil {
		t.Fatal("NewHUDMetrics returned nil")
	}

	// Verify all spawn telemetry counters are initialized (non-nil).
	if m.SpawnTokensTotal == nil {
		t.Error("SpawnTokensTotal is nil")
	}
	if m.SpawnCostTotal == nil {
		t.Error("SpawnCostTotal is nil")
	}
	if m.SpawnTurnsTotal == nil {
		t.Error("SpawnTurnsTotal is nil")
	}
	if m.SpawnToolCallsTotal == nil {
		t.Error("SpawnToolCallsTotal is nil")
	}
	if m.SpawnFileChangesTotal == nil {
		t.Error("SpawnFileChangesTotal is nil")
	}
	if m.SpawnErrorsTotal == nil {
		t.Error("SpawnErrorsTotal is nil")
	}

	// Verify existing counters are still initialized.
	if m.AgentSpawnTotal == nil {
		t.Error("AgentSpawnTotal is nil")
	}
	if m.PushNotificationTotal == nil {
		t.Error("PushNotificationTotal is nil")
	}
	if m.SpawnedAgentActive == nil {
		t.Error("SpawnedAgentActive is nil")
	}
	if m.PushDeliveryLatency == nil {
		t.Error("PushDeliveryLatency is nil")
	}
}
