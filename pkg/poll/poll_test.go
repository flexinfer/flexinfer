package poll

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoll(t *testing.T) {
	t.Run("immediate success", func(t *testing.T) {
		calls := 0
		err := Poll(context.Background(), 10*time.Millisecond, func() (bool, error) {
			calls++
			return true, nil
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if calls != 1 {
			t.Errorf("expected 1 call, got %d", calls)
		}
	})

	t.Run("success after retries", func(t *testing.T) {
		calls := 0
		err := Poll(context.Background(), 10*time.Millisecond, func() (bool, error) {
			calls++
			return calls >= 3, nil
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if calls != 3 {
			t.Errorf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("error propagation", func(t *testing.T) {
		expectedErr := errors.New("test error")
		err := Poll(context.Background(), 10*time.Millisecond, func() (bool, error) {
			return false, expectedErr
		})
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected %v, got %v", expectedErr, err)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0

		go func() {
			time.Sleep(25 * time.Millisecond)
			cancel()
		}()

		err := Poll(ctx, 10*time.Millisecond, func() (bool, error) {
			calls++
			return false, nil
		})

		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
		if calls < 2 {
			t.Errorf("expected at least 2 calls, got %d", calls)
		}
	})
}

func TestPollUntilReady(t *testing.T) {
	t.Run("immediate ready", func(t *testing.T) {
		err := PollUntilReady(context.Background(), time.Second, 10*time.Millisecond, func(ctx context.Context) error {
			return nil
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("becomes ready", func(t *testing.T) {
		calls := int32(0)
		err := PollUntilReady(context.Background(), time.Second, 10*time.Millisecond, func(ctx context.Context) error {
			if atomic.AddInt32(&calls, 1) >= 3 {
				return nil
			}
			return errors.New("not ready")
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if atomic.LoadInt32(&calls) != 3 {
			t.Errorf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		err := PollUntilReady(context.Background(), 50*time.Millisecond, 10*time.Millisecond, func(ctx context.Context) error {
			return errors.New("never ready")
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected context.DeadlineExceeded, got %v", err)
		}
	})

	t.Run("respects parent context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Already cancelled

		err := PollUntilReady(ctx, time.Second, 10*time.Millisecond, func(ctx context.Context) error {
			return errors.New("not ready")
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})
}

func TestWaitWithContext(t *testing.T) {
	t.Run("completes normally", func(t *testing.T) {
		start := time.Now()
		err := WaitWithContext(context.Background(), 50*time.Millisecond)
		elapsed := time.Since(start)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if elapsed < 40*time.Millisecond {
			t.Errorf("wait was too short: %v", elapsed)
		}
	})

	t.Run("cancelled early", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		err := WaitWithContext(ctx, time.Second)
		elapsed := time.Since(start)

		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
		if elapsed > 100*time.Millisecond {
			t.Errorf("wait was too long: %v", elapsed)
		}
	})
}

func TestRetryWithBackoff(t *testing.T) {
	t.Run("immediate success", func(t *testing.T) {
		calls := 0
		err := RetryWithBackoff(context.Background(), 3, 10*time.Millisecond, 100*time.Millisecond, func(ctx context.Context) error {
			calls++
			return nil
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if calls != 1 {
			t.Errorf("expected 1 call, got %d", calls)
		}
	})

	t.Run("success after retries", func(t *testing.T) {
		calls := 0
		err := RetryWithBackoff(context.Background(), 5, 5*time.Millisecond, 50*time.Millisecond, func(ctx context.Context) error {
			calls++
			if calls >= 3 {
				return nil
			}
			return errors.New("retry")
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if calls != 3 {
			t.Errorf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("max attempts exceeded", func(t *testing.T) {
		calls := 0
		expectedErr := errors.New("always fail")
		err := RetryWithBackoff(context.Background(), 3, 5*time.Millisecond, 50*time.Millisecond, func(ctx context.Context) error {
			calls++
			return expectedErr
		})
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected %v, got %v", expectedErr, err)
		}
		if calls != 3 {
			t.Errorf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0

		go func() {
			time.Sleep(15 * time.Millisecond)
			cancel()
		}()

		err := RetryWithBackoff(ctx, 10, 10*time.Millisecond, 100*time.Millisecond, func(ctx context.Context) error {
			calls++
			return errors.New("keep retrying")
		})

		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})
}

func BenchmarkPoll(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Poll(context.Background(), time.Nanosecond, func() (bool, error) {
			return true, nil
		})
	}
}

func BenchmarkWaitWithContext(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = WaitWithContext(context.Background(), time.Nanosecond)
	}
}
