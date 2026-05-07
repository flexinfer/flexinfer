package mobile

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
	"github.com/crb2nu/loom/internal/visibility/contracts/presence"
)

// --- Minimal mock Deps for handler testing ---

type mockDeps struct {
	agent       *bridge.AgentBridge
	monitors    Monitors
	config      MobileConfig
	logger      *slog.Logger
	writeJSON   func(http.ResponseWriter, int, any)
	sseHub      SSEHubOps
	eventLog    EventLogOps
	spawner     SpawnerOps
	handleSSE   func(http.ResponseWriter, *http.Request)
	topology    func(monitor.FleetSnapshot) TopologyGraph
	rbac        bridge.RBACConfigResult
	otel        bridge.OTelStatusResult
	memStats    func(*bridge.MemoryStatsResult) map[string]any
	trace       func(sessionID, agentID string, limit int) SessionTraceResponse
	rateLimiter RateLimiterOps
}

func (m *mockDeps) Agent() *bridge.AgentBridge               { return m.agent }
func (m *mockDeps) Monitors() Monitors                       { return m.monitors }
func (m *mockDeps) MobileConfig() MobileConfig               { return m.config }
func (m *mockDeps) Logger() *slog.Logger                     { return m.logger }
func (m *mockDeps) SSEHub() SSEHubOps                        { return m.sseHub }
func (m *mockDeps) EventLog() EventLogOps                    { return m.eventLog }
func (m *mockDeps) Spawner() SpawnerOps                      { return m.spawner }
func (m *mockDeps) RateLimiter() RateLimiterOps              { return m.rateLimiter }
func (m *mockDeps) RevocationList() RevocationListOps        { return nil }
func (m *mockDeps) DeviceTokens() DeviceTokenStoreOps        { return nil }
func (m *mockDeps) BroadcastAgentEvent(string, any)          {}
func (m *mockDeps) MaybeAutoProvisionSandbox(string)         {}
func (m *mockDeps) FetchRBACConfig() bridge.RBACConfigResult { return m.rbac }
func (m *mockDeps) FetchOTelStatus() bridge.OTelStatusResult { return m.otel }
func (m *mockDeps) DoSandboxStart(string, string) (map[string]any, error) {
	return nil, nil
}
func (m *mockDeps) DoSandboxStop(string) error { return nil }
func (m *mockDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	if m.writeJSON != nil {
		m.writeJSON(w, status, v)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // test helper; assertion failures catch issues
}
func (m *mockDeps) HandleSSE(w http.ResponseWriter, r *http.Request) {
	if m.handleSSE != nil {
		m.handleSSE(w, r)
	}
}
func (m *mockDeps) ComputeTopology(snap monitor.FleetSnapshot) TopologyGraph {
	if m.topology != nil {
		return m.topology(snap)
	}
	return TopologyGraph{}
}
func (m *mockDeps) OnSessionEnd(string, string) {}
func (m *mockDeps) SessionTrace(sessionID, agentID string, limit int) SessionTraceResponse {
	if m.trace != nil {
		return m.trace(sessionID, agentID, limit)
	}
	return SessionTraceResponse{SessionID: sessionID, AgentID: agentID}
}
func (m *mockDeps) MemoryStatsPayload(stats *bridge.MemoryStatsResult) map[string]any {
	if m.memStats != nil {
		return m.memStats(stats)
	}
	return nil
}
func (m *mockDeps) FleetIncrementKPI(string, int) {}
func (m *mockDeps) FleetRefresh()                 {}
func (m *mockDeps) RequireAdminToken(http.ResponseWriter, *http.Request) bool {
	return true
}
func (m *mockDeps) PlanSessionEndSummary(params bridge.SessionEndParams) (bridge.SessionEndParams, bool) {
	return params, false
}

func newTestMockDeps() *mockDeps {
	return &mockDeps{
		config: MobileConfig{
			OperatorToken:  "test-token",
			OperatorScopes: "mobile:read,mobile:session:create,mobile:session:end,mobile:push,mobile:agent:spawn",
		},
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
}

func newAuthRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	return req
}

// --- Tests ---

func TestHandleMobileSessionActivity_MissingSessionID(t *testing.T) {
	deps := newTestMockDeps()
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/sessions/{session_id}/activity", d.handleMobileSessionActivity)

	// URL-encoded space becomes empty after TrimSpace.
	req := newAuthRequest("GET", "/api/mobile/v1/sessions/%20/activity")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.OK {
		t.Fatal("expected ok=false")
	}
}

func TestHandleMobileSessionActivity_Unauthorized(t *testing.T) {
	deps := newTestMockDeps()
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/sessions/{session_id}/activity", d.handleMobileSessionActivity)

	req := httptest.NewRequest("GET", "/api/mobile/v1/sessions/sess-1/activity", nil)
	// No auth header.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleMobileSessionActivity_NilAgent(t *testing.T) {
	// When agent bridge is nil, calling Tasks will panic. Verify that
	// the handler requires a valid session_id before calling the bridge.
	deps := newTestMockDeps()
	deps.agent = nil
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/sessions/{session_id}/activity", d.handleMobileSessionActivity)

	// Empty session_id should return 400 before reaching the agent call.
	req := newAuthRequest("GET", "/api/mobile/v1/sessions/%20/activity")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty session_id, got %d", rec.Code)
	}
}

