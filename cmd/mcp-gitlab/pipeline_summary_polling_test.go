package main

import (
	"context"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/validate"
)

func TestResolvePollPipelineTimeoutSeconds_Default(t *testing.T) {
	t.Parallel()

	got := resolvePollPipelineTimeoutSeconds(context.Background(), map[string]any{}, validate.NewArgs(map[string]any{}))
	if got != defaultPollPipelineTimeoutSeconds {
		t.Fatalf("timeout = %d, want %d", got, defaultPollPipelineTimeoutSeconds)
	}
}

func TestResolvePollPipelineTimeoutSeconds_UsesContextDeadlineForDefault(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(12*time.Second))
	defer cancel()

	got := resolvePollPipelineTimeoutSeconds(ctx, map[string]any{}, validate.NewArgs(map[string]any{}))
	if got < 8 || got > 10 {
		t.Fatalf("timeout = %d, want value clamped near remaining deadline budget", got)
	}
}

func TestResolvePollPipelineTimeoutSeconds_ExplicitValueWins(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(12*time.Second))
	defer cancel()

	args := map[string]any{"timeout_seconds": 300.0}
	got := resolvePollPipelineTimeoutSeconds(ctx, args, validate.NewArgs(args))
	if got != 300 {
		t.Fatalf("timeout = %d, want 300", got)
	}
}
