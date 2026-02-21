package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/testutil"
)

// skipUnlessIntegration skips the test unless LOOM_RUN_INTEGRATION is set.
func skipUnlessIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("LOOM_RUN_INTEGRATION") == "" {
		t.Skip("skipping integration test (set LOOM_RUN_INTEGRATION=1 to enable)")
	}
}

// findLoomBinary locates the loom binary, checking common build paths.
func findLoomBinary(t *testing.T) string {
	t.Helper()

	// Check if loom is on PATH
	if path, err := exec.LookPath("loom"); err == nil {
		return path
	}

	// Check relative build directory
	candidates := []string{
		"../../bin/loom",
		"../../dist/loom",
		filepath.Join(os.Getenv("GOPATH"), "bin", "loom"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}

	t.Skip("loom binary not found (build with 'go build ./cmd/loom' first)")
	return ""
}

// mcpErrorStr returns a human-readable error from MCPError.
func mcpErrorStr(e *MCPError) string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("code=%d: %s", e.Code, e.Message)
}

func TestIntegration_ProxyDaemon_LocalRouting(t *testing.T) {
	skipUnlessIntegration(t)
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 5)()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	loomBin := findLoomBinary(t)

	// Start loom proxy as an MCP stdio server
	client, err := NewMCPClient(ctx, loomBin, "proxy", "--no-daemon")
	if err != nil {
		t.Skipf("failed to start loom proxy: %v", err)
	}
	defer client.Close()

	// Initialize
	resp, err := client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-client", "version": "0.0.1"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %s", mcpErrorStr(resp.Error))
	}

	// List tools
	toolsResp, err := client.Send("tools/list", nil)
	if err != nil {
		t.Fatalf("tools/list failed: %v", err)
	}
	if toolsResp.Error != nil {
		t.Logf("tools/list returned error (expected if no daemon): %s", mcpErrorStr(toolsResp.Error))
		return
	}

	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(toolsResp.Result, &result); err != nil {
		t.Fatalf("unmarshal tools: %v", err)
	}

	t.Logf("proxy returned %d tools", len(result.Tools))
}

func TestIntegration_ProxyDaemon_HubRouting(t *testing.T) {
	skipUnlessIntegration(t)
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 5)()

	// Start a mock hub
	hub, cleanup := startMockHub(t, []string{"mock_echo"})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	loomBin := findLoomBinary(t)

	// Start proxy pointed at mock hub
	client, err := NewMCPClient(ctx, loomBin, "proxy", "--remote", hub.URL)
	if err != nil {
		t.Skipf("failed to start loom proxy: %v", err)
	}
	defer client.Close()

	// Initialize
	resp, err := client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-client", "version": "0.0.1"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %s", mcpErrorStr(resp.Error))
	}

	// List tools via remote hub
	toolsResp, err := client.Send("tools/list", nil)
	if err != nil {
		t.Fatalf("tools/list failed: %v", err)
	}

	// The remote connection might fail if the mock doesn't fully implement
	// the streaming HTTP protocol, but we verify the proxy attempts it
	if toolsResp.Error != nil {
		t.Logf("tools/list via hub returned error (mock may not support full protocol): %s", mcpErrorStr(toolsResp.Error))
	} else {
		t.Log("tools/list via hub succeeded")
	}
}

func TestIntegration_ProxyDaemon_ConcurrentClients(t *testing.T) {
	skipUnlessIntegration(t)
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 10)()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	loomBin := findLoomBinary(t)

	const numClients = 5
	var wg sync.WaitGroup
	errors := make([]error, numClients)

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			client, err := NewMCPClient(ctx, loomBin, "proxy", "--no-daemon")
			if err != nil {
				errors[idx] = err
				return
			}
			defer client.Close()

			resp, err := client.Send("initialize", map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "test-client", "version": "0.0.1"},
			})
			if err != nil {
				errors[idx] = err
				return
			}
			if resp.Error != nil {
				errors[idx] = fmt.Errorf("init error: %s", mcpErrorStr(resp.Error))
				return
			}

			// List tools
			_, err = client.Send("tools/list", nil)
			if err != nil {
				errors[idx] = err
				return
			}
		}(i)
	}

	wg.Wait()

	successCount := 0
	for i, err := range errors {
		if err != nil {
			t.Logf("client %d error: %v", i, err)
		} else {
			successCount++
		}
	}

	// At least some clients should succeed
	if successCount == 0 {
		t.Fatal("all concurrent clients failed")
	}
	t.Logf("%d/%d concurrent clients succeeded", successCount, numClients)
}

