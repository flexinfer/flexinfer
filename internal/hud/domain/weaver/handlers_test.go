package weaver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockBridge implements BridgeCaller for testing.
type mockBridge struct {
	responses map[string]json.RawMessage
	errors    map[string]error
	// captured holds the params of the last Call per method, so tests
	// can assert that the handler forwarded the right struct shape.
	captured map[string]any
}

func (m *mockBridge) Call(method string, params any) (json.RawMessage, error) {
	if m.captured == nil {
		m.captured = map[string]any{}
	}
	m.captured[method] = params
	if m.errors != nil {
		if err, ok := m.errors[method]; ok {
			return nil, err
		}
	}
	if m.responses != nil {
		if resp, ok := m.responses[method]; ok {
			return resp, nil
		}
	}
	return nil, errors.New("unexpected method: " + method)
}

// mockDeps implements Deps for testing.
type mockDeps struct {
	bridge BridgeCaller
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

func (m *mockDeps) WeaverBridge() BridgeCaller {
	return m.bridge
}

// ---------------------------------------------------------------------------
// handleStatus tests
// ---------------------------------------------------------------------------

func TestHandleStatus_WeaverEnabled(t *testing.T) {
	bridge := &mockBridge{
		responses: map[string]json.RawMessage{
			"loom/weaver/status": json.RawMessage(`{
				"enabled": true,
				"router_model": "gemma-4",
				"subagent_model": "gemma-4",
				"domains": [
					{"name": "codebase", "description": "Repository state", "tools": ["git__git_status"]},
					{"name": "cluster-ops", "description": "Cluster health", "tools": ["k8s_apps_k3s__k8s_getPods"]}
				],
				"max_iterations": 8,
				"max_concurrent": 4
			}`),
		},
	}
	d := New(&mockDeps{bridge: bridge})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/weaver/status", nil)
	d.handleStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", resp["enabled"])
	}
	domains, ok := resp["domains"].([]any)
	if !ok || len(domains) != 2 {
		t.Errorf("expected 2 domains, got %v", resp["domains"])
	}
	if resp["router_model"] != "gemma-4" {
		t.Errorf("expected router_model=gemma-4, got %v", resp["router_model"])
	}
}

func TestHandleStatus_NoBridge(t *testing.T) {
	d := New(&mockDeps{bridge: nil})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/weaver/status", nil)
	d.handleStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["enabled"] != false {
		t.Errorf("expected enabled=false, got %v", resp["enabled"])
	}
}

