package hud

import (
	"context"
	"encoding/json"
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
		config: Config{Dev: true},
		client: client,
		agent:  agent,
		cache:  bridge.NewCache(),
		sseHub: NewSSEHub(nil),
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

	return app, mux
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
