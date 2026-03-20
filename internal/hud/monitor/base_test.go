package monitor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestBaseMonitor_SnapshotAndUpdate(t *testing.T) {
	var b BaseMonitor[int]
	b.InitBase(nil, nil, "test")

	// Zero value snapshot.
	if got := b.Snapshot(); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}

	b.Update(42)
	if got := b.Snapshot(); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestBaseMonitor_OnRefreshCallback(t *testing.T) {
	var b BaseMonitor[string]
	b.InitBase(nil, nil, "test")

	var received string
	b.OnRefresh(func(val string) { received = val })

	b.Update("hello")
	if received != "hello" {
		t.Fatalf("expected callback with 'hello', got %q", received)
	}
}

func TestBaseMonitor_StopIdempotent(t *testing.T) {
	var b BaseMonitor[int]
	b.InitBase(nil, nil, "test")

	b.Stop()
	b.Stop() // should not panic
}

func TestBaseMonitor_StartAndStop(t *testing.T) {
	var b BaseMonitor[int]
	b.InitBase(nil, nil, "test")

	var calls atomic.Int32
	refreshFn := func(_ context.Context) (int, error) {
		calls.Add(1)
		return int(calls.Load()), nil
	}

	b.Start(50*time.Millisecond, refreshFn)

	// Wait for at least the initial refresh + one poll cycle.
	time.Sleep(200 * time.Millisecond)
	b.Stop()

	// Allow goroutines to finish.
	time.Sleep(50 * time.Millisecond)

	if c := calls.Load(); c < 2 {
		t.Fatalf("expected at least 2 refresh calls, got %d", c)
	}
	if got := b.Snapshot(); got == 0 {
		t.Fatal("expected non-zero snapshot after refresh")
	}
}

func TestBaseMonitor_BackoffOnErrors(t *testing.T) {
	var b BaseMonitor[int]
	b.InitBase(nil, nil, "test")

	var calls atomic.Int32
	refreshFn := func(_ context.Context) (int, error) {
		n := calls.Add(1)
		if n <= 3 {
			return 0, errors.New("transient error")
		}
		return int(n), nil
	}

	b.Start(20*time.Millisecond, refreshFn)

	// With backoff, after 3 errors the monitor skips ticks. Eventually
	// it should recover. Give it enough time.
	time.Sleep(500 * time.Millisecond)
	b.Stop()

	time.Sleep(50 * time.Millisecond)

	if got := b.Snapshot(); got == 0 {
		t.Fatal("expected recovery after transient errors")
	}
}

func TestBaseMonitor_NoopTracerWhenNil(t *testing.T) {
	var b BaseMonitor[int]
	b.InitBase(nil, nil, "test")

	// The tracer should be a noop tracer (not nil).
	if b.Tracer == nil {
		t.Fatal("expected non-nil tracer")
	}

	// Verify it produces noop spans by starting one.
	_, span := b.Tracer.Start(context.Background(), "test-span")
	if span.SpanContext().IsValid() {
		t.Fatal("expected noop span to have invalid SpanContext")
	}
	span.End()
}

// recordingTracer is a test tracer that records started span names.
type recordingTracer struct {
	noop.Tracer
	spans []string
}

func (rt *recordingTracer) Start(ctx context.Context, name string, _ ...trace.SpanStartOption) (context.Context, trace.Span) {
	rt.spans = append(rt.spans, name)
	return rt.Tracer.Start(ctx, name)
}

func TestBaseMonitor_OTelSpanPerRefresh(t *testing.T) {
	var b BaseMonitor[int]
	tracer := &recordingTracer{}
	b.InitBase(nil, tracer, "my-monitor")

	refreshFn := func(_ context.Context) (int, error) {
		return 1, nil
	}

	b.Start(50*time.Millisecond, refreshFn)
	time.Sleep(200 * time.Millisecond)
	b.Stop()
	time.Sleep(50 * time.Millisecond)

	// Should have at least the initial refresh span + poll spans.
	found := false
	for _, name := range tracer.spans {
		if name == "my-monitor.refresh" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected span named 'my-monitor.refresh', got spans: %v", tracer.spans)
	}
}

func TestBaseMonitor_OTelSpanOnError(t *testing.T) {
	var b BaseMonitor[int]
	tracer := &recordingTracer{}
	b.InitBase(nil, tracer, "err-monitor")

	refreshFn := func(_ context.Context) (int, error) {
		return 0, errors.New("boom")
	}

	// Call doRefresh directly to verify span is recorded on error.
	_, err := b.doRefresh(refreshFn)
	if err == nil {
		t.Fatal("expected error")
	}

	found := false
	for _, name := range tracer.spans {
		if name == "err-monitor.refresh" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected span on error path, got spans: %v", tracer.spans)
	}
}

func TestBaseMonitor_StopDuringPoll(t *testing.T) {
	var b BaseMonitor[int]
	b.InitBase(nil, nil, "test")

	refreshFn := func(_ context.Context) (int, error) {
		return 1, nil
	}

	b.Start(10*time.Millisecond, refreshFn)
	// Stop almost immediately.
	time.Sleep(5 * time.Millisecond)
	b.Stop()

	// Ensure goroutines have exited cleanly.
	time.Sleep(50 * time.Millisecond)
}

func TestBaseMonitor_LockUnlock(t *testing.T) {
	var b BaseMonitor[string]
	b.InitBase(nil, nil, "test")

	b.Lock()
	b.SetSnapshot("direct")
	b.Unlock()

	b.RLock()
	got := b.GetSnapshot()
	b.RUnlock()

	if got != "direct" {
		t.Fatalf("expected 'direct', got %q", got)
	}
}
