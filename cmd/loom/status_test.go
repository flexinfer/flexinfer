package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
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
		Daemon:    daemonStatus{Running: true, Servers: 5, DrainReady: true},
		Agents:    agentStatus{Active: 2, Idle: 1, Total: 3},
		Pipelines: pipelineStatus{Available: true, Running: 1, Pending: 2, Passed: 3, Failed: 4, LastActivity: "5m ago"},
		HUD:       hudStatus{Reachable: true},
		Health: &daemonHealthSnapshot{
			Servers: map[string]daemonHealthServer{
				"alpha": {
					Healthy:          false,
					Ready:            false,
					ConsecutiveFails: 3,
					AvgLatencyMs:     42.5,
					LastError:        "connection refused",
					RestartCount:     2,
				},
			},
			DegradedServers: []string{"alpha"},
		},
		Observability: &daemonObservabilityStatus{
			OTLPEndpoint: "",
			LogFormat:    "text",
			Warnings:     []string{"otlp endpoint not configured", "json logging disabled"},
		},
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
	if daemon["drain_ready"] != true {
		t.Errorf("daemon.drain_ready = %v, want true", daemon["drain_ready"])
	}

	agents := parsed["agents"].(map[string]any)
	if agents["active"].(float64) != 2 {
		t.Errorf("agents.active = %v, want 2", agents["active"])
	}
	pipelines := parsed["pipelines"].(map[string]any)
	if pipelines["available"] != true {
		t.Error("expected pipelines.available = true")
	}
	if pipelines["running"].(float64) != 1 {
		t.Errorf("pipelines.running = %v, want 1", pipelines["running"])
	}
	if pipelines["pending"].(float64) != 2 {
		t.Errorf("pipelines.pending = %v, want 2", pipelines["pending"])
	}
	if pipelines["passed"].(float64) != 3 {
		t.Errorf("pipelines.passed = %v, want 3", pipelines["passed"])
	}
	if pipelines["failed"].(float64) != 4 {
		t.Errorf("pipelines.failed = %v, want 4", pipelines["failed"])
	}

	if parsed["healthy"] != true {
		t.Error("expected healthy = true")
	}

	health := parsed["health"].(map[string]any)
	if got := health["degraded_servers"].([]any); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("health.degraded_servers = %#v, want [alpha]", got)
	}
	servers := health["servers"].(map[string]any)
	alpha := servers["alpha"].(map[string]any)
	if alpha["restart_count"].(float64) != 2 {
		t.Fatalf("alpha.restart_count = %v, want 2", alpha["restart_count"])
	}
	if alpha["avg_latency_ms"].(float64) != 42.5 {
		t.Fatalf("alpha.avg_latency_ms = %v, want 42.5", alpha["avg_latency_ms"])
	}

	obs := parsed["observability"].(map[string]any)
	if obs["log_format"] != "text" {
		t.Fatalf("observability.log_format = %v, want text", obs["log_format"])
	}
	if got := obs["warnings"].([]any); len(got) != 2 {
		t.Fatalf("observability.warnings = %#v, want 2 warnings", got)
	}
}

func TestPrintPlatformStatus_IncludesPipelineSummary(t *testing.T) {
	ps := platformStatus{
		Daemon:    daemonStatus{Running: true, Servers: 5, DrainReady: true},
		Agents:    agentStatus{Active: 2, Idle: 1, Offline: 0, Total: 3},
		Sessions:  sessionCount{Active: 1, Total: 4},
		Pipelines: pipelineStatus{Available: true, Running: 1, Pending: 2, Passed: 3, Failed: 4, LastActivity: "5m ago"},
		HUD:       hudStatus{Reachable: true},
		Healthy:   true,
	}

	got := captureStdout(t, func() {
		printPlatformStatus(ps, "/tmp/loom.sock")
	})

	if !strings.Contains(got, "Pipelines: 1 running, 2 pending, 3 passed, 4 failed") {
		t.Fatalf("expected pipeline summary in output, got: %s", got)
	}
	if !strings.Contains(got, "Pipelines: last activity 5m ago") {
		t.Fatalf("expected last-activity line in output, got: %s", got)
	}
	if !strings.Contains(got, "Readiness: drain ready") {
		t.Fatalf("expected readiness line in output, got: %s", got)
	}
}

