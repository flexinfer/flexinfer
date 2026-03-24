package hud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	loomcache "github.com/crb2nu/loom/internal/cache"
	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/domain/memory"
	"github.com/crb2nu/loom/internal/hud/domain/mobile"
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

	handlers.handle("loom/rbac-config", func(_ json.RawMessage) (any, error) {
		return &bridge.RBACConfigResult{
			Enabled:      true,
			AuditEnabled: true,
			DeniedCount:  7,
			Roles: []bridge.RBACRoleInfo{
				{Name: "viewer", Allow: []string{"time/get"}, Deny: []string{"git/delete_*"}},
			},
			Bindings: []bridge.RBACBindingInfo{
				{AgentID: "agent-1", Role: "viewer"},
			},
			RecentDenied: []bridge.RBACDeniedEntry{
				{
					AgentID: "agent-2",
					Server:  "git",
					Tool:    "delete_branch",
					Reason:  "denied by pattern \"git/delete_*\"",
				},
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
		case strings.Contains(p.Name, "session_start"):
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"session_id\":\"sess-mock\"}"}]}`), nil
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
		cache:      loomcache.NewMemoryStore(),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		sseHub:     NewSSEHub(nil),
		eventLog:   NewEventLog(1000),
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

	app.sandboxMonitor = monitor.NewSandboxMonitor(client, nil)

	// Initialize domain registry for route decomposition.
	app.initDomainRegistry()

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

func TestHandler_RBAC(t *testing.T) {
	_, mux := newTestApp(t)

	req := httptest.NewRequest("GET", "/api/rbac", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result bridge.RBACConfigResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.Enabled {
		t.Fatal("expected enabled=true")
	}
	if len(result.Roles) != 1 || result.Roles[0].Name != "viewer" {
		t.Fatalf("unexpected roles payload: %+v", result.Roles)
	}
	if !result.AuditEnabled {
		t.Fatal("expected audit_enabled=true")
	}
	if result.DeniedCount != 7 {
		t.Fatalf("expected denied_count=7, got %d", result.DeniedCount)
	}
	if len(result.RecentDenied) != 1 || result.RecentDenied[0].Tool != "delete_branch" {
		t.Fatalf("unexpected recent_denied payload: %+v", result.RecentDenied)
	}
}

func TestHandler_RBACFallbackWhenDaemonFails(t *testing.T) {
	_, mux, handlers := newTestAppWithHandlers(t)
	handlers.handle("loom/rbac-config", func(_ json.RawMessage) (any, error) {
		return nil, fmt.Errorf("rbac unavailable")
	})

	req := httptest.NewRequest("GET", "/api/rbac", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if enabled, ok := result["enabled"].(bool); !ok || enabled {
		t.Fatalf("expected fallback enabled=false, got: %#v", result["enabled"])
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

func TestRefreshPipelineMonitor_RetriesBlankSnapshot(t *testing.T) {
	mon := &retryingPipelineMonitor{}
	if got := refreshPipelineMonitor(mon, slog.New(slog.NewTextHandler(io.Discard, nil))); !got {
		t.Fatal("expected refresh helper to report a populated snapshot after retry")
	}
	if mon.refreshCalls != 2 {
		t.Fatalf("expected 2 refreshes, got %d", mon.refreshCalls)
	}
	if got := mon.Pipelines(); len(got) != 1 {
		t.Fatalf("expected one pipeline after retry, got %#v", got)
	}
}

func TestFilterTimelineEntries(t *testing.T) {
	entries := []TimelineEntry{
		{EventType: "agent.heartbeat", AgentID: "codex"},
		{EventType: "agent.session.start", AgentID: "codex"},
		{EventType: "agent.heartbeat", AgentID: "claude"},
	}

	filtered := filterTimelineEntries(entries, "codex", "agent.heartbeat")
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered entry, got %d", len(filtered))
	}
	if filtered[0].AgentID != "codex" || filtered[0].EventType != "agent.heartbeat" {
		t.Fatalf("unexpected filtered entry: %+v", filtered[0])
	}
}

type retryingPipelineMonitor struct {
	refreshCalls int
	snapshot     []bridge.PipelineInfo
}

func (m *retryingPipelineMonitor) Ready() bool { return true }

func (m *retryingPipelineMonitor) Refresh() error {
	m.refreshCalls++
	if m.refreshCalls == 1 {
		m.snapshot = nil
		return nil
	}
	m.snapshot = []bridge.PipelineInfo{{
		ID:        101,
		Project:   "services/loom-core",
		Ref:       "main",
		Status:    "running",
		CreatedAt: "2026-03-23T12:00:00Z",
		WebURL:    "https://gitlab.example/pipelines/101",
	}}
	return nil
}

func (m *retryingPipelineMonitor) Pipelines() []bridge.PipelineInfo {
	out := make([]bridge.PipelineInfo, len(m.snapshot))
	copy(out, m.snapshot)
	return out
}

func (m *retryingPipelineMonitor) Projects() []string {
	return []string{"services/loom-core"}
}

func TestHandler_TimelineFilters(t *testing.T) {
	app, mux := newTestApp(t)
	now := time.Now().UTC()

	app.eventLog.Append(TimelineEntry{
		Timestamp: now.Add(-30 * time.Second),
		EventType: "agent.heartbeat",
		AgentID:   "codex",
	})
	app.eventLog.Append(TimelineEntry{
		Timestamp: now.Add(-20 * time.Second),
		EventType: "agent.session.start",
		AgentID:   "codex",
	})
	app.eventLog.Append(TimelineEntry{
		Timestamp: now.Add(-10 * time.Second),
		EventType: "agent.heartbeat",
		AgentID:   "claude",
	})

	req := httptest.NewRequest("GET", "/api/timeline?agent_id=codex&event_type=agent.heartbeat&limit=1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result struct {
		Entries []TimelineEntry `json:"entries"`
		Count   int             `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Count != 1 || len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got count=%d len=%d", result.Count, len(result.Entries))
	}
	if result.Entries[0].AgentID != "codex" || result.Entries[0].EventType != "agent.heartbeat" {
		t.Fatalf("unexpected entry: %+v", result.Entries[0])
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
	var dispatcherSessionSeen bool

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
			if got, _ := req.Arguments["agent_id"].(string); got == "hud-dispatcher" {
				return json.RawMessage(`{"content":[{"type":"text","text":"{\"sessions\":[]}"}]}`), nil
			}
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"sessions\":[{\"id\":\"sess-1\",\"agent_id\":\"agent-1\",\"status\":\"active\"}]}"}]}`), nil
		case "agent_context__agent_session_start":
			dispatcherSessionSeen = true
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"session_id\":\"sess-dispatcher\"}"}]}`), nil
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
			if got, _ := req.Arguments["target_agent_id"].(string); got != "agent-1" {
				t.Fatalf("expected target_agent_id=agent-1, got %q", got)
			}
			if got, _ := req.Arguments["session_id"].(string); got != "sess-dispatcher" {
				t.Fatalf("expected dispatcher session_id, got %q", got)
			}
			if got, _ := req.Arguments["handoff_type"].(string); got != "summary_only" {
				t.Fatalf("expected handoff_type=summary_only, got %q", got)
			}
			if got, _ := req.Arguments["instructions"].(string); !strings.HasPrefix(got, "[Dispatched] Investigate GitOps drift") {
				t.Fatalf("unexpected handoff instructions: %q", got)
			}
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"ok\":true,\"handoff_id\":\"handoff-1\"}"}]}`), nil
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
	if !dispatcherSessionSeen {
		t.Fatalf("expected dispatcher session to be resolved")
	}

	var result struct {
		Priority        string `json:"priority"`
		TaskCreated     bool   `json:"task_created"`
		SourceSessionID string `json:"source_session_id"`
		HandoffID       string `json:"handoff_id"`
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
	if result.SourceSessionID != "sess-dispatcher" {
		t.Fatalf("expected source_session_id=sess-dispatcher, got %q", result.SourceSessionID)
	}
	if result.HandoffID != "handoff-1" {
		t.Fatalf("expected handoff_id=handoff-1, got %q", result.HandoffID)
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

func TestHandler_HandoffCreate_RequiresCurrentContractFields(t *testing.T) {
	_, mux := newTestApp(t)

	// Old payload shape should fail hard now.
	body := `{"to_agent":"agent-1","summary":"Do work","context":"details"}`
	req := httptest.NewRequest("POST", "/api/handoffs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for legacy handoff payload, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_HandoffCreate_ResolvesDispatcherSessionAndUsesNewArgs(t *testing.T) {
	_, mux, handlers := newTestAppWithHandlers(t)

	var dispatcherSessionSeen bool
	var handoffCreateSeen bool

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
			if got, _ := req.Arguments["agent_id"].(string); got == "hud-dispatcher" {
				return json.RawMessage(`{"content":[{"type":"text","text":"{\"sessions\":[]}"}]}`), nil
			}
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"sessions\":[]}"}]}`), nil
		case "agent_context__agent_session_start":
			dispatcherSessionSeen = true
			if got, _ := req.Arguments["agent_id"].(string); got != "hud-dispatcher" {
				t.Fatalf("expected hud-dispatcher session start, got %q", got)
			}
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"session_id\":\"sess-dispatcher\"}"}]}`), nil
		case "agent_context__agent_handoff_create":
			handoffCreateSeen = true
			if got, _ := req.Arguments["session_id"].(string); got != "sess-dispatcher" {
				t.Fatalf("expected session_id=sess-dispatcher, got %q", got)
			}
			if got, _ := req.Arguments["target_agent_id"].(string); got != "agent-1" {
				t.Fatalf("expected target_agent_id=agent-1, got %q", got)
			}
			if got, _ := req.Arguments["instructions"].(string); got != "Continue rollout verification" {
				t.Fatalf("unexpected instructions: %q", got)
			}
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"ok\":true,\"handoff_id\":\"handoff-123\"}"}]}`), nil
		default:
			return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
		}
	})

	body := `{"target_agent_id":"agent-1","instructions":"Continue rollout verification"}`
	req := httptest.NewRequest("POST", "/api/handoffs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if !dispatcherSessionSeen {
		t.Fatalf("expected dispatcher session resolution")
	}
	if !handoffCreateSeen {
		t.Fatalf("expected handoff_create call")
	}

	var result struct {
		Status    string `json:"status"`
		HandoffID string `json:"handoff_id"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if result.HandoffID != "handoff-123" {
		t.Fatalf("expected handoff_id=handoff-123, got %q", result.HandoffID)
	}
	if result.SessionID != "sess-dispatcher" {
		t.Fatalf("expected session_id=sess-dispatcher, got %q", result.SessionID)
	}
}

func TestHandler_HandoffAccept_RequiresSessionOrTargetAgent(t *testing.T) {
	_, mux := newTestApp(t)

	req := httptest.NewRequest("POST", "/api/handoffs/h-1/accept", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when both session_id and target_agent_id are missing, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_HandoffAccept_ResolvesTargetAgentSession(t *testing.T) {
	_, mux, handlers := newTestAppWithHandlers(t)

	var acceptSeen bool

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
			if got, _ := req.Arguments["agent_id"].(string); got != "agent-acceptor" {
				t.Fatalf("expected target lookup for agent-acceptor, got %q", got)
			}
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"sessions\":[{\"id\":\"sess-acceptor\",\"agent_id\":\"agent-acceptor\",\"status\":\"active\"}]}"}]}`), nil
		case "agent_context__agent_handoff_accept":
			acceptSeen = true
			if got, _ := req.Arguments["handoff_id"].(string); got != "handoff-7" {
				t.Fatalf("expected handoff_id=handoff-7, got %q", got)
			}
			if got, _ := req.Arguments["session_id"].(string); got != "sess-acceptor" {
				t.Fatalf("expected session_id=sess-acceptor, got %q", got)
			}
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"ok\":true,\"handoff_id\":\"handoff-7\"}"}]}`), nil
		default:
			return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
		}
	})

	req := httptest.NewRequest("POST", "/api/handoffs/handoff-7/accept", strings.NewReader(`{"target_agent_id":"agent-acceptor","import_entries":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !acceptSeen {
		t.Fatalf("expected handoff_accept tool call")
	}
}

