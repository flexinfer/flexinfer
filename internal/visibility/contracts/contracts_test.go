// Package contracts_test validates that the lifted DTOs in
// internal/visibility/contracts/* produce the exact JSON wire format that
// downstream consumers (HUD, CLI, mobile, VS Code extension) expect.
//
// These are JSON round-trip tests with explicit field-name assertions. They
// catch accidental tag renames during the lift. Byte-identity for the
// frozen mobile v1 wire format is asserted separately by
// internal/contracts/golden_test.go (`make ci-contracts`).
package contracts_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/visibility/contracts/catalog"
	"github.com/crb2nu/loom/internal/visibility/contracts/cost"
	"github.com/crb2nu/loom/internal/visibility/contracts/health"
	"github.com/crb2nu/loom/internal/visibility/contracts/presence"
	"github.com/crb2nu/loom/internal/visibility/contracts/rbac"
	"github.com/crb2nu/loom/internal/visibility/contracts/status"
)

// requireFields asserts that every name in want appears as a top-level JSON
// key in got. It does NOT assert exhaustiveness (omitempty fields may be
// dropped) — the goal is to catch silent renames, not to enforce shape.
func requireFields(t *testing.T, got []byte, want ...string) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal back into map: %v\nbody: %s", err, got)
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("missing required JSON key %q in body: %s", k, got)
		}
	}
}

// roundTrip marshals v to JSON, unmarshals into a fresh value of the same
// type, and reports both the bytes and any decode error. It catches the
// "tag changed but consumer breaks silently" class of regression.
func roundTrip[T any](t *testing.T, v T) ([]byte, T) {
	t.Helper()
	var zero T
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back T
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("round-trip unmarshal: %v\nbody: %s", err, body)
	}
	_ = zero
	return body, back
}

func TestStatusPlatformStatus_FieldNames(t *testing.T) {
	ps := status.PlatformStatus{
		Daemon:   status.DaemonStatus{Running: true, Servers: 3},
		Agents:   status.AgentStatus{Active: 2, Total: 2},
		Sessions: status.SessionCount{Active: 1, Total: 5},
		Pipelines: status.PipelineStatus{
			Available: true, Running: 1, Passed: 4, Failed: 0,
		},
		HUD:     status.HUDStatus{Reachable: true},
		Healthy: true,
	}
	body, _ := roundTrip(t, ps)
	requireFields(t, body,
		"daemon", "agents", "sessions", "pipelines", "hud", "healthy",
	)
}

func TestStatusDaemonStatus_FieldNames(t *testing.T) {
	d := status.DaemonStatus{
		Running:             true,
		Servers:             7,
		ActiveConns:         3,
		IdleConns:           2,
		ActiveRPCs:          11,
		ActiveProxySessions: 1,
		DaemonEpoch:         123456,
		DrainReady:          true,
		Draining:            false,
		Processes:           []string{"mcp-foo", "mcp-bar"},
	}
	body, _ := roundTrip(t, d)
	requireFields(t, body,
		"running", "servers", "active_conns", "idle_conns",
		"active_rpcs", "active_proxy_sessions", "daemon_epoch",
		"drain_ready", "draining", "processes",
	)
}

func TestStatusDaemonHealthServer_FieldNames(t *testing.T) {
	hs := status.DaemonHealthServer{
		Healthy:          true,
		Ready:            true,
		ConsecutiveFails: 0,
		TotalChecks:      10,
		TotalFailures:    1,
		AvgLatencyMs:     12.5,
		RestartCount:     0,
	}
	body, _ := roundTrip(t, hs)
	requireFields(t, body,
		"healthy", "ready", "consecutive_fails", "total_checks",
		"total_failures", "avg_latency_ms", "restart_count",
	)
}

func TestHealthHealthEntry_FieldNames(t *testing.T) {
	h := health.HealthEntry{
		Healthy:      true,
		ConsecFails:  2,
		AvgLatencyMs: 8.4,
		ErrorMessage: "transient: timeout",
	}
	body, _ := roundTrip(t, h)
	// Note: the health package uses camelCase JSON tags inherited from the
	// pre-existing daemon/health wire format. Don't "normalize" these — the
	// HUD JS consumer reads them as-is.
	requireFields(t, body,
		"healthy", "consecFails", "avgLatencyMs", "errorMessage",
	)
}

