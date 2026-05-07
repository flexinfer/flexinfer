package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	health "github.com/crb2nu/loom/internal/visibility/contracts/health"
)

func sampleHealthSnapshot() *health.HealthResult {
	return &health.HealthResult{
		Servers: map[string]health.ServerHealth{
			"github": {
				Local:     health.HealthEntry{Healthy: true, AvgLatencyMs: 12.4},
				Hub:       health.HealthEntry{Healthy: true, AvgLatencyMs: 18.0},
				Target:    "ws://hub.flexinfer.ai/mcp/github",
				Transport: "ws",
			},
			"gitlab": {
				Local:     health.HealthEntry{Healthy: false, AvgLatencyMs: 0, ErrorMessage: "dial timeout"},
				Hub:       health.HealthEntry{Healthy: true, AvgLatencyMs: 22.5},
				Target:    "ws://hub.flexinfer.ai/mcp/gitlab",
				Transport: "ws",
				Divergence: &health.HealthDivergence{
					MonitorHealthy:  false,
					RouterAvailable: true,
					Reason:          "monitor_failed_router_ok",
				},
			},
		},
		Divergence: []health.HealthDivergenceEntry{
			{Server: "gitlab", Reason: "monitor_failed_router_ok"},
		},
	}
}

func TestRunHealthCommand_JSONRoundTrips(t *testing.T) {
	t.Parallel()

	want := sampleHealthSnapshot()
	fetch := func(_ string) (*health.HealthResult, error) { return want, nil }

	var buf bytes.Buffer
	if err := runHealthCommand(context.Background(), &buf, "/dev/null", true, 0, fetch); err != nil {
		t.Fatalf("runHealthCommand: %v", err)
	}

	var got health.HealthResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal output: %v\noutput: %s", err, buf.String())
	}
	if len(got.Servers) != len(want.Servers) {
		t.Errorf("servers len = %d, want %d", len(got.Servers), len(want.Servers))
	}
	if len(got.Divergence) != 1 {
		t.Errorf("divergence len = %d, want 1", len(got.Divergence))
	}
}

func TestRunHealthCommand_TextTable(t *testing.T) {
	t.Parallel()

	fetch := func(_ string) (*health.HealthResult, error) { return sampleHealthSnapshot(), nil }
	var buf bytes.Buffer
	if err := runHealthCommand(context.Background(), &buf, "/dev/null", false, 0, fetch); err != nil {
		t.Fatalf("runHealthCommand: %v", err)
	}
	got := buf.String()

	wants := []string{
		"SERVER",
		"LOCAL",
		"HUB",
		"TRANSPORT",
		"github",
		"gitlab",
		"ok",
		"down",
		"Divergences:",
		"monitor_failed_router_ok",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output missing %q\nfull output:\n%s", w, got)
		}
	}
}

func TestRunHealthCommand_NoServers(t *testing.T) {
	t.Parallel()

	fetch := func(_ string) (*health.HealthResult, error) { return &health.HealthResult{}, nil }
	var buf bytes.Buffer
	if err := runHealthCommand(context.Background(), &buf, "/dev/null", false, 0, fetch); err != nil {
		t.Fatalf("runHealthCommand: %v", err)
	}
	if !strings.Contains(buf.String(), "No servers") {
		t.Errorf("expected 'No servers' in output, got:\n%s", buf.String())
	}
}

func TestRunHealthCommand_FetchErrorIsNonZero(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("connection refused")
	fetch := func(_ string) (*health.HealthResult, error) { return nil, wantErr }

	var buf bytes.Buffer
	err := runHealthCommand(context.Background(), &buf, "/dev/null", false, 0, fetch)
	if err == nil {
		t.Fatalf("expected error from fetch failure")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error chain missing fetch error: %v", err)
	}
	if !strings.Contains(err.Error(), "daemon unreachable") {
		t.Errorf("error missing daemon-unreachable hint: %v", err)
	}
}

func TestHealthBool(t *testing.T) {
	t.Parallel()

	if got := healthBool(true); got != "ok" {
		t.Errorf("healthBool(true) = %q, want ok", got)
	}
	if got := healthBool(false); got != "down" {
		t.Errorf("healthBool(false) = %q, want down", got)
	}
}

func TestEmptyDash(t *testing.T) {
	t.Parallel()

	if got := emptyDash(""); got != "-" {
		t.Errorf("emptyDash(empty) = %q, want -", got)
	}
	if got := emptyDash("ws"); got != "ws" {
		t.Errorf("emptyDash(ws) = %q, want ws", got)
	}
}

func TestHealthLatency_PrefersLocal(t *testing.T) {
	t.Parallel()

	s := health.ServerHealth{
		Local: health.HealthEntry{AvgLatencyMs: 10.5},
		Hub:   health.HealthEntry{AvgLatencyMs: 22.0},
	}
	if got := healthLatency(s); got != "10.5" {
		t.Errorf("healthLatency(local+hub) = %q, want 10.5", got)
	}

	s = health.ServerHealth{Hub: health.HealthEntry{AvgLatencyMs: 22.0}}
	if got := healthLatency(s); got != "22.0" {
		t.Errorf("healthLatency(hub-only) = %q, want 22.0", got)
	}

	if got := healthLatency(health.ServerHealth{}); got != "-" {
		t.Errorf("healthLatency(empty) = %q, want -", got)
	}
}
