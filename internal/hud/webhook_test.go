package hud

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
)

func TestFleetWebhook_Push(t *testing.T) {
	var received webhookPayload
	var authHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("X-Push-Token")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	logger := slog.Default()
	wh := NewFleetWebhook(srv.URL, "secret-token", "", logger)

	snap := monitor.FleetSnapshot{
		Agents: []bridge.PresenceInfo{
			{AgentID: "claude-code", Status: "active", AgentType: "claude-code", Branch: "main"},
			{AgentID: "gemini", Status: "idle", AgentType: "gemini"},
		},
		Sessions: []bridge.SessionInfo{
			{ID: "sess-1", AgentID: "claude-code", Status: "active", StartedAt: "2026-01-01T00:00:00Z"},
		},
	}

	wh.Push(snap)

	// Verify auth header.
	if authHeader != "secret-token" {
		t.Errorf("expected X-Push-Token 'secret-token', got %q", authHeader)
	}

	// Verify payload shape.
	if len(received.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(received.Agents))
	}
	if received.Agents[0].AgentID != "claude-code" {
		t.Errorf("expected agent_id claude-code, got %s", received.Agents[0].AgentID)
	}
	if received.Agents[0].Branch != "main" {
		t.Errorf("expected branch main, got %s", received.Agents[0].Branch)
	}
	if len(received.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(received.Sessions))
	}
	if received.Sessions[0].ID != "sess-1" {
		t.Errorf("expected session id sess-1, got %s", received.Sessions[0].ID)
	}
}

func TestFleetWebhook_Backoff(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wh := NewFleetWebhook(srv.URL, "", "", logger)

	snap := monitor.FleetSnapshot{
		Agents: []bridge.PresenceInfo{{AgentID: "test"}},
	}

	// First push should go through (and fail).
	wh.Push(snap)
	if callCount.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", callCount.Load())
	}

	// Immediate second push should be skipped by backoff.
	wh.Push(snap)
	if callCount.Load() != 1 {
		t.Fatalf("expected backoff to skip second push, got %d calls", callCount.Load())
	}
}

func TestFleetWebhook_NoToken(t *testing.T) {
	var authHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("X-Push-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wh := NewFleetWebhook(srv.URL, "", "", logger)

	wh.Push(monitor.FleetSnapshot{})

	if authHeader != "" {
		t.Errorf("expected no X-Push-Token header when token is empty, got %q", authHeader)
	}
}

func TestFleetWebhook_RecoveryResetsBackoff(t *testing.T) {
	var callCount atomic.Int32
	failFirst := atomic.Bool{}
	failFirst.Store(true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		if failFirst.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wh := NewFleetWebhook(srv.URL, "", "", logger)

	snap := monitor.FleetSnapshot{}

	// First push fails.
	wh.Push(snap)
	if wh.consecutiveErrors != 1 {
		t.Fatalf("expected 1 consecutive error, got %d", wh.consecutiveErrors)
	}

	// Force backoff timer to expire so next push goes through.
	wh.lastError = time.Now().Add(-1 * time.Minute)
	failFirst.Store(false)

	wh.Push(snap)
	if wh.consecutiveErrors != 0 {
		t.Fatalf("expected consecutive errors to reset after success, got %d", wh.consecutiveErrors)
	}
}

func TestFleetWebhook_ResolveOverride(t *testing.T) {
	// Start a TLS server to verify the resolve override routes traffic correctly.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Extract host and port from the test server address.
	addr := srv.Listener.Addr().String()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("failed to parse test server addr %q: %v", addr, err)
	}

	// Use a fake hostname in the URL. The resolve override points to the
	// test server's IP so the dialer connects there instead of doing DNS.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	wh := NewFleetWebhook(
		"https://fake-host.example.com:"+port+"/push",
		"",
		host,
		logger,
	)

	// Extract the client-side TLS root CA from the test server's helper client,
	// then configure it on our transport while keeping the custom dialer.
	srvClient := srv.Client()
	srvTransport := srvClient.Transport.(*http.Transport)
	if transport, ok := wh.httpClient.Transport.(*http.Transport); ok {
		transport.TLSClientConfig.RootCAs = srvTransport.TLSClientConfig.RootCAs
	} else {
		t.Fatal("expected *http.Transport when resolveOverride is set")
	}

	wh.Push(monitor.FleetSnapshot{
		Agents: []bridge.PresenceInfo{
			{AgentID: "test", Status: "active"},
		},
	})

	if wh.consecutiveErrors != 0 {
		t.Errorf("expected 0 consecutive errors with resolve override, got %d", wh.consecutiveErrors)
	}
}
