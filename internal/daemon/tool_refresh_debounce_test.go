package daemon

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestToolRefreshDebounce_Coalesces verifies that many rapid schedule() calls
// within the debounce window result in exactly one onFire callback invocation,
// fired only after the interval has elapsed since the last call.
func TestToolRefreshDebounce_Coalesces(t *testing.T) {
	t.Parallel()

	var fires atomic.Int32
	// Use a short interval so the test runs quickly; the real daemon interval
	// is 3s but the debounce logic is agnostic.
	interval := 50 * time.Millisecond
	d := newToolRefreshDebounce(interval, func() {
		fires.Add(1)
	})

	// Fire 20 rapid schedule() calls inside the window.
	start := time.Now()
	for i := 0; i < 20; i++ {
		d.schedule()
		time.Sleep(interval / 10) // ~5ms; well under the window
	}
	elapsedBatch := time.Since(start)

	// Right after the batch we must not have fired yet (each schedule() resets
	// the timer). Allow a tiny race slack.
	if got := fires.Load(); got != 0 {
		t.Fatalf("expected 0 fires during rapid-batch (took %v), got %d", elapsedBatch, got)
	}

	// Wait comfortably longer than the debounce interval and verify exactly
	// one fire.
	time.Sleep(interval * 3)

	if got := fires.Load(); got != 1 {
		t.Fatalf("expected exactly 1 fire after quiet period, got %d", got)
	}
}

// TestToolRefreshDebounce_StopCancels verifies stop() prevents a pending fire.
func TestToolRefreshDebounce_StopCancels(t *testing.T) {
	t.Parallel()

	var fires atomic.Int32
	d := newToolRefreshDebounce(30*time.Millisecond, func() {
		fires.Add(1)
	})

	d.schedule()
	d.stop()

	time.Sleep(80 * time.Millisecond)

	if got := fires.Load(); got != 0 {
		t.Fatalf("expected 0 fires after stop(), got %d", got)
	}
}

// TestToolRefreshDebounce_NilSafe exercises the nil-receiver guards.
func TestToolRefreshDebounce_NilSafe(t *testing.T) {
	t.Parallel()
	var d *toolRefreshDebounce
	d.schedule()
	d.stop()
}
