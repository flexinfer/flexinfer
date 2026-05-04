package squads

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// FlexInferChatClient is the surface FlexInferSpawner depends on. The
// production implementation is *clients.FlexInferClient (its public Chat
// method). Tests inject a fake.
type FlexInferChatClient interface {
	Chat(ctx context.Context, model, prompt string, maxTokens int) (string, float64, error)
}

// FlexInferSpawner adapts a FlexInfer chat client to the squads.Spawner
// interface so the planner can drive a single-shot completion against a
// configurable local model. This is the v2.0 default backing — it's
// cheap, runs on the existing FlexInfer proxy, and never blocks behind
// frontier inference. Spawn-backed editors (Claude Opus / Codex GPT-5
// via the headless spawn controller) ship as a separate adapter in
// v2.1 once budget + UI handles are in place.
type FlexInferSpawner struct {
	Client FlexInferChatClient

	// MaxTokens caps each completion. Defaults to 2048 — large enough for
	// a refined slice plan + sidecar JSON without runaway output.
	MaxTokens int

	// FallbackModel is used when SpawnerOptions.Driver is empty. The
	// caller usually fills Driver from squad.Ensemble.editor.driver,
	// which mirrors the manifest YAML.
	FallbackModel string
}

// NewFlexInferSpawner returns a configured FlexInfer-backed spawner. The
// client must be non-nil; passing nil returns nil so the planner falls
// back to its parse-error path on every call (which is the correct
// behavior when FlexInfer isn't available).
func NewFlexInferSpawner(c FlexInferChatClient) *FlexInferSpawner {
	if c == nil {
		return nil
	}
	return &FlexInferSpawner{Client: c, MaxTokens: 2048}
}

// PlanSlices satisfies squads.Spawner. It dispatches the planner's
// rendered prompt to the FlexInfer chat endpoint with the model selected
// by SpawnerOptions.Driver (squad's editor.driver), respecting the
// per-squad MaxCostUSD as a soft hint surfaced via context but enforced
// at the budget tier — this method never blocks on cost, the budget
// enforcer pre-checks before calling.
func (s *FlexInferSpawner) PlanSlices(ctx context.Context, prompt string, opts SpawnerOptions) (SpawnerResult, error) {
	if s == nil || s.Client == nil {
		return SpawnerResult{}, errors.New("squads: flexinfer spawner not configured")
	}
	if strings.TrimSpace(prompt) == "" {
		return SpawnerResult{}, errors.New("squads: prompt empty")
	}
	maxTokens := s.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	model := opts.Driver
	if strings.TrimSpace(model) == "" {
		model = s.FallbackModel
	}
	content, cost, err := s.Client.Chat(ctx, model, prompt, maxTokens)
	if err != nil {
		return SpawnerResult{}, fmt.Errorf("squads: flexinfer plan: %w", err)
	}
	return SpawnerResult{JSONBody: content, CostUSD: cost}, nil
}