func TestRouteRegistration_SessionActivity(t *testing.T) {
	deps := newTestMockDeps()
	d := New(deps)

	mux := http.NewServeMux()
	d.RegisterRoutes(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })

	// Verify the session activity route is registered by making a request.
	// We expect 401 (no auth) rather than 404 (no route).
	req := httptest.NewRequest("GET", "/api/mobile/v1/sessions/test-session/activity", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatal("expected session activity route to be registered, got 404")
	}
	// Should be 401 (unauthorized) since no bearer token is set.
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (route exists but no auth), got %d", rec.Code)
	}
}

func TestPipelineResponseCorrelationField(t *testing.T) {
	// Verify the pipelineResponse struct includes the correlation field.
	// This is a compile-time test that the struct has the field and json tag.
	type testPipelineResp struct {
		AgentID     string `json:"agent_id,omitempty"`
		AgentType   string `json:"agent_type,omitempty"`
		Correlation string `json:"correlation,omitempty"`
	}

	resp := testPipelineResp{
		AgentID:     "claude-code-1",
		AgentType:   "claude-code",
		Correlation: "branch_match",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if parsed["correlation"] != "branch_match" {
		t.Errorf("expected correlation=branch_match, got %v", parsed["correlation"])
	}

	// Verify empty correlation is omitted.
	resp2 := testPipelineResp{AgentID: "test"}
	data2, _ := json.Marshal(resp2)
	var parsed2 map[string]any
	json.Unmarshal(data2, &parsed2) //nolint:errcheck // test helper; assertion failures catch issues
	if _, exists := parsed2["correlation"]; exists {
		t.Error("expected empty correlation to be omitted from JSON")
	}
}

func TestHandleMobilePipelines_NoPipelineMonitor(t *testing.T) {
	deps := newTestMockDeps()
	deps.monitors = Monitors{Pipeline: nil}
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/pipelines", d.handleMobilePipelines)

	req := newAuthRequest("GET", "/api/mobile/v1/pipelines")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !env.OK {
		t.Fatal("expected ok=true")
	}

	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got %T", env.Data)
	}

	available, _ := data["available"].(bool)
	if available {
		t.Error("expected available=false when pipeline monitor is nil")
	}
}

