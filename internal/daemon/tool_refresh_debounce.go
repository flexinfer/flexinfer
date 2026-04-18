package daemon

import (
	"context"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// toolRefreshDebounce coalesces rapid schedule calls into a single refresh that
// fires after the quiet period elapses. It exists so upstream pod flapping
// (disconnect / reconnect / disconnect / ...) does not thrash the tool cache.
type toolRefreshDebounce struct {
	mu       sync.Mutex
	timer    *time.Timer
	interval time.Duration
	// onFire runs when the debounced timer finally fires. Kept as a field so
	// tests can swap in a counter.
	onFire func()
}

func newToolRefreshDebounce(interval time.Duration, onFire func()) *toolRefreshDebounce {
	return &toolRefreshDebounce{interval: interval, onFire: onFire}
}

// schedule resets the debounce window. If no call arrives within interval,
// onFire runs exactly once.
func (t *toolRefreshDebounce) schedule() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(t.interval, t.onFire)
}

// stop cancels any pending refresh. Used during daemon shutdown.
func (t *toolRefreshDebounce) stop() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

// toolRefreshDebounceInterval is the quiet period after the last upstream
// disconnect/reconnect event before the daemon refreshes the tool cache.
const toolRefreshDebounceInterval = 3 * time.Second

// scheduleToolRefresh debounces tool-cache refreshes triggered by upstream
// reconnects. The first call to this during daemon lifetime lazily constructs
// the debouncer; callers race-safely via the Daemon mutex is not required
// because toolRefreshDebounce itself guards with its own mutex, but we do need
// a CAS for the lazy init. We use a sync.Once guarded by an atomic pointer.
func (d *Daemon) scheduleToolRefresh() {
	if d == nil {
		return
	}
	d.toolRefreshOnce.Do(func() {
		d.toolRefresh = newToolRefreshDebounce(toolRefreshDebounceInterval, func() {
			// Guard against firing before the daemon is fully initialized
			// (e.g., unit tests construct a minimal Daemon and call
			// transportFailure directly).
			if d.registry == nil {
				return
			}
			// Use a bounded background context: the original request context
			// is long dead by the time the timer fires.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := d.refreshToolCacheDeduplicated(ctx); err != nil && d.logger != nil {
				d.logger.Debug("debounced tool cache refresh failed", "error", err)
			}
		})
	})
	d.toolRefresh.schedule()
}

// handleToolsReload is a manual escape hatch: forces a synchronous refresh of
// the daemon's tool cache. Useful after redeploying an upstream MCP server
// without having to restart every connected client.
func (d *Daemon) handleToolsReload(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tools, err := d.refreshToolCacheDeduplicated(refreshCtx)
	if err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error()), nil
	}
	return mcp.NewResponse(msg.ID, map[string]any{
		"ok":         true,
		"tool_count": len(tools),
	})
}
