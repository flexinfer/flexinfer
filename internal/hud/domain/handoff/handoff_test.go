package handoff

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// --- Mock Caller ---

// testCaller implements bridge.Caller for domain tests.
type testCaller struct {
	callToolFn func(name string, args map[string]any) (json.RawMessage, error)
}

func (c *testCaller) Call(string, any) (json.RawMessage, error) { return nil, nil }
func (c *testCaller) CallWithTimeout(string, any, time.Duration) (json.RawMessage, error) {
	return nil, nil
}
func (c *testCaller) CallTool(name string, args map[string]any) (json.RawMessage, error) {
	if c.callToolFn != nil {
		return c.callToolFn(name, args)
	}
	return nil, fmt.Errorf("unexpected CallTool for %s", name)
}
func (c *testCaller) CallToolWithTimeout(name string, args map[string]any, _ time.Duration) (json.RawMessage, error) {
	return c.CallTool(name, args)
}
func (c *testCaller) CircuitOpen() bool { return false }
func (c *testCaller) Close() error      { return nil }

// --- Mock Deps ---

type mockDeps struct {
	agent           *bridge.AgentBridge
	broadcastCalled int
}

func (d *mockDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func (d *mockDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}

func (d *mockDeps) Logger() *slog.Logger                { return slog.Default() }
func (d *mockDeps) Agent() *bridge.AgentBridge          { return d.agent }
func (d *mockDeps) BroadcastAgentEvent(_ string, _ any) { d.broadcastCalled++ }

// --- normalizeStringList table-driven tests ---

func TestNormalizeStringList(t *testing.T) {
	tests := []struct {
		name   string
		input  []string
		expect []string
	}{
		{
			name:   "nil input returns empty slice",
			input:  nil,
			expect: []string{},
		},
		{
			name:   "empty input returns empty slice",
			input:  []string{},
			expect: []string{},
		},
		{
			name:   "trims whitespace",
			input:  []string{"  hello  ", " world "},
			expect: []string{"hello", "world"},
		},
		{
			name:   "removes empty strings",
			input:  []string{"a", "", "  ", "b"},
			expect: []string{"a", "b"},
		},
		{
			name:   "deduplicates",
			input:  []string{"a", "b", "a", "c", "b"},
			expect: []string{"a", "b", "c"},
		},
		{
			name:   "deduplicates after trimming",
			input:  []string{" a ", "a", " a"},
			expect: []string{"a"},
		},
		{
			name:   "mixed case and whitespace",
			input:  []string{"  Alpha ", "Beta", "", "Alpha", " gamma ", "Beta"},
			expect: []string{"Alpha", "Beta", "gamma"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeStringList(tt.input)
			if len(result) != len(tt.expect) {
				t.Fatalf("expected %d items, got %d: %v", len(tt.expect), len(result), result)
			}
			for i, v := range result {
				if v != tt.expect[i] {
					t.Errorf("index %d: expected %q, got %q", i, tt.expect[i], v)
				}
			}
		})
	}
}

// --- Domain tests ---

func TestHandoffDomainName(t *testing.T) {
	d := New(&mockDeps{})
	if d.Name() != "handoff" {
		t.Fatalf("expected name 'handoff', got %q", d.Name())
	}
}

func TestHandoffDomainRouteRegistration(t *testing.T) {
	caller := &testCaller{}
	agent := bridge.NewAgentBridge(caller)
	deps := &mockDeps{agent: agent}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	// Wrap with recovery to catch nil-pointer panics from handler internals.
	safeHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { recover() }()
		mux.ServeHTTP(w, r)
	})

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/handoffs"},
		{"POST", "/api/handoffs"},
		{"POST", "/api/handoffs/test-id/accept"},
	}

	for _, rt := range routes {
		var req *http.Request
		if rt.method == "POST" {
			req = httptest.NewRequest(rt.method, rt.path, strings.NewReader(`{}`))
		} else {
			req = httptest.NewRequest(rt.method, rt.path, nil)
		}
		rec := httptest.NewRecorder()
		safeHandler.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s: route not registered (got %d)", rt.method, rt.path, rec.Code)
		}
	}
}

// --- handleHandoffList tests ---

func TestHandleHandoffList_Success(t *testing.T) {
	// The HandoffList method calls PresenceList first, then handoffInbox
	// for each agent. We simulate a presence response with one agent,
	// then an inbox response with one handoff.
	caller := &testCaller{
		callToolFn: func(name string, _ map[string]any) (json.RawMessage, error) {
			switch name {
			case "agent_context__agent_presence_list":
				return wrapToolResult(`{"agents":[{"agent_id":"agent-1","status":"active"}]}`), nil
			case "agent_context__agent_handoff_inbox":
				return wrapToolResult(`{"handoffs":[{"handoff_id":"h1","source_agent":"agent-2","status":"pending","instructions":"do stuff","summary":"summary","created_at":"2026-03-27T10:00:00Z"}]}`), nil
			default:
				return nil, fmt.Errorf("unexpected tool: %s", name)
			}
		},
	}
	agent := bridge.NewAgentBridge(caller)
	deps := &mockDeps{agent: agent}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/handoffs", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck
	handoffs, ok := body["handoffs"].([]any)
	if !ok {
		t.Fatalf("expected handoffs array in response")
	}
	if len(handoffs) != 1 {
		t.Errorf("expected 1 handoff, got %d", len(handoffs))
	}
}

