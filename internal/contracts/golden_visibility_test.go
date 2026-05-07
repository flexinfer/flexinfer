// Package contracts: golden coverage for the new visibility/contracts DTOs
// (UNIFY-1b, EPIC 2 #66). Mirrors the pattern in golden_test.go.
//
// Each test function exercises one DTO surface from
// internal/visibility/contracts/<surface>. Fixtures populate every field with
// non-zero/non-empty values so a future tag rename or field removal produces a
// visible diff in the matching testdata/visibility_<surface>.golden file.
//
// Mobile v1 envelope/dashboard goldens are intentionally untouched — those
// files freeze the mobile wire format and are owned by golden_test.go.
package contracts

import (
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/visibility/contracts/catalog"
	"github.com/crb2nu/loom/internal/visibility/contracts/cost"
	"github.com/crb2nu/loom/internal/visibility/contracts/health"
	"github.com/crb2nu/loom/internal/visibility/contracts/presence"
	"github.com/crb2nu/loom/internal/visibility/contracts/rbac"
	"github.com/crb2nu/loom/internal/visibility/contracts/status"
)

// fixedSnapshotTime is the canonical timestamp embedded in visibility golden
// fixtures. Hard-coded so re-running -update-golden is deterministic.
var fixedSnapshotTime = time.Date(2026, 5, 6, 14, 30, 0, 0, time.UTC)

// ---------------------------------------------------------------------------
// status.PlatformStatus
// ---------------------------------------------------------------------------

func TestVisibilityStatusPlatformStatusContract(t *testing.T) {
	ps := status.PlatformStatus{
		Daemon: status.DaemonStatus{
			Running:             true,
			Servers:             12,
			ActiveConns:         8,
			IdleConns:           3,
			ActiveRPCs:          27,
			ActiveProxySessions: 4,
			DaemonEpoch:         1746535800,
			DrainReady:          false,
			Draining:            false,
			Processes:           []string{"loom-daemon", "loom-hud"},
		},
		Agents: status.AgentStatus{
			Active:  5,
			Idle:    2,
			Offline: 1,
			Total:   8,
		},
		Sessions: status.SessionCount{
			Active: 3,
			Total:  142,
		},
		Pipelines: status.PipelineStatus{
			Available:    true,
			Running:      2,
			Passed:       18,
			Failed:       1,
			Pending:      0,
			LastActivity: "2026-05-06T14:25:12Z",
		},
		HUD: status.HUDStatus{Reachable: true},
		Health: &status.DaemonHealthSnapshot{
			Servers: map[string]status.DaemonHealthServer{
				"git": {
					Healthy:           true,
					Ready:             true,
					ConsecutiveFails:  0,
					TotalChecks:       1024,
					TotalFailures:     2,
					AvgLatencyMs:      18.5,
					LastError:         "",
					RestartCount:      0,
					LastCheck:         "2026-05-06T14:29:55Z",
					LastHealthy:       "2026-05-06T14:29:55Z",
					LastRestart:       "",
					AutoRestartFailed: false,
					LastDeepProbe:     "2026-05-06T14:00:00Z",
				},
				"github": {
					Healthy:           false,
					Ready:             false,
					ConsecutiveFails:  3,
					TotalChecks:       512,
					TotalFailures:     11,
					AvgLatencyMs:      245.7,
					LastError:         "rate limit exceeded",
					RestartCount:      1,
					LastCheck:         "2026-05-06T14:29:50Z",
					LastHealthy:       "2026-05-06T14:15:00Z",
					LastRestart:       "2026-05-06T14:20:00Z",
					AutoRestartFailed: false,
					LastDeepProbe:     "2026-05-06T14:00:00Z",
				},
			},
			DegradedServers: []string{"github"},
		},
		Observability: &status.DaemonObservabilityStatus{
			OTLPEndpoint:           "http://otel-collector:4317",
			OTLPConfigured:         true,
			LogFormat:              "json",
			JSONLogsEnabled:        true,
			TracedServers:          10,
			TotalServers:           12,
			TraceCoverage:          "83%",
			RuntimeOTLPConfigured:  true,
			RuntimeOTLPEnabled:     true,
			RuntimeOTLPEndpoint:    "http://otel-collector:4318",
			RuntimeOTLPProtocol:    "grpc",
			RuntimeOTLPServiceName: "loom-daemon",
			RuntimeOTLPSampleRate:  0.25,
			RuntimeOTLPError:       "",
			RuntimeMeterEnabled:    true,
			RuntimeTraceSurfaces: map[string]bool{
				"daemon": true,
				"hud":    true,
				"router": false,
			},
			RuntimeTraceCoverage: "67%",
			Warnings:             []string{"sampling below 50%"},
		},
		Healthy: false,
	}
	assertGolden(t, "visibility_status", marshalIndent(t, ps))
}

