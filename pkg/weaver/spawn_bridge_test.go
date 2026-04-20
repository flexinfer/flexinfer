package weaver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureBridge records the arguments of the most recent Dispatch call and
// returns a preset response. Satisfies SpawnBridge.
type captureBridge struct {
	mu      sync.Mutex
	gotCall bool
	calls   []bridgeCall
	result  BridgeResult
	err     error
}

type bridgeCall struct {
	Agent           SubAgent
	Query           string
	ParentSessionID string
	WeaverQueryID   string
}

func (b *captureBridge) Dispatch(_ context.Context, agent SubAgent, query, parentSessionID, weaverQueryID string) (BridgeResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gotCall = true
	b.calls = append(b.calls, bridgeCall{
		Agent:           agent,
		Query:           query,
		ParentSessionID: parentSessionID,
		WeaverQueryID:   weaverQueryID,
	})
	return b.result, b.err
}

func (b *captureBridge) lastCall() (bridgeCall, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.calls) == 0 {
		return bridgeCall{}, false
	}
	return b.calls[len(b.calls)-1], true
}

func TestNoopSpawnBridge_ReturnsNotConfiguredError(t *testing.T) {
	var b SpawnBridge = NoopSpawnBridge{}
	_, err := b.Dispatch(context.Background(), SubAgent{Name: "x", Backend: BackendClaude}, "q", "sess", "qid")
	if err == nil {
		t.Fatal("expected error from NoopSpawnBridge.Dispatch")
	}
	if !errors.Is(err, ErrSpawnBridgeNotConfigured) {
		t.Fatalf("error %v does not wrap ErrSpawnBridgeNotConfigured", err)
	}
	if !strings.Contains(err.Error(), BendOrName(SubAgent{Name: "x", Backend: BackendClaude})) {
		// Don't be too strict here — just ensure the message is contextual.
		if !strings.Contains(err.Error(), "claude-code") {
			t.Fatalf("expected error to mention the backend, got %q", err.Error())
		}
	}
}

// BendOrName is unused in production but keeps the assertion above readable.
func BendOrName(a SubAgent) string { return fmt.Sprintf("%s/%s", a.Name, a.Backend) }

func TestRouter_SetSpawnBridge_NilRevertsToNoop(t *testing.T) {
	router, _, _ := newTestRouter(t, func(_ chatCompletionRequestWithTools, _ int) chatCompletionResponseWithTools {
		return terminalResponse("unused")
	})

	router.SetSpawnBridge(&captureBridge{})
	router.SetSpawnBridge(nil)

	// NoopSpawnBridge should be installed after nil — non-flexinfer dispatch fails fast.
	got := router.runSubAgentViaBridge(
		context.Background(),
		SubAgent{Name: "x", Backend: BackendClaude, RequiresSpawn: true},
		QueryRequest{Query: "q"},
		"qid",
		time.Now(),
		router.logger,
	)
	if got.Error == "" {
		t.Fatal("expected error from Noop bridge after nil SetSpawnBridge")
	}
	if !strings.Contains(got.Error, "spawn bridge not configured") {
		t.Fatalf("unexpected error message: %q", got.Error)
	}
}