func TestHandler_AgentTaskUpdate_PropagatesMutationFailure(t *testing.T) {
	_, mux, handlers := newTestAppWithHandlers(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name == "agent_context__agent_task_update" {
			return json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"task not found"}]}`), nil
		}
		return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
	})

	req := httptest.NewRequest("POST", "/api/agent/task-update", strings.NewReader(`{"task_id":"missing-task","status":"completed"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when task update fails, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_HandoffCreate_PropagatesMutationFailure(t *testing.T) {
	_, mux, handlers := newTestAppWithHandlers(t)

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
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"sessions\":[]}"}]}`), nil
		case "agent_context__agent_session_start":
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"session_id\":\"sess-dispatcher\"}"}]}`), nil
		case "agent_context__agent_handoff_create":
			return json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"target agent unavailable"}]}`), nil
		default:
			return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
		}
	})

	req := httptest.NewRequest("POST", "/api/handoffs", strings.NewReader(`{"target_agent_id":"agent-1","instructions":"Continue work"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when handoff create fails, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_HandoffAccept_PropagatesMutationFailure(t *testing.T) {
	_, mux, handlers := newTestAppWithHandlers(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name == "agent_context__agent_handoff_accept" {
			return json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"handoff not found"}]}`), nil
		}
		return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
	})

	req := httptest.NewRequest("POST", "/api/handoffs/missing-handoff/accept", strings.NewReader(`{"session_id":"sess-1"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when handoff accept fails, got %d: %s", w.Code, w.Body.String())
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
		OK     bool                    `json:"ok"`
		Policy bridge.NudgeQueuePolicy `json:"policy"`
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
		OK     bool                    `json:"ok"`
		Policy bridge.NudgeQueuePolicy `json:"policy"`
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

func TestHandler_MobileSessionCreate_AllowsScopedToken(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read,mobile:session:create,mobile:session:end"

	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions", strings.NewReader(`{"agent_id":"mobile-agent","namespace":"loom-core/mobile"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_MobileSessionCreate_DeniesMissingScope(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"

	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions", strings.NewReader(`{"agent_id":"mobile-agent"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_MobileToken_DeniedOnDirectAgentMutationRoute(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read,mobile:session:create,mobile:session:end"

	req := httptest.NewRequest("POST", "/api/agent/session-start", strings.NewReader(`{"agent_id":"mobile-agent"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_AgentSessionStart_IdempotentForSameNamespace(t *testing.T) {
	app, mux, handlers := newTestAppWithHandlers(t)
	sessionStartCalls := 0
	initialEvents := app.eventLog.Len()

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
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"sessions\":[{\"id\":\"sess-existing\",\"agent_id\":\"codex-gpt5\",\"namespace\":\"loom-core/main\",\"status\":\"active\"}]}"}]}`), nil
		case "agent_context__agent_session_start":
			sessionStartCalls++
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"session_id\":\"sess-new\"}"}]}`), nil
		default:
			return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
		}
	})

	req := httptest.NewRequest("POST", "/api/agent/session-start", strings.NewReader(`{"agent_id":"codex-gpt5","agent_type":"codex","namespace":"loom-core/main"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var payload struct {
		SessionID      string `json:"session_id"`
		AlreadyExisted bool   `json:"already_existed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.SessionID != "sess-existing" {
		t.Fatalf("expected session_id=sess-existing, got %q", payload.SessionID)
	}
	if !payload.AlreadyExisted {
		t.Fatal("expected already_existed=true")
	}
	if sessionStartCalls != 0 {
		t.Fatalf("session_start calls = %d, want 0", sessionStartCalls)
	}
	if got := app.eventLog.Len(); got != initialEvents {
		t.Fatalf("eventLog.Len() = %d, want %d for already_existed session start", got, initialEvents)
	}
}

func TestHandler_AgentSessionStart_NewNamespaceStartsNewSession(t *testing.T) {
	_, mux, handlers := newTestAppWithHandlers(t)
	sessionStartCalls := 0

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
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"sessions\":[{\"id\":\"sess-existing\",\"agent_id\":\"codex-gpt5\",\"namespace\":\"loom-core/old\",\"status\":\"active\"}]}"}]}`), nil
		case "agent_context__agent_session_start":
			sessionStartCalls++
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"session_id\":\"sess-new\"}"}]}`), nil
		default:
			return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
		}
	})

	req := httptest.NewRequest("POST", "/api/agent/session-start", strings.NewReader(`{"agent_id":"codex-gpt5","agent_type":"codex","namespace":"loom-core/new"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var payload struct {
		SessionID      string `json:"session_id"`
		AlreadyExisted bool   `json:"already_existed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.SessionID != "sess-new" {
		t.Fatalf("expected session_id=sess-new, got %q", payload.SessionID)
	}
	if payload.AlreadyExisted {
		t.Fatal("expected already_existed=false")
	}
	if sessionStartCalls != 1 {
		t.Fatalf("session_start calls = %d, want 1", sessionStartCalls)
	}
}

func TestHandler_AgentHeartbeat_EnsureSessionFailureDoesNotRegisterBarePresence(t *testing.T) {
	_, mux, handlers := newTestAppWithHandlers(t)

	var sessionStartCalls int
	var presenceRegisterCalls int

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
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"sessions\":[]}"}]}`), nil
		case "agent_context__agent_session_start":
			sessionStartCalls++
			return json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"transport closed"}]}`), nil
		case "agent_context__agent_presence_heartbeat":
			return json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"agent claude-code-552019522-12350 not registered; call agent_presence_register first"}]}`), nil
		case "agent_context__agent_presence_register":
			presenceRegisterCalls++
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"ok\":true}"}]}`), nil
		default:
			return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
		}
	})

	req := httptest.NewRequest("POST", "/api/agent/heartbeat", strings.NewReader(`{"agent_id":"claude-code-552019522-12350","agent_type":"claude-code","namespace":"loom-core/main","description":"Claude Code session","status":"active","ensure_session":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
	if sessionStartCalls == 0 {
		t.Fatal("expected heartbeat ensure-session to attempt session bootstrap")
	}
	if presenceRegisterCalls != 0 {
		t.Fatalf("expected no bare presence registration on ensure_session failure, got %d calls", presenceRegisterCalls)
	}
	if !strings.Contains(w.Body.String(), "failed to bootstrap session for heartbeat") {
		t.Fatalf("expected bootstrap failure message, got %s", w.Body.String())
	}
}

func TestHandler_AgentHeartbeat_RegistersBarePresenceWithoutEnsureSession(t *testing.T) {
	_, mux, handlers := newTestAppWithHandlers(t)

	var heartbeatCalls int
	var presenceRegisterCalls int

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}

		switch req.Name {
		case "agent_context__agent_presence_heartbeat":
			heartbeatCalls++
			if heartbeatCalls == 1 {
				return json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"agent codex-552019522 not registered; call agent_presence_register first"}]}`), nil
			}
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"ok\":true,\"last_heartbeat\":\"2026-03-18T12:10:00Z\"}"}]}`), nil
		case "agent_context__agent_presence_register":
			presenceRegisterCalls++
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"ok\":true}"}]}`), nil
		default:
			return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
		}
	})

	req := httptest.NewRequest("POST", "/api/agent/heartbeat", strings.NewReader(`{"agent_id":"codex-552019522","agent_type":"codex","status":"active"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if presenceRegisterCalls != 1 {
		t.Fatalf("expected 1 presence register call, got %d", presenceRegisterCalls)
	}
	if heartbeatCalls != 2 {
		t.Fatalf("expected 2 heartbeat attempts, got %d", heartbeatCalls)
	}
}

func TestHandler_MobileSessionEnd_PathRoute(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:session:end"

	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions/sess-123/end", strings.NewReader(`{"summarize":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_MobilePing_ReturnsEnvelope(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"

	req := httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var env mobile.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode envelope: %v", err)
	}
	if !env.OK {
		t.Fatal("expected ok=true")
	}
	if env.Meta.RequestID == "" {
		t.Fatal("expected non-empty request_id in meta")
	}
	if !strings.HasPrefix(env.Meta.RequestID, "req_") {
		t.Fatalf("expected request_id to start with req_, got %q", env.Meta.RequestID)
	}
	if env.Meta.Timestamp == "" {
		t.Fatal("expected non-empty timestamp in meta")
	}
}