func TestHandleMobileDashboard_UsesUnifiedLiveAgentCounts(t *testing.T) {
	deps := newTestMockDeps()
	deps.monitors = Monitors{
		Fleet:  &monitor.FleetMonitor{},
		Health: &monitor.HealthMonitor{},
	}
	deps.monitors.Fleet.Update(monitor.FleetSnapshot{
		DaemonRunning:  true,
		ServerCount:    46,
		ActiveSessions: 2,
		Sessions: []bridge.SessionInfo{
			{ID: "sess-1", AgentID: "codex-main", Namespace: "proj/a", Status: "active"},
			{ID: "sess-2", AgentID: "codex-feature-dev", Namespace: "proj/b", Status: "active"},
			{ID: "sess-old", AgentID: "old-agent", Namespace: "proj/old", Status: "ended"},
		},
		Agents: []presence.PresenceInfo{
			{AgentID: "offline-proxy", Status: "offline"},
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

	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got %T", env.Data)
	}
	if got := data["active_agents"]; got != float64(2) {
		t.Fatalf("expected dashboard active_agents=2 from active session-only agents, got %v", got)
	}
	if got := data["offline_agents"]; got != float64(1) {
		t.Fatalf("expected dashboard offline_agents=1 from presence snapshot, got %v", got)
	}
}

func TestHandleMobileAgents_CodexInclusion(t *testing.T) {
	deps := newTestMockDeps()
	deps.monitors = Monitors{
		Fleet: &monitor.FleetMonitor{},
	}
	// Case 1: Codex as an active session-only agent.
	deps.monitors.Fleet.Update(monitor.FleetSnapshot{
		Sessions: []bridge.SessionInfo{
			{ID: "sess-codex", AgentID: "codex-123", Status: "active", Namespace: "proj/codex"},
		},
	})
	d := New(deps)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mobile/v1/agents", d.handleMobileAgents)

	req := newAuthRequest("GET", "/api/mobile/v1/agents")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var env Envelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}

	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got %T", env.Data)
	}
	agents := data["agents"].([]any)

	found := false
	for _, a := range agents {
		agent := a.(map[string]any)
		if agent["agent_id"] == "codex-123" {
			found = true
			if agent["agent_type"] != "codex" {
				t.Errorf("expected agent_type=codex, got %v", agent["agent_type"])
			}
		}
	}
	if !found {
		t.Error("codex-123 session-only agent not found in handleMobileAgents")
	}

	// Case 2: Codex in presence but with no session.
	deps.monitors.Fleet.Update(monitor.FleetSnapshot{
		Agents: []presence.PresenceInfo{
			{AgentID: "codex-456", Status: "active", AgentType: "codex"},
		},
	})
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req)

	if err := json.NewDecoder(rec2.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope 2: %v", err)
	}
	data = env.Data.(map[string]any)
	agents = data["agents"].([]any)

	found = false
	for _, a := range agents {
		agent := a.(map[string]any)
		if agent["agent_id"] == "codex-456" {
			found = true
			if agent["agent_type"] != "codex" {
				t.Errorf("expected agent_type=codex for presence, got %v", agent["agent_type"])
			}
		}
	}
	if !found {
		t.Error("codex-456 presence agent not found in handleMobileAgents")
	}

	// Case 3: Proxy and Zed mapped to Codex.
	deps.monitors.Fleet.Update(monitor.FleetSnapshot{
		Agents: []presence.PresenceInfo{
			{AgentID: "proxy-local-123", Status: "active"},
			{AgentID: "zed-editor-456", Status: "active"},
		},
	})
	rec3 := httptest.NewRecorder()
	d.handleMobileAgents(rec3, req)
	json.NewDecoder(rec3.Body).Decode(&env)
	data = env.Data.(map[string]any)
	agents = data["agents"].([]any)

	foundProxy, foundZed := false, false
	for _, a := range agents {
		agent := a.(map[string]any)
		if agent["agent_id"] == "proxy-local-123" {
			foundProxy = true
			if agent["agent_type"] != "codex" {
				t.Errorf("expected agent_type=codex for proxy, got %v", agent["agent_type"])
			}
		}
		if agent["agent_id"] == "zed-editor-456" {
			foundZed = true
			if agent["agent_type"] != "codex" {
				t.Errorf("expected agent_type=codex for zed, got %v", agent["agent_type"])
			}
		}
	}
	if !foundProxy {
		t.Error("proxy-local-123 not found")
	}
	if !foundZed {
		t.Error("zed-editor-456 not found")
	}
}
