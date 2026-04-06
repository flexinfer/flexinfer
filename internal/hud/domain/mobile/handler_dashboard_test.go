package mobile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crb2nu/loom/internal/hud/monitor"
)

func TestHandleMobileDashboard_SpawnsSummary(t *testing.T) {
	deps := newTestMockDeps()
	deps.monitors = Monitors{
		Fleet:  &monitor.FleetMonitor{},
		Health: &monitor.HealthMonitor{},
	}
	deps.monitors.Fleet.Update(monitor.FleetSnapshot{
		DaemonRunning:  true,
		ServerCount:    10,
		ActiveSessions: 1,
		Spawns: []monitor.SpawnInfo{
			{
				SpawnID:         "spawn-001",
				AgentID:         "spawn-claude-code-001",
				Status:          "running",
				AgentType:       "claude-code",
				Project:         "loom-core",
				TurnCount:       3,
				TotalCostUSD:    0.15,
				InputTokens:     500,
				OutputTokens:    200,
				ToolCallCount:   5,
				FileChangeCount: 2,
			},
			{
				SpawnID:   "spawn-002",
				AgentID:   "spawn-codex-002",
				Status:    "building",
				AgentType: "codex",
				Project:   "flexdeck",
			},
			{
				SpawnID:   "spawn-003",
				AgentID:   "spawn-claude-code-003",
				Status:    "completed",
				AgentType: "claude-code",
				Project:   "loom-core",
			},
		},
	})
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/dashboard", d.handleMobileDashboard)

	req := newAuthRequest("GET", "/api/mobile/v1/dashboard")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !env.OK {
		t.Fatal("expected ok=true")
	}

	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got %T", env.Data)
	}

	// Verify spawns section exists.
	spawnsRaw, ok := data["spawns"]
	if !ok {
		t.Fatal("expected spawns key in dashboard response")
	}
	spawns, ok := spawnsRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected spawns to be a map, got %T", spawnsRaw)
	}

	// 2 active (running + building), 3 total.
	if got := spawns["active"]; got != float64(2) {
		t.Errorf("spawns.active = %v, want 2", got)
	}
	if got := spawns["total"]; got != float64(3) {
		t.Errorf("spawns.total = %v, want 3", got)
	}

	// Verify items are included.
	items, ok := spawns["items"].([]any)
	if !ok {
		t.Fatalf("expected spawns.items to be a slice, got %T", spawns["items"])
	}
	if len(items) != 3 {
		t.Errorf("spawns.items length = %d, want 3", len(items))
	}

	// Verify telemetry fields are present in the first spawn item.
	firstItem, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first item to be a map, got %T", items[0])
	}
	if got := firstItem["turn_count"]; got != float64(3) {
		t.Errorf("first item turn_count = %v, want 3", got)
	}
	if got := firstItem["total_cost_usd"]; got != float64(0.15) {
		t.Errorf("first item total_cost_usd = %v, want 0.15", got)
	}
	if got := firstItem["tool_call_count"]; got != float64(5) {
		t.Errorf("first item tool_call_count = %v, want 5", got)
	}
}

func TestHandleMobileDashboard_SpawnsEmpty(t *testing.T) {
	deps := newTestMockDeps()
	deps.monitors = Monitors{
		Fleet:  &monitor.FleetMonitor{},
		Health: &monitor.HealthMonitor{},
	}
	deps.monitors.Fleet.Update(monitor.FleetSnapshot{
		DaemonRunning: true,
	})
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/dashboard", d.handleMobileDashboard)

	req := newAuthRequest("GET", "/api/mobile/v1/dashboard")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}

	data := env.Data.(map[string]any)
	spawns := data["spawns"].(map[string]any)

	if got := spawns["active"]; got != float64(0) {
		t.Errorf("spawns.active = %v, want 0", got)
	}
	if got := spawns["total"]; got != float64(0) {
		t.Errorf("spawns.total = %v, want 0", got)
	}
}
