package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// mockSSEBroadcaster captures broadcast events for testing.
type mockSSEBroadcaster struct {
	events []bridge.SSEEvent
}

func (m *mockSSEBroadcaster) Broadcast(event bridge.SSEEvent) {
	m.events = append(m.events, event)
}

func TestNewCoordinator_NilWhenDisabled(t *testing.T) {
	cfg := DefaultConfig()
	// FlexInferURL is empty, so coordinator should be nil.
	c := NewCoordinator(cfg, nil, nil, slog.Default())
	if c != nil {
		t.Fatal("expected nil coordinator when disabled")
	}
}

func TestNewCoordinator_CreatesSubsystems(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FlexInferURL = "http://localhost:9999"

	sse := &mockSSEBroadcaster{}
	c := NewCoordinator(cfg, nil, sse, slog.Default())

	if c == nil {
		t.Fatal("expected non-nil coordinator")
	}
	if c.summarizer == nil {
		t.Error("expected summarizer to be initialized")
	}
	if c.compressor == nil {
		t.Error("expected compressor to be initialized")
	}
	if c.triager == nil {
		t.Error("expected triager to be initialized")
	}
	if c.extractor == nil {
		t.Error("expected extractor to be initialized")
	}
	if c.planner == nil {
		t.Error("expected planner to be initialized")
	}
}

func TestNewCoordinator_DisabledSubsystems(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FlexInferURL = "http://localhost:9999"
	cfg.EnableSummarizer = false
	cfg.EnableCompressor = false
	cfg.EnableTriager = false
	cfg.EnableExtractor = false
	cfg.EnablePlanner = false

	c := NewCoordinator(cfg, nil, nil, slog.Default())

	if c.summarizer != nil {
		t.Error("expected summarizer to be nil when disabled")
	}
	if c.compressor != nil {
		t.Error("expected compressor to be nil when disabled")
	}
	if c.triager != nil {
		t.Error("expected triager to be nil when disabled")
	}
	if c.extractor != nil {
		t.Error("expected extractor to be nil when disabled")
	}
	if c.planner != nil {
		t.Error("expected planner to be nil when disabled")
	}
}

func TestCoordinator_Status(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FlexInferURL = "http://localhost:9999"

	c := NewCoordinator(cfg, nil, nil, slog.Default())
	c.healthy = true
	c.models = []string{"qwen3-8b", "llama3-70b"}

	status := c.Status()

	if !status.Enabled {
		t.Error("expected enabled")
	}
	if !status.Healthy {
		t.Error("expected healthy")
	}
	if status.Model != "qwen3-8b" {
		t.Errorf("expected model qwen3-8b, got %s", status.Model)
	}
	if status.CircuitState != "closed" {
		t.Errorf("expected circuit closed, got %s", status.CircuitState)
	}
	if !status.Subsystems.Summarizer {
		t.Error("expected summarizer enabled")
	}
	if len(status.AvailableModels) != 2 {
		t.Errorf("expected 2 models, got %d", len(status.AvailableModels))
	}
}

func TestCoordinator_SelectModel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FlexInferURL = "http://localhost:9999"
	cfg.DefaultModel = "qwen3-8b"
	cfg.FallbackModel = "llama3-8b"

	c := NewCoordinator(cfg, nil, nil, slog.Default())
	c.models = []string{"qwen3-8b", "llama3-70b"}

	// Preferred is available.
	if m := c.selectModel("qwen3-8b"); m != "qwen3-8b" {
		t.Errorf("expected qwen3-8b, got %s", m)
	}

	// Preferred not available, fallback not available, returns preferred.
	if m := c.selectModel("missing-model"); m != "missing-model" {
		t.Errorf("expected missing-model, got %s", m)
	}

	// Empty preferred, uses default.
	if m := c.selectModel(""); m != "qwen3-8b" {
		t.Errorf("expected default qwen3-8b, got %s", m)
	}

	// Fallback is used when default missing.
	c.models = []string{"llama3-8b"}
	c.config.DefaultModel = "nonexistent"
	if m := c.selectModel(""); m != "llama3-8b" {
		t.Errorf("expected fallback llama3-8b, got %s", m)
	}
}

func TestCoordinator_BroadcastEvent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FlexInferURL = "http://localhost:9999"

	sse := &mockSSEBroadcaster{}
	c := NewCoordinator(cfg, nil, sse, slog.Default())

	c.broadcastEvent("coordinator.test", map[string]any{"key": "value"})

	if len(sse.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sse.events))
	}
	if sse.events[0].Type != "coordinator.test" {
		t.Errorf("expected event type coordinator.test, got %s", sse.events[0].Type)
	}
}

func TestCoordinator_StartAndStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(modelsResponse{
			Data: []ModelInfo{{ID: "qwen3-8b"}},
		})
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.FlexInferURL = server.URL
	cfg.PollInterval = 100 * time.Millisecond

	sse := &mockSSEBroadcaster{}
	c := NewCoordinator(cfg, nil, sse, slog.Default())

	if err := c.Start(); err != nil {
		t.Fatalf("unexpected start error: %v", err)
	}

	// Let it poll once.
	time.Sleep(50 * time.Millisecond)

	status := c.Status()
	if !status.Healthy {
		t.Error("expected healthy after start")
	}

	c.Stop()
}

