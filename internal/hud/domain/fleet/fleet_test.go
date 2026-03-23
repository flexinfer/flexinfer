package fleet

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// mockDeps provides stub implementations for the fleet Deps interface.
type mockDeps struct{}

func (m *mockDeps) WriteJSON(w http.ResponseWriter, _ int, _ any) { w.WriteHeader(http.StatusOK) }
func (m *mockDeps) WriteError(w http.ResponseWriter, status int, _ string, _ error) {
	w.WriteHeader(status)
}
func (m *mockDeps) RequireAdminToken(_ http.ResponseWriter, _ *http.Request) bool { return true }
func (m *mockDeps) Logger() *slog.Logger                                          { return slog.Default() }
func (m *mockDeps) Agent() *bridge.AgentBridge                                    { return nil }
func (m *mockDeps) FleetIncrementKPI(_ string, _ int)                             {}
func (m *mockDeps) FleetRefresh()                                                 {}
func (m *mockDeps) BroadcastAgentEvent(_ string, _ any)                           {}
func (m *mockDeps) OnSessionEnd(_, _ string)                                      {}
func (m *mockDeps) MaybeAutoProvisionSandbox(_ string)                            {}
func (m *mockDeps) MaybeSampleContextTelemetry(_, _, _, _ string)                 {}
func (m *mockDeps) NudgeQueue() NudgeQueueOps                                     { return &mockNudgeQueue{} }
func (m *mockDeps) CacheGet(_ string) (any, bool)                                 { return nil, false }
func (m *mockDeps) CacheSet(_ string, _ any, _ time.Duration)                     {}
func (m *mockDeps) PlanSessionEndSummary(params bridge.SessionEndParams) (bridge.SessionEndParams, bool) {
	return params, false
}

type mockNudgeQueue struct{}

func (n *mockNudgeQueue) QueueNudge(_, _, _, _, _ string) string { return "nudge-test-1" }
func (n *mockNudgeQueue) Count(_ string) int                     { return 0 }
func (n *mockNudgeQueue) Drain(_ string) []any                   { return nil }
func (n *mockNudgeQueue) StatusView(_ string) bridge.NudgeQueueStatus {
	return bridge.NudgeQueueStatus{}
}
func (n *mockNudgeQueue) PolicyView() bridge.NudgeQueuePolicy { return bridge.NudgeQueuePolicy{} }
func (n *mockNudgeQueue) ApplyPolicy(_ bridge.NudgeQueuePolicyMutation) (bridge.NudgeQueuePolicy, bridge.NudgeQueuePolicy, error) {
	return bridge.NudgeQueuePolicy{}, bridge.NudgeQueuePolicy{}, nil
}

func TestFleetDomainName(t *testing.T) {
	d := New(&mockDeps{})
	if d.Name() != "fleet" {
		t.Fatalf("expected name 'fleet', got %q", d.Name())
	}
}

func TestFleetDomainRouteRegistration(t *testing.T) {
	d := New(&mockDeps{})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	// Wrap with recovery to catch nil-pointer panics from handlers calling
	// Agent() on the mock (returns nil). We only care that the route is
	// registered, not that the handler succeeds.
	safeHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { recover() }()
		mux.ServeHTTP(w, r)
	})

	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/agent/session-start"},
		{"POST", "/api/agent/session-end"},
		{"POST", "/api/agent/heartbeat"},
		{"GET", "/api/agent/session"},
		{"POST", "/api/agent/session-list"},
		{"POST", "/api/agent/session-prune"},
		{"GET", "/api/agent/session-detail"},
		{"POST", "/api/agent/context/add"},
		{"GET", "/api/agent/context-inspect"},
		{"GET", "/api/knowledge"},
		{"POST", "/api/agent/task-update"},
		{"POST", "/api/agent/workflow-define"},
		{"GET", "/api/agent/workflow-definitions"},
		{"POST", "/api/agent/nudge"},
		{"GET", "/api/agent/nudge-queue"},
		{"GET", "/api/agent/nudge-queue-policy"},
		{"POST", "/api/agent/nudge-queue-policy"},
		{"POST", "/api/agent/dispatch"},
	}

	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		safeHandler.ServeHTTP(rec, req)
		// The handler will likely return 400 (missing body/params) or panic
		// on nil Agent. 404/405 means the route was not registered.
		if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: route not registered (got %d)", rt.method, rt.path, rec.Code)
		}
	}
}