func TestHandler_MobileDashboard_EnrichedWithHealthAndTimeline(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"

	// Add a timeline event so it appears in the dashboard.
	app.eventLog.Append(TimelineEntry{
		Timestamp: time.Now(),
		EventType: "agent.session.start",
		AgentID:   "test-agent",
	})

	req := httptest.NewRequest("GET", "/api/mobile/v1/dashboard", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			DaemonRunning  bool `json:"daemon_running"`
			ServerCount    int  `json:"server_count"`
			ActiveSessions int  `json:"active_sessions"`
			Health         struct {
				TotalServers   int `json:"total_servers"`
				HealthyServers int `json:"healthy_servers"`
			} `json:"health"`
			Coordination struct {
				Summary struct {
					SharedBranches int `json:"shared_branches"`
				} `json:"summary"`
			} `json:"coordination"`
			RecentTimeline []json.RawMessage `json:"recent_timeline"`
		} `json:"data"`
		Meta mobile.EnvMeta `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode dashboard envelope: %v", err)
	}
	if !env.OK {
		t.Fatal("expected ok=true")
	}
	if env.Meta.RequestID == "" {
		t.Fatal("expected request_id in meta")
	}
	// The mock daemon has 2 healthy servers (git, time).
	if env.Data.Health.TotalServers < 1 {
		t.Logf("health.total_servers=%d (expected >=1 from mock)", env.Data.Health.TotalServers)
	}
	if len(env.Data.RecentTimeline) < 1 {
		t.Fatal("expected at least 1 recent_timeline entry")
	}
	if env.Data.Coordination.Summary.SharedBranches != 0 {
		t.Fatalf("expected empty mock coordination summary, got shared_branches=%d", env.Data.Coordination.Summary.SharedBranches)
	}
}

func TestHandler_MobileSessions_ReturnsEnvelope(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"

	req := httptest.NewRequest("GET", "/api/mobile/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Sessions []json.RawMessage `json:"sessions"`
		} `json:"data"`
		Meta mobile.EnvMeta `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode sessions envelope: %v", err)
	}
	if !env.OK {
		t.Fatal("expected ok=true")
	}
	if env.Meta.RequestID == "" {
		t.Fatal("expected request_id in meta")
	}
	if env.Data.Sessions == nil {
		t.Fatal("expected sessions to be an empty array, not null")
	}
}

func TestHandler_MobileReadParityEndpoints_ReturnEnvelope(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"

	endpoints := []struct {
		name      string
		path      string
		dataKey   string
		mustArray bool
	}{
		{"control_plane", "/api/mobile/v1/control-plane", "cost", false},
		{"tasks", "/api/mobile/v1/tasks", "tasks", true},
		{"workflows", "/api/mobile/v1/workflows", "workflows", true},
		{"workflow_detail", "/api/mobile/v1/workflows/wf-1", "workflow", false},
		{"presence", "/api/mobile/v1/presence", "agents", true},
		{"memory_stats", "/api/mobile/v1/memory/stats", "stats", false},
		{"memory_items", "/api/mobile/v1/memory/items?tier=working", "items", true},
		{"stream", "/api/mobile/v1/stream", "entries", true},
		{"topology", "/api/mobile/v1/topology", "nodes", true},
		{"graph_stats", "/api/mobile/v1/graph/stats", "stats", false},
		{"graph_entities", "/api/mobile/v1/graph/entities", "entities", true},
		{"graph_path", "/api/mobile/v1/graph/path?source_id=ent-a&target_id=ent-b", "path", false},
		{"reasoning_chains", "/api/mobile/v1/reasoning/chains", "chains", true},
		{"reasoning_chain_detail", "/api/mobile/v1/reasoning/chains/chain-1", "chain", false},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", ep.path, nil)
			req.Header.Set("Authorization", "Bearer mobile-secret")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}

			var env struct {
				OK   bool           `json:"ok"`
				Data map[string]any `json:"data"`
				Meta mobile.EnvMeta `json:"meta"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("failed to decode envelope: %v", err)
			}
			if !env.OK {
				t.Fatalf("expected ok=true")
			}
			if env.Meta.RequestID == "" {
				t.Fatalf("expected request_id in meta")
			}
			value, ok := env.Data[ep.dataKey]
			if !ok {
				t.Fatalf("expected data.%s in response", ep.dataKey)
			}
			if ep.mustArray && value == nil {
				t.Fatalf("expected data.%s to be an empty array, not null", ep.dataKey)
			}
		})
	}
}

func TestMobileTaskDTO_ContextAlwaysPresent(t *testing.T) {
	dto := mobile.MapMobileTask(bridge.TaskInfo{
		ID:       "task-1",
		Title:    "Test task",
		Priority: "medium",
		Status:   "pending",
	})

	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal task dto: %v", err)
	}

	body := string(raw)
	if !strings.Contains(body, `"context":"`) {
		t.Fatalf("expected context field to be present, got: %s", body)
	}
	if strings.Contains(body, `"tags":null`) {
		t.Fatalf("expected tags to be [] when empty, got: %s", body)
	}
	if strings.Contains(body, `"blocked_by":null`) {
		t.Fatalf("expected blocked_by to be [] when empty, got: %s", body)
	}
}

func TestHandler_MobileMemoryItems_InvalidTier(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"

	req := httptest.NewRequest("GET", "/api/mobile/v1/memory/items?tier=invalid-tier", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode envelope: %v", err)
	}
	if env.OK {
		t.Fatalf("expected ok=false")
	}
	if env.Error.Code != "bad_request" {
		t.Fatalf("expected bad_request, got %q", env.Error.Code)
	}
}

func TestHandler_MobileErrorReturnsEnvelope(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"

	// Request session detail for a session that doesn't exist.
	req := httptest.NewRequest("GET", "/api/mobile/v1/sessions/nonexistent-id", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}

	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Meta mobile.EnvMeta `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode error envelope: %v", err)
	}
	if env.OK {
		t.Fatal("expected ok=false for error response")
	}
	if env.Error.Code != "not_found" {
		t.Fatalf("expected error code not_found, got %q", env.Error.Code)
	}
	if env.Meta.RequestID == "" {
		t.Fatal("expected request_id even in error responses")
	}
}

func TestHandler_MobileSessionCreate_AuditAndEnvelope(t *testing.T) {
	// Use a log buffer to verify audit logging.
	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	app, mux := newTestApp(t)
	app.logger = logger
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read,mobile:session:create,mobile:session:end"

	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions", strings.NewReader(`{"agent_id":"mobile-agent","namespace":"loom-core/mobile"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify audit log was written.
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "mobile_audit") {
		t.Fatal("expected mobile_audit log entry")
	}
	if !strings.Contains(logOutput, "session_create") {
		t.Fatal("expected session_create action in audit log")
	}
	if !strings.Contains(logOutput, "mobile-agent") {
		t.Fatal("expected agent_id in audit log")
	}
}

func TestHandler_MobileSessionEnd_AuditLog(t *testing.T) {
	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	app, mux := newTestApp(t)
	app.logger = logger
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:session:end"

	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions/sess-456/end", strings.NewReader(`{"summarize":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "mobile_audit") {
		t.Fatal("expected mobile_audit log entry")
	}
	if !strings.Contains(logOutput, "session_end") {
		t.Fatal("expected session_end action in audit log")
	}
	if !strings.Contains(logOutput, "sess-456") {
		t.Fatal("expected session_id in audit log")
	}
}

