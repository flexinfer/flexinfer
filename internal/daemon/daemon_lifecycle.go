// daemon_lifecycle.go contains daemon startup, shutdown, and drain methods.
package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/pkg/sync"
)

func (d *Daemon) acquireLock() error {
	home, _ := os.UserHomeDir()
	lockDir := filepath.Join(home, ".config", "loom")
	if err := os.MkdirAll(lockDir, 0700); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	lockPath := filepath.Join(lockDir, "loomd.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	// Non-blocking exclusive lock. If another daemon holds it, do not touch the socket.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return fmt.Errorf("daemon already running (lock held): %w", err)
	}
	// Prevent child MCP server processes from inheriting the lock FD.
	// If loomd crashes while children run, orphans must not hold the lock.
	syscall.CloseOnExec(int(f.Fd()))
	// Write PID to lock file for status reporting.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)
	d.lockFile = f
	return nil
}

// Start starts the daemon.
func (d *Daemon) Start(ctx context.Context) (err error) {
	ctx, span := d.daemonTracer().Start(ctx, "daemon.start",
		trace.WithAttributes(
			attribute.String("loom.socket_path", d.cfg.SocketPath),
			attribute.Int("loom.warm_server_count", len(d.cfg.WarmOnStart)),
			attribute.Bool("loom.streamable_http_enabled", d.cfg.HTTPAddr != ""),
		),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	// Bail out early if registry was not provided; running without it will panic.
	if d.registry == nil {
		err = fmt.Errorf("registry not loaded (pass --registry /path/to/registry.yaml)")
		return err
	}

	// Prevent multiple daemons from unlinking/rebinding the same socket path.
	if err = d.acquireLock(); err != nil {
		return err
	}
	started := false
	defer func() {
		// If we fail during startup, release the lock so the user can retry.
		// On success, keep it held for the process lifetime; Stop() releases it.
		if !started && d.lockFile != nil {
			_ = d.lockFile.Close()
			d.lockFile = nil
		}
	}()

	// Load cached manifest for instant tool availability
	if err := d.manifest.Load(); err != nil {
		d.logger.Warn("failed to load manifest", "error", err)
	} else if d.manifest.ServerCount() > 0 {
		// Pre-populate tool cache from manifest
		cachedTools := d.manifest.GetAllTools()
		d.toolCache.mu.Lock()
		d.toolCache.tools = cachedTools
		d.toolCache.updatedAt = d.manifest.LastUpdated()
		d.toolCache.mu.Unlock()
		d.logger.Info("loaded cached tools from manifest",
			"tools", len(cachedTools),
			"servers", d.manifest.ServerCount(),
			"age", time.Since(d.manifest.LastUpdated()).Round(time.Second))
	}

	// Ensure socket directory exists
	if err := os.MkdirAll(filepath.Dir(d.cfg.SocketPath), 0700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	// Lock is held — any existing socket is stale. The "daemon already running"
	// check is handled entirely by acquireLock(). Remove unconditionally.
	_ = os.Remove(d.cfg.SocketPath)

	// Listen on Unix socket
	lc := net.ListenConfig{}
	listener, err := lc.Listen(ctx, "unix", d.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	d.listener = listener

	d.logger.Info("daemon started", "socket", d.cfg.SocketPath)

	// Warm up connections if configured
	if len(d.cfg.WarmOnStart) > 0 {
		d.logger.Info("warming up connections", "servers", d.cfg.WarmOnStart)
		if err := d.pool.WarmUp(ctx, d.cfg.WarmOnStart); err != nil {
			d.logger.Warn("warm up failed", "error", err)
		}
	}

	// Background refresh of tool cache (non-blocking)
	go func() {
		warmCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if _, err := d.refreshToolCache(warmCtx); err != nil {
			d.logger.Warn("background tool cache refresh failed", "error", err)
		}
	}()

	// Start weaver in background (non-fatal).
	go func() {
		orchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := d.startEmbeddedWeaver(orchCtx); err != nil {
			d.logger.Error("weaver init failed", "error", err)
		}
	}()

	// Initialize proxy session manager
	sessMax := d.fileCfg.HTTP.MaxSessions
	if sessMax <= 0 {
		sessMax = 1000
	}
	sessTimeout := time.Duration(d.fileCfg.HTTP.SessionTimeoutMinutes) * time.Minute
	if sessTimeout <= 0 {
		sessTimeout = 30 * time.Minute
	}
	d.sessions = NewSessionManager(sessMax, sessTimeout, d.daemonEpoch, d.logger)
	if d.metrics != nil {
		d.sessions.SetMetrics(d.metrics)
	}
	d.logger.Info("proxy session manager initialized", "max_sessions", sessMax, "lease_minutes", int(sessTimeout.Minutes()))

	// Start session reaper
	go d.sessionReaperLoop()

	// Start idle server reaper
	go d.idleReaperLoop()

	// Start metrics collector
	go d.metricsCollectorLoop()

	// Start health monitor
	if d.healthMonitor != nil {
		d.healthMonitor.Start()
		d.logger.Info("health monitor started")
	}

	// Start hub WebSocket keepalive if hub is configured
	if d.hubClient != nil && d.hubPool != nil {
		d.wg.Add(1)
		go d.hubKeepaliveLoop()
		d.logger.Info("hub keepalive started",
			"interval_seconds", d.fileCfg.Hub.PingIntervalSeconds)
	}

	// Start tunnel manager and establish tunnels for servers with SSH config
	if d.tunnelMgr != nil {
		d.tunnelMgr.Start(ctx)
		d.startTunnelsForServers()
		d.logger.Info("tunnel manager started")
	}

	// Start file watcher for hot reload
	if d.syncManager != nil {
		watcher, err := sync.NewWatcher(sync.WatcherConfig{
			Manager:      d.syncManager,
			RepoRoot:     d.repoRoot,
			RegistryPath: d.cfg.RegistryPath,
			Logger:       d.logger,
		})
		if err != nil {
			d.logger.Warn("failed to create file watcher", "error", err)
		} else {
			d.watcher = watcher
			if err := watcher.Start(); err != nil {
				d.logger.Warn("failed to start file watcher", "error", err)
			} else {
				d.logger.Info("file watcher started")
				go d.watcherLoop(ctx)
			}
		}
	}

	// Start SIGHUP handler for manual reload
	go d.signalLoop(ctx)

	// Accept connections
	d.wg.Add(1)
	go d.acceptLoop(ctx)

	// Start Streamable HTTP listener if configured
	if d.cfg.HTTPAddr != "" {
		if httpErr := d.startHTTPListener(ctx); httpErr != nil {
			d.logger.Error("failed to start HTTP listener", "error", httpErr)
			span.RecordError(httpErr)
			span.SetAttributes(attribute.Bool("loom.http_listener_start_failed", true))
			// Non-fatal: Unix socket still works
		}
	}

	started = true
	span.SetAttributes(attribute.Bool("loom.started", true))
	return nil
}

// SetDraining transitions the daemon into drain mode. New loom/call requests
// are rejected with a retryable error and all active sessions are drained.
func (d *Daemon) SetDraining() {
	d.draining.Store(true)
	if d.sessions != nil {
		d.sessions.DrainAll()
	}
}

// IsDraining returns true if the daemon is in drain mode.
func (d *Daemon) IsDraining() bool {
	return d.draining.Load()
}

// Stop stops the daemon.
func (d *Daemon) Stop() (err error) {
	_, span := d.daemonTracer().Start(context.Background(), "daemon.stop")
	defer func() {
		d.otelShutdownOnce.Do(func() {
			if d.otelShutdown != nil {
				_ = d.otelShutdown(context.Background())
			}
		})
	}()
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	d.stopOnce.Do(func() {
		if d.done != nil {
			close(d.done)
		}

		// Drain all proxy sessions and set daemon-level drain flag.
		d.SetDraining()

		// Stop health monitor first
		if d.healthMonitor != nil {
			d.healthMonitor.Stop()
		}

		// Stop tunnel manager
		if d.tunnelMgr != nil {
			d.tunnelMgr.Stop()
		}

		// Stop embedded HUD monitors
		if d.hudApp != nil {
			d.hudApp.StopMonitors()
		}

		// Shutdown HTTP server
		if d.httpServer != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = d.httpServer.Shutdown(shutdownCtx)
			cancel()
		}

		if d.listener != nil {
			_ = d.listener.Close()
		}
		_ = os.Remove(d.cfg.SocketPath)

		if d.pool != nil {
			d.pool.Close()
		}
		if d.hubPool != nil {
			d.hubPool.Close()
		}
		if d.hubClient != nil {
			_ = d.hubClient.Close()
		}
		// Emit process.stop events for all running servers before shutdown.
		if d.eventBus != nil && d.procMgr != nil {
			for _, name := range d.procMgr.List() {
				d.eventBus.Publish(EventProcessStop, map[string]any{
					"server": name,
					"reason": "daemon_shutdown",
				})
			}
		}
		if d.procMgr != nil {
			d.procMgr.StopAll()
		}

		// Stop file watcher
		if d.watcher != nil {
			if err := d.watcher.Stop(); err != nil && d.logger != nil {
				d.logger.Warn("failed to stop watcher", "error", err)
			}
		}

		// Close audit logger
		if d.audit != nil {
			if err := d.audit.Close(); err != nil && d.logger != nil {
				d.logger.Warn("failed to close audit logger", "error", err)
			}
		}

		// Save manifest for next startup
		if d.manifest != nil {
			if err := d.manifest.Save(); err != nil && d.logger != nil {
				d.logger.Warn("failed to save manifest", "error", err)
			}
		}

		d.wg.Wait()
		if d.logger != nil {
			d.logger.Info("daemon stopped")
		}

		if d.lockFile != nil {
			_ = d.lockFile.Close()
			d.lockFile = nil
		}
	})
	err = d.stopErr
	return err
}

// Wait waits for the daemon to stop.
func (d *Daemon) Wait() {
	d.wg.Wait()
}
