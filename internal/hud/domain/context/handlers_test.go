package context

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/monitor"
)

// mockMonitor implements ContextHealthMonitorOps for testing.
type mockMonitor struct {
	snapshot     monitor.ContextHealthSnapshot
	agentHealth  map[string]*monitor.AgentContextHealth
	compactError error
	refreshError error
}

func newMockMonitor() *mockMonitor {
	return &mockMonitor{
		snapshot: monitor.ContextHealthSnapshot{
			Agents: []monitor.AgentContextHealth{
				{
					AgentID:           "claude-code",
					SessionID:         "sess-1",
					Namespace:         "project/main",
					TokenBudget:       100000,
					TokensUsed:        50000,
					BudgetUtilization: 0.5,
					HealthScore:       85,
					CompactionNeeded:  false,
					StaleEntries:      3,
					LastEntryAge:      "5m",
					RecallHitRate:     0.8,
				},
			},
			SystemHealth:    85,
			TotalBudget:     100000,
			TotalUsed:       50000,
			CompactionQueue: 0,
			UpdatedAt:       time.Now(),
		},
		agentHealth: map[string]*monitor.AgentContextHealth{
			"claude-code": {
				AgentID:           "claude-code",
				SessionID:         "sess-1",
				Namespace:         "project/main",
				TokenBudget:       100000,
				TokensUsed:        50000,
				BudgetUtilization: 0.5,
				HealthScore:       85,
			},
		},
	}
}

func (m *mockMonitor) Snapshot() monitor.ContextHealthSnapshot { return m.snapshot }
func (m *mockMonitor) AgentHealth(agentID string) *monitor.AgentContextHealth {
	return m.agentHealth[agentID]
}
func (m *mockMonitor) SetBudgetOverride(_ string, _ int) {}
func (m *mockMonitor) TriggerCompaction(_ context.Context, _ string) error {
	return m.compactError
}
func (m *mockMonitor) Refresh() error { return m.refreshError }

// mockDeps provides stub implementations for the context Deps interface.
type mockDeps struct {
	monitor ContextHealthMonitorOps
}

func (d *mockDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (d *mockDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (d *mockDeps) Logger() *slog.Logger { return slog.Default() }

func (d *mockDeps) ContextHealthMonitor() ContextHealthMonitorOps { return d.monitor }

func TestContextDomainName(t *testing.T) {
	d := New(&mockDeps{monitor: newMockMonitor()})
	if d.Name() != "context" {
		t.Fatalf("expected name 'context', got %q", d.Name())
	}
}

func TestContextDomainRouteRegistration(t *testing.T) {
	d := New(&mockDeps{monitor: newMockMonitor()})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/context/health"},
		{"GET", "/api/context/health/claude-code"},
		{"POST", "/api/context/compact/sess-1"},
		{"GET", "/api/context/budget"},
		{"PUT", "/api/context/budget/claude-code"},
	}

	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		if rt.method == "PUT" {
			req = httptest.NewRequest(rt.method, rt.path, strings.NewReader(`{"token_budget":200000}`))
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: route not registered (got %d)", rt.method, rt.path, rec.Code)
		}
	}
}

func TestHandleContextHealth(t *testing.T) {
	mon := newMockMonitor()
	d := New(&mockDeps{monitor: mon})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/context/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var snap monitor.ContextHealthSnapshot
	if err := json.NewDecoder(rec.Body).Decode(&snap); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if snap.SystemHealth != 85 {
		t.Errorf("expected system_health 85, got %d", snap.SystemHealth)
	}
	if len(snap.Agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(snap.Agents))
	}
}

func TestHandleContextHealthAgent(t *testing.T) {
	mon := newMockMonitor()
	d := New(&mockDeps{monitor: mon})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/context/health/claude-code", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var health monitor.AgentContextHealth
	if err := json.NewDecoder(rec.Body).Decode(&health); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if health.AgentID != "claude-code" {
		t.Errorf("expected agent_id 'claude-code', got %q", health.AgentID)
	}
}

func TestHandleContextHealthAgent_NotFound(t *testing.T) {
	mon := newMockMonitor()
	d := New(&mockDeps{monitor: mon})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/context/health/unknown-agent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestHandleContextCompact(t *testing.T) {
	mon := newMockMonitor()
	d := New(&mockDeps{monitor: mon})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("POST", "/api/context/compact/sess-1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleContextBudget(t *testing.T) {
	mon := newMockMonitor()
	d := New(&mockDeps{monitor: mon})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/context/budget", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result["total_budget"].(float64) != 100000 {
		t.Errorf("expected total_budget 100000, got %v", result["total_budget"])
	}
}

func TestHandleContextBudgetSet(t *testing.T) {
	mon := newMockMonitor()
	d := New(&mockDeps{monitor: mon})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	body := `{"token_budget": 200000}`
	req := httptest.NewRequest("PUT", "/api/context/budget/claude-code", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleContextBudgetSet_InvalidBody(t *testing.T) {
	mon := newMockMonitor()
	d := New(&mockDeps{monitor: mon})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	body := `{"token_budget": -100}`
	req := httptest.NewRequest("PUT", "/api/context/budget/claude-code", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleContextHealth_NilMonitor(t *testing.T) {
	d := New(&mockDeps{monitor: nil})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/context/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}
