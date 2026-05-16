package spawn

import (
	"context"
	"time"
)

// StartReconcileLoop runs Reconcile periodically in a background goroutine.
// The loop exits when the context is cancelled.
func (c *K8sController) StartReconcileLoop(ctx context.Context, interval time.Duration) {
	// Run an initial reconciliation immediately on startup.
	if err := c.Reconcile(ctx); err != nil {
		c.logger.Warn("initial reconciliation failed", "error", err)
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.Reconcile(ctx); err != nil {
					c.logger.Warn("reconciliation failed", "error", err)
				}
			}
		}
	}()
}

// StartPruneLoop runs Prune periodically in a background goroutine, removing
// terminal spawn records older than maxAge. The loop exits when the context
// is cancelled. Without this, the in-memory map and on-disk store grow
// unboundedly with completed/failed spawns and the HUD's spawn list shows
// runs from days ago as if they were still relevant.
func (c *K8sController) StartPruneLoop(ctx context.Context, interval, maxAge time.Duration) {
	if interval <= 0 || maxAge <= 0 {
		return
	}
	// Run an initial prune immediately so a fresh restart drops anything
	// the previous instance left behind beyond the retention window.
	c.Prune(ctx, maxAge)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.Prune(ctx, maxAge)
			}
		}
	}()
}
