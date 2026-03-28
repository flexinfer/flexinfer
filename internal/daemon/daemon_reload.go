// daemon_reload.go contains file watcher and signal-driven reload loops.
package daemon

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// watcherLoop handles file watcher events and triggers reloads.
func (d *Daemon) watcherLoop(ctx context.Context) {
	if d.watcher == nil {
		return
	}

	for {
		select {
		case <-d.done:
			return
		case <-ctx.Done():
			return
		case event, ok := <-d.watcher.Events():
			if !ok {
				return
			}
			d.logger.Info("file change detected", "type", event.Type, "path", event.Path, "profile", event.Profile)

			// Trigger reload
			reloadCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			if err := d.Reload(reloadCtx); err != nil {
				d.logger.Error("reload after file change failed", "error", err)
			} else {
				d.logger.Info("reload completed after file change")
			}
			cancel()
		}
	}
}

// signalLoop handles SIGHUP for manual reload.
func (d *Daemon) signalLoop(ctx context.Context) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP)
	defer signal.Stop(sigChan)

	for {
		select {
		case <-d.done:
			return
		case <-ctx.Done():
			return
		case sig := <-sigChan:
			d.logger.Info("received signal", "signal", sig)

			reloadCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			if err := d.Reload(reloadCtx); err != nil {
				d.logger.Error("reload after SIGHUP failed", "error", err)
			} else {
				d.logger.Info("reload completed after SIGHUP")
			}
			cancel()
		}
	}
}