func TestPrintPlatformStatus_HighlightsHealthAndObservabilityWarnings(t *testing.T) {
	ps := platformStatus{
		Daemon: daemonStatus{Running: true, Servers: 2, DrainReady: false, Draining: true},
		Health: &daemonHealthSnapshot{
			Servers: map[string]daemonHealthServer{
				"alpha": {
					Healthy:          false,
					ConsecutiveFails: 4,
					AvgLatencyMs:     88.1,
					LastError:        "timeout",
					RestartCount:     1,
				},
				"beta": {
					Healthy:          true,
					ConsecutiveFails: 0,
					AvgLatencyMs:     12.4,
				},
			},
			DegradedServers: []string{"alpha"},
		},
		Observability: &daemonObservabilityStatus{
			Warnings: []string{"otlp endpoint not configured", "json logging disabled"},
		},
	}

	got := captureStdout(t, func() {
		printPlatformStatus(ps, "/tmp/loom.sock")
	})

	if !strings.Contains(got, "Readiness: draining") {
		t.Fatalf("expected draining readiness line, got: %s", got)
	}
	if !strings.Contains(got, "Health:   degraded servers: alpha") {
		t.Fatalf("expected degraded server list, got: %s", got)
	}
	if !strings.Contains(got, "Health:   alpha(restarts=1, latency=88ms, error=timeout)") {
		t.Fatalf("expected degraded detail line, got: %s", got)
	}
	if !strings.Contains(got, "OTel:     warning: otlp endpoint not configured; json logging disabled") {
		t.Fatalf("expected OTel warning line, got: %s", got)
	}
}

func TestShowStatus_DaemonDown_ReturnsError(t *testing.T) {
	err := showStatus("/nonexistent.sock", "0", false)
	if err == nil {
		t.Error("expected error for daemon not running")
	}
	if !strings.Contains(err.Error(), "daemon not running") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCountPresenceStatuses(t *testing.T) {
	t.Parallel()

	got := countPresenceStatuses([]bridge.PresenceInfo{
		{Status: "active"},
		{Status: "idle"},
		{Status: "offline"},
		{Status: "unknown"},
	})

	if got.Active != 1 || got.Idle != 1 || got.Offline != 2 || got.Total != 4 {
		t.Fatalf("countPresenceStatuses() = %+v", got)
	}
}

func TestCountSessionStatuses(t *testing.T) {
	t.Parallel()

	got := countSessionStatuses([]bridge.SessionInfo{
		{Status: "active", EndedAt: "", AgentID: "claude-1", Namespace: "repo/a"},
		{Status: "summarized", EndedAt: "2026-03-06T00:00:00Z"},
		{Status: "", EndedAt: "", AgentID: "claude-1", Namespace: "repo/a"},
	})

	if got.Active != 1 || got.Total != 3 {
		t.Fatalf("countSessionStatuses() = %+v", got)
	}
}

func TestCountSessionStatuses_GroupsDuplicateActiveIdentities(t *testing.T) {
	t.Parallel()

	got := countSessionStatuses([]bridge.SessionInfo{
		{AgentID: "agent-1", Namespace: "loom-core/main", Status: "active", EndedAt: ""},
		{AgentID: "agent-1", Namespace: "loom-core/main", Status: "active", EndedAt: ""},
		{AgentID: "agent-1", Namespace: "loom-core/other", Status: "active", EndedAt: ""},
		{AgentID: "agent-1", Namespace: "loom-core/main", Status: "ended", EndedAt: "2026-03-06T00:00:00Z"},
	})

	if got.Active != 2 || got.Total != 4 {
		t.Fatalf("countSessionStatuses() = %+v, want 2 active across 4 total", got)
	}
}

func TestCountSessionStatuses_KeepsAnonymousActiveSessionsDistinct(t *testing.T) {
	t.Parallel()

	got := countSessionStatuses([]bridge.SessionInfo{
		{Status: "active", EndedAt: ""},
		{Status: "", EndedAt: ""},
		{Status: "ended", EndedAt: "2026-03-06T00:00:00Z"},
	})

	if got.Active != 2 || got.Total != 3 {
		t.Fatalf("countSessionStatuses() = %+v, want 2 active across 3 total", got)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	orig := os.Stdout
	defer func() { os.Stdout = orig }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	_ = w.Close()

	return <-done
}
