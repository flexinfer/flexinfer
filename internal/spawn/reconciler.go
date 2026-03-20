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
