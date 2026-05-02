package squads

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeChatClient satisfies FlexInferChatClient so the FlexInferSpawner
// adapter is unit-testable without spinning up a real proxy.
type fakeChatClient struct {
	lastModel  string
	lastPrompt string
	lastTokens int
	content    string
	cost       float64
	err        error
}

func (f *fakeChatClient) Chat(ctx context.Context, model, prompt string, maxTokens int) (string, float64, error) {
	f.lastModel = model
	f.lastPrompt = prompt
	f.lastTokens = maxTokens
	if f.err != nil {
		return "", 0, f.err
	}
	return f.content, f.cost, nil
}

func TestFlexInferSpawner_HappyPath(t *testing.T) {
	chat := &fakeChatClient{content: `{"slices":[{"name":"x"}]}`, cost: 0.07}
	sp := NewFlexInferSpawner(chat)
	if sp == nil {
		t.Fatal("expected spawner, got nil")
	}
	got, err := sp.PlanSlices(context.Background(), "plan this",
		SpawnerOptions{Driver: "claude-opus", MaxCostUSD: 4.0})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if got.JSONBody != chat.content {
		t.Errorf("body: got %q want %q", got.JSONBody, chat.content)
	}
	if got.CostUSD != chat.cost {
		t.Errorf("cost: got %v want %v", got.CostUSD, chat.cost)
	}
	if chat.lastModel != "claude-opus" {
		t.Errorf("driver should pass through to model: got %q", chat.lastModel)
	}
	if chat.lastPrompt != "plan this" {
		t.Errorf("prompt round-trip failed: got %q", chat.lastPrompt)
	}
	if chat.lastTokens != 2048 {
		t.Errorf("default max tokens should be 2048; got %d", chat.lastTokens)
	}
}

func TestFlexInferSpawner_FallbackModelWhenDriverEmpty(t *testing.T) {
	chat := &fakeChatClient{content: "{}", cost: 0.01}
	sp := NewFlexInferSpawner(chat)
	sp.FallbackModel = "qwen-3-32b"
	if _, err := sp.PlanSlices(context.Background(), "plan", SpawnerOptions{}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if chat.lastModel != "qwen-3-32b" {
		t.Errorf("fallback model: got %q want qwen-3-32b", chat.lastModel)
	}
}

func TestFlexInferSpawner_CustomMaxTokens(t *testing.T) {
	chat := &fakeChatClient{content: "{}", cost: 0.01}
	sp := NewFlexInferSpawner(chat)
	sp.MaxTokens = 512
	if _, err := sp.PlanSlices(context.Background(), "plan", SpawnerOptions{Driver: "x"}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if chat.lastTokens != 512 {
		t.Errorf("max tokens override: got %d want 512", chat.lastTokens)
	}
}

func TestFlexInferSpawner_PropagatesChatError(t *testing.T) {
	chat := &fakeChatClient{err: errors.New("upstream 503")}
	sp := NewFlexInferSpawner(chat)
	_, err := sp.PlanSlices(context.Background(), "plan", SpawnerOptions{Driver: "x"})
	if err == nil {
		t.Fatal("expected error to propagate from chat client")
	}
	if !strings.Contains(err.Error(), "upstream 503") {
		t.Errorf("error should wrap upstream: %v", err)
	}
}

func TestFlexInferSpawner_RejectsEmptyPrompt(t *testing.T) {
	sp := NewFlexInferSpawner(&fakeChatClient{})
	if _, err := sp.PlanSlices(context.Background(), "   ", SpawnerOptions{}); err == nil {
		t.Error("expected error for whitespace-only prompt")
	}
}

func TestFlexInferSpawner_NewWithNilClientReturnsNil(t *testing.T) {
	sp := NewFlexInferSpawner(nil)
	if sp != nil {
		t.Errorf("nil client should yield nil spawner; got %+v", sp)
	}
}

func TestFlexInferSpawner_NilReceiverErrors(t *testing.T) {
	var sp *FlexInferSpawner
	if _, err := sp.PlanSlices(context.Background(), "p", SpawnerOptions{}); err == nil {
		t.Error("nil receiver should error")
	}
}

// Compile-time guard: the production *clients.FlexInferClient must
// satisfy our adapter interface. We don't import clients here (avoid an
// indirect cycle through the parent module's internal packages); the
// integration test for this lives in the operator's main_test.go.
var _ Spawner = (*FlexInferSpawner)(nil)
var _ FlexInferChatClient = (*fakeChatClient)(nil)
