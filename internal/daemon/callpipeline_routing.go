package daemon

import (
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/attribute"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/router"
)

func (p *callPipeline) releaseConnection() {
	if p.conn != nil {
		// If the context was cancelled (client disconnect), the upstream
		// connection may be in an indeterminate state (server still processing
		// the request). Mark it unhealthy and return it through the pool so the
		// pool decrements active connection accounting before closing it.
		cancelled := p.ctx != nil && p.ctx.Err() != nil
		if cancelled {
			p.conn.Healthy = false
			if p.target == router.TargetLocal {
				if p.daemon.pool != nil {
					p.daemon.pool.Put(p.conn)
				} else if p.conn.Transport != nil {
					_ = p.conn.Transport.Close()
				}
			} else {
				if p.daemon.hubPool != nil {
					p.daemon.hubPool.Put(p.conn)
				} else if p.conn.Transport != nil {
					_ = p.conn.Transport.Close()
				}
			}
		} else if p.target == router.TargetLocal {
			if p.daemon.pool != nil {
				p.daemon.pool.Put(p.conn)
			}
		} else {
			if p.daemon.hubPool != nil {
				p.daemon.hubPool.Put(p.conn)
			} else if p.conn.Transport != nil {
				_ = p.conn.Transport.Close()
			}
		}
		p.conn = nil
	}
	if p.lockHeld && p.callMu != nil {
		p.callMu.Unlock()
		p.lockHeld = false
	}
}

func (p *callPipeline) retryLocalAfterLocalSendFailure(err error, req *mcp.Message, start time.Time) (*mcp.Message, bool) {
	if p.localTransportRetryUsed || p.target != router.TargetLocal || p.daemon == nil || p.daemon.pool == nil {
		return nil, false
	}
	if !shouldResetDaemonTransport(err) {
		return nil, false
	}

	p.localTransportRetryUsed = true
	if p.conn != nil {
		p.conn.Healthy = false
	}
	p.daemon.router.RecordFailure(p.serverName, p.target, err)
	p.daemon.metrics.RecordServerFailure(p.serverName, p.targetStr, "send")
	p.daemon.logger.Warn("local transport send failed; reconnecting and retrying once",
		"server", p.serverName, "error", err)

	p.daemon.pool.ClearServer(p.serverName)
	if p.daemon.procMgr != nil {
		_ = p.daemon.procMgr.Stop(p.serverName)
	}
	p.daemon.runningServers.Delete(p.serverName)
	if p.daemon.eventBus != nil {
		p.daemon.eventBus.Publish(EventProcessStop, map[string]any{
			"server": p.serverName,
			"reason": "transport_send_retry",
		})
	}

	p.releaseConnection()
	if connectErr := p.connectTarget(router.TargetLocal, "local transport send retry"); connectErr != nil {
		combined := fmt.Errorf("local send failed: %v; local retry failed: %w", err, connectErr)
		p.daemon.metrics.RecordRequest(p.serverName, p.method, "error", p.targetStr, time.Since(start))
		return p.internalErrorWithAudit(p.targetStr, combined), true
	}

	return p.execute(req), true
}

func (p *callPipeline) connectTarget(target router.Target, reason string) error {
	p.target = target
	p.targetStr = target.String()
	p.conn = nil

	if target == router.TargetUnavailable {
		return fmt.Errorf("server unavailable: %s", reason)
	}
	if target != router.TargetLocal && target != router.TargetHub {
		return fmt.Errorf("unsupported routing target: %s", target)
	}

	// Acquire the call lock briefly for bookkeeping (mark activity), then
	// release it BEFORE pool.Get() which may block waiting for a connection.
	// The pool is internally thread-safe; holding the call lock during pool
	// acquisition caused cascade timeouts for concurrent callers (heartbeats,
	// task-sync) that share the same per-server lock.
	var (
		err      error
		lockWait time.Duration
	)
	p.callMu, lockWait, err = p.daemon.acquireCallLock(p.ctx, p.serverName)
	if err != nil {
		p.daemon.router.RecordFailure(p.serverName, p.target, err)
		p.daemon.metrics.RecordServerFailure(p.serverName, p.targetStr, "call_lock")
		return err
	}
	p.lockHeld = true
	if lockWait > 100*time.Millisecond {
		p.daemon.metrics.CallLockWaitTotal.WithLabelValues(p.serverName).Inc()
		p.daemon.logger.Debug("call lock contention", "server", p.serverName, "wait_ms", lockWait.Milliseconds())
	}

	// Mark activity under the lock (quick operation).
	if target == router.TargetLocal && p.daemon.procMgr != nil {
		p.daemon.procMgr.MarkActivity(p.serverName)
	}

	// Release the lock before the potentially-blocking pool.Get().
	p.callMu.Unlock()
	p.lockHeld = false

	switch target {
	case router.TargetLocal:
		if p.daemon.pool == nil {
			err = fmt.Errorf("local pool not configured")
		} else {
			p.conn, err = p.daemon.pool.Get(p.ctx, p.serverName)
			if err == nil {
				p.conn = p.discardIfStale(p.conn, p.daemon.pool)
				if p.conn == nil {
					err = fmt.Errorf("stale connection discarded and fresh dial failed for %s", p.serverName)
				}
			}
		}
	case router.TargetHub:
		if p.daemon.hubPool == nil {
			err = fmt.Errorf("hub fallback not configured")
		} else {
			p.conn, err = p.daemon.hubPool.Get(p.ctx, p.serverName)
			if err == nil {
				p.conn = p.discardIfStale(p.conn, p.daemon.hubPool)
				if p.conn == nil {
					err = fmt.Errorf("stale connection discarded and fresh dial failed for %s", p.serverName)
				}
			}
		}
	}

	if err != nil {
		p.daemon.router.RecordFailure(p.serverName, p.target, err)
		p.daemon.metrics.RecordServerFailure(p.serverName, p.targetStr, "connect")
		return err
	}

	// Re-acquire the lock for the RPC send phase. The lock now only
	// serializes the actual send/recv on the connection, not the wait
	// for a pool slot.
	p.callMu, _, err = p.daemon.acquireCallLock(p.ctx, p.serverName)
	if err != nil {
		// Connection acquired but can't get lock — return connection to pool.
		if p.conn != nil {
			switch target {
			case router.TargetLocal:
				if p.daemon.pool != nil {
					p.daemon.pool.Put(p.conn)
				}
			case router.TargetHub:
				if p.daemon.hubPool != nil {
					p.daemon.hubPool.Put(p.conn)
				}
			}
			p.conn = nil
		}
		p.daemon.router.RecordFailure(p.serverName, p.target, err)
		p.daemon.metrics.RecordServerFailure(p.serverName, p.targetStr, "call_lock")
		return err
	}
	p.lockHeld = true

	return nil
}

