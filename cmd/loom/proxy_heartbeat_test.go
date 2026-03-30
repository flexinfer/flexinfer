package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud"
)

func TestHeartbeatRateLimiting(t *testing.T) {
	// Reset state
	lastHeartbeat.Store(0)
	saved := heartbeatIntervalNanos
	defer func() { heartbeatIntervalNanos = saved }()

	// Use a long interval so second call is suppressed
	heartbeatIntervalNanos = int64(10 * time.Second)

	var count atomic.Int64

	// Simulate two rapid calls checking the rate limit
	for i := 0; i < 10; i++ {
		now := time.Now().UnixNano()
		prev := lastHeartbeat.Load()
		if now-prev >= heartbeatIntervalNanos {
			if lastHeartbeat.CompareAndSwap(prev, now) {
				count.Add(1)
			}
		}
	}

	if got := count.Load(); got != 1 {
		t.Errorf("expected 1 heartbeat within interval, got %d", got)
	}
}

func TestHeartbeatRateLimiting_AllowsAfterInterval(t *testing.T) {
	lastHeartbeat.Store(0)
	saved := heartbeatIntervalNanos
	defer func() { heartbeatIntervalNanos = saved }()

	// Very short interval
	heartbeatIntervalNanos = int64(1 * time.Millisecond)

	var count atomic.Int64

	// First call
	now := time.Now().UnixNano()
	prev := lastHeartbeat.Load()
	if now-prev >= heartbeatIntervalNanos {
		if lastHeartbeat.CompareAndSwap(prev, now) {
			count.Add(1)
		}
	}

	// Wait past interval
	time.Sleep(2 * time.Millisecond)

	// Second call should be allowed
	now = time.Now().UnixNano()
	prev = lastHeartbeat.Load()
	if now-prev >= heartbeatIntervalNanos {
		if lastHeartbeat.CompareAndSwap(prev, now) {
			count.Add(1)
		}
	}

	if got := count.Load(); got != 2 {
		t.Errorf("expected 2 heartbeats after interval, got %d", got)
	}
}

func TestNextSessionKeepaliveInterval_NoActivity(t *testing.T) {
	lastProxyCallTime.Store(0)
	savedActive := sessionKeepaliveActive
	savedIdle := sessionKeepaliveIdle
	savedThreshold := sessionIdleThreshold
	defer func() {
		sessionKeepaliveActive = savedActive
		sessionKeepaliveIdle = savedIdle
		sessionIdleThreshold = savedThreshold
	}()

	sessionKeepaliveActive = 5 * time.Second
	sessionKeepaliveIdle = 30 * time.Second
	sessionIdleThreshold = 30 * time.Second

	// No activity recorded → idle interval.
	got := nextSessionKeepaliveInterval()
	if got != 30*time.Second {
		t.Fatalf("expected 30s idle interval, got %v", got)
	}
}

func TestNextSessionKeepaliveInterval_RecentActivity(t *testing.T) {
	savedActive := sessionKeepaliveActive
	savedIdle := sessionKeepaliveIdle
	savedThreshold := sessionIdleThreshold
	defer func() {
		sessionKeepaliveActive = savedActive
		sessionKeepaliveIdle = savedIdle
		sessionIdleThreshold = savedThreshold
		lastProxyCallTime.Store(0)
	}()

	sessionKeepaliveActive = 5 * time.Second
	sessionKeepaliveIdle = 30 * time.Second
	sessionIdleThreshold = 30 * time.Second

	// Activity just now → active interval.
	lastProxyCallTime.Store(time.Now().UnixNano())
	got := nextSessionKeepaliveInterval()
	if got != 5*time.Second {
		t.Fatalf("expected 5s active interval, got %v", got)
	}
}