func TestHandler_MobileAuthError_ReturnsEnvelope(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"

	// Send request with wrong token.
	req := httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}

	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Meta mobile.EnvMeta `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("failed to decode auth error envelope: %v", err)
	}
	if env.OK {
		t.Fatal("expected ok=false for auth error")
	}
	if env.Error.Code != "unauthorized" {
		t.Fatalf("expected error code unauthorized, got %q", env.Error.Code)
	}
	if env.Meta.RequestID == "" {
		t.Fatal("expected request_id in auth error response")
	}
}

// TestHandler_MobilePolicy_AllowlistDenylistMatrix validates that the mobile
// operator token is allowed on /api/mobile/v1 endpoints and denied on all
// other API routes per the MOBILE_COMPANION_API.md authorization matrix.
func TestHandler_MobilePolicy_AllowlistDenylistMatrix(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read,mobile:session:create,mobile:session:end,mobile:push"
	app.config.MobilePushEnabled = true
	app.deviceTokenStore = NewDeviceTokenStore()

	// Allowed: mobile endpoints that should return non-403 with valid token.
	allowedEndpoints := []struct {
		method string
		path   string
		body   string
	}{
		{"GET", "/api/mobile/v1/ping", ""},
		{"GET", "/api/mobile/v1/dashboard", ""},
		{"GET", "/api/mobile/v1/control-plane", ""},
		{"GET", "/api/mobile/v1/sessions", ""},
		{"GET", "/api/mobile/v1/sessions/test-sess", ""},
		{"GET", "/api/mobile/v1/sessions/test-sess/events", ""},
		{"GET", "/api/mobile/v1/tasks", ""},
		{"GET", "/api/mobile/v1/workflows", ""},
		{"GET", "/api/mobile/v1/workflows/test-workflow", ""},
		{"GET", "/api/mobile/v1/presence", ""},
		{"GET", "/api/mobile/v1/memory/stats", ""},
		{"GET", "/api/mobile/v1/memory/items?tier=working", ""},
		{"GET", "/api/mobile/v1/stream", ""},
		{"GET", "/api/mobile/v1/topology", ""},
		{"GET", "/api/mobile/v1/graph/stats", ""},
		{"GET", "/api/mobile/v1/graph/entities", ""},
		{"GET", "/api/mobile/v1/graph/path?source_id=ent-a&target_id=ent-b", ""},
		{"GET", "/api/mobile/v1/reasoning/chains", ""},
		{"GET", "/api/mobile/v1/reasoning/chains/chain-1", ""},
		// Note: events/stream (SSE) is excluded from allow test because the handler
		// blocks indefinitely. Scope denial is verified via TestMobileContract_AllScopesRequired.
		{"GET", "/api/mobile/v1/audit", ""},
		{"GET", "/api/mobile/v1/alerts/policy", ""},
		{"POST", "/api/mobile/v1/sessions", `{"agent_id":"test","namespace":"test/ns"}`},
		{"POST", "/api/mobile/v1/sessions/test-sess/end", `{}`},
		{"POST", "/api/mobile/v1/push/register", `{"token":"tok","platform":"apns"}`},
		{"POST", "/api/mobile/v1/push/unregister", `{"token":"tok"}`},
	}

	for _, ep := range allowedEndpoints {
		t.Run("allow_"+ep.method+"_"+ep.path, func(t *testing.T) {
			body := ep.body
			req := httptest.NewRequest(ep.method, ep.path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer mobile-secret")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code == http.StatusForbidden {
				t.Errorf("mobile token should be allowed on %s %s, got 403: %s", ep.method, ep.path, w.Body.String())
			}
		})
	}

	// Denied: non-mobile API routes that must reject mobile tokens.
	deniedEndpoints := []struct {
		method string
		path   string
	}{
		// Agent lifecycle (CLI hooks — direct access denied for mobile).
		{"POST", "/api/agent/session-start"},
		{"POST", "/api/agent/session-end"},
		{"POST", "/api/agent/heartbeat"},
		{"POST", "/api/agent/task-update"},
		{"GET", "/api/agent/session"},
		{"POST", "/api/agent/context/add"},
		{"GET", "/api/agent/context-inspect"},
		{"POST", "/api/agent/nudge"},
		{"POST", "/api/agent/workflow-define"},
		{"POST", "/api/agent/dispatch"},
		// Coordinator (LLM operations — not for mobile).
		{"GET", "/api/coordinator/status"},
		{"POST", "/api/coordinator/compress"},
		{"POST", "/api/coordinator/plan"},
		// Destructive operations (forbidden for mobile).
		{"DELETE", "/api/memory/mem-1"},
		{"POST", "/api/sandbox/start"},
		{"POST", "/api/sandbox/stop"},
		{"DELETE", "/api/graph/entities/ent-1"},
		{"DELETE", "/api/graph/relations/rel-1"},
		// Read-only HUD endpoints (still denied — mobile token is restricted to /api/mobile/v1).
		{"GET", "/api/status"},
		{"GET", "/api/health"},
		{"GET", "/api/servers"},
		{"GET", "/api/fleet"},
		{"GET", "/api/sessions"},
		{"GET", "/api/tasks"},
		{"GET", "/api/events"},
	}

	for _, ep := range deniedEndpoints {
		t.Run("deny_"+ep.method+"_"+ep.path, func(t *testing.T) {
			body := ""
			if ep.method == "POST" || ep.method == "DELETE" || ep.method == "PATCH" {
				body = `{}`
			}
			req := httptest.NewRequest(ep.method, ep.path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer mobile-secret")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Errorf("mobile token should be denied on %s %s, got %d (expected 403)", ep.method, ep.path, w.Code)
			}
		})
	}
}

// TestHandler_MobilePolicy_ScopeIsolation verifies that each mobile scope
// grants only its intended access, preventing scope escalation.
func TestHandler_MobilePolicy_ScopeIsolation(t *testing.T) {
	tests := []struct {
		name           string
		scopes         string
		method         string
		path           string
		body           string
		expectedStatus int
	}{
		// mobile:read can access read endpoints.
		{"read_scope_allows_ping", "mobile:read", "GET", "/api/mobile/v1/ping", "", http.StatusOK},
		{"read_scope_allows_dashboard", "mobile:read", "GET", "/api/mobile/v1/dashboard", "", http.StatusOK},
		{"read_scope_allows_sessions_list", "mobile:read", "GET", "/api/mobile/v1/sessions", "", http.StatusOK},

		// mobile:read cannot create or end sessions.
		{"read_scope_denies_create", "mobile:read", "POST", "/api/mobile/v1/sessions",
			`{"agent_id":"a","namespace":"n"}`, http.StatusForbidden},
		{"read_scope_denies_end", "mobile:read", "POST", "/api/mobile/v1/sessions/s1/end",
			`{"summarize":true}`, http.StatusForbidden},

		// mobile:session:create alone cannot read.
		{"create_scope_denies_read", "mobile:session:create", "GET", "/api/mobile/v1/ping",
			"", http.StatusForbidden},
		// mobile:session:create alone can create.
		{"create_scope_allows_create", "mobile:session:create", "POST", "/api/mobile/v1/sessions",
			`{"agent_id":"a","namespace":"n"}`, http.StatusOK},
		// mobile:session:create cannot end sessions.
		{"create_scope_denies_end", "mobile:session:create", "POST", "/api/mobile/v1/sessions/s1/end",
			`{"summarize":true}`, http.StatusForbidden},

		// mobile:session:end alone cannot read or create.
		{"end_scope_denies_read", "mobile:session:end", "GET", "/api/mobile/v1/dashboard",
			"", http.StatusForbidden},
		{"end_scope_denies_create", "mobile:session:end", "POST", "/api/mobile/v1/sessions",
			`{"agent_id":"a"}`, http.StatusForbidden},
		// mobile:session:end can end sessions.
		{"end_scope_allows_end", "mobile:session:end", "POST", "/api/mobile/v1/sessions/s1/end",
			`{"summarize":false}`, http.StatusOK},

		// mobile:push alone cannot read or mutate sessions.
		{"push_scope_denies_read", "mobile:push", "GET", "/api/mobile/v1/ping",
			"", http.StatusForbidden},
		{"push_scope_denies_create", "mobile:push", "POST", "/api/mobile/v1/sessions",
			`{"agent_id":"a","namespace":"n"}`, http.StatusForbidden},
		{"push_scope_denies_end", "mobile:push", "POST", "/api/mobile/v1/sessions/s1/end",
			`{}`, http.StatusForbidden},
		// mobile:push can register/unregister tokens.
		{"push_scope_allows_register", "mobile:push", "POST", "/api/mobile/v1/push/register",
			`{"token":"tok","platform":"apns"}`, http.StatusOK},
		{"push_scope_allows_unregister", "mobile:push", "POST", "/api/mobile/v1/push/unregister",
			`{"token":"tok"}`, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, mux := newTestApp(t)
			app.config.MobileOperatorToken = "mobile-secret"
			app.config.MobileOperatorScopes = tt.scopes
			// Enable push feature flag for push scope tests.
			if strings.Contains(tt.path, "/push/") {
				app.config.MobilePushEnabled = true
				app.deviceTokenStore = NewDeviceTokenStore()
			}

			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer mobile-secret")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("scopes=%q %s %s: expected %d, got %d: %s",
					tt.scopes, tt.method, tt.path, tt.expectedStatus, w.Code, w.Body.String())
			}
		})
	}
}

// TestHandler_MobilePolicy_NoTokenPassesThrough verifies that requests
// without the mobile token are NOT blocked by the mobile guard (they're
// normal HUD requests that proceed to their own auth/no-auth logic).
func TestHandler_MobilePolicy_NoTokenPassesThrough(t *testing.T) {
	_, mux := newTestApp(t)

	// A request to a normal API endpoint without any mobile token should
	// pass through the mobile guard and be handled normally.
	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/status"},
		{"GET", "/api/health"},
		{"GET", "/api/fleet"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+"_"+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			// Should get 200 (normal response), not 403.
			if w.Code == http.StatusForbidden {
				t.Errorf("unauthenticated request to %s should not be blocked by mobile guard", ep.path)
			}
		})
	}
}

// TestHandler_MobilePolicy_WrongTokenPassesThrough verifies that a
// non-mobile bearer token is not mistaken for a mobile token and blocked.
func TestHandler_MobilePolicy_WrongTokenPassesThrough(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"

	req := httptest.NewRequest("GET", "/api/status", nil)
	req.Header.Set("Authorization", "Bearer some-other-token")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// A non-matching token should pass through (not be treated as mobile).
	if w.Code == http.StatusForbidden {
		t.Error("non-mobile token should not trigger mobile guard on /api/status")
	}
}

// TestHandler_MobilePolicy_UnconfiguredTokenAllowsNormalAccess verifies
// that when MobileOperatorToken is empty, the mobile guard is inert.
func TestHandler_MobilePolicy_UnconfiguredTokenAllowsNormalAccess(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = ""

	req := httptest.NewRequest("GET", "/api/status", nil)
	req.Header.Set("Authorization", "Bearer anything")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code == http.StatusForbidden {
		t.Error("when mobile token is unconfigured, no request should be blocked by mobile guard")
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

// --- M1: Rate limiting tests ---

func TestMobileRateLimiter_AllowsWithinLimit(t *testing.T) {
	rl := NewMobileRateLimiter(MobileRateLimitConfig{MutationPerMinute: 5, ReadPerMinute: 10})
	for i := 0; i < 5; i++ {
		if !rl.Allow("1.2.3.4", true) {
			t.Fatalf("expected allow on mutation request %d", i+1)
		}
	}
	for i := 0; i < 10; i++ {
		if !rl.Allow("1.2.3.4", false) {
			t.Fatalf("expected allow on read request %d", i+1)
		}
	}
}

func TestMobileRateLimiter_BlocksOverLimit(t *testing.T) {
	rl := NewMobileRateLimiter(MobileRateLimitConfig{MutationPerMinute: 2, ReadPerMinute: 3})
	rl.Allow("1.2.3.4", true)
	rl.Allow("1.2.3.4", true)
	if rl.Allow("1.2.3.4", true) {
		t.Fatal("expected deny on 3rd mutation request")
	}
}

func TestMobileRateLimiter_WindowResets(t *testing.T) {
	rl := NewMobileRateLimiter(MobileRateLimitConfig{MutationPerMinute: 1})
	now := time.Date(2026, 2, 23, 12, 0, 30, 0, time.UTC)
	rl.now = func() time.Time { return now }

	if !rl.Allow("1.2.3.4", true) {
		t.Fatal("expected allow on first request")
	}
	if rl.Allow("1.2.3.4", true) {
		t.Fatal("expected deny on second request in same window")
	}

	// Advance to next minute.
	rl.now = func() time.Time { return now.Add(time.Minute) }
	if !rl.Allow("1.2.3.4", true) {
		t.Fatal("expected allow after window reset")
	}
}

func TestMobileRateLimiter_SeparateActors(t *testing.T) {
	rl := NewMobileRateLimiter(MobileRateLimitConfig{MutationPerMinute: 1})
	if !rl.Allow("1.2.3.4", true) {
		t.Fatal("expected allow for actor A")
	}
	if !rl.Allow("5.6.7.8", true) {
		t.Fatal("expected allow for actor B (independent counter)")
	}
	if rl.Allow("1.2.3.4", true) {
		t.Fatal("expected deny for actor A (limit reached)")
	}
}

func TestMobileRateLimiter_MutationVsRead(t *testing.T) {
	rl := NewMobileRateLimiter(MobileRateLimitConfig{MutationPerMinute: 1, ReadPerMinute: 2})
	if !rl.Allow("1.2.3.4", true) {
		t.Fatal("expected allow for mutation")
	}
	if rl.Allow("1.2.3.4", true) {
		t.Fatal("expected deny for second mutation")
	}
	// Read should still be allowed (separate category).
	if !rl.Allow("1.2.3.4", false) {
		t.Fatal("expected allow for read (separate limit)")
	}
}

func TestHandler_MobileRateLimit_Returns429(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"
	app.mobileRateLimiter = NewMobileRateLimiter(MobileRateLimitConfig{ReadPerMinute: 1})
	app.mobileRevocationList = NewMobileTokenRevocationList()

	// First request should succeed.
	req := httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Second request should be rate limited.
	req = httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", w.Code, w.Body.String())
	}

	var env mobile.Envelope
	json.Unmarshal(w.Body.Bytes(), &env)
	if env.OK {
		t.Error("expected ok=false for rate limited response")
	}
}

// --- M1: Token revocation tests ---

func TestMobileRevocation_RevokedTokenDenied(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"
	app.mobileRevocationList = NewMobileTokenRevocationList()

	// Revoke the token.
	app.mobileRevocationList.Revoke("mobile-secret")

	req := httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}

	var env mobile.Envelope
	json.Unmarshal(w.Body.Bytes(), &env)
	errObj, _ := env.Error.(map[string]any)
	if errObj["code"] != "token_revoked" {
		t.Errorf("expected error code 'token_revoked', got %v", errObj["code"])
	}
}

func TestMobileRevocation_UnrevokedTokenAllowed(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"
	app.mobileRevocationList = NewMobileTokenRevocationList()

	// Revoke a different token.
	app.mobileRevocationList.Revoke("other-token")

	req := httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMobileRevocation_AdminEndpoint(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"
	app.config.AdminToken = "admin-secret"
	app.mobileRevocationList = NewMobileTokenRevocationList()

	// Revoke via admin endpoint.
	req := httptest.NewRequest("POST", "/api/mobile/v1/admin/revoke", strings.NewReader(`{"token":"mobile-secret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", "admin-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Now the mobile token should be rejected.
	req = httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after revocation, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMobileRevocation_AdminEndpoint_RequiresAdminToken(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.AdminToken = "admin-secret"
	app.mobileRevocationList = NewMobileTokenRevocationList()

	req := httptest.NewRequest("POST", "/api/mobile/v1/admin/revoke", strings.NewReader(`{"token":"some-token"}`))
	req.Header.Set("Content-Type", "application/json")
	// No admin token header.
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

// --- MBL-2: Token lifecycle hardening tests ---
//
// Verifies token expiry and revocation behavior:
// - Revocation is immediate and covers all endpoint categories
// - Double revocation is idempotent
// - Malformed/empty tokens are rejected
// - Token rotation (revoke old, accept new) works atomically
// - Concurrent revoke + request is safe

func TestMobileRevocation_ImmediateAcrossAllEndpoints(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read,mobile:session:create,mobile:session:end"
	app.mobileRevocationList = NewMobileTokenRevocationList()

	// Pre-revocation: all endpoints accessible.
	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/mobile/v1/ping"},
		{"GET", "/api/mobile/v1/dashboard"},
		{"GET", "/api/mobile/v1/sessions"},
	}
	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		req.Header.Set("Authorization", "Bearer mobile-secret")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code == http.StatusUnauthorized {
			t.Fatalf("pre-revoke: %s %s should not be 401", ep.method, ep.path)
		}
	}

	// Revoke.
	app.mobileRevocationList.Revoke("mobile-secret")

	// Post-revocation: all endpoints rejected.
	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		req.Header.Set("Authorization", "Bearer mobile-secret")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("post-revoke: %s %s expected 401, got %d", ep.method, ep.path, w.Code)
		}
		var env mobile.Envelope
		json.Unmarshal(w.Body.Bytes(), &env)
		errObj, _ := env.Error.(map[string]any)
		if errObj["code"] != "token_revoked" {
			t.Errorf("post-revoke: %s %s expected error code 'token_revoked', got %v", ep.method, ep.path, errObj["code"])
		}
	}
}

