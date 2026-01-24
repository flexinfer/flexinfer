package lifecycle

import (
	"context"
	"errors"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestRunWithSignals(t *testing.T) {
	t.Run("function completes normally", func(t *testing.T) {
		called := false
		err := RunWithSignals(context.Background(), func(ctx context.Context) error {
			called = true
			return nil
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !called {
			t.Error("function was not called")
		}
	})

	t.Run("function returns error", func(t *testing.T) {
		expectedErr := errors.New("test error")
		err := RunWithSignals(context.Background(), func(ctx context.Context) error {
			return expectedErr
		})
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected %v, got %v", expectedErr, err)
		}
	})

	t.Run("respects parent context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Already cancelled

		err := RunWithSignals(ctx, func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
				return errors.New("should have been cancelled")
			}
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})
}

func TestSetupSignalHandler(t *testing.T) {
	t.Run("returns valid context and cleanup", func(t *testing.T) {
		ctx, cancel, cleanup := SetupSignalHandler(context.Background())
		defer cleanup()
		defer cancel()

		if ctx == nil {
			t.Error("context is nil")
		}
		if ctx.Err() != nil {
			t.Errorf("context should not be cancelled: %v", ctx.Err())
		}
	})

	t.Run("cancel stops context", func(t *testing.T) {
		ctx, cancel, cleanup := SetupSignalHandler(context.Background())
		defer cleanup()

		cancel()

		if ctx.Err() == nil {
			t.Error("context should be cancelled")
		}
	})

	t.Run("cleanup is safe to call multiple times", func(t *testing.T) {
		_, cancel, cleanup := SetupSignalHandler(context.Background())
		defer cancel()

		// Should not panic
		cleanup()
		cleanup()
		cleanup()
	})
}

func TestDefaultSignals(t *testing.T) {
	signals := DefaultSignals()
	if len(signals) != 2 {
		t.Errorf("expected 2 signals, got %d", len(signals))
	}

	found := make(map[syscall.Signal]bool)
	for _, s := range signals {
		if sig, ok := s.(syscall.Signal); ok {
			found[sig] = true
		}
	}

	if !found[syscall.SIGINT] {
		t.Error("SIGINT not in default signals")
	}
	if !found[syscall.SIGTERM] {
		t.Error("SIGTERM not in default signals")
	}
}

func TestRunWithCustomSignals(t *testing.T) {
	t.Run("function completes normally", func(t *testing.T) {
		var called atomic.Bool
		err := RunWithCustomSignals(context.Background(), Signals{syscall.SIGUSR1}, func(ctx context.Context) error {
			called.Store(true)
			return nil
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !called.Load() {
			t.Error("function was not called")
		}
	})
}

func BenchmarkSetupSignalHandler(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx, cancel, cleanup := SetupSignalHandler(context.Background())
		cancel()
		cleanup()
		_ = ctx
	}
}
