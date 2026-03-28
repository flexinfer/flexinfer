package memory

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

// --- Mock MemoryMonitor ---

type mockMemoryMonitor struct {
	stats      *bridge.MemoryStatsResult
	promoteErr error
	demoteErr  error
	refreshErr error
}

func (m *mockMemoryMonitor) Stats() *bridge.MemoryStatsResult { return m.stats }
func (m *mockMemoryMonitor) Promote(_ string) error           { return m.promoteErr }
func (m *mockMemoryMonitor) Demote(_ string) error            { return m.demoteErr }
func (m *mockMemoryMonitor) Refresh() error                   { return m.refreshErr }

// --- Mock Deps ---

type mockDeps struct {
	agent           *bridge.AgentBridge
	monitor         MemoryMonitorOps
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
func (d *mockDeps) MemoryMonitor() MemoryMonitorOps     { return d.monitor }

// --- StatsPayload tests ---

func TestStatsPayload(t *testing.T) {
	tests := []struct {
		name        string
		stats       bridge.MemoryStatsResult
		wantTotal   int
		hasCompress bool
	}{
		{
			name: "basic stats without compression",
			stats: bridge.MemoryStatsResult{
				WorkingMemory:   bridge.MemoryTierStats{Items: 5, Tokens: 500},
				ShortTermMemory: bridge.MemoryTierStats{Items: 10, Tokens: 1000},
				LongTermMemory:  bridge.MemoryTierStats{Items: 20, Tokens: 2000},
				TotalItems:      35,
				TotalTokens:     3500,
			},
			wantTotal:   35,
			hasCompress: false,
		},
		{
			name: "stats with compression data",
			stats: bridge.MemoryStatsResult{
				WorkingMemory:          bridge.MemoryTierStats{Items: 5, Tokens: 500},
				ShortTermMemory:        bridge.MemoryTierStats{Items: 10, Tokens: 1000},
				LongTermMemory:         bridge.MemoryTierStats{Items: 20, Tokens: 2000},
				TotalItems:             35,
				TotalTokens:            3500,
				CompressionRatio:       0.7,
				ItemsCompressedLast24h: 5,
				ItemsAddedLast24h:      3,
			},
			wantTotal:   35,
			hasCompress: true,
		},
		{
			name: "zero items",
			stats: bridge.MemoryStatsResult{
				TotalItems:  0,
				TotalTokens: 0,
			},
			wantTotal:   0,
			hasCompress: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := StatsPayload(&tt.stats)
			totalItems, ok := payload["total_items"].(int)
			if !ok {
				t.Fatalf("expected total_items to be int")
			}
			if totalItems != tt.wantTotal {
				t.Errorf("expected total_items %d, got %d", tt.wantTotal, totalItems)
			}
			if _, hasWM := payload["working_memory"]; !hasWM {
				t.Error("expected working_memory key")
			}
			if _, hasSTM := payload["short_term_memory"]; !hasSTM {
				t.Error("expected short_term_memory key")
			}
			if _, hasLTM := payload["long_term_memory"]; !hasLTM {
				t.Error("expected long_term_memory key")
			}
			_, hasCompress := payload["compression"]
			if hasCompress != tt.hasCompress {
				t.Errorf("expected compression presence=%v, got %v", tt.hasCompress, hasCompress)
			}
		})
	}
}

func TestStatsPayload_TierShape(t *testing.T) {
	stats := &bridge.MemoryStatsResult{
		WorkingMemory: bridge.MemoryTierStats{Items: 5, Tokens: 500},
	}
	payload := StatsPayload(stats)
	wm, ok := payload["working_memory"].(map[string]any)
	if !ok {
		t.Fatalf("expected working_memory to be map[string]any")
	}
	if wm["items"] != 5 {
		t.Errorf("expected items=5, got %v", wm["items"])
	}
	if wm["tokens"] != 500 {
		t.Errorf("expected tokens=500, got %v", wm["tokens"])
	}
}

// --- Domain tests ---

func TestMemoryDomainName(t *testing.T) {
	d := New(&mockDeps{monitor: &mockMemoryMonitor{}})
	if d.Name() != "memory" {
		t.Fatalf("expected name 'memory', got %q", d.Name())
	}
}