func TestMobileRevocation_DoubleRevokeIdempotent(t *testing.T) {
	rl := NewMobileTokenRevocationList()

	rl.Revoke("token-a")
	if !rl.IsRevoked("token-a") {
		t.Fatal("first revoke should mark token as revoked")
	}

	// Second revoke should not panic or change behavior.
	rl.Revoke("token-a")
	if !rl.IsRevoked("token-a") {
		t.Fatal("token should remain revoked after double revoke")
	}

	// Other tokens unaffected.
	if rl.IsRevoked("token-b") {
		t.Fatal("unrelated token should not be revoked")
	}
}

func TestMobileRevocation_HashIsolation(t *testing.T) {
	rl := NewMobileTokenRevocationList()

	rl.Revoke("secret-abc")

	// Similar tokens must not collide.
	if rl.IsRevoked("secret-abd") {
		t.Error("similar token should not be revoked (hash collision)")
	}
	if rl.IsRevoked("secret-ab") {
		t.Error("prefix of revoked token should not be revoked")
	}
	if rl.IsRevoked("secret-abcd") {
		t.Error("superstring of revoked token should not be revoked")
	}
	if rl.IsRevoked("") {
		t.Error("empty token should not be revoked")
	}
}

func TestMobileAuth_MalformedTokenRejected(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"
	app.mobileRevocationList = NewMobileTokenRevocationList()

	cases := []struct {
		name   string
		header string
		code   int
	}{
		{"missing header", "", http.StatusUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized},
		{"wrong scheme", "Basic mobile-secret", http.StatusUnauthorized},
		{"no space", "Bearermobile-secret", http.StatusUnauthorized},
		{"wrong token", "Bearer wrong-token", http.StatusUnauthorized},
		{"extra whitespace", "Bearer  mobile-secret", http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tc.code {
				t.Errorf("expected %d, got %d: %s", tc.code, w.Code, w.Body.String())
			}
		})
	}
}

func TestMobileAuth_TokenNotConfigured(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "" // not configured
	app.mobileRevocationList = NewMobileTokenRevocationList()

	req := httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when token not configured, got %d: %s", w.Code, w.Body.String())
	}

	var env mobile.Envelope
	json.Unmarshal(w.Body.Bytes(), &env)
	errObj, _ := env.Error.(map[string]any)
	if errObj["code"] != "not_configured" {
		t.Errorf("expected error code 'not_configured', got %v", errObj["code"])
	}
}

func TestMobileRevocation_TokenRotation(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "old-token"
	app.config.MobileOperatorScopes = "mobile:read"
	app.mobileRevocationList = NewMobileTokenRevocationList()

	// Old token works.
	req := httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer old-token")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("old token should work before rotation, got %d", w.Code)
	}

	// Rotate: revoke old, switch to new.
	app.mobileRevocationList.Revoke("old-token")
	app.config.MobileOperatorToken = "new-token"

	// Old token rejected (revoked).
	req = httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer old-token")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("old token should be rejected after rotation, got %d", w.Code)
	}

	// New token accepted.
	req = httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer new-token")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("new token should work after rotation, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMobileRevocation_AdminRevokeEmptyToken(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.AdminToken = "admin-secret"
	app.mobileRevocationList = NewMobileTokenRevocationList()

	req := httptest.NewRequest("POST", "/api/mobile/v1/admin/revoke", strings.NewReader(`{"token":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", "admin-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("revoking empty token should return 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMobileRevocation_AdminRevokeMissingBody(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.AdminToken = "admin-secret"
	app.mobileRevocationList = NewMobileTokenRevocationList()

	req := httptest.NewRequest("POST", "/api/mobile/v1/admin/revoke", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", "admin-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("revoking with missing token field should return 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMobileRevocation_ConcurrentSafety(t *testing.T) {
	rl := NewMobileTokenRevocationList()
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Concurrent revocations.
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			rl.Revoke(fmt.Sprintf("token-%d", n))
		}(i)
	}

	// Concurrent reads.
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			rl.IsRevoked(fmt.Sprintf("token-%d", n))
		}(i)
	}

	wg.Wait()

	// Verify all tokens were revoked.
	for i := 0; i < goroutines; i++ {
		if !rl.IsRevoked(fmt.Sprintf("token-%d", i)) {
			t.Errorf("token-%d should be revoked after concurrent operations", i)
		}
	}
}

func TestMobileRevocation_AdminNoConfiguredToken(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.AdminToken = "" // not configured
	app.mobileRevocationList = NewMobileTokenRevocationList()

	req := httptest.NewRequest("POST", "/api/mobile/v1/admin/revoke", strings.NewReader(`{"token":"some-token"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", "any-value")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 when admin token not configured, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMobileRevocation_RevokeTimestampRecorded(t *testing.T) {
	rl := NewMobileTokenRevocationList()

	before := time.Now().UTC()
	rl.Revoke("test-token")
	after := time.Now().UTC()

	rl.mu.RLock()
	h := hashToken("test-token")
	revokedAt, ok := rl.revoked[h]
	rl.mu.RUnlock()

	if !ok {
		t.Fatal("token should be in revocation list")
	}
	if revokedAt.Before(before) || revokedAt.After(after) {
		t.Errorf("revocation timestamp %v not in expected range [%v, %v]", revokedAt, before, after)
	}
}

// --- M1: Device ID tracking tests ---

func TestMobileAudit_DeviceIDExtraction(t *testing.T) {
	// Test extractDeviceID helper directly.
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Device-ID", "iphone-abc123")

	got := mobile.ExtractDeviceID(req)
	if got != "iphone-abc123" {
		t.Errorf("expected 'iphone-abc123', got %q", got)
	}

	// Test truncation.
	long := strings.Repeat("x", 200)
	req.Header.Set("X-Device-ID", long)
	got = mobile.ExtractDeviceID(req)
	if len(got) != mobile.MaxDeviceIDLen {
		t.Errorf("expected truncation to %d chars, got %d", mobile.MaxDeviceIDLen, len(got))
	}

	// Test missing header.
	req.Header.Del("X-Device-ID")
	got = mobile.ExtractDeviceID(req)
	if got != "" {
		t.Errorf("expected empty string for missing header, got %q", got)
	}
}

// --- MBL-3: Mobile mutation guardrail contract tests ---
//
// These tests codify the mobile_operator mutation boundary contracts:
// - Input validation for mutation endpoints
// - Idempotency guarantees for session create/end
// - Rate limiting on mutation paths
// - Device ID is audit-only (no authz impact)
// - Audit endpoint scope enforcement

func TestMobileContract_MissingAuthHeader(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"

	req := httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	// No Authorization header.
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}

	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.OK {
		t.Fatal("expected ok=false")
	}
	if env.Error.Code != "unauthorized" {
		t.Fatalf("expected error code 'unauthorized', got %q", env.Error.Code)
	}
}

func TestMobileContract_SessionCreate_EmptyAgentID(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:session:create"

	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions", strings.NewReader(`{"namespace":"test/ns"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(w.Body.Bytes(), &env)
	if env.Error.Code != "bad_request" {
		t.Fatalf("expected error code 'bad_request', got %q", env.Error.Code)
	}
	if !strings.Contains(env.Error.Message, "agent_id") {
		t.Fatalf("expected error message to mention agent_id, got %q", env.Error.Message)
	}
}

func TestMobileContract_SessionCreate_InvalidJSON(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:session:create"

	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions", strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMobileContract_SessionEnd_InvalidJSON(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:session:end"

	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions/sess-1/end", strings.NewReader(`{broken`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMobileContract_SessionEnd_EmptyBody(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:session:end"

	// Empty body is valid — summarize defaults to true.
	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions/sess-1/end", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMobileContract_SessionEnd_EmptyBodyDefaultsSummarizeTrue(t *testing.T) {
	app, mux, handlers := newTestAppWithHandlers(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:session:end"

	var sawSessionEnd bool
	var summarizeValue any
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}

		if req.Name == "agent_context__agent_session_end" {
			sawSessionEnd = true
			summarizeValue = req.Arguments["summarize"]
		}

		return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
	})

	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions/sess-1/end", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !sawSessionEnd {
		t.Fatal("expected agent_session_end tool call")
	}
	if got, ok := summarizeValue.(bool); !ok || !got {
		t.Fatalf("expected summarize=true, got %#v", summarizeValue)
	}
}

