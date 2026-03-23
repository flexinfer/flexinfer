package hud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCollectAgentContextTelemetry(t *testing.T) {
	app, _, _ := newTestAppWithHandlers(t)

	sample, err := app.collectAgentContextTelemetry("codex-test", "sess-mock", "codex", contextTelemetryReasonHeartbeat)
	if err != nil {
		t.Fatalf("collectAgentContextTelemetry() error = %v", err)
	}
	if sample.AgentType != "codex" {
		t.Fatalf("AgentType = %q, want codex", sample.AgentType)
	}
	if sample.SessionID != "sess-mock" {
		t.Fatalf("SessionID = %q, want sess-mock", sample.SessionID)
	}
	if sample.ToolSchemaTokens <= 0 {
		t.Fatalf("ToolSchemaTokens = %d, want > 0", sample.ToolSchemaTokens)
	}
}

func TestRecordAgentContextTelemetryUpdatesMetricsAndEventLog(t *testing.T) {
	app, mux, _ := newTestAppWithHandlers(t)

	app.recordAgentContextTelemetry("codex-test", "sess-mock", "codex", contextTelemetryReasonHeartbeat)

	entries := app.eventLog.All(5)
	if len(entries) == 0 {
		t.Fatalf("expected telemetry event to be appended")
	}
	if entries[0].EventType != contextTelemetryEventType {
		t.Fatalf("EventType = %q, want %q", entries[0].EventType, contextTelemetryEventType)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agent/metrics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/agent/metrics = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "loom_hud_agent_context_prompt_estimated_tokens") {
		t.Fatalf("metrics body missing prompt metric: %s", body)
	}
	if !strings.Contains(body, "agent_type=\"codex\"") {
		t.Fatalf("metrics body missing codex label: %s", body)
	}
}

func TestAgentContextLatestStoreListFiltersAndLimits(t *testing.T) {
	store := NewAgentContextLatestStore()
	store.Record(AgentContextTelemetrySnapshot{
		AgentID:     "codex-a",
		AgentType:   "codex",
		SessionID:   "sess-a",
		Reason:      contextTelemetryReasonHeartbeat,
		RetrievedAt: "2026-03-17T20:00:00Z",
	})
	store.Record(AgentContextTelemetrySnapshot{
		AgentID:     "claude-a",
		AgentType:   "claude",
		SessionID:   "sess-b",
		Reason:      contextTelemetryReasonContextAdd,
		RetrievedAt: "2026-03-17T20:01:00Z",
	})

	filtered := store.List(AgentContextTelemetryFilter{AgentType: "claude", Limit: 1})
	if len(filtered) != 1 {
		t.Fatalf("len(filtered) = %d, want 1", len(filtered))
	}
	if filtered[0].AgentID != "claude-a" {
		t.Fatalf("AgentID = %q, want claude-a", filtered[0].AgentID)
	}
}

func TestHandleAgentContextTelemetryReturnsSnapshots(t *testing.T) {
	app, mux, _ := newTestAppWithHandlers(t)
	app.recordAgentContextTelemetry("codex-test", "sess-mock", "codex", contextTelemetryReasonHeartbeat)

	req := httptest.NewRequest(http.MethodGet, "/api/agent/context-telemetry?agent_id=codex-test&limit=1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/agent/context-telemetry = %d: %s", w.Code, w.Body.String())
	}

	var payload struct {
		Count     int                             `json:"count"`
		Snapshots []AgentContextTelemetrySnapshot `json:"snapshots"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload.Count != 1 || len(payload.Snapshots) != 1 {
		t.Fatalf("got count=%d len=%d, want 1", payload.Count, len(payload.Snapshots))
	}
	if payload.Snapshots[0].AgentID != "codex-test" {
		t.Fatalf("AgentID = %q, want codex-test", payload.Snapshots[0].AgentID)
	}
}
