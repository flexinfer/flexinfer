package main

import (
	"testing"
	"time"
)

func TestAsyncRegistryCleanup_UsesCompletedTimestamp(t *testing.T) {
	t.Parallel()

	registry := newAsyncRegistry()
	now := time.Now()
	oldStart := now.Add(-30 * time.Minute)
	recentCompletion := now.Add(-2 * time.Minute)

	registry.add(&asyncExec{
		ID:          "exec-completed-recently",
		Status:      "completed",
		StartedAt:   oldStart,
		CompletedAt: &recentCompletion,
	})

	registry.cleanup(10 * time.Minute)

	if got := registry.get("exec-completed-recently"); got == nil {
		t.Fatalf("expected completed async exec to remain when completion is within max age")
	}
}

func TestAsyncRegistryCleanup_RemovesOldCompletedTimestamp(t *testing.T) {
	t.Parallel()

	registry := newAsyncRegistry()
	now := time.Now()
	oldCompletion := now.Add(-20 * time.Minute)

	registry.add(&asyncExec{
		ID:          "exec-completed-old",
		Status:      "failed",
		StartedAt:   now.Add(-30 * time.Minute),
		CompletedAt: &oldCompletion,
	})

	registry.cleanup(10 * time.Minute)

	if got := registry.get("exec-completed-old"); got != nil {
		t.Fatalf("expected old completed async exec to be removed")
	}
}

func TestAsyncRegistryCleanup_BackCompatWithoutCompletedTimestamp(t *testing.T) {
	t.Parallel()

	registry := newAsyncRegistry()
	oldStart := time.Now().Add(-20 * time.Minute)
	registry.add(&asyncExec{
		ID:        "legacy-entry",
		Status:    "completed",
		StartedAt: oldStart,
	})

	registry.cleanup(10 * time.Minute)

	if got := registry.get("legacy-entry"); got != nil {
		t.Fatalf("expected legacy completed async exec to be removed based on started time fallback")
	}
}