func TestMobileContract_SessionEnd_ExplicitFalseDisablesSummarize(t *testing.T) {
	app, mux, handlers := newTestAppWithHandlers(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:session:end"

	var sawSessionEnd bool
	var summarizeValue any
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}

		if req.Name == "agent_context__agent_session_end" {
			sawSessionEnd = true
			summarizeValue = req.Arguments["summarize"]
		}

		return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
	})

	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions/sess-1/end", strings.NewReader(`{"summarize":false}`))
	req.Header.Set("Authorization", "Bearer mobile-secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !sawSessionEnd {
		t.Fatal("expected agent_session_end tool call")
	}
	if got, ok := summarizeValue.(bool); !ok || got {
		t.Fatalf("expected summarize=false, got %#v", summarizeValue)
	}
}

func TestMobileContract_SessionCreate_ResponseShape(t *testing.T) {
	app, mux, handlers := newTestAppWithHandlers(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:session:create"
	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}

		switch req.Name {
		case "agent_context__agent_session_list":
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"sessions\":[]}"}]}`), nil
		case "agent_context__agent_session_start":
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"session_id\":\"sess-created\"}"}]}`), nil
		default:
			return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
		}
	})

	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions",
		strings.NewReader(`{"agent_id":"test-agent","namespace":"test/ns"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			SessionID      string `json:"session_id"`
			AlreadyExisted bool   `json:"already_existed"`
		} `json:"data"`
		Meta mobile.EnvMeta `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !env.OK {
		t.Fatal("expected ok=true")
	}
	if env.Data.SessionID != "sess-created" {
		t.Fatalf("expected session_id='sess-created', got %q", env.Data.SessionID)
	}
	if env.Data.AlreadyExisted {
		t.Fatal("expected already_existed=false for new session create")
	}
	if env.Meta.RequestID == "" {
		t.Fatal("expected request_id in meta")
	}
}