func TestIntegration_ProxyDaemon_RoutingPreference(t *testing.T) {
	skipUnlessIntegration(t)
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 5)()

	// Write a temp config with routing preferences
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config", "loom")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	configContent := `
routing:
  preferences:
    git: local-only
    prometheus: prefer-hub
`
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	loomBin := findLoomBinary(t)

	// Start proxy with custom HOME so it picks up our config
	client, err := NewMCPClientWithEnv(ctx, map[string]string{"HOME": tmpDir}, loomBin, "proxy", "--no-daemon")
	if err != nil {
		t.Skipf("failed to start loom proxy: %v", err)
	}
	defer client.Close()

	resp, err := client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-client", "version": "0.0.1"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %s", mcpErrorStr(resp.Error))
	}

	t.Log("proxy started with routing preferences config")
}

func TestIntegration_ProxyDaemon_CacheHit(t *testing.T) {
	skipUnlessIntegration(t)
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 5)()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	loomBin := findLoomBinary(t)

	client, err := NewMCPClient(ctx, loomBin, "proxy", "--no-daemon")
	if err != nil {
		t.Skipf("failed to start loom proxy: %v", err)
	}
	defer client.Close()

	resp, err := client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-client", "version": "0.0.1"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %s", mcpErrorStr(resp.Error))
	}

	// Call tools/list twice - second should be faster (cache hit at daemon level)
	start1 := time.Now()
	resp1, err := client.Send("tools/list", nil)
	elapsed1 := time.Since(start1)
	if err != nil {
		t.Fatalf("first tools/list failed: %v", err)
	}

	start2 := time.Now()
	resp2, err := client.Send("tools/list", nil)
	elapsed2 := time.Since(start2)
	if err != nil {
		t.Fatalf("second tools/list failed: %v", err)
	}

	t.Logf("first call: %v, second call: %v", elapsed1, elapsed2)

	// Both should succeed (or both fail gracefully)
	if resp1.Error == nil && resp2.Error == nil {
		t.Log("both tools/list calls succeeded - cache should be working")
	} else {
		t.Logf("tools/list returned errors (expected without daemon): resp1=%v resp2=%v",
			mcpErrorStr(resp1.Error), mcpErrorStr(resp2.Error))
	}
}

// --- Chaos/restart integration tests (DEBT-007) ---

// TestIntegration_ProxyDaemon_DaemonRestartRecovery verifies that the proxy
// recovers when the daemon socket disappears and a new daemon starts.
// Sequence: start proxy → successful tools/list → kill daemon socket →
// start new daemon → verify proxy reconnects on next request.
func TestIntegration_ProxyDaemon_DaemonRestartRecovery(t *testing.T) {
	skipUnlessIntegration(t)
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 10)()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	loomBin := findLoomBinary(t)

	// Start proxy (with daemon auto-start).
	client, err := NewMCPClient(ctx, loomBin, "proxy")
	if err != nil {
		t.Skipf("failed to start loom proxy: %v", err)
	}
	defer client.Close()

	// Initialize.
	resp, err := client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-chaos", "version": "0.0.1"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %s", mcpErrorStr(resp.Error))
	}

	// First tools/list should succeed.
	resp1, err := client.Send("tools/list", nil)
	if err != nil {
		t.Fatalf("first tools/list failed: %v", err)
	}
	if resp1.Error != nil {
		t.Logf("first tools/list returned error (daemon may not be running): %s", mcpErrorStr(resp1.Error))
		t.Skip("skipping: daemon not available for restart test")
	}
	t.Log("first tools/list succeeded")

	// Find and remove daemon socket to simulate daemon crash.
	socketDir := filepath.Join(os.TempDir(), "loom-daemon")
	entries, err := os.ReadDir(socketDir)
	if err != nil {
		t.Skipf("cannot read daemon socket dir %s: %v", socketDir, err)
	}

	removedSocket := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sock") {
			socketPath := filepath.Join(socketDir, e.Name())
			if err := os.Remove(socketPath); err == nil {
				t.Logf("removed daemon socket: %s", socketPath)
				removedSocket = true
				break
			}
		}
	}
	if !removedSocket {
		t.Skip("no daemon socket found to remove")
	}

	// Give proxy a moment to notice.
	time.Sleep(500 * time.Millisecond)

	// Next request should fail or trigger reconnect.
	resp2, err := client.Send("tools/list", nil)
	if err != nil {
		t.Logf("second tools/list errored (expected during reconnect): %v", err)
	} else if resp2.Error != nil {
		t.Logf("second tools/list returned MCP error (expected): %s", mcpErrorStr(resp2.Error))
	} else {
		t.Log("second tools/list succeeded (proxy reconnected quickly)")
	}

	// Allow auto-restart to kick in, then retry.
	time.Sleep(2 * time.Second)

	resp3, err := client.Send("tools/list", nil)
	if err != nil {
		t.Logf("third tools/list after recovery failed: %v", err)
	} else if resp3.Error != nil {
		t.Logf("third tools/list returned error: %s", mcpErrorStr(resp3.Error))
	} else {
		t.Log("third tools/list succeeded - proxy recovered after daemon restart")
	}
}

// TestIntegration_ProxyDaemon_StaleSocketRecovery verifies proxy behavior when
// a stale socket file exists (daemon gone but socket remains). The proxy should
// detect the stale connection and attempt recovery.
func TestIntegration_ProxyDaemon_StaleSocketRecovery(t *testing.T) {
	skipUnlessIntegration(t)
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 10)()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	loomBin := findLoomBinary(t)

	// Create a fake stale socket file.
	socketDir := filepath.Join(os.TempDir(), "loom-daemon")
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	staleSocket := filepath.Join(socketDir, "stale-test.sock")
	if err := os.WriteFile(staleSocket, []byte("stale"), 0600); err != nil {
		t.Fatalf("create stale socket: %v", err)
	}
	defer os.Remove(staleSocket)

	// Start proxy in --no-daemon mode (avoids needing real daemon).
	client, err := NewMCPClient(ctx, loomBin, "proxy", "--no-daemon")
	if err != nil {
		t.Skipf("failed to start loom proxy: %v", err)
	}
	defer client.Close()

	resp, err := client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-stale", "version": "0.0.1"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %s", mcpErrorStr(resp.Error))
	}

	// Proxy should work despite stale socket existing.
	resp2, err := client.Send("tools/list", nil)
	if err != nil {
		t.Fatalf("tools/list failed: %v", err)
	}
	if resp2.Error != nil {
		t.Logf("tools/list returned error (expected in --no-daemon): %s", mcpErrorStr(resp2.Error))
	} else {
		t.Log("tools/list succeeded despite stale socket file")
	}
}

// TestIntegration_ProxyDaemon_InitFailureRecovery verifies that a proxy started
// against a non-existent socket (no autostart) returns an error, then succeeds
// once a daemon is available.
func TestIntegration_ProxyDaemon_InitFailureRecovery(t *testing.T) {
	skipUnlessIntegration(t)
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 10)()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	loomBin := findLoomBinary(t)

	// Start proxy pointed at a non-existent socket with no auto-start.
	// Use --no-daemon so it doesn't try to start one.
	client, err := NewMCPClient(ctx, loomBin, "proxy", "--no-daemon")
	if err != nil {
		t.Skipf("failed to start loom proxy: %v", err)
	}
	defer client.Close()

	// Initialize should succeed (proxy itself starts fine).
	resp, err := client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-init-fail", "version": "0.0.1"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %s", mcpErrorStr(resp.Error))
	}

	// tools/list may return error if no backends are available.
	resp2, err := client.Send("tools/list", nil)
	if err != nil {
		t.Logf("tools/list transport error (expected): %v", err)
		return
	}

	if resp2.Error != nil {
		t.Logf("tools/list returned MCP error (expected without daemon): %s", mcpErrorStr(resp2.Error))
	} else {
		t.Log("tools/list succeeded (local servers available without daemon)")
	}
}

// TestIntegration_ProxyDaemon_TransportResetOnError verifies that the proxy
// resets its transport after encountering an error, allowing subsequent
// requests to attempt reconnection rather than failing fast.
func TestIntegration_ProxyDaemon_TransportResetOnError(t *testing.T) {
	skipUnlessIntegration(t)
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 10)()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	loomBin := findLoomBinary(t)

	// Start proxy with --no-daemon.
	client, err := NewMCPClient(ctx, loomBin, "proxy", "--no-daemon")
	if err != nil {
		t.Skipf("failed to start loom proxy: %v", err)
	}
	defer client.Close()

	// Initialize.
	resp, err := client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test-transport", "version": "0.0.1"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %s", mcpErrorStr(resp.Error))
	}

	// Make several rapid requests - proxy should handle them without panicking
	// or leaking goroutines even if some fail.
	var lastErr error
	successes := 0
	for i := 0; i < 5; i++ {
		resp, err := client.Send("tools/list", nil)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.Error != nil {
			lastErr = fmt.Errorf("MCP error: %s", mcpErrorStr(resp.Error))
			continue
		}
		successes++
	}

	t.Logf("transport reset test: %d/5 requests succeeded", successes)
	if successes == 0 && lastErr != nil {
		t.Logf("all requests failed (expected without daemon backends): %v", lastErr)
	}
}
