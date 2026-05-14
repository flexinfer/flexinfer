package main

import (
	"context"
	"testing"

	"github.com/crb2nu/loom/pkg/mills/pipeline"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// captureWorker records the JobContext it receives so the test can assert
// that ResumeSpawnID was propagated. Used by TestFallbackDispatcher_*.
type captureWorker struct {
	got pipeline.JobContext
}

func (w *captureWorker) Run(_ context.Context, jc pipeline.JobContext) (pipeline.StageOutput, error) {
	w.got = jc
	return pipeline.StageOutput{}, nil
}

// TestFallbackDispatcher_PropagatesResumeSpawnID guards the bug that left
// PIPE-MILLS-CANARY-20260513-004154 stuck in plan_slice for >24h: the
// runner stashed the resume spawn id on the stage context, but
// fallbackDispatcher built a JobContext without it, so SpawnWorker.Run
// tried to start a fresh spawn and hit ErrStageSpawnConflict against the
// existing pending row on every operator restart.
func TestFallbackDispatcher_PropagatesResumeSpawnID(t *testing.T) {
	w := &captureWorker{}
	d := &fallbackDispatcher{
		routes:   map[string]pipeline.Worker{"plan_slice": w},
		fallback: &pipeline.NoOpDispatcher{},
	}

	ctx := pipeline.WithResumeSpawnID(context.Background(), "spawn-abc123")
	run := &store.PipelineRun{ID: "PIPE-1", BacklogID: "BL-1"}
	item := &store.BacklogItem{ID: "BL-1"}
	stage := pipeline.Stage{ID: "plan_slice"}

	if _, err := d.Dispatch(ctx, run, item, stage, nil); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	if got := w.got.ResumeSpawnID; got != "spawn-abc123" {
		t.Fatalf("ResumeSpawnID not propagated: got %q, want %q", got, "spawn-abc123")
	}
}