func TestHandleHandoffList_Error(t *testing.T) {
	caller := &testCaller{
		callToolFn: func(_ string, _ map[string]any) (json.RawMessage, error) {
			return nil, fmt.Errorf("bridge error")
		},
	}
	agent := bridge.NewAgentBridge(caller)
	deps := &mockDeps{agent: agent}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/handoffs", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// --- handleHandoffCreate tests ---

func TestHandleHandoffCreate_MissingTargetAgentID(t *testing.T) {
	deps := &mockDeps{agent: bridge.NewAgentBridge(&testCaller{})}
	d := New(deps)

	body := `{"instructions":"do this"}`
	req := httptest.NewRequest("POST", "/api/handoffs", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleHandoffCreate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleHandoffCreate_MissingInstructions(t *testing.T) {
	deps := &mockDeps{agent: bridge.NewAgentBridge(&testCaller{})}
	d := New(deps)

	body := `{"target_agent_id":"agent-1"}`
	req := httptest.NewRequest("POST", "/api/handoffs", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleHandoffCreate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleHandoffCreate_InvalidBody(t *testing.T) {
	deps := &mockDeps{agent: bridge.NewAgentBridge(&testCaller{})}
	d := New(deps)

	req := httptest.NewRequest("POST", "/api/handoffs", strings.NewReader("not-json"))
	rec := httptest.NewRecorder()
	d.handleHandoffCreate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleHandoffCreate_WithSessionID(t *testing.T) {
	caller := &testCaller{
		callToolFn: func(name string, _ map[string]any) (json.RawMessage, error) {
			if name == "agent_context__agent_handoff_create" {
				return wrapToolResult(`{"ok":true,"handoff_id":"h-new","token_count":100}`), nil
			}
			return nil, fmt.Errorf("unexpected tool: %s", name)
		},
	}
	deps := &mockDeps{agent: bridge.NewAgentBridge(caller)}
	d := New(deps)

	body := `{"session_id":"sess-1","target_agent_id":"agent-2","instructions":"implement feature"}`
	req := httptest.NewRequest("POST", "/api/handoffs", strings.NewReader(body))
	rec := httptest.NewRecorder()
	d.handleHandoffCreate(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp["status"] != "created" {
		t.Errorf("expected status 'created', got %v", resp["status"])
	}
	if resp["handoff_id"] != "h-new" {
		t.Errorf("expected handoff_id 'h-new', got %v", resp["handoff_id"])
	}
}

// --- handleHandoffAccept tests ---

func TestHandleHandoffAccept_MissingHandoffID(t *testing.T) {
	deps := &mockDeps{agent: bridge.NewAgentBridge(&testCaller{})}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	// The route pattern extracts {id}, but we need to test with an empty id.
	// Use direct handler call with a request that has no path value.
	req := httptest.NewRequest("POST", "/api/handoffs//accept", strings.NewReader(`{"session_id":"s1"}`))
	rec := httptest.NewRecorder()
	// Call handler directly to control path values.
	d.handleHandoffAccept(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing id, got %d", rec.Code)
	}
}

func TestHandleHandoffAccept_MissingSessionAndAgent(t *testing.T) {
	deps := &mockDeps{agent: bridge.NewAgentBridge(&testCaller{})}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("POST", "/api/handoffs/h1/accept", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleHandoffAccept_WithSessionID(t *testing.T) {
	caller := &testCaller{
		callToolFn: func(name string, _ map[string]any) (json.RawMessage, error) {
			if name == "agent_context__agent_handoff_accept" {
				return wrapToolResult(`{"ok":true,"handoff_id":"h1","source_agent":"agent-1","instructions":"do stuff"}`), nil
			}
			return nil, fmt.Errorf("unexpected tool: %s", name)
		},
	}
	deps := &mockDeps{agent: bridge.NewAgentBridge(caller)}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	body := `{"session_id":"sess-1"}`
	req := httptest.NewRequest("POST", "/api/handoffs/h1/accept", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp["status"] != "accepted" {
		t.Errorf("expected status 'accepted', got %v", resp["status"])
	}
}

func TestHandleHandoffAccept_InvalidBody(t *testing.T) {
	deps := &mockDeps{agent: bridge.NewAgentBridge(&testCaller{})}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("POST", "/api/handoffs/h1/accept", strings.NewReader("invalid-json"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- Helper ---

// wrapToolResult wraps a JSON string in the MCP CallToolResult envelope.
// The text field must be a JSON string (not a raw object), so we marshal the
// payload into a quoted string first.
func wrapToolResult(jsonPayload string) json.RawMessage {
	quoted, _ := json.Marshal(jsonPayload)
	envelope := fmt.Sprintf(`{"content":[{"type":"text","text":%s}]}`, string(quoted))
	return json.RawMessage(envelope)
}