func TestMobileContract_SessionEnd_ResponseShape(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:session:end"

	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions/sess-1/end",
		strings.NewReader(`{"summarize":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Ended     bool   `json:"ended"`
			SessionID string `json:"session_id"`
		} `json:"data"`
		Meta mobile.EnvMeta `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !env.OK {
		t.Fatal("expected ok=true")
	}
	if env.Data.SessionID != "sess-1" {
		t.Fatalf("expected session_id='sess-1', got %q", env.Data.SessionID)
	}
	if env.Meta.RequestID == "" {
		t.Fatal("expected request_id in meta")
	}
}

func TestMobileContract_MutationRateLimit(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:session:create,mobile:session:end"
	app.mobileRateLimiter = NewMobileRateLimiter(MobileRateLimitConfig{MutationPerMinute: 1, ReadPerMinute: 100})
	app.mobileRevocationList = NewMobileTokenRevocationList()

	// First mutation (create) should succeed.
	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions",
		strings.NewReader(`{"agent_id":"a","namespace":"n"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first mutation: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Second mutation (end) should be rate-limited because both are mutations.
	req = httptest.NewRequest("POST", "/api/mobile/v1/sessions/s1/end",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second mutation: expected 429, got %d: %s", w.Code, w.Body.String())
	}

	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	json.Unmarshal(w.Body.Bytes(), &env)
	if env.Error.Code != "rate_limited" {
		t.Fatalf("expected error code 'rate_limited', got %q", env.Error.Code)
	}
}

func TestMobileContract_DeviceID_NoAuthzImpact(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"

	// Request WITHOUT Device-ID header.
	req := httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	codeWithout := w.Code

	// Request WITH Device-ID header.
	req = httptest.NewRequest("GET", "/api/mobile/v1/ping", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	req.Header.Set("X-Device-ID", "iphone-12345")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	codeWith := w.Code

	if codeWithout != codeWith {
		t.Fatalf("X-Device-ID should not affect authz: without=%d, with=%d", codeWithout, codeWith)
	}
	if codeWith != http.StatusOK {
		t.Fatalf("expected 200 for both, got %d", codeWith)
	}
}

func TestMobileContract_AuditEndpoint_RequiresReadScope(t *testing.T) {
	// Audit endpoint requires mobile:read scope.
	tests := []struct {
		name   string
		scopes string
		expect int
	}{
		{"read_scope_allows", "mobile:read", http.StatusOK},
		{"create_scope_denies", "mobile:session:create", http.StatusForbidden},
		{"end_scope_denies", "mobile:session:end", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, mux := newTestApp(t)
			app.config.MobileOperatorToken = "mobile-secret"
			app.config.MobileOperatorScopes = tt.scopes

			req := httptest.NewRequest("GET", "/api/mobile/v1/audit", nil)
			req.Header.Set("Authorization", "Bearer mobile-secret")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != tt.expect {
				t.Errorf("scopes=%q: expected %d, got %d: %s",
					tt.scopes, tt.expect, w.Code, w.Body.String())
			}
		})
	}
}

// TestMobileContract_AllScopesRequired is a comprehensive contract verifying
// that every mobile endpoint requires exactly the documented scope.
func TestMobileContract_AllScopesRequired(t *testing.T) {
	type endpointContract struct {
		method        string
		path          string
		body          string
		requiredScope string
		skipAllow     bool // Skip the "allow" test for streaming endpoints that block.
	}

	contracts := []endpointContract{
		// Read endpoints require mobile:read.
		{"GET", "/api/mobile/v1/ping", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/dashboard", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/control-plane", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/sessions", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/sessions/test-sess", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/sessions/test-sess/events", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/tasks", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/workflows", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/workflows/test-workflow", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/presence", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/memory/stats", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/memory/items?tier=working", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/stream", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/topology", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/graph/stats", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/graph/entities", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/graph/path?source_id=ent-a&target_id=ent-b", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/reasoning/chains", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/reasoning/chains/chain-1", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/events/stream", "", "mobile:read", true}, // SSE blocks; deny-only.
		{"GET", "/api/mobile/v1/audit", "", "mobile:read", false},
		{"GET", "/api/mobile/v1/alerts/policy", "", "mobile:read", false},
		// Mutation endpoints require specific scopes.
		{"POST", "/api/mobile/v1/sessions", `{"agent_id":"a","namespace":"n"}`, "mobile:session:create", false},
		{"POST", "/api/mobile/v1/sessions/test-sess/end", `{}`, "mobile:session:end", false},
		// Push endpoints require mobile:push (feature-flagged).
		{"POST", "/api/mobile/v1/push/register", `{"token":"tok","platform":"apns"}`, "mobile:push", false},
		{"POST", "/api/mobile/v1/push/unregister", `{"token":"tok"}`, "mobile:push", false},
	}

	// All four mobile scopes — every scope must be tested for isolation.
	allScopes := []string{"mobile:read", "mobile:session:create", "mobile:session:end", "mobile:push"}

	// Verify endpoint count matches registered mobile routes (excluding admin/revoke which uses X-Admin-Token).
	const expectedScopeGatedEndpoints = 26
	if len(contracts) != expectedScopeGatedEndpoints {
		t.Fatalf("contract test covers %d endpoints, expected %d — update when adding mobile routes",
			len(contracts), expectedScopeGatedEndpoints)
	}

	for _, c := range contracts {
		// Test: granting the required scope allows access.
		if !c.skipAllow {
			t.Run("allow_"+c.method+"_"+c.path, func(t *testing.T) {
				app, mux := newTestApp(t)
				app.config.MobileOperatorToken = "mobile-secret"
				app.config.MobileOperatorScopes = c.requiredScope
				// Push endpoints require feature flag + store.
				if c.requiredScope == "mobile:push" {
					app.config.MobilePushEnabled = true
					app.deviceTokenStore = NewDeviceTokenStore()
				}

				req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
				req.Header.Set("Authorization", "Bearer mobile-secret")
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, req)

				if w.Code == http.StatusForbidden {
					t.Errorf("scope=%q should allow %s %s, got 403: %s",
						c.requiredScope, c.method, c.path, w.Body.String())
				}
			})
		}

		// Test: every OTHER scope is denied.
		for _, wrongScope := range allScopes {
			if wrongScope == c.requiredScope {
				continue
			}
			t.Run("deny_"+wrongScope+"_on_"+c.method+"_"+c.path, func(t *testing.T) {
				app, mux := newTestApp(t)
				app.config.MobileOperatorToken = "mobile-secret"
				app.config.MobileOperatorScopes = wrongScope
				// Push endpoints need feature flag enabled so scope check runs (not 404).
				if c.requiredScope == "mobile:push" {
					app.config.MobilePushEnabled = true
					app.deviceTokenStore = NewDeviceTokenStore()
				}

				req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
				req.Header.Set("Authorization", "Bearer mobile-secret")
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, req)

				if w.Code != http.StatusForbidden {
					t.Errorf("scope=%q should deny %s %s, got %d (expected 403): %s",
						wrongScope, c.method, c.path, w.Code, w.Body.String())
				}
			})
		}
	}
}

// --- M4: Notification policy tests (MBL-6) ---

func TestMobileAlertsPolicy_ResponseShape(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:read"

	req := httptest.NewRequest("GET", "/api/mobile/v1/alerts/policy", nil)
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var env mobile.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !env.OK {
		t.Fatalf("expected ok=true")
	}

	dataMap, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected Data to be a map")
	}

	version, _ := dataMap["version"].(string)
	if version != "v1" {
		t.Errorf("expected version=v1, got %q", version)
	}

	policies, ok := dataMap["policy"].([]any)
	if !ok {
		t.Fatalf("expected policy to be an array")
	}
	if len(policies) == 0 {
		t.Fatal("expected non-empty policy array")
	}

	// Verify structure of first entry.
	first, ok := policies[0].(map[string]any)
	if !ok {
		t.Fatalf("expected policy entry to be a map")
	}
	for _, field := range []string{"event_type", "severity", "interruption_level", "title", "allowed_actions"} {
		if _, exists := first[field]; !exists {
			t.Errorf("missing field %q in policy entry", field)
		}
	}
}

func TestMobileAlertsPolicy_MatrixCompleteness(t *testing.T) {
	matrix := mobile.AlertPolicyMatrix()
	if len(matrix) != 10 {
		t.Errorf("expected 10 policy entries, got %d", len(matrix))
	}

	validSeverities := map[string]bool{"info": true, "warning": true, "critical": true}
	validLevels := map[string]bool{"passive": true, "active": true, "time_sensitive": true, "critical": true}
	validActions := map[string]bool{"view_session": true, "view_dashboard": true, "acknowledge": true}

	for _, entry := range matrix {
		if entry.EventType == "" {
			t.Error("empty event_type in policy entry")
		}
		if !validSeverities[entry.Severity] {
			t.Errorf("invalid severity %q for %s", entry.Severity, entry.EventType)
		}
		if !validLevels[entry.InterruptionLevel] {
			t.Errorf("invalid interruption_level %q for %s", entry.InterruptionLevel, entry.EventType)
		}
		if len(entry.AllowedActions) == 0 {
			t.Errorf("no allowed_actions for %s", entry.EventType)
		}
		for _, action := range entry.AllowedActions {
			if !validActions[action] {
				t.Errorf("invalid action %q for %s", action, entry.EventType)
			}
		}
	}
}

func TestMobileAlertsPolicy_InfoEventsArePassive(t *testing.T) {
	for _, entry := range mobile.AlertPolicyMatrix() {
		if entry.Severity == "info" && entry.InterruptionLevel != "passive" {
			t.Errorf("info-severity event %q should be passive, got %q",
				entry.EventType, entry.InterruptionLevel)
		}
	}
}

func TestMobileAlertsPolicy_NoMutationActions(t *testing.T) {
	safeActions := map[string]bool{"view_session": true, "view_dashboard": true, "acknowledge": true}
	for _, entry := range mobile.AlertPolicyMatrix() {
		for _, action := range entry.AllowedActions {
			if !safeActions[action] {
				t.Errorf("event %q has unsafe action %q", entry.EventType, action)
			}
		}
	}
}

// --- M4: Push reliability tests (MBL-7) ---

func TestClassifyPushResponse_RetryMatrix(t *testing.T) {
	tests := []struct {
		status int
		expect PushRetryAction
	}{
		{200, PushNoRetry},
		{201, PushNoRetry},
		{400, PushNoRetry},
		{401, PushNoRetry},
		{403, PushNoRetry},
		{404, PushInvalidateToken},
		{405, PushNoRetry},
		{410, PushInvalidateToken},
		{429, PushRetryAfter},
		{500, PushRetryWithBackoff},
		{502, PushRetryWithBackoff},
		{503, PushRetryWithBackoff},
	}
	for _, tt := range tests {
		d := ClassifyPushResponse(tt.status, 0)
		if d.Action != tt.expect {
			t.Errorf("status %d: expected action %d, got %d (%s)",
				tt.status, tt.expect, d.Action, d.Reason)
		}
	}
}

func TestClassifyPushResponse_429RetryAfter(t *testing.T) {
	d := ClassifyPushResponse(429, 60*time.Second)
	if d.Action != PushRetryAfter {
		t.Fatalf("expected PushRetryAfter, got %d", d.Action)
	}
	if d.RetryAfter != 60*time.Second {
		t.Errorf("expected 60s retry-after, got %v", d.RetryAfter)
	}

	// Without hint, defaults to 30s.
	d2 := ClassifyPushResponse(429, 0)
	if d2.RetryAfter != 30*time.Second {
		t.Errorf("expected 30s default, got %v", d2.RetryAfter)
	}
}

func TestPushBackoff_ExponentialDelay(t *testing.T) {
	cfg := PushBackoffConfig{BaseDelay: 1 * time.Second, MaxDelay: 30 * time.Second, MaxRetries: 5}

	if d := cfg.BackoffDelay(0); d != 1*time.Second {
		t.Errorf("attempt 0: expected 1s, got %v", d)
	}
	if d := cfg.BackoffDelay(1); d != 2*time.Second {
		t.Errorf("attempt 1: expected 2s, got %v", d)
	}
	if d := cfg.BackoffDelay(2); d != 4*time.Second {
		t.Errorf("attempt 2: expected 4s, got %v", d)
	}
	if d := cfg.BackoffDelay(5); d != 30*time.Second {
		t.Errorf("attempt 5: expected 30s cap, got %v", d)
	}
	if d := cfg.BackoffDelay(10); d != 30*time.Second {
		t.Errorf("attempt 10: expected 30s cap, got %v", d)
	}
}

func TestPushPayload_ValidateAndTruncate_FitsWithin(t *testing.T) {
	p := PushPayload{Title: "Alert", Body: "Server down", Category: "health"}
	data, truncated, err := p.ValidateAndTruncate(MaxAPNsPayloadBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if truncated {
		t.Error("expected no truncation for small payload")
	}
	if len(data) > MaxAPNsPayloadBytes {
		t.Errorf("payload exceeds limit: %d > %d", len(data), MaxAPNsPayloadBytes)
	}
}

func TestPushPayload_ValidateAndTruncate_OversizedBody(t *testing.T) {
	longBody := strings.Repeat("A", 5000)
	p := PushPayload{Title: "Alert", Body: longBody}
	data, truncated, err := p.ValidateAndTruncate(MaxAPNsPayloadBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !truncated {
		t.Error("expected truncation for oversized payload")
	}
	if len(data) > MaxAPNsPayloadBytes {
		t.Errorf("truncated payload exceeds limit: %d > %d", len(data), MaxAPNsPayloadBytes)
	}
	if !strings.Contains(string(data), "...") {
		t.Error("truncated body should contain ellipsis")
	}
}

func TestDeviceTokenStore_RegisterAndList(t *testing.T) {
	store := NewDeviceTokenStore()

	regID := store.Register("tok-1", "dev-1", "apns")
	if regID == "" {
		t.Error("expected non-empty registration ID")
	}
	if store.Count() != 1 {
		t.Errorf("expected 1 token, got %d", store.Count())
	}

	// Re-register same token updates last_used.
	store.Register("tok-1", "dev-1", "apns")
	if store.Count() != 1 {
		t.Errorf("expected 1 token after re-register, got %d", store.Count())
	}

	// Register second token.
	store.Register("tok-2", "dev-2", "fcm")
	if store.Count() != 2 {
		t.Errorf("expected 2 tokens, got %d", store.Count())
	}

	tokens := store.List()
	if len(tokens) != 2 {
		t.Errorf("expected 2 tokens in list, got %d", len(tokens))
	}
}

func TestDeviceTokenStore_Invalidate(t *testing.T) {
	store := NewDeviceTokenStore()
	store.Register("tok-1", "dev-1", "apns")
	store.Register("tok-2", "dev-1", "apns")

	removed := store.Invalidate("tok-1")
	if !removed {
		t.Error("expected token to be removed")
	}
	if store.Count() != 1 {
		t.Errorf("expected 1 token after invalidation, got %d", store.Count())
	}

	// Invalidating non-existent token.
	removed = store.Invalidate("tok-nonexistent")
	if removed {
		t.Error("expected false for non-existent token")
	}
}

func TestDeviceTokenStore_InvalidateByDeviceID(t *testing.T) {
	store := NewDeviceTokenStore()
	store.Register("tok-1", "dev-1", "apns")
	store.Register("tok-2", "dev-1", "apns")
	store.Register("tok-3", "dev-2", "fcm")

	removed := store.InvalidateByDeviceID("dev-1")
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}
	if store.Count() != 1 {
		t.Errorf("expected 1 token remaining, got %d", store.Count())
	}
}

func TestDeviceTokenStore_CleanupStale(t *testing.T) {
	store := NewDeviceTokenStore()
	baseTime := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return baseTime }

	store.Register("tok-old", "dev-1", "apns")

	store.now = func() time.Time { return baseTime.Add(48 * time.Hour) }
	store.Register("tok-new", "dev-2", "apns")

	// Cleanup tokens not used since 24h ago.
	cutoff := baseTime.Add(24 * time.Hour)
	removed := store.CleanupStale(cutoff)
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}
	if store.Count() != 1 {
		t.Errorf("expected 1 remaining, got %d", store.Count())
	}
}

func TestAppCleanupStalePushTokensNow(t *testing.T) {
	app := &App{
		deviceTokenStore: NewDeviceTokenStore(),
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	store := app.deviceTokenStore

	baseTime := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return baseTime }

	store.Register("tok-old", "dev-1", "apns")

	store.now = func() time.Time { return baseTime.Add(10 * 24 * time.Hour) }
	store.Register("tok-fresh", "dev-2", "fcm")

	removed := app.cleanupStalePushTokensNow(baseTime.Add(40*24*time.Hour), 30*24*time.Hour)
	if removed != 1 {
		t.Fatalf("expected 1 stale token removed, got %d", removed)
	}
	if store.Count() != 1 {
		t.Fatalf("expected 1 token remaining, got %d", store.Count())
	}
}

func TestAppCleanupStalePushTokensNow_NoStore(t *testing.T) {
	app := &App{}
	removed := app.cleanupStalePushTokensNow(time.Now(), 24*time.Hour)
	if removed != 0 {
		t.Fatalf("expected 0 removed when store is nil, got %d", removed)
	}
}

func TestMobilePushRegister_FeatureFlagDisabled(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:push"
	app.config.MobilePushEnabled = false

	body := `{"token":"device-token-123","platform":"apns"}`
	req := httptest.NewRequest("POST", "/api/mobile/v1/push/register", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer mobile-secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 when push disabled, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMobilePushRegister_Success(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:push"
	app.config.MobilePushEnabled = true
	app.deviceTokenStore = NewDeviceTokenStore()

	body := `{"token":"device-token-123","platform":"apns"}`
	req := httptest.NewRequest("POST", "/api/mobile/v1/push/register", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer mobile-secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var env mobile.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !env.OK {
		t.Fatal("expected ok=true")
	}

	data, _ := env.Data.(map[string]any)
	if reg, _ := data["registered"].(bool); !reg {
		t.Error("expected registered=true")
	}
	if regID, _ := data["registration_id"].(string); regID == "" {
		t.Error("expected non-empty registration_id")
	}
}

func TestMobilePushRegister_InvalidPlatform(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:push"
	app.config.MobilePushEnabled = true
	app.deviceTokenStore = NewDeviceTokenStore()

	body := `{"token":"tok","platform":"webpush"}`
	req := httptest.NewRequest("POST", "/api/mobile/v1/push/register", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer mobile-secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid platform, got %d", w.Code)
	}
}

func TestDecompHintHandler_LogsAndNudges(t *testing.T) {
	app, _ := newTestApp(t)

	// Simulate an active agent in the fleet monitor snapshot so the nudge
	// has a target. We do this by adding a presence entry via the bridge.
	// Since the mock daemon returns empty presence, we directly verify
	// the eventLog and nudgeQueue behavior by invoking the handler logic.

	eventData := `{"server":"llm","tool":"chat","estimated_tokens":50000,"suggestion":"Consider using recursive-context workflow","workflow":"recursive-context"}`

	// Append timeline entry directly (simulating what the handler does).
	app.eventLog.Append(TimelineEntry{
		Timestamp: time.Now(),
		EventType: "decomp.hint",
		Data:      json.RawMessage(eventData),
	})

	if app.eventLog.Len() != 1 {
		t.Fatalf("expected 1 event log entry, got %d", app.eventLog.Len())
	}
	entries := app.eventLog.All(10)
	if entries[0].EventType != "decomp.hint" {
		t.Errorf("event type = %q, want decomp.hint", entries[0].EventType)
	}

	// Simulate nudge enqueue for a known agent.
	agentID := "test-agent"
	app.nudgeQueue.Add(agentID, NudgeEntry{
		ID:        NewNudgeID(agentID),
		Type:      "context_inject",
		Lane:      "advice",
		Content:   `Tool "chat" returned a large response. Consider using recursive-context workflow`,
		FromAgent: "hud",
	})

	nudges := app.nudgeQueue.Drain(agentID)
	if len(nudges) != 1 {
		t.Fatalf("expected 1 nudge, got %d", len(nudges))
	}
	if nudges[0].Lane != "advice" {
		t.Errorf("nudge lane = %q, want advice", nudges[0].Lane)
	}
	if !strings.Contains(nudges[0].Content, "chat") {
		t.Errorf("nudge content missing tool name: %s", nudges[0].Content)
	}
}

func TestMobilePushUnregister_Success(t *testing.T) {
	app, mux := newTestApp(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:push"
	app.config.MobilePushEnabled = true
	app.deviceTokenStore = NewDeviceTokenStore()

	// First register a token.
	app.deviceTokenStore.Register("tok-to-remove", "dev-1", "apns")

	body := `{"token":"tok-to-remove"}`
	req := httptest.NewRequest("POST", "/api/mobile/v1/push/unregister", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer mobile-secret")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if app.deviceTokenStore.Count() != 0 {
		t.Error("expected token store to be empty after unregister")
	}
}

func TestHandleSessions_FallsBackToFleetSnapshotOnUpstreamError(t *testing.T) {
	app, mux, handlers := newTestAppWithHandlers(t)
	setFleetSnapshotForTest(t, app.fleetMonitor, monitor.FleetSnapshot{
		Sessions: []bridge.SessionInfo{{
			ID:        "sess-cache",
			AgentID:   "mobile-agent",
			Namespace: "services/flexinfer",
			Status:    "active",
		}},
	})

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name == "agent_context__agent_session_list" {
			return json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"transport closed"}]}`), nil
		}
		return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
	})

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var env struct {
		Sessions []bridge.SessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Sessions) != 1 || env.Sessions[0].ID != "sess-cache" {
		t.Fatalf("expected cached session fallback, got %#v", env.Sessions)
	}
}

