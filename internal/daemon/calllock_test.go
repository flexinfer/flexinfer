package daemon

import (
	"context"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/testutil"
)

func TestLockWithContext_FastPath(t *testing.T) {
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	var mu gosync.Mutex
	start := time.Now()
	err := lockWithContext(context.Background(), &mu)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("lockWithContext returned unexpected error: %v", err)
	}
	// Fast path should acquire nearly instantly (well under 5ms).
	if elapsed > 5*time.Millisecond {
		t.Errorf("fast path took %v, expected < 5ms", elapsed)
	}
	mu.Unlock()
}

func TestLockWithContext_ContextCancellation(t *testing.T) {
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	var mu gosync.Mutex
	mu.Lock() // Hold the lock so lockWithContext cannot acquire it.
	defer mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := lockWithContext(ctx, &mu)
	if err == nil {
		t.Fatal("expected error when context expires, got nil")
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
}

func TestLockWithContext_AcquiresAfterRelease(t *testing.T) {
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	var mu gosync.Mutex
	mu.Lock()

	// Release the lock after ~50ms in a background goroutine.
	go func() {
		time.Sleep(50 * time.Millisecond)
		mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	err := lockWithContext(ctx, &mu)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("lockWithContext returned error: %v", err)
	}
	defer mu.Unlock()

	// Should have waited roughly 50ms (the poll interval is 10ms, so some jitter is expected).
	if elapsed < 40*time.Millisecond {
		t.Errorf("expected wait >= 40ms, got %v", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected wait < 500ms, got %v (too slow)", elapsed)
	}
}

func TestAcquireCallLock_TimeoutReturnsError(t *testing.T) {
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	t.Setenv("LOOM_DAEMON_CALL_LOCK_TIMEOUT", "50ms")

	d := &Daemon{}

	// Pre-acquire the call lock for "test-server" to create contention.
	mu := d.callLock("test-server")
	mu.Lock()
	defer mu.Unlock()

	ctx := context.Background()
	_, _, err := d.acquireCallLock(ctx, "test-server")
	if err == nil {
		t.Fatal("expected error from acquireCallLock under contention, got nil")
	}
	if !strings.Contains(err.Error(), "acquire call lock") {
		t.Errorf("error should contain 'acquire call lock', got: %v", err)
	}
}

func TestAcquireCallLock_UncontendedSucceeds(t *testing.T) {
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	d := &Daemon{}

	ctx := context.Background()
	mu, waitDur, err := d.acquireCallLock(ctx, "uncontended-server")
	if err != nil {
		t.Fatalf("acquireCallLock failed: %v", err)
	}

	// Wait duration should be very small for an uncontended lock.
	if waitDur > 5*time.Millisecond {
		t.Errorf("expected near-zero wait, got %v", waitDur)
	}

	// The mutex should be locked; verify by checking TryLock fails.
	if mu.TryLock() {
		mu.Unlock()
		t.Fatal("expected mutex to be locked after acquireCallLock, but TryLock succeeded")
	}

	// Release the lock and verify it can be acquired again.
	mu.Unlock()
	if !mu.TryLock() {
		t.Fatal("expected mutex to be unlocked after Unlock, but TryLock failed")
	}
	mu.Unlock()
}

func TestResolveCallLockAcquireTimeout_Defaults(t *testing.T) {
	// No env var set: returns default.
	t.Setenv("LOOM_DAEMON_CALL_LOCK_TIMEOUT", "")
	got := resolveCallLockAcquireTimeout()
	if got != defaultCallLockAcquireTimeout {
		t.Errorf("expected default %v, got %v", defaultCallLockAcquireTimeout, got)
	}
}

func TestResolveCallLockAcquireTimeout_CustomValue(t *testing.T) {
	t.Setenv("LOOM_DAEMON_CALL_LOCK_TIMEOUT", "10s")
	got := resolveCallLockAcquireTimeout()
	if got != 10*time.Second {
		t.Errorf("expected 10s, got %v", got)
	}
}

func TestResolveCallLockAcquireTimeout_InvalidFallsBackToDefault(t *testing.T) {
	t.Setenv("LOOM_DAEMON_CALL_LOCK_TIMEOUT", "not-a-duration")
	got := resolveCallLockAcquireTimeout()
	if got != defaultCallLockAcquireTimeout {
		t.Errorf("expected default %v for invalid input, got %v", defaultCallLockAcquireTimeout, got)
	}
}