func TestNextSessionKeepaliveInterval_StaleActivity(t *testing.T) {
	savedActive := sessionKeepaliveActive
	savedIdle := sessionKeepaliveIdle
	savedThreshold := sessionIdleThreshold
	defer func() {
		sessionKeepaliveActive = savedActive
		sessionKeepaliveIdle = savedIdle
		sessionIdleThreshold = savedThreshold
		lastProxyCallTime.Store(0)
	}()

	sessionKeepaliveActive = 5 * time.Second
	sessionKeepaliveIdle = 30 * time.Second
	sessionIdleThreshold = 1 * time.Millisecond // tiny threshold for test

	// Activity in the past, beyond threshold → idle interval.
	lastProxyCallTime.Store(time.Now().Add(-10 * time.Millisecond).UnixNano())
	time.Sleep(2 * time.Millisecond) // ensure we're past threshold
	got := nextSessionKeepaliveInterval()
	if got != 30*time.Second {
		t.Fatalf("expected 30s idle interval for stale activity, got %v", got)
	}
}

func TestResolveProxyIdentity_UsesEnvOverride(t *testing.T) {
	t.Setenv("LOOM_PROXY_AGENT_ID", "custom-proxy-id")
	proxyIdentityOnce = sync.Once{}
	proxyAgentID = ""

	id, typ := resolveProxyIdentity("codex")
	if typ != "codex" {
		t.Fatalf("agentType = %q, want codex", typ)
	}
	if id != "custom-proxy-id" {
		t.Fatalf("agentID = %q, want custom-proxy-id", id)
	}
}

func TestResolveProxyIdentity_GeneratesStableProcessScopedID(t *testing.T) {
	t.Setenv("LOOM_PROXY_AGENT_ID", "")
	proxyIdentityOnce = sync.Once{}
	proxyAgentID = ""

	id1, typ1 := resolveProxyIdentity("claude-code")
	id2, typ2 := resolveProxyIdentity("claude-code")

	if typ1 != "claude-code" || typ2 != "claude-code" {
		t.Fatalf("unexpected agent type values: %q %q", typ1, typ2)
	}
	if id1 == "" || id2 == "" {
		t.Fatalf("expected non-empty generated IDs, got %q and %q", id1, id2)
	}
	if id1 != id2 {
		t.Fatalf("expected stable ID within process, got %q != %q", id1, id2)
	}

	prefix := "claude-code-"
	if !strings.HasPrefix(id1, prefix) {
		t.Fatalf("expected id %q to start with %q", id1, prefix)
	}
	pidFragment := fmt.Sprintf("-%d", os.Getpid())
	if !strings.Contains(id1, pidFragment) {
		t.Fatalf("expected id %q to include pid fragment %q", id1, pidFragment)
	}
}

// TestProxyHeartbeat_ReadsCanonicalPortFile verifies that proxyHeartbeat reads
// the port from hud.PortFilePath() (the canonical ~/.config/loom/hud.port)
// instead of the old hardcoded /tmp/loom-hud.port path.
func TestProxyHeartbeat_ReadsCanonicalPortFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Start a test HTTP server that records heartbeat requests.
	var received atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agent/heartbeat" {
			received.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Extract the port from the test server URL.
	parts := strings.SplitN(srv.URL, ":", 3)
	port := parts[2] // "PORT" from "http://127.0.0.1:PORT"

	// Write the port to the canonical location.
	_, err := hud.WritePortFile(mustAtoi(port))
	if err != nil {
		t.Fatalf("WritePortFile: %v", err)
	}
	defer hud.RemovePortFile()

	// Reset heartbeat state so the call goes through.
	lastHeartbeat.Store(0)
	proxyIdentityOnce = sync.Once{}
	proxyAgentID = ""
	proxyNamespaceOnce = sync.Once{}

	// proxyHeartbeat should discover the test server via the port file.
	proxyHeartbeat("test-agent")

	// Give a moment for the HTTP request to complete.
	time.Sleep(100 * time.Millisecond)

	if !received.Load() {
		t.Fatal("proxyHeartbeat did not send request to the server discovered via hud.PortFilePath()")
	}
}

func mustAtoi(s string) int {
	var n int
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