func TestHealthHealthResult_FieldNames(t *testing.T) {
	r := health.HealthResult{
		Servers: map[string]health.ServerHealth{
			"mcp-foo": {
				Local:     health.HealthEntry{Healthy: true},
				Hub:       health.HealthEntry{Healthy: true},
				Target:    "ws://localhost:1",
				Transport: "ws",
			},
		},
		Divergence: []health.HealthDivergenceEntry{
			{Server: "mcp-bar", Reason: "router says down"},
		},
	}
	body, _ := roundTrip(t, r)
	requireFields(t, body, "servers", "divergence")
	// And the nested entry's keys.
	if !strings.Contains(string(body), `"local":`) ||
		!strings.Contains(string(body), `"hub":`) ||
		!strings.Contains(string(body), `"target":`) {
		t.Errorf("nested ServerHealth missing local/hub/target keys: %s", body)
	}
}

func TestCostStatsResult_FieldNames(t *testing.T) {
	r := cost.CostStatsResult{
		Enabled:   true,
		Reason:    "",
		Timestamp: "2026-05-06T12:00:00Z",
		ByAgent: []cost.CostAgentUsage{
			{AgentID: "claude-code", CallCount: 12, TotalDuration: 4200},
		},
		ByServer: []cost.CostServerUsage{
			{Server: "mcp-foo", CallCount: 7},
		},
		Totals: cost.CostTotals{CallCount: 12},
	}
	body, _ := roundTrip(t, r)
	requireFields(t, body, "enabled", "by_agent", "by_server", "totals")
	if !strings.Contains(string(body), `"agent_id":"claude-code"`) {
		t.Errorf("CostAgentUsage agent_id tag missing: %s", body)
	}
	if !strings.Contains(string(body), `"total_duration_ms":4200`) {
		t.Errorf("CostAgentUsage total_duration_ms tag missing: %s", body)
	}
}

func TestPresenceInfo_FieldNames(t *testing.T) {
	p := presence.PresenceInfo{
		AgentID:       "claude-code",
		SessionID:     "sess-1",
		Status:        "active",
		AgentType:     "claude",
		Description:   "implementing slice S1",
		CurrentTask:   "scaffold contracts",
		ActiveFiles:   []string{"foo.go"},
		Branch:        "feat/unify-1a-contracts-scaffold",
		WorktreeID:    "wt-1",
		LastHeartbeat: "2026-05-06T12:00:00Z",
		RegisteredAt:  "2026-05-06T11:55:00Z",
		IsOrphan:      false,
	}
	body, _ := roundTrip(t, p)
	requireFields(t, body,
		"agent_id", "session_id", "status", "agent_type",
		"description", "current_task", "active_files", "branch",
		"worktree_id", "last_heartbeat", "registered_at",
	)
}

func TestRBACSnapshot_FieldNames(t *testing.T) {
	s := rbac.Snapshot{
		PolicyVersion:  "v1.2.0",
		DeniedCount24h: 3,
		AuditEnabled:   true,
		SimulationMode: false,
		RecentDenials: []rbac.Denial{
			{
				Time:     time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC),
				Actor:    "claude-code",
				Resource: "tools/call:dangerous_tool",
				Reason:   "role denies",
			},
		},
	}
	body, _ := roundTrip(t, s)
	requireFields(t, body,
		"denied_count_24h", "audit_enabled", "simulation_mode",
		"recent_denials",
	)
	if !strings.Contains(string(body), `"actor":"claude-code"`) {
		t.Errorf("Denial actor tag missing: %s", body)
	}
}

func TestCatalogStatus_FieldNames(t *testing.T) {
	c := catalog.Status{
		Servers: []catalog.Entry{
			{Name: "mcp-foo", Enabled: true, Description: "demo"},
			{Name: "mcp-bar", Enabled: false, LastError: "boom"},
		},
		LastSyncTime: time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC),
	}
	body, _ := roundTrip(t, c)
	requireFields(t, body, "servers", "last_sync_time")
	if !strings.Contains(string(body), `"name":"mcp-foo"`) {
		t.Errorf("catalog Entry name tag missing: %s", body)
	}
}