// ---------------------------------------------------------------------------
// status.DaemonRPCStatus
// ---------------------------------------------------------------------------

func TestVisibilityDaemonRPCStatusContract(t *testing.T) {
	d := status.DaemonRPCStatus{
		Running:     true,
		Servers:     12,
		ActiveConns: 8,
		IdleConns:   3,
		Processes:   []string{"loom-daemon", "loom-hud", "mcp-agent-context"},
	}
	assertGolden(t, "visibility_daemon_rpc", marshalIndent(t, d))
}

// ---------------------------------------------------------------------------
// health.HealthResult
// ---------------------------------------------------------------------------

func TestVisibilityHealthResultContract(t *testing.T) {
	monitor := health.HealthEntry{
		Healthy:      true,
		ConsecFails:  0,
		AvgLatencyMs: 12.3,
		ErrorMessage: "",
	}
	hr := health.HealthResult{
		Servers: map[string]health.ServerHealth{
			"git": {
				Local: health.HealthEntry{
					Healthy:      true,
					ConsecFails:  0,
					AvgLatencyMs: 18.5,
					ErrorMessage: "",
				},
				Hub: health.HealthEntry{
					Healthy:      true,
					ConsecFails:  0,
					AvgLatencyMs: 21.4,
					ErrorMessage: "",
				},
				Monitor:   &monitor,
				Target:    "ws://hub.lan:9090/git",
				Transport: "ws",
				Divergence: &health.HealthDivergence{
					MonitorHealthy:  true,
					RouterAvailable: false,
					Reason:          "router circuit open",
				},
			},
			"github": {
				Local: health.HealthEntry{
					Healthy:      false,
					ConsecFails:  3,
					AvgLatencyMs: 245.7,
					ErrorMessage: "rate limit exceeded",
				},
				Hub: health.HealthEntry{
					Healthy:      false,
					ConsecFails:  3,
					AvgLatencyMs: 250.1,
					ErrorMessage: "rate limit exceeded",
				},
				Target:    "ws://hub.lan:9090/github",
				Transport: "ws",
			},
		},
		Divergence: []health.HealthDivergenceEntry{
			{Server: "git", Reason: "router circuit open"},
		},
	}
	assertGolden(t, "visibility_health", marshalIndent(t, hr))
}

// ---------------------------------------------------------------------------
// cost.CostStatsResult
// ---------------------------------------------------------------------------

func TestVisibilityCostStatsResultContract(t *testing.T) {
	cs := cost.CostStatsResult{
		Enabled:   true,
		Reason:    "telemetry collection enabled",
		Timestamp: "2026-05-06T14:30:00Z",
		ByAgent: []cost.CostAgentUsage{
			{
				AgentID:       "claude-code-1",
				CallCount:     1240,
				ErrorCount:    8,
				DeniedCount:   2,
				CachedCount:   312,
				TotalDuration: 18450,
			},
			{
				AgentID:       "gemini-1",
				CallCount:     420,
				ErrorCount:    3,
				DeniedCount:   1,
				CachedCount:   98,
				TotalDuration: 6210,
			},
		},
		ByServer: []cost.CostServerUsage{
			{
				Server:        "agent-context",
				CallCount:     1100,
				ErrorCount:    5,
				TotalDuration: 12340,
			},
			{
				Server:        "git",
				CallCount:     560,
				ErrorCount:    6,
				TotalDuration: 12320,
			},
		},
		Totals: cost.CostTotals{
			CallCount:     1660,
			ErrorCount:    11,
			DeniedCount:   3,
			CachedCount:   410,
			TotalDuration: 24660,
		},
	}
	assertGolden(t, "visibility_cost", marshalIndent(t, cs))
}

// ---------------------------------------------------------------------------
// rbac.Snapshot
// ---------------------------------------------------------------------------

func TestVisibilityRBACSnapshotContract(t *testing.T) {
	snap := rbac.Snapshot{
		PolicyVersion:  "policy-2026-05-06-r1",
		DeniedCount24h: 7,
		AuditEnabled:   true,
		SimulationMode: false,
		RecentDenials: []rbac.Denial{
			{
				Time:     fixedSnapshotTime.Add(-15 * time.Minute),
				Actor:    "claude-code-1",
				Resource: "k8s_apply",
				Reason:   "namespace not in allowlist",
			},
			{
				Time:     fixedSnapshotTime.Add(-2 * time.Hour),
				Actor:    "gemini-1",
				Resource: "shell.exec",
				Reason:   "command pattern blocked",
			},
		},
	}
	assertGolden(t, "visibility_rbac", marshalIndent(t, snap))
}

// ---------------------------------------------------------------------------
// catalog.Status
// ---------------------------------------------------------------------------

