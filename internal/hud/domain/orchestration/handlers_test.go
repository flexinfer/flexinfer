package orchestration

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	orch "github.com/crb2nu/loom/internal/hud/orchestration"
)

// mockDeps implements the Deps interface for testing.
type mockDeps struct {
	engine  *orch.Engine
	monitor *orch.OrchestrationMonitor
	bridge  orch.Bridge
}

func (m *mockDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (m *mockDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (m *mockDeps) Logger() *slog.Logger { return slog.Default() }

func (m *mockDeps) OrchestrationEngine() *orch.Engine { return m.engine }

func (m *mockDeps) OrchestrationMonitor() *orch.OrchestrationMonitor { return m.monitor }

func (m *mockDeps) OrchestrationBridge() orch.Bridge { return m.bridge }

func newTestDomain() (*OrchestrationDomain, *mockDeps) {
	engine := orch.NewEngine(nil)
	mon := orch.NewOrchestrationMonitor(engine, nil, nil)

	// Pre-populate the monitor snapshot with test data.
	mon.Update(orch.OrchestrationSnapshot{
		Capacities: []orch.CapacityInfo{
			{AgentID: "agent-1", ActiveTasks: 2, AvailableSlots: 3},
			{AgentID: "agent-2", ActiveTasks: 0, AvailableSlots: 5},
		},
		Recommendations: []orch.DispatchRecommendation{
			{TaskID: "task-1", RecommendedAgent: "agent-2", Score: 0.85, Reason: "capacity=0.40"},
		},
		PendingTasks: 3,
		ActiveAgents: 2,
	})

	deps := &mockDeps{
		engine:  engine,
		monitor: mon,
	}
	return New(deps), deps
}

func TestHandleStatus(t *testing.T) {
	d, _ := newTestDomain()

	req := httptest.NewRequest("GET", "/api/orchestration/status", nil)
	w := httptest.NewRecorder()

	d.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var snap orch.OrchestrationSnapshot
	if err := json.NewDecoder(w.Body).Decode(&snap); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(snap.Capacities) != 2 {
		t.Errorf("expected 2 capacities, got %d", len(snap.Capacities))
	}
	if snap.PendingTasks != 3 {
		t.Errorf("expected 3 pending tasks, got %d", snap.PendingTasks)
	}
}

func TestHandleCapacity(t *testing.T) {
	d, _ := newTestDomain()

	req := httptest.NewRequest("GET", "/api/orchestration/capacity", nil)
	w := httptest.NewRecorder()

	d.handleCapacity(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, ok := resp["capacities"]; !ok {
		t.Error("expected 'capacities' key in response")
	}
}

func TestHandleRecommendations(t *testing.T) {
	d, _ := newTestDomain()

	req := httptest.NewRequest("GET", "/api/orchestration/recommendations", nil)
	w := httptest.NewRecorder()

	d.handleRecommendations(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Recommendations []orch.DispatchRecommendation `json:"recommendations"`
		PendingTasks    int                           `json:"pending_tasks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Recommendations) != 1 {
		t.Errorf("expected 1 recommendation, got %d", len(resp.Recommendations))
	}
}

func TestHandleGetPolicies(t *testing.T) {
	d, _ := newTestDomain()

	req := httptest.NewRequest("GET", "/api/orchestration/policies", nil)
	w := httptest.NewRecorder()

	d.handleGetPolicies(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var cfg orch.PolicyConfig
	if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !cfg.Dispatch.Enabled {
		t.Error("expected dispatch to be enabled by default")
	}
	if cfg.Dispatch.CapacityWeight != 0.40 {
		t.Errorf("expected capacity weight 0.40, got %f", cfg.Dispatch.CapacityWeight)
	}
}

func TestHandleUpdatePolicies(t *testing.T) {
	d, deps := newTestDomain()

	newCfg := orch.PolicyConfig{
		Dispatch: orch.DispatchPolicy{
			Enabled:         false,
			Mode:            "capacity",
			CapacityWeight:  0.60,
			ExpertiseWeight: 0.20,
			AffinityWeight:  0.10,
			FreshnessWeight: 0.10,
		},
		Load: orch.LoadPolicy{
			MaxConcurrentTasks: 10,
			TokenBudgetCeiling: 200000,
		},
		Conflict: orch.ConflictPolicy{
			FileClaimPreCheck: false,
		},
	}
	body, _ := json.Marshal(newCfg)
	req := httptest.NewRequest("PUT", "/api/orchestration/policies", bytes.NewReader(body))
	w := httptest.NewRecorder()

	d.handleUpdatePolicies(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify the engine was actually updated.
	stored := deps.engine.GetPolicy()
	if stored.Dispatch.Enabled {
		t.Error("expected dispatch to be disabled after update")
	}
	if stored.Load.MaxConcurrentTasks != 10 {
		t.Errorf("expected max concurrent tasks 10, got %d", stored.Load.MaxConcurrentTasks)
	}
}

func TestHandlePreflight_MissingAgentID(t *testing.T) {
	d, _ := newTestDomain()

	body, _ := json.Marshal(map[string]string{"file_path": "foo.go"})
	req := httptest.NewRequest("POST", "/api/orchestration/preflight", bytes.NewReader(body))
	w := httptest.NewRecorder()

	d.handlePreflight(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleDispatch_MissingTaskID(t *testing.T) {
	d, _ := newTestDomain()

	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest("POST", "/api/orchestration/dispatch", bytes.NewReader(body))
	w := httptest.NewRecorder()

	d.handleDispatch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleRecommendations_EmptyState(t *testing.T) {
	engine := orch.NewEngine(nil)
	mon := orch.NewOrchestrationMonitor(engine, nil, nil)
	deps := &mockDeps{
		engine:  engine,
		monitor: mon,
	}
	d := New(deps)

	req := httptest.NewRequest("GET", "/api/orchestration/recommendations", nil)
	w := httptest.NewRecorder()

	d.handleRecommendations(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Recommendations []orch.DispatchRecommendation `json:"recommendations"`
		PendingTasks    int                           `json:"pending_tasks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.PendingTasks != 0 {
		t.Errorf("expected 0 pending tasks, got %d", resp.PendingTasks)
	}
}
