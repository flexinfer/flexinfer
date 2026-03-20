package fleet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockAppHandlers provides stub implementations for all fleet handler methods.
type mockAppHandlers struct{}

func (m *mockAppHandlers) HandleAgentSessionStart(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentSessionEnd(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentHeartbeat(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentSession(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentSessionList(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentSessionPrune(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentSessionDetail(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentContextAdd(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentContextInspect(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleKnowledge(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentTaskUpdate(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentWorkflowDefine(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentWorkflowDefinitions(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentNudge(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentNudgeQueue(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentNudgeQueuePolicy(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentNudgeQueuePolicyUpdate(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleAgentDispatch(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (m *mockAppHandlers) HandleClaimRelease(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func TestFleetDomainName(t *testing.T) {
	d := New(&mockAppHandlers{})
	if d.Name() != "fleet" {
		t.Fatalf("expected name 'fleet', got %q", d.Name())
	}
}

func TestFleetDomainRouteRegistration(t *testing.T) {
	d := New(&mockAppHandlers{})
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

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
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s: expected 200, got %d", rt.method, rt.path, rec.Code)
		}
	}
}

func TestFleetDomainLifecycle(t *testing.T) {
	d := New(&mockAppHandlers{})
	if err := d.Start(context.TODO()); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop: unexpected error: %v", err)
	}
}
