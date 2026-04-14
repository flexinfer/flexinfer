package daemon

import (
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestSummarizeAuditTraceEntries(t *testing.T) {
	summary := summarizeAuditTraceEntries([]AuditEntry{
		{DurationMs: 20, Status: "success"},
		{DurationMs: 40, Status: "error"},
		{DurationMs: 80, Status: "denied", Cached: true},
	})

	if summary.Count != 3 {
		t.Fatalf("count = %d, want 3", summary.Count)
	}
	if summary.Errors != 1 {
		t.Fatalf("errors = %d, want 1", summary.Errors)
	}
	if summary.Denied != 1 {
		t.Fatalf("denied = %d, want 1", summary.Denied)
	}
	if summary.Cached != 1 {
		t.Fatalf("cached = %d, want 1", summary.Cached)
	}
	if summary.P50Ms != 40 {
		t.Fatalf("p50 = %v, want 40", summary.P50Ms)
	}
	if summary.P95Ms <= 40 || summary.P95Ms > 80 {
		t.Fatalf("p95 = %v, want within (40,80]", summary.P95Ms)
	}
	if summary.Slowest != 80 {
		t.Fatalf("slowest = %d, want 80", summary.Slowest)
	}
}

func TestHandleAuditTraces(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "audit.jsonl")
	audit, err := NewAuditLogger(AuditConfig{Enabled: true, LogPath: logPath}, slog.Default())
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer audit.Close()

	now := time.Date(2026, 4, 14, 21, 0, 0, 0, time.UTC)
	audit.Log(AuditEntry{Timestamp: now, AgentID: "agent-1", Server: "git", Tool: "status", Status: "success", DurationMs: 24, RouteMs: 3, ExecuteMs: 20})
	audit.Log(AuditEntry{Timestamp: now.Add(time.Second), AgentID: "agent-2", Server: "gitlab", Tool: "search", Status: "error", Error: "boom", DurationMs: 88, RouteMs: 10, ExecuteMs: 70})

	d := &Daemon{audit: audit}
	msg, err := mcp.NewRequest("1", "loom/audit-traces", map[string]any{"limit": 1})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := d.handleAuditTraces(t.Context(), msg)
	if err != nil {
		t.Fatalf("handleAuditTraces: %v", err)
	}

	var result struct {
		Enabled bool              `json:"enabled"`
		Count   int               `json:"count"`
		Summary auditTraceSummary `json:"summary"`
		Traces  []AuditEntry      `json:"traces"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if !result.Enabled {
		t.Fatal("expected audit traces to be enabled")
	}
	if result.Count != 1 {
		t.Fatalf("count = %d, want 1", result.Count)
	}
	if len(result.Traces) != 1 {
		t.Fatalf("trace len = %d, want 1", len(result.Traces))
	}
	if result.Traces[0].Tool != "search" {
		t.Fatalf("trace tool = %q, want search", result.Traces[0].Tool)
	}
	if result.Summary.Errors != 1 {
		t.Fatalf("summary errors = %d, want 1", result.Summary.Errors)
	}
}
