package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockDaemon creates a mock daemon that speaks newline-delimited JSON-RPC
// over a Unix socket. Returns the socket path and a mux-like handler map.
// The mock reads one JSON-RPC request per line and responds with a JSON-RPC
// response using the provided handler functions.
func mockDaemon(t *testing.T) (string, *mockHandlers) {
	t.Helper()

	// Use /tmp for shorter socket paths (macOS has a 108-char limit).
	dir, err := os.MkdirTemp("", "loom-test-*")
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

	handlers := &mockHandlers{
		methods: make(map[string]func(json.RawMessage) (any, error)),
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go handlers.handleConn(conn)
		}
	}()

	t.Cleanup(func() { ln.Close() })
	return sockPath, handlers
}

type mockHandlers struct {
	mu      sync.RWMutex
	methods map[string]func(json.RawMessage) (any, error)
}

func (m *mockHandlers) handle(method string, fn func(json.RawMessage) (any, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.methods[method] = fn
}

func (m *mockHandlers) handleConn(conn net.Conn) {
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
				"error": map[string]any{
					"code":    -32601,
					"message": fmt.Sprintf("unknown method: %s", req.Method),
				},
			})
		} else {
			result, err := fn(req.Params)
			if err != nil {
				resp, _ = json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error": map[string]any{
						"code":    -32603,
						"message": err.Error(),
					},
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

func TestDaemonClient_Connect(t *testing.T) {
	sockPath, _ := mockDaemon(t)

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer client.Close()
}

func TestDaemonClient_ConnectBadPath(t *testing.T) {
	client := NewDaemonClient("/tmp/nonexistent-socket-path-test.sock", nil)
	if err := client.Connect(); err == nil {
		t.Fatal("expected error for bad socket path")
	}
}

func TestDaemonClient_Status(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		return &StatusResult{
			Running:     true,
			Servers:     5,
			ActiveConns: 2,
			Processes:   []string{"git", "time"},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	status, err := client.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if !status.Running {
		t.Error("expected running=true")
	}
	if status.Servers != 5 {
		t.Errorf("expected 5 servers, got %d", status.Servers)
	}
	if status.ActiveConns != 2 {
		t.Errorf("expected 2 active conns, got %d", status.ActiveConns)
	}
	if len(status.Processes) != 2 {
		t.Errorf("expected 2 processes, got %d", len(status.Processes))
	}
}

func TestDaemonClient_Health(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("loom/health", func(_ json.RawMessage) (any, error) {
		return &HealthResult{
			Servers: map[string]ServerHealth{
				"git": {
					Local: HealthEntry{
						Healthy:      true,
						AvgLatencyMs: 12.5,
					},
					Target: "local",
				},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	health, err := client.Health()
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	if len(health.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(health.Servers))
	}
	git, ok := health.Servers["git"]
	if !ok {
		t.Fatal("expected 'git' server in health results")
	}
	if !git.Local.Healthy {
		t.Error("expected git local to be healthy")
	}
	if git.Local.AvgLatencyMs != 12.5 {
		t.Errorf("expected latency 12.5, got %f", git.Local.AvgLatencyMs)
	}
}

func TestDaemonClient_Health_WithDivergence(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("loom/health", func(_ json.RawMessage) (any, error) {
		return &HealthResult{
			Servers: map[string]ServerHealth{
				"git": {
					Local: HealthEntry{
						Healthy:      true,
						AvgLatencyMs: 12.5,
					},
					Monitor: &HealthEntry{
						Healthy:     true,
						ConsecFails: 0,
					},
					Target: "local",
					Divergence: &HealthDivergence{
						MonitorHealthy:  true,
						RouterAvailable: false,
						Reason:          "monitor_healthy_router_unavailable",
					},
				},
			},
			Divergence: []HealthDivergenceEntry{
				{Server: "git", Reason: "monitor_healthy_router_unavailable"},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	health, err := client.Health()
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	git, ok := health.Servers["git"]
	if !ok {
		t.Fatal("expected 'git' server in health results")
	}
	if git.Divergence == nil {
		t.Fatal("expected divergence for 'git' server")
	}
	if git.Divergence.Reason != "monitor_healthy_router_unavailable" {
		t.Errorf("expected reason 'monitor_healthy_router_unavailable', got %q", git.Divergence.Reason)
	}
	if !git.Divergence.MonitorHealthy {
		t.Error("expected monitor_healthy to be true")
	}
	if git.Divergence.RouterAvailable {
		t.Error("expected router_available to be false")
	}
	if git.Monitor == nil {
		t.Fatal("expected monitor entry for 'git' server")
	}
	if !git.Monitor.Healthy {
		t.Error("expected monitor to be healthy")
	}

	// Top-level divergence summary
	if len(health.Divergence) != 1 {
		t.Fatalf("expected 1 top-level divergence entry, got %d", len(health.Divergence))
	}
	if health.Divergence[0].Server != "git" {
		t.Errorf("expected divergence server 'git', got %q", health.Divergence[0].Server)
	}
}

func TestDaemonClient_Health_NoDivergence(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("loom/health", func(_ json.RawMessage) (any, error) {
		return &HealthResult{
			Servers: map[string]ServerHealth{
				"git": {
					Local:  HealthEntry{Healthy: true},
					Target: "local",
				},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	health, err := client.Health()
	if err != nil {
		t.Fatalf("health: %v", err)
	}

	git := health.Servers["git"]
	if git.Divergence != nil {
		t.Errorf("expected nil divergence, got %+v", git.Divergence)
	}
	if len(health.Divergence) != 0 {
		t.Errorf("expected no top-level divergence, got %d", len(health.Divergence))
	}
}

func TestDaemonClient_Servers(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("loom/servers", func(_ json.RawMessage) (any, error) {
		return &ServersResult{
			Servers: []ServerInfo{
				{Name: "git", Running: true, Categories: []string{"dev-tools"}},
				{Name: "time", Running: false},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	result, err := client.Servers()
	if err != nil {
		t.Fatalf("servers: %v", err)
	}

	if len(result.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(result.Servers))
	}
	if result.Servers[0].Name != "git" {
		t.Errorf("expected first server 'git', got %s", result.Servers[0].Name)
	}
	if !result.Servers[0].Running {
		t.Error("expected git to be running")
	}
}

func TestDaemonClient_Reconnect(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	callCount := 0
	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		callCount++
		return &StatusResult{Running: true, Servers: callCount}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	// First call should work.
	status, err := client.Status()
	if err != nil {
		t.Fatalf("first status call: %v", err)
	}
	if !status.Running {
		t.Error("expected running=true")
	}

	// Force close the underlying connection to simulate a disconnect.
	client.mu.Lock()
	if client.conn != nil {
		client.conn.Close()
	}
	client.mu.Unlock()

	// Next call should trigger reconnect and succeed.
	status, err = client.Status()
	if err != nil {
		t.Fatalf("status after reconnect: %v", err)
	}
	if !status.Running {
		t.Error("expected running=true after reconnect")
	}
}

func TestDaemonClient_Timeout(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		// Simulate slow response — sleep longer than the client timeout.
		time.Sleep(250 * time.Millisecond)
		return &StatusResult{Running: true}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	client.callTimeout = 50 * time.Millisecond
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	start := time.Now()
	_, err := client.Status()
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "deadline exceeded") && !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout-related error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout path took too long: %v", elapsed)
	}
}

func TestDaemonClient_CloseIdempotent(t *testing.T) {
	// Use /tmp directly for a shorter path (macOS 108-char socket limit).
	dir, err := os.MkdirTemp("", "loom-test-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "t.sock")
	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Multiple closes should not panic.
	if err := client.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestDaemonClient_ErrorResponse(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		return nil, fmt.Errorf("registry not loaded")
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	_, err := client.Status()
	if err == nil {
		t.Fatal("expected error from daemon error response")
	}
}

func TestDaemonClient_DoesNotReconnectOnDaemonError(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	callCount := 0
	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		callCount++
		return nil, fmt.Errorf("server unavailable: backend timeout")
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	_, err := client.Status()
	if err == nil {
		t.Fatal("expected error from daemon error response")
	}
	if callCount != 1 {
		t.Fatalf("expected exactly one RPC attempt on daemon error, got %d", callCount)
	}
}

func TestDaemonClient_DoesNotReconnectOnDaemonErrorContainingBrokenPipe(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	callCount := 0
	handlers.handle("loom/status", func(_ json.RawMessage) (any, error) {
		callCount++
		// Simulate an application-level daemon error that mentions a downstream
		// broken pipe. The client should NOT treat this as a transport error and
		// should not reconnect.
		return nil, fmt.Errorf("write body: write |1: broken pipe")
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	_, err := client.Status()
	if err == nil {
		t.Fatal("expected error from daemon error response")
	}
	if callCount != 1 {
		t.Fatalf("expected exactly one RPC attempt on daemon error, got %d", callCount)
	}
}

// mockDaemonWithNotifications creates a mock daemon that injects a
// notifications/tools/list_changed notification before each response.
// This exercises the callLocked notification-skipping logic.
func mockDaemonWithNotifications(t *testing.T) (string, *mockHandlers) {
	t.Helper()

	dir, err := os.MkdirTemp("", "loom-test-*")
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

	handlers := &mockHandlers{
		methods: make(map[string]func(json.RawMessage) (any, error)),
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleConnWithNotifications(conn, handlers)
		}
	}()

	t.Cleanup(func() { ln.Close() })
	return sockPath, handlers
}

func handleConnWithNotifications(conn net.Conn, m *mockHandlers) {
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

		// Inject a notification before the response.
		notif, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  "notifications/tools/list_changed",
		})
		notif = append(notif, '\n')
		conn.Write(notif) //nolint:errcheck

		m.mu.RLock()
		fn, ok := m.methods[req.Method]
		m.mu.RUnlock()

		var resp []byte
		if !ok {
			resp, _ = json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error": map[string]any{
					"code":    -32601,
					"message": fmt.Sprintf("unknown method: %s", req.Method),
				},
			})
		} else {
			result, fnErr := fn(req.Params)
			if fnErr != nil {
				resp, _ = json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error": map[string]any{
						"code":    -32603,
						"message": fnErr.Error(),
					},
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
		conn.Write(resp) //nolint:errcheck
	}
}

func TestDaemonClient_SkipsNotifications(t *testing.T) {
	sockPath, handlers := mockDaemonWithNotifications(t)

	handlers.handle("loom/servers", func(_ json.RawMessage) (any, error) {
		return &ServersResult{
			Servers: []ServerInfo{
				{Name: "git", Running: true},
				{Name: "time", Running: false},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	// Despite a notification arriving before the response, Servers()
	// should skip it and return the correct result.
	result, err := client.Servers()
	if err != nil {
		t.Fatalf("servers: %v", err)
	}
	if len(result.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(result.Servers))
	}
	if result.Servers[0].Name != "git" {
		t.Errorf("expected first server 'git', got %s", result.Servers[0].Name)
	}

	// Call again to verify repeated notification skipping works.
	result, err = client.Servers()
	if err != nil {
		t.Fatalf("servers (2nd call): %v", err)
	}
	if len(result.Servers) != 2 {
		t.Fatalf("expected 2 servers on 2nd call, got %d", len(result.Servers))
	}
}
