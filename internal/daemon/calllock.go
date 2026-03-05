package daemon

import (
	"context"
	"fmt"
	gosync "sync"
	"time"

	"github.com/crb2nu/loom/pkg/env"
)

const (
	defaultCallLockAcquireTimeout = 5 * time.Second
	callLockPollInterval          = 10 * time.Millisecond
)

func resolveCallLockAcquireTimeout() time.Duration {
	return normalizePositiveDuration(
		env.Duration("LOOM_DAEMON_CALL_LOCK_TIMEOUT", defaultCallLockAcquireTimeout),
		defaultCallLockAcquireTimeout,
	)
}

// acquireCallLock acquires the per-server call mutex while respecting both the
// caller context and an upper-bound lock timeout.
func (d *Daemon) acquireCallLock(ctx context.Context, serverName string) (*gosync.Mutex, time.Duration, error) {
	mu := d.callLock(serverName)
	start := time.Now()

	lockCtx, cancel := context.WithTimeout(ctx, resolveCallLockAcquireTimeout())
	defer cancel()

	if err := lockWithContext(lockCtx, mu); err != nil {
		waited := time.Since(start).Round(time.Millisecond)
		return nil, waited, fmt.Errorf("acquire call lock for %q after %s: %w", serverName, waited, err)
	}

	return mu, time.Since(start), nil
}

func lockWithContext(ctx context.Context, mu *gosync.Mutex) error {
	// Fast path.
	if mu.TryLock() {
		return nil
	}

	ticker := time.NewTicker(callLockPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if mu.TryLock() {
				return nil
			}
		}
	}
}