func TestRouter_RunSubAgentViaBridge_HappyPath(t *testing.T) {
	router, _, _ := newTestRouter(t, func(_ chatCompletionRequestWithTools, _ int) chatCompletionResponseWithTools {
		return terminalResponse("should not be called — bridge path in effect")
	})

	bridge := &captureBridge{
		result: BridgeResult{
			SpawnID:      "spawn-abc123",
			LastMessage:  "headless Claude said hello",
			ToolCalls:    3,
			TotalCostUSD: 0.42,
			StopReason:   "end_turn",
			Tokens:       1234,
		},
	}
	router.SetSpawnBridge(bridge)

	agent := SubAgent{
		Name:          "cluster-ops-claude",
		Description:   "Cluster ops delegated to a real Claude pod",
		Tools:         []string{"k8s_apps_k3s__k8s_getPods"},
		Backend:       BackendClaude,
		RequiresSpawn: true,
	}

	got := router.runSubAgentViaBridge(
		context.Background(),
		agent,
		QueryRequest{Query: "what's k8s health?", ParentSessionID: "sess-aaa"},
		"qid-zzz",
		time.Now(),
		router.logger,
	)

	if got.Error != "" {
		t.Fatalf("expected no error, got %q", got.Error)
	}
	if got.Domain != agent.Name {
		t.Fatalf("Domain = %q, want %q", got.Domain, agent.Name)
	}
	if got.Answer != "headless Claude said hello" {
		t.Fatalf("Answer = %q, want bridge's LastMessage", got.Answer)
	}
	if got.Tokens != 1234 {
		t.Fatalf("Tokens = %d, want 1234", got.Tokens)
	}
	if got.LatencyMs < 0 {
		t.Fatalf("LatencyMs should be non-negative, got %d", got.LatencyMs)
	}

	call, ok := bridge.lastCall()
	if !ok {
		t.Fatal("expected bridge to be called")
	}
	if call.Query != "what's k8s health?" {
		t.Fatalf("bridge got query %q, want %q", call.Query, "what's k8s health?")
	}
	if call.ParentSessionID != "sess-aaa" {
		t.Fatalf("bridge got parent_session_id %q, want %q", call.ParentSessionID, "sess-aaa")
	}
	if call.WeaverQueryID != "qid-zzz" {
		t.Fatalf("bridge got query_id %q, want %q", call.WeaverQueryID, "qid-zzz")
	}
	if call.Agent.Backend != BackendClaude {
		t.Fatalf("bridge got backend %q, want %q", call.Agent.Backend, BackendClaude)
	}
}

func TestRouter_RunSubAgentViaBridge_ValidateFailsFast(t *testing.T) {
	router, _, _ := newTestRouter(t, func(_ chatCompletionRequestWithTools, _ int) chatCompletionResponseWithTools {
		return terminalResponse("unused")
	})

	bridge := &captureBridge{}
	router.SetSpawnBridge(bridge)

	// Invalid: non-flexinfer backend without RequiresSpawn.
	agent := SubAgent{
		Name:    "bad-domain",
		Backend: BackendClaude, // RequiresSpawn missing
	}

	got := router.runSubAgentViaBridge(
		context.Background(), agent, QueryRequest{Query: "q"}, "qid", time.Now(), router.logger,
	)
	if got.Error == "" {
		t.Fatal("expected validation error, got none")
	}
	if !strings.Contains(got.Error, "requires_spawn") {
		t.Fatalf("expected requires_spawn error, got %q", got.Error)
	}
	if bridge.gotCall {
		t.Fatal("bridge should NOT be called when validation fails")
	}
}

func TestRouter_RunSubAgentViaBridge_DispatchErrorReturnsDomainResult(t *testing.T) {
	router, _, _ := newTestRouter(t, func(_ chatCompletionRequestWithTools, _ int) chatCompletionResponseWithTools {
		return terminalResponse("unused")
	})

	bridge := &captureBridge{err: errors.New("pod scheduling failed: no quota")}
	router.SetSpawnBridge(bridge)

	agent := SubAgent{Name: "d", Backend: BackendCodex, RequiresSpawn: true}

	got := router.runSubAgentViaBridge(
		context.Background(), agent, QueryRequest{Query: "q"}, "qid", time.Now(), router.logger,
	)
	if got.Error == "" {
		t.Fatal("expected error from bridge to surface in DomainResult")
	}
	if !strings.Contains(got.Error, "pod scheduling failed") {
		t.Fatalf("unexpected error propagation: %q", got.Error)
	}
	if got.Answer != "" {
		t.Fatalf("on error, Answer should be empty; got %q", got.Answer)
	}
}
