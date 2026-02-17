package hud

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
)

// newTestApp creates an App with mock monitors for handler testing.
// It uses a mock daemon that returns controlled data, then refreshes
// the monitors once so they have data for the handlers to read.
func newTestApp(t *testing.T) (*App, *http.ServeMux) {
	t.Helper()

	app, mux, _ := newTestAppWithHandlers(t)
	return app, mux
}

func newTestAppWithHandlers(t *testing.T) (*App, *http.ServeMux, *appMockHandlers) {
	t.Helper()

	// Create a mock daemon over Unix socket.
	sockPath, handlers := newMockDaemonForApp(t)

	// Register mock responses for the daemon RPC methods.
	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		return &bridge.StatusResult{
			Running:     true,
			Servers:     3,
			ActiveConns: 1,
			Processes:   []string{"git", "time"},
		}, nil
	})

	handlers.handle("loom/health", func(_ json.RawMessage) (any, error) {
		return &bridge.HealthResult{
			Servers: map[string]bridge.ServerHealth{
				"git": {
					Local:  bridge.HealthEntry{Healthy: true, AvgLatencyMs: 5.2},
					Target: "local",
				},
				"time": {
					Local:  bridge.HealthEntry{Healthy: true, AvgLatencyMs: 3.1},
					Target: "local",
				},
			},
		}, nil
	})

	handlers.handle("loom/servers", func(_ json.RawMessage) (any, error) {
		return &bridge.ServersResult{
			Servers: []bridge.ServerInfo{
				{Name: "git", Running: true, Categories: []string{"dev-tools"}},
				{Name: "time", Running: true, Categories: []string{"utility"}},
				{Name: "memory", Running: false},
			},
		}, nil
	})

	// Agent bridge tool calls — return empty results.
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		json.Unmarshal(params, &p)

		switch {
		case strings.Contains(p.Name, "session_list"):
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"sessions\":[]}"}]}`), nil
		case strings.Contains(p.Name, "task_list"):
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"tasks\":[]}"}]}`), nil
		case strings.Contains(p.Name, "memory_stats"):
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"total_items\":0,\"total_tokens\":0}"}]}`), nil
		case strings.Contains(p.Name, "graph_stats"):
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"entity_count\":0,\"relation_count\":0}"}]}`), nil
		case strings.Contains(p.Name, "workflow_list"):
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"workflows\":[]}"}]}`), nil
		default:
			return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
		}
	})

	client := bridge.NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect to mock daemon: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	agent := bridge.NewAgentBridge(client)

	app := &App{
		config:     Config{Dev: true},
		client:     client,
		agent:      agent,
		cache:      bridge.NewCache(),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		sseHub:     NewSSEHub(nil),
		nudgeQueue: NewNudgeQueue(),
	}

	// Create monitors pointing at the mock daemon. Don't start polling —
	// we just call Refresh() once to populate the snapshots.
	app.fleetMonitor = monitor.NewFleetMonitor(client, agent, nil)
	app.fleetMonitor.Refresh()

	app.healthMonitor = monitor.NewHealthMonitor(client, nil)
	app.healthMonitor.Refresh()

	app.memoryMonitor = monitor.NewMemoryMonitor(agent, nil)
	// Don't refresh memory — the mock returns minimal data that may not parse.

	app.workflowMonitor = monitor.NewWorkflowMonitor(agent, nil)

	mux := http.NewServeMux()
	app.registerRoutes(mux)

	return app, mux, handlers
}

func TestHandler_Status(t *testing.T) {
	_, mux := newTestApp(t)

	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected application/json, got %s", ct)
	}

	var result bridge.StatusResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !result.Running {
		t.Error("expected running=true")
	}
	if result.Servers != 3 {
		t.Errorf("expected 3 servers, got %d", result.Servers)
	}
}

func TestHandler_Health(t *testing.T) {
	_, mux := newTestApp(t)

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result bridge.HealthResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(result.Servers) == 0 {
		t.Fatal("expected at least one server in health result")
	}
}

func TestHandler_Fleet(t *testing.T) {
	_, mux := newTestApp(t)

	req := httptest.NewRequest("GET", "/api/fleet", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Fleet snapshot should contain daemon_running.
	if _, ok := result["daemon_running"]; !ok {
		t.Error("expected daemon_running field in fleet snapshot")
	}
	if _, ok := result["server_count"]; !ok {
		t.Error("expected server_count field in fleet snapshot")
	}
}

func TestHandler_SSE(t *testing.T) {
	_, mux := newTestApp(t)

	server := httptest.NewServer(mux)
	defer server.Close()

	req2, _ := http.NewRequestWithContext(context.Background(), "GET", server.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}

	// Read the initial connected event.
	buf := make([]byte, 4096)
	n, err := resp.Body.Read(buf)
	if err != nil {
		t.Fatalf("read SSE: %v", err)
	}

	body := string(buf[:n])
	if !strings.Contains(body, "event: connected") {
		t.Fatalf("expected connected event, got: %s", body)
	}
}

func TestHandler_CORS(t *testing.T) {
	_, mux := newTestApp(t)

	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// App is created with Dev: true, so CORS headers should be present.
	acao := w.Header().Get("Access-Control-Allow-Origin")
	if acao != "*" {
		t.Fatalf("expected Access-Control-Allow-Origin: *, got %q", acao)
	}
}

func TestHandler_Preflight(t *testing.T) {
	_, mux := newTestApp(t)

	req := httptest.NewRequest("OPTIONS", "/api/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	acao := w.Header().Get("Access-Control-Allow-Origin")
	if acao != "*" {
		t.Fatalf("expected CORS header on preflight, got %q", acao)
	}
}

func TestHandler_Servers(t *testing.T) {
	_, mux := newTestApp(t)

	req := httptest.NewRequest("GET", "/api/servers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result bridge.ServersResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(result.Servers) == 0 {
		t.Fatal("expected servers in result")
	}
}

func TestHandler_Sandbox(t *testing.T) {
	_, mux := newTestApp(t)

	req := httptest.NewRequest("GET", "/api/sandbox", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected application/json, got %s", ct)
	}

	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The mock daemon returns {} for unknown tools, so the handler should
	// parse it and add available=true.
	if _, ok := result["available"]; !ok {
		t.Error("expected 'available' field in sandbox response")
	}
}

func TestHandler_Sandbox_Cached(t *testing.T) {
	_, mux := newTestApp(t)

	// First request populates the cache.
	req1 := httptest.NewRequest("GET", "/api/sandbox", nil)
	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", w1.Code)
	}

	// Second request should hit the cache and still return 200.
	req2 := httptest.NewRequest("GET", "/api/sandbox", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("cached request: expected 200, got %d", w2.Code)
	}
}

func TestHandler_AgentContextAdd(t *testing.T) {
	_, mux := newTestApp(t)

	body := `{"entries":[{"entry_type":"finding","title":"devbox.exec: myproject","content":"ran make test"}]}`
	req := httptest.NewRequest("POST", "/api/agent/context/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !result["ok"] {
		t.Error("expected ok=true in response")
	}
}

func TestHandler_AgentContextAdd_EmptyEntries(t *testing.T) {
	_, mux := newTestApp(t)

	body := `{"entries":[]}`
	req := httptest.NewRequest("POST", "/api/agent/context/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty entries, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_AgentDispatch_HandoffOnlyWhenNoActiveSession(t *testing.T) {
	_, mux := newTestApp(t)

	body := `{"target_agent_id":"agent-1","title":"Investigate k3s drift","context":"compare live state to gitops","priority":"high"}`
	req := httptest.NewRequest("POST", "/api/agent/dispatch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result struct {
		OK          bool `json:"ok"`
		TaskCreated bool `json:"task_created"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected ok=true")
	}
	if result.TaskCreated {
		t.Fatalf("expected task_created=false when target has no active session")
	}
}

