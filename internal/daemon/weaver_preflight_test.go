package daemon

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/flexinfer"
	"github.com/crb2nu/loom/pkg/weaver"
)

type fakeModelLister struct {
	models []flexinfer.ModelInfo
	err    error
}

func (f fakeModelLister) Models(_ context.Context) ([]flexinfer.ModelInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.models, nil
}

func TestRunWeaverPreflight_AllReady(t *testing.T) {
	t.Parallel()
	cfg := weaver.Config{
		RouterModel:   "qwen3-router",
		SubagentModel: "qwen3-8b",
	}
	registry := weaver.NewDomainRegistry()
	registry.Register(weaver.SubAgent{Name: "ops", Tools: []string{"x"}, Model: "qwen3-8b"})
	registry.Register(weaver.SubAgent{Name: "ci", Tools: []string{"x"}, Model: "fast-text"})

	lister := fakeModelLister{models: []flexinfer.ModelInfo{
		{ID: "qwen3-router"},
		{ID: "qwen3-8b"},
		{ID: "fast-text"},
		{ID: "extra-thing"}, // catalog has more than we use
	}}

	pre := runWeaverPreflight(context.Background(), lister, cfg, registry)

	if pre.Degraded {
		t.Errorf("expected not degraded, got Degraded=true with missing=%v", pre.MissingModels)
	}
	if pre.CatalogSize != 4 {
		t.Errorf("CatalogSize = %d, want 4", pre.CatalogSize)
	}
	wantReady := []string{"fast-text", "qwen3-8b", "qwen3-router"}
	sort.Strings(pre.ReadyModels)
	if !reflect.DeepEqual(pre.ReadyModels, wantReady) {
		t.Errorf("ReadyModels = %v, want %v", pre.ReadyModels, wantReady)
	}
	if len(pre.MissingModels) != 0 {
		t.Errorf("MissingModels = %v, want empty", pre.MissingModels)
	}
	if pre.CheckedAt.IsZero() {
		t.Error("CheckedAt should be populated")
	}
}

func TestRunWeaverPreflight_DegradedSomeMissing(t *testing.T) {
	t.Parallel()
	cfg := weaver.Config{
		RouterModel:   "qwen3-router",
		SubagentModel: "absent-model",
	}
	registry := weaver.NewDomainRegistry()
	registry.Register(weaver.SubAgent{Name: "ops", Tools: []string{"x"}, Model: "also-absent"})
	// Backend != flexinfer should be skipped from preflight.
	registry.Register(weaver.SubAgent{
		Name:          "claude-codex",
		Tools:         []string{"x"},
		Model:         "claude-opus-not-on-flexinfer",
		Backend:       weaver.BackendClaude,
		RequiresSpawn: true,
	})

	lister := fakeModelLister{models: []flexinfer.ModelInfo{
		{ID: "qwen3-router"},
	}}

	pre := runWeaverPreflight(context.Background(), lister, cfg, registry)

	if !pre.Degraded {
		t.Error("expected degraded, got not degraded")
	}
	wantMissing := []string{"absent-model", "also-absent"}
	if !reflect.DeepEqual(pre.MissingModels, wantMissing) {
		t.Errorf("MissingModels = %v, want %v", pre.MissingModels, wantMissing)
	}
	wantReady := []string{"qwen3-router"}
	if !reflect.DeepEqual(pre.ReadyModels, wantReady) {
		t.Errorf("ReadyModels = %v, want %v", pre.ReadyModels, wantReady)
	}
	// claude-opus-not-on-flexinfer should NOT show up as missing —
	// claude-code backend domains don't go through the FlexInfer proxy.
	for _, m := range pre.MissingModels {
		if m == "claude-opus-not-on-flexinfer" {
			t.Error("non-flexinfer-backend model leaked into MissingModels")
		}
	}
}

func TestRunWeaverPreflight_CatalogError(t *testing.T) {
	t.Parallel()
	cfg := weaver.Config{
		RouterModel:   "qwen3-router",
		SubagentModel: "qwen3-8b",
	}
	registry := weaver.NewDomainRegistry()

	lister := fakeModelLister{err: errors.New("connection refused")}

	pre := runWeaverPreflight(context.Background(), lister, cfg, registry)

	if !pre.Degraded {
		t.Error("expected degraded on catalog error")
	}
	if pre.CatalogError == "" {
		t.Error("expected CatalogError populated")
	}
	wantMissing := []string{"qwen3-8b", "qwen3-router"}
	if !reflect.DeepEqual(pre.MissingModels, wantMissing) {
		t.Errorf("MissingModels = %v, want %v (everything configured)", pre.MissingModels, wantMissing)
	}
	if len(pre.ReadyModels) != 0 {
		t.Errorf("ReadyModels = %v, want empty when catalog unreachable", pre.ReadyModels)
	}
}

func TestRunWeaverPreflight_NoConfiguredModels(t *testing.T) {
	t.Parallel()
	cfg := weaver.Config{}
	registry := weaver.NewDomainRegistry()

	lister := fakeModelLister{models: []flexinfer.ModelInfo{{ID: "anything"}}}

	pre := runWeaverPreflight(context.Background(), lister, cfg, registry)

	if pre.Degraded {
		t.Error("no configured models = nothing missing = not degraded")
	}
	if len(pre.MissingModels) != 0 || len(pre.ReadyModels) != 0 {
		t.Errorf("expected empty lists, got missing=%v ready=%v", pre.MissingModels, pre.ReadyModels)
	}
	if pre.CatalogSize != 1 {
		t.Errorf("CatalogSize = %d, want 1", pre.CatalogSize)
	}
}

func TestPreflightStore_GetSet(t *testing.T) {
	t.Parallel()
	var s preflightStore

	if _, ok := s.Get(); ok {
		t.Error("zero-value store should report not-set")
	}

	want := WeaverPreflight{
		Degraded:      true,
		MissingModels: []string{"x"},
		CheckedAt:     time.Now().UTC(),
	}
	s.Set(want)
	got, ok := s.Get()
	if !ok {
		t.Fatal("Get after Set: ok=false")
	}
	if got.Degraded != want.Degraded {
		t.Errorf("Degraded mismatch: got %v want %v", got.Degraded, want.Degraded)
	}
	if !reflect.DeepEqual(got.MissingModels, want.MissingModels) {
		t.Errorf("MissingModels mismatch: got %v want %v", got.MissingModels, want.MissingModels)
	}
}