func TestHandleStatus_BridgeError(t *testing.T) {
	bridge := &mockBridge{
		errors: map[string]error{
			"loom/weaver/status": errors.New("connection refused"),
		},
	}
	d := New(&mockDeps{bridge: bridge})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/weaver/status", nil)
	d.handleStatus(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["enabled"] != false {
		t.Errorf("expected enabled=false on error, got %v", resp["enabled"])
	}
	// The handler also includes an error message on bridge failure.
	if resp["error"] != "connection refused" {
		t.Errorf("expected error message, got %v", resp["error"])
	}
}

// ---------------------------------------------------------------------------
// handleDomains tests
// ---------------------------------------------------------------------------

func TestHandleDomains_Success(t *testing.T) {
	bridge := &mockBridge{
		responses: map[string]json.RawMessage{
			"loom/weaver/status": json.RawMessage(`{
				"enabled": true,
				"router_model": "gemma-4",
				"subagent_model": "qwen3-8b",
				"domains": [
					{"name": "codebase", "description": "Repository state", "tools": ["git__git_status"]},
					{"name": "ci-pipeline", "description": "Pipeline status", "tools": ["gitlab__list_pipelines"]},
					{"name": "cluster-ops", "description": "Cluster health", "tools": ["k8s_apps_k3s__k8s_getPods"]}
				]
			}`),
		},
	}
	d := New(&mockDeps{bridge: bridge})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/weaver/domains", nil)
	d.handleDomains(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	domains, ok := resp["domains"].([]any)
	if !ok || len(domains) != 3 {
		t.Errorf("expected 3 domains, got %v", resp["domains"])
	}
	if resp["router_model"] != "gemma-4" {
		t.Errorf("expected router_model=gemma-4, got %v", resp["router_model"])
	}
	if resp["subagent_model"] != "qwen3-8b" {
		t.Errorf("expected subagent_model=qwen3-8b, got %v", resp["subagent_model"])
	}
}

func TestHandleDomains_NoBridge(t *testing.T) {
	d := New(&mockDeps{bridge: nil})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/weaver/domains", nil)
	d.handleDomains(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	domains, ok := resp["domains"].([]any)
	if !ok || len(domains) != 0 {
		t.Errorf("expected empty domains, got %v", resp["domains"])
	}
}

func TestHandleDomains_BridgeError(t *testing.T) {
	bridge := &mockBridge{
		errors: map[string]error{
			"loom/weaver/status": errors.New("timeout"),
		},
	}
	d := New(&mockDeps{bridge: bridge})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/weaver/domains", nil)
	d.handleDomains(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	domains, ok := resp["domains"].([]any)
	if !ok || len(domains) != 0 {
		t.Errorf("expected empty domains on error, got %v", resp["domains"])
	}
}

// ---------------------------------------------------------------------------
// handleHistory tests
// ---------------------------------------------------------------------------

func TestHandleHistory_Success(t *testing.T) {
	bridge := &mockBridge{
		responses: map[string]json.RawMessage{
			"loom/weaver/history": json.RawMessage(`{
				"entries": [
					{"query": "cluster status", "status": "ok", "latency_ms": 1200, "total_tokens": 500},
					{"query": "git status", "status": "ok", "latency_ms": 800, "total_tokens": 300}
				]
			}`),
		},
	}
	d := New(&mockDeps{bridge: bridge})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/weaver/history", nil)
	d.handleHistory(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	entries, ok := resp["entries"].([]any)
	if !ok || len(entries) != 2 {
		t.Errorf("expected 2 entries, got %v", resp["entries"])
	}
}

func TestHandleHistory_NoBridge(t *testing.T) {
	d := New(&mockDeps{bridge: nil})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/weaver/history", nil)
	d.handleHistory(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	entries, ok := resp["entries"].([]any)
	if !ok || len(entries) != 0 {
		t.Errorf("expected empty entries, got %v", resp["entries"])
	}
}

func TestHandleHistory_BridgeError(t *testing.T) {
	bridge := &mockBridge{
		errors: map[string]error{
			"loom/weaver/history": errors.New("not found"),
		},
	}
	d := New(&mockDeps{bridge: bridge})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/weaver/history", nil)
	d.handleHistory(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	entries, ok := resp["entries"].([]any)
	if !ok || len(entries) != 0 {
		t.Errorf("expected empty entries on error, got %v", resp["entries"])
	}
}

// ---------------------------------------------------------------------------
// handleMetrics tests
// ---------------------------------------------------------------------------

func TestHandleMetrics_DerivedFromHistory(t *testing.T) {
	// handleMetrics derives metrics from loom/weaver/history.
	bridge := &mockBridge{
		responses: map[string]json.RawMessage{
			"loom/weaver/history": json.RawMessage(`{
				"entries": [
					{"query": "q1", "status": "ok",    "latency_ms": 1000, "total_tokens": 500},
					{"query": "q2", "status": "error", "latency_ms": 2000, "total_tokens": 300},
					{"query": "q3", "status": "ok",    "latency_ms": 1500, "total_tokens": 200}
				]
			}`),
		},
	}
	d := New(&mockDeps{bridge: bridge})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/weaver/metrics", nil)
	d.handleMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// 3 entries total.
	if resp["total_queries"].(float64) != 3 {
		t.Errorf("expected total_queries=3, got %v", resp["total_queries"])
	}
	// avg_latency_ms = (1000+2000+1500)/3 = 1500.
	if resp["avg_latency_ms"].(float64) != 1500 {
		t.Errorf("expected avg_latency_ms=1500, got %v", resp["avg_latency_ms"])
	}
	// 1 error out of 3.
	expectedErrorRate := 1.0 / 3.0
	actualErrorRate := resp["error_rate"].(float64)
	if actualErrorRate < expectedErrorRate-0.001 || actualErrorRate > expectedErrorRate+0.001 {
		t.Errorf("expected error_rate~%.4f, got %v", expectedErrorRate, actualErrorRate)
	}
	// total_tokens = 500+300+200 = 1000.
	if resp["total_tokens"].(float64) != 1000 {
		t.Errorf("expected total_tokens=1000, got %v", resp["total_tokens"])
	}
	// 1 error entry.
	if resp["error_count"].(float64) != 1 {
		t.Errorf("expected error_count=1, got %v", resp["error_count"])
	}
}

func TestHandleMetrics_NoBridge(t *testing.T) {
	d := New(&mockDeps{bridge: nil})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/weaver/metrics", nil)
	d.handleMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["total_queries"].(float64) != 0 {
		t.Errorf("expected total_queries=0, got %v", resp["total_queries"])
	}
	if resp["avg_latency_ms"].(float64) != 0 {
		t.Errorf("expected avg_latency_ms=0, got %v", resp["avg_latency_ms"])
	}
	if resp["error_rate"].(float64) != 0 {
		t.Errorf("expected error_rate=0, got %v", resp["error_rate"])
	}
	if resp["total_tokens"].(float64) != 0 {
		t.Errorf("expected total_tokens=0, got %v", resp["total_tokens"])
	}
	if resp["error_count"].(float64) != 0 {
		t.Errorf("expected error_count=0, got %v", resp["error_count"])
	}
}

func TestHandleMetrics_BridgeError(t *testing.T) {
	bridge := &mockBridge{
		errors: map[string]error{
			"loom/weaver/history": errors.New("service unavailable"),
		},
	}
	d := New(&mockDeps{bridge: bridge})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/weaver/metrics", nil)
	d.handleMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["total_queries"].(float64) != 0 {
		t.Errorf("expected total_queries=0 on error, got %v", resp["total_queries"])
	}
	if resp["error_count"].(float64) != 0 {
		t.Errorf("expected error_count=0 on error, got %v", resp["error_count"])
	}
}

// ---------------------------------------------------------------------------
// handleQuery tests
// ---------------------------------------------------------------------------

func TestHandleQuery_Success(t *testing.T) {
	bridge := &mockBridge{
		responses: map[string]json.RawMessage{
			"loom/weaver/query": json.RawMessage(`{
				"answer": "k3s nodes are healthy",
				"domain_results": [
					{"domain": "cluster-ops", "answer": "k3s nodes are healthy", "tokens": 120, "latency_ms": 850}
				],
				"total_tokens": 120,
				"latency_ms": 850,
				"domains_used": ["cluster-ops"]
			}`),
		},
	}
	d := New(&mockDeps{bridge: bridge})

	body := `{"query":"check k3s health","max_tokens":1024,"agent_id":"loom-mills-operator"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/weaver/query", strings.NewReader(body))
	d.handleQuery(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["answer"] != "k3s nodes are healthy" {
		t.Errorf("expected forwarded answer, got %v", resp["answer"])
	}
	if resp["total_tokens"].(float64) != 120 {
		t.Errorf("expected total_tokens=120, got %v", resp["total_tokens"])
	}

	// The handler must forward params to the daemon; sniff the captured
	// struct to make sure agent_id + max_tokens propagated.
	captured, ok := bridge.captured["loom/weaver/query"]
	if !ok {
		t.Fatal("expected loom/weaver/query to be called")
	}
	raw, _ := json.Marshal(captured)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	if got["query"] != "check k3s health" {
		t.Errorf("forwarded query = %v, want %q", got["query"], "check k3s health")
	}
	if got["agent_id"] != "loom-mills-operator" {
		t.Errorf("forwarded agent_id = %v, want loom-mills-operator", got["agent_id"])
	}
}

func TestHandleQuery_TrimsAndValidatesQuery(t *testing.T) {
	d := New(&mockDeps{bridge: &mockBridge{}})
	for _, body := range []string{`{"query":""}`, `{"query":"   "}`, `{}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/weaver/query", strings.NewReader(body))
		d.handleQuery(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %q: expected 400, got %d", body, w.Code)
		}
	}
}

func TestHandleQuery_BridgeError(t *testing.T) {
	bridge := &mockBridge{
		errors: map[string]error{
			"loom/weaver/query": errors.New("router off"),
		},
	}
	d := New(&mockDeps{bridge: bridge})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/weaver/query",
		strings.NewReader(`{"query":"q"}`))
	d.handleQuery(w, r)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestHandleQuery_NoBridge(t *testing.T) {
	d := New(&mockDeps{bridge: nil})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/weaver/query",
		strings.NewReader(`{"query":"q"}`))
	d.handleQuery(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandleQuery_BodyTooLarge(t *testing.T) {
	d := New(&mockDeps{bridge: &mockBridge{}})

	// Build a JSON body whose query field overflows the cap. The
	// LimitReader keeps the read bounded; the handler must still
	// produce a 413 even when JSON unmarshal would otherwise succeed.
	big := strings.Repeat("x", maxQueryBodyBytes+128)
	body := `{"query":"` + big + `"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/weaver/query", strings.NewReader(body))
	d.handleQuery(w, r)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", w.Code)
	}
}

func TestHandleQuery_InvalidJSON(t *testing.T) {
	d := New(&mockDeps{bridge: &mockBridge{}})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/weaver/query",
		strings.NewReader(`{"query":}`))
	d.handleQuery(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleMetrics_EmptyHistory(t *testing.T) {
	// Bridge returns history with no entries -- metrics should all be zero.
	bridge := &mockBridge{
		responses: map[string]json.RawMessage{
			"loom/weaver/history": json.RawMessage(`{"entries": []}`),
		},
	}
	d := New(&mockDeps{bridge: bridge})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/weaver/metrics", nil)
	d.handleMetrics(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["total_queries"].(float64) != 0 {
		t.Errorf("expected total_queries=0, got %v", resp["total_queries"])
	}
	if resp["avg_latency_ms"].(float64) != 0 {
		t.Errorf("expected avg_latency_ms=0, got %v", resp["avg_latency_ms"])
	}
	if resp["error_rate"].(float64) != 0 {
		t.Errorf("expected error_rate=0, got %v", resp["error_rate"])
	}
}