func TestHandler_AgentDispatch_NormalizesExtendedTaskPayload(t *testing.T) {
	_, mux, handlers := newTestAppWithHandlers(t)

	var taskAddSeen bool
	var handoffSeen bool

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}

		switch req.Name {
		case "agent_context__agent_session_list":
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"sessions\":[{\"id\":\"sess-1\",\"agent_id\":\"agent-1\",\"status\":\"active\"}]}"}]}`), nil
		case "agent_context__agent_task_add":
			taskAddSeen = true
			if got, _ := req.Arguments["session_id"].(string); got != "sess-1" {
				t.Fatalf("expected session_id=sess-1, got %q", got)
			}
			tasks, ok := req.Arguments["tasks"].([]any)
			if !ok || len(tasks) != 1 {
				t.Fatalf("expected one task, got %#v", req.Arguments["tasks"])
			}
			task, ok := tasks[0].(map[string]any)
			if !ok {
				t.Fatalf("task is not object: %#v", tasks[0])
			}
			if got, _ := task["title"].(string); got != "Investigate GitOps drift" {
				t.Fatalf("expected trimmed title, got %q", got)
			}
			if got, _ := task["priority"].(string); got != "medium" {
				t.Fatalf("expected invalid priority to normalize to medium, got %q", got)
			}
			if got, _ := task["context"].(string); got != "check Flux reconciliation" {
				t.Fatalf("expected trimmed context, got %q", got)
			}
			if got, _ := task["file_path"].(string); got != "platform/gitops/k3s/mcp-hub/servers/loom/deployment.yaml" {
				t.Fatalf("unexpected file_path: %q", got)
			}
			if got, _ := task["line_number"].(float64); int(got) != 42 {
				t.Fatalf("expected line_number=42, got %#v", task["line_number"])
			}
			tags, ok := task["tags"].([]any)
			if !ok || len(tags) != 3 {
				t.Fatalf("expected normalized tags, got %#v", task["tags"])
			}
			if tags[0] != "dispatched" || tags[1] != "team" || tags[2] != "gitops" {
				t.Fatalf("unexpected tags order/content: %#v", tags)
			}
			blockedBy, ok := task["blocked_by"].([]any)
			if !ok || len(blockedBy) != 1 || blockedBy[0] != "task-123" {
				t.Fatalf("unexpected blocked_by: %#v", task["blocked_by"])
			}
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"ok\":true}"}]}`), nil
		case "agent_context__agent_handoff_create":
			handoffSeen = true
			if got, _ := req.Arguments["to_agent"].(string); got != "agent-1" {
				t.Fatalf("expected to_agent=agent-1, got %q", got)
			}
			if got, _ := req.Arguments["summary"].(string); got != "[Dispatched] Investigate GitOps drift" {
				t.Fatalf("unexpected handoff summary: %q", got)
			}
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"ok\":true}"}]}`), nil
		default:
			// Generic success for monitor refresh calls.
			return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
		}
	})

	body := `{
		"target_agent_id":"  agent-1  ",
		"title":"  Investigate GitOps drift  ",
		"context":"  check Flux reconciliation  ",
		"priority":"P0",
		"tags":[" team ","","gitops","team"],
		"file_path":"  platform/gitops/k3s/mcp-hub/servers/loom/deployment.yaml  ",
		"line_number":42,
		"blocked_by":[" task-123 ","","task-123"]
	}`
	req := httptest.NewRequest("POST", "/api/agent/dispatch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !taskAddSeen {
		t.Fatalf("expected task_add call to occur")
	}
	if !handoffSeen {
		t.Fatalf("expected handoff_create call to occur")
	}

	var result struct {
		Priority    string `json:"priority"`
		TaskCreated bool   `json:"task_created"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if result.Priority != "medium" {
		t.Fatalf("expected normalized response priority=medium, got %q", result.Priority)
	}
	if !result.TaskCreated {
		t.Fatalf("expected task_created=true")
	}
}