func TestMemoryDomainRouteRegistration(t *testing.T) {
	caller := &testCaller{}
	agent := bridge.NewAgentBridge(caller)
	deps := &mockDeps{agent: agent, monitor: &mockMemoryMonitor{}}
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
		{"GET", "/api/memory/stats"},
		{"POST", "/api/memory/test-id/promote"},
		{"POST", "/api/memory/test-id/demote"},
		{"GET", "/api/memory/items"},
		{"POST", "/api/memory"},
		{"DELETE", "/api/memory/test-id"},
		{"GET", "/api/memory/compaction"},
	}

	for _, rt := range routes {
		var req *http.Request
		if rt.method == "POST" {
			req = httptest.NewRequest(rt.method, rt.path, strings.NewReader(`{"title":"test","content":"content"}`))
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

// --- handleMemoryStats tests ---

func TestHandleMemoryStats_FromMonitor(t *testing.T) {
	mon := &mockMemoryMonitor{
		stats: &bridge.MemoryStatsResult{
			WorkingMemory:   bridge.MemoryTierStats{Items: 5, Tokens: 500},
			ShortTermMemory: bridge.MemoryTierStats{Items: 10, Tokens: 1000},
			LongTermMemory:  bridge.MemoryTierStats{Items: 20, Tokens: 2000},
			TotalItems:      35,
			TotalTokens:     3500,
		},
	}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(&testCaller{}),
		monitor: mon,
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/memory/stats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck
	if body["total_items"].(float64) != 35 {
		t.Errorf("expected total_items=35, got %v", body["total_items"])
	}
}

func TestHandleMemoryStats_FallbackToAgent(t *testing.T) {
	// When monitor returns nil stats, the handler falls back to agent.MemoryStats().
	caller := &testCaller{
		callToolFn: func(name string, _ map[string]any) (json.RawMessage, error) {
			if name == "agent_context__agent_memory_stats" {
				return wrapToolResult(`{"working_memory":{"item_count":2,"token_count":200},"short_term_memory":{"item_count":3,"token_count":300},"long_term_memory":{"item_count":0,"token_count":0},"total_items":5,"total_tokens":500}`), nil
			}
			return nil, fmt.Errorf("unexpected tool: %s", name)
		},
	}
	mon := &mockMemoryMonitor{stats: nil} // nil stats triggers fallback
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(caller),
		monitor: mon,
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/memory/stats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck
	if body["total_items"].(float64) != 5 {
		t.Errorf("expected total_items=5, got %v", body["total_items"])
	}
}

func TestHandleMemoryStats_FallbackError(t *testing.T) {
	caller := &testCaller{
		callToolFn: func(_ string, _ map[string]any) (json.RawMessage, error) {
			return nil, fmt.Errorf("bridge error")
		},
	}
	mon := &mockMemoryMonitor{stats: nil}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(caller),
		monitor: mon,
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/memory/stats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

// --- handleMemoryPromote tests ---

func TestHandleMemoryPromote_Success(t *testing.T) {
	mon := &mockMemoryMonitor{promoteErr: nil}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(&testCaller{}),
		monitor: mon,
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("POST", "/api/memory/item-1/promote", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if deps.broadcastCalled != 1 {
		t.Errorf("expected 1 broadcast call, got %d", deps.broadcastCalled)
	}
}

func TestHandleMemoryPromote_Error(t *testing.T) {
	mon := &mockMemoryMonitor{promoteErr: fmt.Errorf("promote failed")}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(&testCaller{}),
		monitor: mon,
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("POST", "/api/memory/item-1/promote", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

// --- handleMemoryDemote tests ---

func TestHandleMemoryDemote_Success(t *testing.T) {
	mon := &mockMemoryMonitor{demoteErr: nil}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(&testCaller{}),
		monitor: mon,
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("POST", "/api/memory/item-1/demote", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if deps.broadcastCalled != 1 {
		t.Errorf("expected 1 broadcast call, got %d", deps.broadcastCalled)
	}
}

func TestHandleMemoryDemote_Error(t *testing.T) {
	mon := &mockMemoryMonitor{demoteErr: fmt.Errorf("demote failed")}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(&testCaller{}),
		monitor: mon,
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("POST", "/api/memory/item-1/demote", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

// --- handleMemoryItems tests ---

func TestHandleMemoryItems_Success(t *testing.T) {
	caller := &testCaller{
		callToolFn: func(name string, _ map[string]any) (json.RawMessage, error) {
			if name == "agent_context__agent_memory_recall" {
				return wrapToolResult(`{"items":[{"id":"m1","title":"Test Memory","content":"content","tier":"working","importance":"high","original_tokens":100}]}`), nil
			}
			return nil, fmt.Errorf("unexpected tool: %s", name)
		},
	}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(caller),
		monitor: &mockMemoryMonitor{},
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/memory/items", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body) //nolint:errcheck
	items := body["items"].([]any)
	if len(items) != 1 {
		t.Errorf("expected 1 item, got %d", len(items))
	}
}

func TestHandleMemoryItems_WithQueryParams(t *testing.T) {
	var capturedArgs map[string]any
	caller := &testCaller{
		callToolFn: func(name string, args map[string]any) (json.RawMessage, error) {
			if name == "agent_context__agent_memory_recall" {
				capturedArgs = args
				return wrapToolResult(`{"items":[]}`), nil
			}
			return nil, fmt.Errorf("unexpected tool: %s", name)
		},
	}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(caller),
		monitor: &mockMemoryMonitor{},
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/memory/items?tier=working&query=test&limit=10", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// The AgentBridge.MemoryRecall translates tier/query/limit into args;
	// we verify the caller received the call.
	if capturedArgs == nil {
		t.Fatal("expected capturedArgs to be set")
	}
}

func TestHandleMemoryItems_Error(t *testing.T) {
	caller := &testCaller{
		callToolFn: func(_ string, _ map[string]any) (json.RawMessage, error) {
			return nil, fmt.Errorf("bridge error")
		},
	}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(caller),
		monitor: &mockMemoryMonitor{},
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/memory/items", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

// --- handleMemoryAdd tests ---

func TestHandleMemoryAdd_Success(t *testing.T) {
	caller := &testCaller{
		callToolFn: func(name string, _ map[string]any) (json.RawMessage, error) {
			if name == "agent_context__agent_memory_add" {
				return wrapToolResult(`{"ok":true}`), nil
			}
			return nil, fmt.Errorf("unexpected tool: %s", name)
		},
	}
	mon := &mockMemoryMonitor{}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(caller),
		monitor: mon,
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	body := `{"title":"Test","content":"Some content","tier":"working"}`
	req := httptest.NewRequest("POST", "/api/memory", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if deps.broadcastCalled != 1 {
		t.Errorf("expected 1 broadcast, got %d", deps.broadcastCalled)
	}
}

func TestHandleMemoryAdd_MissingTitle(t *testing.T) {
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(&testCaller{}),
		monitor: &mockMemoryMonitor{},
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	body := `{"content":"content"}`
	req := httptest.NewRequest("POST", "/api/memory", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleMemoryAdd_MissingContent(t *testing.T) {
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(&testCaller{}),
		monitor: &mockMemoryMonitor{},
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	body := `{"title":"test"}`
	req := httptest.NewRequest("POST", "/api/memory", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestHandleMemoryAdd_InvalidBody(t *testing.T) {
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(&testCaller{}),
		monitor: &mockMemoryMonitor{},
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("POST", "/api/memory", strings.NewReader("bad-json"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// --- handleMemoryDelete tests ---

func TestHandleMemoryDelete_Success(t *testing.T) {
	caller := &testCaller{
		callToolFn: func(name string, _ map[string]any) (json.RawMessage, error) {
			if name == "agent_context__agent_memory_delete" {
				return wrapToolResult(`{"ok":true}`), nil
			}
			return nil, fmt.Errorf("unexpected tool: %s", name)
		},
	}
	mon := &mockMemoryMonitor{}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(caller),
		monitor: mon,
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("DELETE", "/api/memory/item-1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if deps.broadcastCalled != 1 {
		t.Errorf("expected 1 broadcast, got %d", deps.broadcastCalled)
	}
}

func TestHandleMemoryDelete_Error(t *testing.T) {
	caller := &testCaller{
		callToolFn: func(_ string, _ map[string]any) (json.RawMessage, error) {
			return nil, fmt.Errorf("bridge error")
		},
	}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(caller),
		monitor: &mockMemoryMonitor{},
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("DELETE", "/api/memory/item-1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
	}
}

// --- handleMemoryCompaction tests ---

func TestHandleMemoryCompaction_Success(t *testing.T) {
	caller := &testCaller{
		callToolFn: func(name string, _ map[string]any) (json.RawMessage, error) {
			if name == "agent_context__agent_compaction_status" {
				return wrapToolResult(`{"running":false,"items_compacted":10,"items_promoted":2,"items_expired":1}`), nil
			}
			return nil, fmt.Errorf("unexpected tool: %s", name)
		},
	}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(caller),
		monitor: &mockMemoryMonitor{},
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/memory/compaction", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleMemoryCompaction_Error(t *testing.T) {
	caller := &testCaller{
		callToolFn: func(_ string, _ map[string]any) (json.RawMessage, error) {
			return nil, fmt.Errorf("bridge error")
		},
	}
	deps := &mockDeps{
		agent:   bridge.NewAgentBridge(caller),
		monitor: &mockMemoryMonitor{},
	}
	d := New(deps)
	mux := http.NewServeMux()
	mw := func(next http.HandlerFunc) http.HandlerFunc { return next }
	d.RegisterRoutes(mux, mw)

	req := httptest.NewRequest("GET", "/api/memory/compaction", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rec.Code)
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