func TestVisibilityCatalogStatusContract(t *testing.T) {
	cs := catalog.Status{
		Servers: []catalog.Entry{
			{
				Name:        "agent-context",
				Enabled:     true,
				LastError:   "",
				Description: "Agent context, sessions, tasks, and memory",
			},
			{
				Name:        "github",
				Enabled:     true,
				LastError:   "rate limit exceeded",
				Description: "GitHub repos, issues, and PRs",
			},
			{
				Name:        "deprecated-server",
				Enabled:     false,
				LastError:   "missing config",
				Description: "Disabled until config restored",
			},
		},
		LastSyncTime: fixedSnapshotTime,
	}
	assertGolden(t, "visibility_catalog", marshalIndent(t, cs))
}

// ---------------------------------------------------------------------------
// sessions — bridge.SessionInfo (canonical type still in bridge per
// internal/visibility/contracts/sessions/types.go scaffold note; this golden
// freezes the wire shape that future slices will move into the sessions
// package without changing JSON output).
// ---------------------------------------------------------------------------

func TestVisibilitySessionsSessionInfoContract(t *testing.T) {
	s := bridge.SessionInfo{
		ID:          "sess_unify_001",
		AgentID:     "claude-code-1",
		Namespace:   "services/loom-core/unify",
		Project:     "services/loom-core",
		StartedAt:   "2026-05-06T13:00:00Z",
		EndedAt:     "2026-05-06T14:25:00Z",
		Status:      "ended",
		Description: "Implementing UNIFY-1b golden coverage",
		PipelineRef: &bridge.PipelineRef{
			ID:      98765,
			Project: "services/loom-core",
			Ref:     "feat/unify-1b-contracts-golden",
			WebURL:  "https://gitlab.example.com/services/loom-core/-/pipelines/98765",
		},
		EntryCount:      57,
		TotalTokens:     12480,
		ParentSessionID: "sess_unify_root",
		RootSessionID:   "sess_unify_root",
	}
	assertGolden(t, "visibility_sessions", marshalIndent(t, s))
}

// ---------------------------------------------------------------------------
// tasks — bridge.TaskInfo (canonical type still in bridge per
// internal/visibility/contracts/tasks/types.go scaffold note; future slices
// will move it into the tasks package without changing JSON output).
// ---------------------------------------------------------------------------

func TestVisibilityTasksTaskInfoContract(t *testing.T) {
	task := bridge.TaskInfo{
		ID:        "task_unify_1b_001",
		SessionID: "sess_unify_001",
		AgentID:   "claude-code-1",
		Namespace: "services/loom-core/unify",
		Project:   "services/loom-core",
		Title:     "Add visibility/contracts golden coverage",
		Context:   "Mirror existing contracts/golden_test.go pattern; 9 surfaces.",
		Priority:  "high",
		Status:    "in_progress",
		Tags:      []string{"epic-2", "unify", "contracts", "golden"},
		BlockedBy: []string{"task_unify_1a_scaffold"},
		PipelineRef: &bridge.PipelineRef{
			ID:      98765,
			Project: "services/loom-core",
			Ref:     "feat/unify-1b-contracts-golden",
			WebURL:  "https://gitlab.example.com/services/loom-core/-/pipelines/98765",
		},
		WorkflowID: "wf_unify_1b_2026_05_06",
		CreatedAt:  "2026-05-06T13:05:00Z",
		UpdatedAt:  "2026-05-06T14:25:00Z",
	}
	assertGolden(t, "visibility_tasks", marshalIndent(t, task))
}

// ---------------------------------------------------------------------------
// presence.PresenceInfo
// ---------------------------------------------------------------------------

func TestVisibilityPresenceInfoContract(t *testing.T) {
	p := presence.PresenceInfo{
		AgentID:             "claude-code-1",
		SessionID:           "sess_unify_001",
		Status:              "active",
		AgentType:           "claude-code",
		Description:         "Implementing UNIFY-1b golden coverage",
		CurrentTask:         "Seed visibility golden files",
		ActiveFiles:         []string{"internal/contracts/golden_visibility_test.go", "internal/contracts/testdata/visibility_status.golden"},
		Branch:              "feat/unify-1b-contracts-golden",
		PRUrl:               "https://gitlab.example.com/services/loom-core/-/merge_requests/12345",
		WorktreeID:          "wt_unify_1b_001",
		LastHeartbeat:       "2026-05-06T14:29:55Z",
		RegisteredAt:        "2026-05-06T13:00:00Z",
		Source:              "presence",
		HasPresence:         true,
		HasSession:          true,
		SessionStatus:       "active",
		SessionStartedAt:    "2026-05-06T13:00:00Z",
		HeartbeatAgeSeconds: 5,
		SessionAgeSeconds:   5400,
		TelemetryStatus:     "healthy",
		IsOrphan:            false,
		OrphanAgeSeconds:    0,
	}
	assertGolden(t, "visibility_presence", marshalIndent(t, p))
}
