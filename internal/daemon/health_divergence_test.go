package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/registry"

	"github.com/crb2nu/loom/internal/router"
)

func TestComputeHealthDivergence(t *testing.T) {
	cases := []struct {
		name            string
		monitor         *ServerHealthStatus
		routerAvailable bool
		expectNil       bool
		expectReason    string
	}{
		{
			name:            "nil monitor",
			monitor:         nil,
			routerAvailable: true,
			expectNil:       true,
		},
		{
			name:            "agree healthy",
			monitor:         &ServerHealthStatus{Healthy: true},
			routerAvailable: true,
			expectNil:       true,
		},
		{
			name:            "agree unhealthy",
			monitor:         &ServerHealthStatus{Healthy: false},
			routerAvailable: false,
			expectNil:       true,
		},
		{
			name:            "monitor healthy router unavailable",
			monitor:         &ServerHealthStatus{Healthy: true},
			routerAvailable: false,
			expectReason:    "monitor_healthy_router_unavailable",
		},
		{
			name:            "monitor unhealthy router available",
			monitor:         &ServerHealthStatus{Healthy: false},
			routerAvailable: true,
			expectReason:    "monitor_unhealthy_router_available",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result := computeHealthDivergence(tc.monitor, tc.routerAvailable)
			if tc.expectNil {
				if result != nil {
					t.Fatalf("expected nil divergence, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected divergence, got nil")
			}
			if result.Reason != tc.expectReason {
				t.Fatalf("expected reason %q, got %q", tc.expectReason, result.Reason)
			}
		})
	}
}

// newTestDaemon creates a minimal Daemon for health handler testing.
func newTestDaemon(servers []*registry.Server, monitorStatuses map[string]*ServerHealthStatus) *Daemon {
	reg := &registry.Registry{Servers: servers}
	r := router.New(router.Config{Registry: reg})
	hm := &HealthMonitor{
		statuses: monitorStatuses,
	}
	return &Daemon{
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		registry:      reg,
		router:        r,
		healthMonitor: hm,
	}
}

func TestHealthHandler_WithDivergence(t *testing.T) {
	// Set up: monitor says "git" is healthy, but router's local health
	// marks it unhealthy (consecutive failures above threshold).
	servers := []*registry.Server{
		{Name: "git", Categories: []string{"local-only"}},
	}
	monitorStatuses := map[string]*ServerHealthStatus{
		"git": {Name: "git", Healthy: true, AvgLatencyMs: 10.0},
	}
	d := newTestDaemon(servers, monitorStatuses)

	// Mark the router's local health as unhealthy to force TargetUnavailable.
	connErr := errors.New("connection refused")
	d.router.RecordFailure("git", router.TargetLocal, connErr)
	d.router.RecordFailure("git", router.TargetLocal, connErr)
	d.router.RecordFailure("git", router.TargetLocal, connErr)

	handler := d.HealthHandler()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	body, _ := io.ReadAll(rr.Body)
	var resp HealthResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Status != "diverged" {
		t.Errorf("expected status 'diverged', got %q", resp.Status)
	}
	if len(resp.Divergence) != 1 {
		t.Fatalf("expected 1 divergence entry, got %d", len(resp.Divergence))
	}
	if resp.Divergence[0].Server != "git" {
		t.Errorf("expected divergence server 'git', got %q", resp.Divergence[0].Server)
	}
	if resp.Divergence[0].Reason != "monitor_healthy_router_unavailable" {
		t.Errorf("expected reason 'monitor_healthy_router_unavailable', got %q", resp.Divergence[0].Reason)
	}
}

func TestHealthHandler_NoDivergence(t *testing.T) {
	servers := []*registry.Server{
		{Name: "git", Categories: []string{"local-only"}},
	}
	monitorStatuses := map[string]*ServerHealthStatus{
		"git": {Name: "git", Healthy: true, AvgLatencyMs: 10.0},
	}
	d := newTestDaemon(servers, monitorStatuses)

	// Router's local health defaults to healthy — should agree with monitor.
	handler := d.HealthHandler()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	body, _ := io.ReadAll(rr.Body)
	var resp HealthResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Status != "healthy" {
		t.Errorf("expected status 'healthy', got %q", resp.Status)
	}
	if len(resp.Divergence) != 0 {
		t.Errorf("expected no divergence, got %d entries", len(resp.Divergence))
	}
}

func TestHealthHandler_NilRouter(t *testing.T) {
	// Daemon without a router should still work (no divergence computed).
	hm := &HealthMonitor{
		statuses: map[string]*ServerHealthStatus{
			"git": {Name: "git", Healthy: true},
		},
	}
	d := &Daemon{
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		healthMonitor: hm,
	}

	handler := d.HealthHandler()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	body, _ := io.ReadAll(rr.Body)
	var resp HealthResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Divergence) != 0 {
		t.Errorf("expected no divergence when router is nil, got %d", len(resp.Divergence))
	}
}