func TestHandler_AgentDispatch_RejectsBlankTitle(t *testing.T) {
	_, mux := newTestApp(t)

	body := `{"target_agent_id":"agent-1","title":"   "}`
	req := httptest.NewRequest("POST", "/api/agent/dispatch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_AgentNudgeQueuePolicy_Get(t *testing.T) {
	_, mux := newTestApp(t)

	req := httptest.NewRequest("GET", "/api/agent/nudge-queue-policy", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result struct {
		OK     bool                 `json:"ok"`
		Policy nudgeQueuePolicyView `json:"policy"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected ok=true")
	}
	if result.Policy.Cap <= 0 {
		t.Fatalf("expected policy cap > 0, got %d", result.Policy.Cap)
	}
}

func TestHandler_AgentNudgeQueuePolicy_UpdateRequiresToken(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.AdminToken = "secret"

	req := httptest.NewRequest("POST", "/api/agent/nudge-queue-policy", strings.NewReader(`{"cap": 32}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_AgentNudgeQueuePolicy_Update(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.AdminToken = "secret"

	req := httptest.NewRequest("POST", "/api/agent/nudge-queue-policy", strings.NewReader(`{"cap": 32, "drop_policy": "drop_new", "debounce_ms": 25, "updated_by":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result struct {
		OK     bool                 `json:"ok"`
		Policy nudgeQueuePolicyView `json:"policy"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected ok=true")
	}
	if result.Policy.Cap != 32 {
		t.Fatalf("expected cap=32, got %d", result.Policy.Cap)
	}
	if result.Policy.DropPolicy != string(DropPolicyDropNew) {
		t.Fatalf("expected drop_policy=drop_new, got %q", result.Policy.DropPolicy)
	}
	if result.Policy.DebounceMs != 25 {
		t.Fatalf("expected debounce_ms=25, got %d", result.Policy.DebounceMs)
	}
}

// --- Mock daemon helpers (same pattern as bridge/daemon_test.go) ---

func newMockDaemonForApp(t *testing.T) (string, *appMockHandlers) {
	t.Helper()

	// Use /tmp for shorter socket paths (macOS has a 108-char limit).
	dir, err := os.MkdirTemp("", "loom-app-test-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "d.sock")
	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	handlers := &appMockHandlers{
		methods: make(map[string]func(json.RawMessage) (any, error)),
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handlers.handleConn(conn)
		}
	}()

	t.Cleanup(func() { ln.Close() })
	return sockPath, handlers
}

type appMockHandlers struct {
	mu      sync.RWMutex
	methods map[string]func(json.RawMessage) (any, error)
}

func (m *appMockHandlers) handle(method string, fn func(json.RawMessage) (any, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.methods[method] = fn
}

func (m *appMockHandlers) handleConn(conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, 64*1024)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}

		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      any             `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(buf[:n], &req); err != nil {
			continue
		}

		m.mu.RLock()
		fn, ok := m.methods[req.Method]
		m.mu.RUnlock()

		var resp []byte
		if !ok {
			resp, _ = json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]any{"code": -32601, "message": "unknown method: " + req.Method},
			})
		} else {
			result, err := fn(req.Params)
			if err != nil {
				resp, _ = json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]any{"code": -32603, "message": err.Error()},
				})
			} else {
				resultBytes, _ := json.Marshal(result)
				resp, _ = json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  json.RawMessage(resultBytes),
				})
			}
		}

		resp = append(resp, '\n')
		conn.Write(resp)
	}
}
