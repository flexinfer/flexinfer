package monitor

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// RefreshFunc fetches fresh state and returns it. Monitors supply this to
// BaseMonitor.Start so the generic poll loop can drive the refresh cycle.
type RefreshFunc[T any] func(ctx context.Context) (T, error)

// BaseMonitor provides lifecycle management for polling monitors.
// It handles the shared concerns: RWMutex-protected snapshot, stop channel
// with sync.Once, exponential-backoff poll loop, OnRefresh callback, and
// optional OTel span instrumentation per refresh cycle.
//
// Monitors embed BaseMonitor and either:
//   - Supply a simple RefreshFunc to Start (for straightforward monitors), or
//   - Call StartManual to get stop/pollLoop management while keeping their
//     own Refresh implementation that calls Update when done.
type BaseMonitor[T any] struct {
	// Logger is the structured logger for this monitor.
	Logger *slog.Logger

	// Tracer is the OTel tracer. Defaults to noop when unset.
	Tracer trace.Tracer

	// Component is the human-readable name used in log messages and span names.
	Component string

	mu        sync.RWMutex
	snapshot  T
	onRefresh func(T)
	stopCh    chan struct{}
	stopOnce  sync.Once
}

// InitBase initializes the BaseMonitor fields. Call this from the concrete
// monitor's constructor. If tracer is nil a noop tracer is used.
func (b *BaseMonitor[T]) InitBase(logger *slog.Logger, tracer trace.Tracer, component string) {
	if logger == nil {
		logger = slog.Default()
	}
	b.Logger = logger.With("component", component)
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer(component)
	}
	b.Tracer = tracer
	b.Component = component
	b.stopCh = make(chan struct{})
}

// OnRefresh registers a callback invoked after each successful refresh with
// the new snapshot value. Used to broadcast state changes via SSE.
func (b *BaseMonitor[T]) OnRefresh(fn func(T)) {
	b.onRefresh = fn
}

// Snapshot returns the current cached snapshot under a read lock.
func (b *BaseMonitor[T]) Snapshot() T {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.snapshot
}

// Update stores a new snapshot under a write lock and fires the OnRefresh
// callback (outside the lock). Monitors with complex Refresh logic call
// this directly rather than using a RefreshFunc.
func (b *BaseMonitor[T]) Update(val T) {
	b.mu.Lock()
	b.snapshot = val
	b.mu.Unlock()

	if b.onRefresh != nil {
		b.onRefresh(val)
	}
}

// Stop signals the background goroutine to exit. Safe to call multiple times.
func (b *BaseMonitor[T]) Stop() {
	b.stopOnce.Do(func() { close(b.stopCh) })
}

// StopCh returns the stop channel for monitors that run their own poll loop.
func (b *BaseMonitor[T]) StopCh() <-chan struct{} {
	return b.stopCh
}

// Start launches an async initial refresh followed by the standard poll loop.
// The supplied RefreshFunc is called each cycle. For monitors that need a
// custom poll loop, use StartManual instead.
func (b *BaseMonitor[T]) Start(interval time.Duration, refreshFn RefreshFunc[T]) {
	go func() {
		if _, err := b.doRefresh(refreshFn); err != nil {
			b.Logger.Warn("initial refresh failed", "error", err)
		}
	}()
	go b.pollLoop(interval, refreshFn)
}

// StartManual launches only the stop-channel infrastructure without starting
// any poll loop. The caller is responsible for running its own goroutine.
// This is used by monitors like PipelineMonitor that need adaptive polling.
func (b *BaseMonitor[T]) StartManual() {
	// stopCh is already initialized by InitBase; nothing else needed.
}

// doRefresh executes the refresh function inside an OTel span, stores the
// result on success, and returns the value and error.
func (b *BaseMonitor[T]) doRefresh(refreshFn RefreshFunc[T]) (T, error) {
	ctx, span := b.Tracer.Start(context.Background(), b.Component+".refresh",
		trace.WithAttributes(attribute.String("monitor.component", b.Component)),
	)
	defer span.End()

	val, err := refreshFn(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		var zero T
		return zero, err
	}

	span.SetStatus(codes.Ok, "")
	b.Update(val)
	return val, nil
}

// pollLoop runs the refresh function on a ticker with exponential backoff.
// On consecutive errors it skips up to 4 ticks (5x the base interval).
func (b *BaseMonitor[T]) pollLoop(interval time.Duration, refreshFn RefreshFunc[T]) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	consecutiveErrors := 0
	for {
		select {
		case <-b.stopCh:
			b.Logger.Debug("monitor stopped")
			return
		case <-ticker.C:
			if _, err := b.doRefresh(refreshFn); err != nil {
				consecutiveErrors++
				if consecutiveErrors <= 3 {
					b.Logger.Warn("refresh error", "error", err)
				}
				skipTicks := min(consecutiveErrors-1, 4)
				for range skipTicks {
					select {
					case <-b.stopCh:
						return
					case <-ticker.C:
					}
				}
			} else {
				if consecutiveErrors > 0 {
					b.Logger.Info("refresh recovered", "after_errors", consecutiveErrors)
				}
				consecutiveErrors = 0
			}
		}
	}
}

// RLock acquires a read lock on the snapshot mutex. Monitors with complex
// internal state (HealthMonitor, FleetMonitor) use this for direct access.
func (b *BaseMonitor[T]) RLock() { b.mu.RLock() }

// RUnlock releases the read lock.
func (b *BaseMonitor[T]) RUnlock() { b.mu.RUnlock() }

// Lock acquires a write lock on the snapshot mutex.
func (b *BaseMonitor[T]) Lock() { b.mu.Lock() }

// Unlock releases the write lock.
func (b *BaseMonitor[T]) Unlock() { b.mu.Unlock() }

// SetSnapshot sets the snapshot directly (caller must hold the write lock).
// This is for monitors that need to update the snapshot while holding the
// lock for other operations (e.g., FleetMonitor updating KPIs atomically).
func (b *BaseMonitor[T]) SetSnapshot(val T) {
	b.snapshot = val
}

// GetSnapshot returns the snapshot directly (caller must hold at least a
// read lock). This is for monitors that need to read the snapshot while
// holding the lock for other operations.
func (b *BaseMonitor[T]) GetSnapshot() T {
	return b.snapshot
}

// FireOnRefresh calls the onRefresh callback if set. Call outside the lock.
func (b *BaseMonitor[T]) FireOnRefresh(val T) {
	if b.onRefresh != nil {
		b.onRefresh(val)
	}
}
