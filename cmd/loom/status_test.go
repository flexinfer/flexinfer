package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCollectPlatformStatus_DaemonDown(t *testing.T) {
	t.Parallel()
	ps := collectPlatformStatus("/nonexistent.sock", "0")
	if ps.Daemon.Running {
		t.Error("expected daemon not running")
	}
	if ps.Healthy {
		t.Error("expected unhealthy when daemon is down")
	}
}

func TestCollectPlatformStatus_HUDPresenceAndSessions(t *testing.T) {
	// Not parallel — temporarily overrides LOOM_HUD_URL env var.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/presence", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"agents":[],"active_agents":2,"idle_agents":1,"offline_agents":0,"total":3}`)
	})
	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"sessions":[{"ended_at":""},{"ended_at":""},{"ended_at":"2026-01-01T00:00:00Z"}]}`)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Override HUD URL to point at test server (bypasses config.yaml / env).
	t.Setenv("LOOM_HUD_URL", ts.URL)
	// Reset the cached config so hudBaseURL picks up the env var.
	hudConfigOnce.loaded = false
	defer func() { hudConfigOnce.loaded = false }()

	port := strings.TrimPrefix(ts.URL, "http://127.0.0.1:")

	// Test presence parsing via hudGetFast.
	presenceData, err := hudGetFast(port, "/api/presence", defaultHUDTimeout)
	if err != nil {
		t.Fatalf("presence request: %v", err)
	}

	var presence struct {
		ActiveAgents  int `json:"active_agents"`
		IdleAgents    int `json:"idle_agents"`
		OfflineAgents int `json:"offline_agents"`
		Total         int `json:"total"`
	}
	if err := json.Unmarshal(presenceData, &presence); err != nil {
		t.Fatalf("unmarshal presence: %v", err)
	}
	if presence.ActiveAgents != 2 {
		t.Errorf("active agents = %d, want 2", presence.ActiveAgents)
	}
	if presence.Total != 3 {
		t.Errorf("total agents = %d, want 3", presence.Total)
	}

	// Test session parsing.
	sessData, err := hudGetFast(port, "/api/sessions", defaultHUDTimeout)
	if err != nil {
		t.Fatalf("sessions request: %v", err)
	}
	var sessResp struct {
		Sessions []struct {
			EndedAt string `json:"ended_at"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(sessData, &sessResp); err != nil {
		t.Fatalf("unmarshal sessions: %v", err)
	}
	active := 0
	for _, s := range sessResp.Sessions {
		if s.EndedAt == "" {
			active++
		}
	}
	if active != 2 {
		t.Errorf("active sessions = %d, want 2", active)
	}
	if len(sessResp.Sessions) != 3 {
		t.Errorf("total sessions = %d, want 3", len(sessResp.Sessions))
	}
}

func TestPlatformStatus_JSONOutput(t *testing.T) {
	t.Parallel()
	ps := platformStatus{
		Daemon:  daemonStatus{Running: true, Servers: 5},
		Agents:  agentStatus{Active: 2, Idle: 1, Total: 3},
		HUD:     hudStatus{Reachable: true},
		Healthy: true,
	}

	data, err := json.Marshal(ps)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	daemon := parsed["daemon"].(map[string]any)
	if daemon["running"] != true {
		t.Error("expected daemon.running = true")
	}
	if daemon["servers"].(float64) != 5 {
		t.Errorf("daemon.servers = %v, want 5", daemon["servers"])
	}

	agents := parsed["agents"].(map[string]any)
	if agents["active"].(float64) != 2 {
		t.Errorf("agents.active = %v, want 2", agents["active"])
	}

	if parsed["healthy"] != true {
		t.Error("expected healthy = true")
	}
}

func TestShowStatus_DaemonDown_ReturnsError(t *testing.T) {
	t.Parallel()
	err := showStatus("/nonexistent.sock", "0", false)
	if err == nil {
		t.Error("expected error for daemon not running")
	}
	if !strings.Contains(err.Error(), "daemon not running") {
		t.Errorf("unexpected error: %v", err)
	}
}