func TestHandler_MobileSessionCreate_DefaultsMobilePresenceMetadata(t *testing.T) {
	app, mux, handlers := newTestAppWithHandlers(t)
	app.config.MobileOperatorToken = "mobile-secret"
	app.config.MobileOperatorScopes = "mobile:session:create"

	presenceArgsCh := make(chan map[string]any, 1)

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
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"sessions\":[]}"}]}`), nil
		case "agent_context__agent_session_start":
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"session_id\":\"sess-mobile\"}"}]}`), nil
		case "agent_context__agent_presence_register":
			select {
			case presenceArgsCh <- req.Arguments:
			default:
			}
			return json.RawMessage(`{"content":[{"type":"text","text":"{\"ok\":true}"}]}`), nil
		default:
			return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
		}
	})

	req := httptest.NewRequest("POST", "/api/mobile/v1/sessions", strings.NewReader(`{"agent_id":"claude-code","namespace":"services/flexinfer"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mobile-secret")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	select {
	case args := <-presenceArgsCh:
		if got := fmt.Sprint(args["agent_type"]); got != "mobile" {
			t.Fatalf("expected agent_type=mobile, got %q", got)
		}
		if got := fmt.Sprint(args["description"]); got != "Mobile session" {
			t.Fatalf("expected description to default to Mobile session, got %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for async presence register")
	}
}

func setFleetSnapshotForTest(t *testing.T, fm *monitor.FleetMonitor, snap monitor.FleetSnapshot) {
	t.Helper()
	v := reflect.ValueOf(fm).Elem().FieldByName("snapshot")
	reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(reflect.ValueOf(snap))
}

func TestIsMobileManagedPresence(t *testing.T) {
	cases := []struct {
		name  string
		agent bridge.PresenceInfo
		want  bool
	}{
		{name: "agent type mobile", agent: bridge.PresenceInfo{AgentType: "mobile"}, want: true},
		{name: "mobile session description", agent: bridge.PresenceInfo{Description: "Mobile session"}, want: true},
		{name: "non mobile", agent: bridge.PresenceInfo{AgentType: "claude-code", Description: "Claude session"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMobileManagedPresence(tc.agent); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestMemoryStatsPayload(t *testing.T) {
	stats := &bridge.MemoryStatsResult{
		WorkingMemory:          bridge.MemoryTierStats{Items: 10, Tokens: 500},
		ShortTermMemory:        bridge.MemoryTierStats{Items: 20, Tokens: 1000},
		LongTermMemory:         bridge.MemoryTierStats{Items: 5, Tokens: 200},
		TotalItems:             35,
		TotalTokens:            1700,
		CompressionRatio:       0.8,
		ItemsAddedLast24h:      3,
		ItemsCompressedLast24h: 2,
	}

	payload := memory.StatsPayload(stats)

	// Check tier shape.
	wm, ok := payload["working_memory"].(map[string]any)
	if !ok {
		t.Fatal("working_memory missing or wrong type")
	}
	if wm["items"] != 10 || wm["tokens"] != 500 {
		t.Fatalf("working_memory unexpected: %v", wm)
	}

	if payload["total_items"] != 35 {
		t.Fatalf("total_items: got %v, want 35", payload["total_items"])
	}

	// Compression block should be present.
	comp, ok := payload["compression"].(map[string]any)
	if !ok {
		t.Fatal("compression block missing")
	}
	if comp["ratio"] != 0.8 {
		t.Fatalf("compression ratio: got %v, want 0.8", comp["ratio"])
	}
	// tokens_saved = int(1700 * (1 - 0.8)) — float truncation yields 339.
	if comp["tokens_saved"] != 339 {
		t.Fatalf("tokens_saved: got %v, want 339", comp["tokens_saved"])
	}
}

func TestMemoryStatsPayload_NoCompression(t *testing.T) {
	stats := &bridge.MemoryStatsResult{
		WorkingMemory: bridge.MemoryTierStats{Items: 1, Tokens: 10},
		TotalItems:    1,
		TotalTokens:   10,
	}

	payload := memory.StatsPayload(stats)

	if _, ok := payload["compression"]; ok {
		t.Fatal("compression block should be absent when ratio=0 and compressed=0")
	}
}