// discardIfStale checks whether a pooled connection has been idle longer than
// the configured stale threshold. If so, it marks the connection unhealthy,
// returns it to the pool (which closes it), and dials a fresh connection.
// Returns the original connection if not stale or if the threshold is disabled.
func (p *callPipeline) discardIfStale(conn *pool.Conn, pl *pool.Pool) *pool.Conn {
	threshold := p.daemon.poolStaleThreshold()
	if threshold <= 0 || conn == nil {
		return conn
	}
	idle := time.Since(conn.LastUsed)
	if idle <= threshold {
		return conn
	}

	p.daemon.logger.Debug("discarding stale pool connection",
		"server", p.serverName,
		"idle", idle.Round(time.Second),
		"threshold", threshold)

	conn.Healthy = false
	pl.Put(conn)

	fresh, err := pl.Get(p.ctx, p.serverName)
	if err != nil {
		p.daemon.logger.Warn("failed to dial fresh connection after discarding stale",
			"server", p.serverName, "error", err)
		return nil
	}
	return fresh
}

func (p *callPipeline) shouldRetryLocalAfterHubFailure() bool {
	return p.preferHubRetryEligible &&
		!p.localRetryUsed &&
		p.target == router.TargetHub &&
		p.daemon != nil &&
		p.daemon.pool != nil
}

func (p *callPipeline) retryLocalAfterHubFailure(stage string, err error, req *mcp.Message, start time.Time) (*mcp.Message, bool) {
	if !p.shouldRetryLocalAfterHubFailure() {
		return nil, false
	}

	p.localRetryUsed = true
	if p.conn != nil {
		p.conn.Healthy = false
	}
	p.daemon.router.RecordFailure(p.serverName, p.target, err)
	p.daemon.metrics.RecordServerFailure(p.serverName, p.targetStr, stage)
	if p.daemon.hubPool != nil {
		p.daemon.hubPool.ClearServer(p.serverName)
	}
	if p.daemon.hubClient != nil {
		p.daemon.hubClient.CloseConnection(p.serverName)
	}

	until := p.daemon.setPreferHubBackoff(p.serverName, preferHubBackoffDuration)
	p.daemon.logger.Warn("prefer-hub override transport failed; retrying local",
		"server", p.serverName,
		"stage", stage,
		"error", err,
		"backoff_until", until)
	p.recordTransportSpanEvent("daemon.proxy.local_retry_after_hub_failure",
		attribute.String("server.name", p.serverName),
		attribute.String("failure.stage", stage),
		attribute.String("failure.error", err.Error()),
		attribute.String("routing.from", router.TargetHub.String()),
		attribute.String("routing.to", router.TargetLocal.String()),
		attribute.String("retry.backoff_until", until.Format(time.RFC3339Nano)),
	)

	p.releaseConnection()

	if connectErr := p.connectTarget(router.TargetLocal, "prefer-hub fallback after hub transport failure"); connectErr != nil {
		combined := fmt.Errorf("hub %s failed: %v; local retry failed: %w", stage, err, connectErr)
		p.daemon.metrics.RecordRequest(p.serverName, p.method, "error", p.targetStr, time.Since(start))
		return p.internalErrorWithAudit(p.targetStr, combined), true
	}

	return p.execute(req), true
}
