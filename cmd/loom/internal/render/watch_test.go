package render

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatch_RendersAtLeastOnceThenCancels(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var calls int32
	var buf bytes.Buffer

	render := func() error {
		atomic.AddInt32(&calls, 1)
		_, _ = buf.WriteString("frame\n")
		// Cancel immediately after the first render so the test exits
		// without relying on real time.
		cancel()
		return nil
	}

	if err := Watch(ctx, &buf, 50*time.Millisecond, render); err != nil {
		t.Fatalf("Watch returned error: %v", err)
	}
	if atomic.LoadInt32(&calls) < 1 {
		t.Fatalf("expected render to be called at least once, got %d", calls)
	}
	if buf.Len() == 0 {
		t.Fatalf("expected output to contain rendered frame")
	}
}

func TestWatch_ReturnsRenderError(t *testing.T) {
	t.Parallel()

	want := errors.New("boom")
	ctx := context.Background()
	var buf bytes.Buffer

	render := func() error { return want }

	got := Watch(ctx, &buf, 50*time.Millisecond, render)
	if !errors.Is(got, want) {
		t.Fatalf("expected error %v, got %v", want, got)
	}
}

func TestWatch_NilRenderFunc(t *testing.T) {
	t.Parallel()

	err := Watch(context.Background(), &bytes.Buffer{}, time.Second, nil)
	if err == nil {
		t.Fatalf("expected error for nil render func")
	}
}

func TestWatch_ContextCanceledIsCleanExit(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel before first render

	var buf bytes.Buffer
	calls := 0
	render := func() error {
		calls++
		return nil
	}

	if err := Watch(ctx, &buf, 50*time.Millisecond, render); err != nil {
		t.Fatalf("expected nil error on canceled context, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one render before cancel exits loop, got %d", calls)
	}
}

func TestWatch_DeadlineExceededReturnsErr(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(10*time.Millisecond))
	defer cancel()

	var buf bytes.Buffer
	render := func() error { return nil }

	err := Watch(ctx, &buf, 50*time.Millisecond, render)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestWatch_SubMinIntervalIsClamped(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var buf bytes.Buffer
	render := func() error {
		cancel()
		return nil
	}

	// Pass an unrealistically short interval; clamp must prevent panics.
	if err := Watch(ctx, &buf, time.Microsecond, render); err != nil {
		t.Fatalf("Watch returned error: %v", err)
	}
}