func TestCoordinator_StartHealthyWhenModelCacheRefreshFails(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(modelsResponse{
				Data: []ModelInfo{{ID: "fast-chat"}},
			})
			return
		}
		http.Error(w, "temporary model list failure", http.StatusBadGateway)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.FlexInferURL = server.URL
	cfg.PollInterval = 100 * time.Millisecond

	c := NewCoordinator(cfg, nil, nil, slog.Default())
	if err := c.Start(); err != nil {
		t.Fatalf("unexpected start error: %v", err)
	}
	defer c.Stop()

	status := c.Status()
	if !status.Healthy {
		t.Fatal("expected healthy status after successful startup health check")
	}
}

func TestCoordinator_StartFails_Unreachable(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FlexInferURL = "http://127.0.0.1:1"

	c := NewCoordinator(cfg, nil, nil, slog.Default())
	err := c.Start()
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestCoordinator_SemaphoreNonBlocking(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FlexInferURL = "http://localhost:9999"
	cfg.MaxConcurrentLLM = 1

	c := NewCoordinator(cfg, nil, nil, slog.Default())

	// Acquire the only slot.
	if !c.acquireSem() {
		t.Fatal("expected to acquire semaphore")
	}

	// Second acquire should fail (non-blocking).
	if c.acquireSem() {
		t.Fatal("expected semaphore to be full")
	}

	// Release and re-acquire.
	c.releaseSem()
	if !c.acquireSem() {
		t.Fatal("expected to acquire after release")
	}
	c.releaseSem()
}

func TestAdjustInterval_BackoffOnFailure(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FlexInferURL = "http://localhost:9999"
	cfg.PollInterval = 30 * time.Second

	c := NewCoordinator(cfg, nil, nil, slog.Default())

	// Success resets to base interval.
	if got := c.adjustInterval(true); got != 30*time.Second {
		t.Fatalf("expected 30s on success, got %s", got)
	}

	// First failure → 2× base.
	if got := c.adjustInterval(false); got != 60*time.Second {
		t.Fatalf("expected 60s after 1 failure, got %s", got)
	}

	// Second failure → 4× base.
	if got := c.adjustInterval(false); got != 120*time.Second {
		t.Fatalf("expected 120s after 2 failures, got %s", got)
	}

	// Third failure → 8× base = 240s.
	if got := c.adjustInterval(false); got != 240*time.Second {
		t.Fatalf("expected 240s after 3 failures, got %s", got)
	}

	// Capped at 5 minutes.
	c.adjustInterval(false)
	got := c.adjustInterval(false)
	if got > 5*time.Minute {
		t.Fatalf("expected cap at 5min, got %s", got)
	}

	// Success resets.
	if got := c.adjustInterval(true); got != 30*time.Second {
		t.Fatalf("expected reset to 30s on success, got %s", got)
	}
}

func TestErrUnavailable_WhenCircuitOpen(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FlexInferURL = "http://localhost:9999"
	cfg.CircuitBreakerThreshold = 1

	c := NewCoordinator(cfg, nil, nil, slog.Default())

	// Trip the circuit breaker.
	c.client.breaker.Execute(func() error { return fmt.Errorf("fail") })

	// All API methods should return ErrUnavailable.
	_, err := c.PlanWorkflow(context.TODO(), "test", "")
	if err != ErrUnavailable {
		t.Errorf("PlanWorkflow: expected ErrUnavailable, got %v", err)
	}

	_, err = c.SummarizeSession(context.TODO(), "test-session")
	if err != ErrUnavailable {
		t.Errorf("SummarizeSession: expected ErrUnavailable, got %v", err)
	}

	_, err = c.RunCompression(context.TODO())
	if err != ErrUnavailable {
		t.Errorf("RunCompression: expected ErrUnavailable, got %v", err)
	}
}

func TestErrUnavailable_WhenSemaphoreFull(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FlexInferURL = "http://localhost:9999"
	cfg.MaxConcurrentLLM = 1

	c := NewCoordinator(cfg, nil, nil, slog.Default())

	// Fill the semaphore.
	c.acquireSem()
	defer c.releaseSem()

	// API methods should return ErrUnavailable when semaphore is full.
	_, err := c.PlanWorkflow(context.TODO(), "test", "")
	if err != ErrUnavailable {
		t.Errorf("PlanWorkflow: expected ErrUnavailable, got %v", err)
	}
}

func TestDefaultConfig_SafetyCaps(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxSweepSessions != 2 {
		t.Errorf("expected MaxSweepSessions=2, got %d", cfg.MaxSweepSessions)
	}
	if cfg.MaxCompressItems != 3 {
		t.Errorf("expected MaxCompressItems=3, got %d", cfg.MaxCompressItems)
	}
	if cfg.CompactionPromptTokenThreshold != 12000 {
		t.Errorf("expected CompactionPromptTokenThreshold=12000, got %d", cfg.CompactionPromptTokenThreshold)
	}
	if cfg.CompactionInspectSessions != 3 {
		t.Errorf("expected CompactionInspectSessions=3, got %d", cfg.CompactionInspectSessions)
	}
	if cfg.CircuitBreakerThreshold != 3 {
		t.Errorf("expected CircuitBreakerThreshold=3, got %d", cfg.CircuitBreakerThreshold)
	}
	if cfg.SubsystemTimeout != 15*time.Second {
		t.Errorf("expected SubsystemTimeout=15s, got %s", cfg.SubsystemTimeout)
	}
}

func TestHasCompactionWork_AllowsMergeOnlyResults(t *testing.T) {
	t.Parallel()

	if hasCompactionWork(nil) {
		t.Fatal("expected nil result to report no work")
	}
	if hasCompactionWork(&CompactionResult{}) {
		t.Fatal("expected empty result to report no work")
	}
	if !hasCompactionWork(&CompactionResult{MergedCount: 1}) {
		t.Fatal("expected merge-only result to report work")
	}
	if !hasCompactionWork(&CompactionResult{CompressedCount: 1}) {
		t.Fatal("expected compressed result to report work")
	}
}

func TestCompactionEventPayload_IncludesPromptPressureFields(t *testing.T) {
	t.Parallel()

	payload := compactionEventPayload(&CompactionResult{
		Tier:                    "working",
		Trigger:                 "prompt_pressure",
		CompressedCount:         1,
		MergedCount:             2,
		TokensSaved:             3000,
		PressureSessionID:       "sess-1",
		PressureAgentID:         "codex",
		PressureNamespace:       "loom-core/context-telemetry",
		PromptTokensBefore:      15000,
		PromptTokensAfter:       11800,
		PromptTokensDelta:       3200,
		PressureEstimatedTokens: 15000,
	})

	if got := payload["merged_count"]; got != 2 {
		t.Fatalf("expected merged_count=2, got %#v", got)
	}
	if got := payload["prompt_tokens_before"]; got != 15000 {
		t.Fatalf("expected prompt_tokens_before=15000, got %#v", got)
	}
	if got := payload["prompt_tokens_after"]; got != 11800 {
		t.Fatalf("expected prompt_tokens_after=11800, got %#v", got)
	}
	if got := payload["prompt_tokens_delta"]; got != 3200 {
		t.Fatalf("expected prompt_tokens_delta=3200, got %#v", got)
	}
	if got := payload["pressure_session_id"]; got != "sess-1" {
		t.Fatalf("expected pressure_session_id=sess-1, got %#v", got)
	}
}

func TestEnrichSummaryPromptDelta(t *testing.T) {
	t.Parallel()

	result := &SessionSummaryResult{SessionID: "sess-1", Summary: "done"}
	before := &bridge.ContextInspectResult{EstimatedTokens: 9600}
	after := &bridge.ContextInspectResult{EstimatedTokens: 10100}

	enrichSummaryPromptDelta(result, "codex", before, after)

	if result.AgentID != "codex" {
		t.Fatalf("AgentID = %q, want codex", result.AgentID)
	}
	if result.PromptTokensBefore != 9600 {
		t.Fatalf("PromptTokensBefore = %d, want 9600", result.PromptTokensBefore)
	}
	if result.PromptTokensAfter != 10100 {
		t.Fatalf("PromptTokensAfter = %d, want 10100", result.PromptTokensAfter)
	}
	if result.PromptTokensDelta != -500 {
		t.Fatalf("PromptTokensDelta = %d, want -500", result.PromptTokensDelta)
	}
}

func TestSummaryEventPayload_IncludesPromptTelemetry(t *testing.T) {
	t.Parallel()

	payload := summaryEventPayload(&SessionSummaryResult{
		SessionID:          "sess-1",
		AgentID:            "claude-code",
		Summary:            "Wrapped up session summary ownership cleanup.",
		PromptTokensBefore: 9600,
		PromptTokensAfter:  10100,
		PromptTokensDelta:  -500,
	})

	if got := payload["session_id"]; got != "sess-1" {
		t.Fatalf("session_id = %#v, want sess-1", got)
	}
	if got := payload["agent_id"]; got != "claude-code" {
		t.Fatalf("agent_id = %#v, want claude-code", got)
	}
	if got := payload["prompt_tokens_before"]; got != 9600 {
		t.Fatalf("prompt_tokens_before = %#v, want 9600", got)
	}
	if got := payload["prompt_tokens_after"]; got != 10100 {
		t.Fatalf("prompt_tokens_after = %#v, want 10100", got)
	}
	if got := payload["prompt_tokens_delta"]; got != -500 {
		t.Fatalf("prompt_tokens_delta = %#v, want -500", got)
	}
	if got, _ := payload["summary_preview"].(string); got == "" {
		t.Fatal("expected non-empty summary_preview")
	}
}
