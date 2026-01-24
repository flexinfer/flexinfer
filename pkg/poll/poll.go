// Package poll provides context-aware polling utilities that prevent timer leaks
// and properly respect context cancellation.
package poll

import (
	"context"
	"errors"
	"time"
)

// ErrTimeout is returned when polling times out before the condition is met.
var ErrTimeout = errors.New("polling timed out")

// Poll repeatedly calls fn at the given interval until fn returns done=true,
// an error, or the context is cancelled. Unlike time.After in a select loop,
// this properly reuses timers to prevent memory leaks.
func Poll(ctx context.Context, interval time.Duration, fn func() (done bool, err error)) error {
	// Check immediately before starting the timer
	done, err := fn()
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			done, err := fn()
			if err != nil {
				return err
			}
			if done {
				return nil
			}
		}
	}
}

// PollUntilReady polls until the check function returns nil (success) or the
// timeout is reached. The check function receives the context so it can make
// context-aware calls (e.g., HTTP requests with context).
func PollUntilReady(ctx context.Context, timeout, interval time.Duration, check func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return Poll(ctx, interval, func() (bool, error) {
		err := check(ctx)
		if err == nil {
			return true, nil
		}
		// If context is done, return the context error instead of the check error
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// Not ready yet, keep polling
		return false, nil
	})
}

// WaitWithContext waits for the specified duration or until the context is cancelled.
// Returns ctx.Err() if context is cancelled, nil otherwise.
// This is a drop-in replacement for time.Sleep that respects context cancellation.
func WaitWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// RetryWithBackoff executes fn with exponential backoff until it succeeds,
// the maximum attempts are reached, or the context is cancelled.
// The backoff starts at initialDelay and doubles each attempt up to maxDelay.
func RetryWithBackoff(ctx context.Context, maxAttempts int, initialDelay, maxDelay time.Duration, fn func(ctx context.Context) error) error {
	delay := initialDelay

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}

		// Check if context is already cancelled
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// If this was the last attempt, return the error
		if attempt == maxAttempts {
			return err
		}

		// Wait before retrying with context-aware sleep
		if err := WaitWithContext(ctx, delay); err != nil {
			return err
		}

		// Exponential backoff
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}

	return nil
}
