package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/process"
	"github.com/crb2nu/loom/internal/router"
	pkgtestutil "github.com/crb2nu/loom/pkg/testutil"
)

// newLoopsTestPool creates a minimal pool for daemon loop tests.
func newLoopsTestPool() *pool.Pool {
	return pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Minute,
		DialFunc: func(context.Context, string) (mcp.Transport, error) {
			return nil, errors.New("dial not implemented in loop test")
		},
	})
}

func TestSessionReaperLoop_ExitsOnDone(t *testing.T) {
	defer pkgtestutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	d := &Daemon{
		done:   make(chan struct{}),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	exited := make(chan struct{})
	go func() {
		d.sessionReaperLoop()
		close(exited)
	}()

	// Signal the loop to stop.
	close(d.done)

	select {
	case <-exited:
		// Success: loop exited cleanly.
	case <-time.After(2 * time.Second):
		t.Fatal("sessionReaperLoop did not exit within 2s after done channel closed")
	}
}

func TestIdleReaperLoop_ExitsOnDone(t *testing.T) {
	defer pkgtestutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	d := &Daemon{
		done:    make(chan struct{}),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		fileCfg: FileConfig{Resources: ResourceConfig{IdleTimeoutMinutes: 5}},
		procMgr: process.NewManager(nil, "test"),
	}

	exited := make(chan struct{})
	go func() {
		d.idleReaperLoop()
		close(exited)
	}()

	close(d.done)

	select {
	case <-exited:
		// Success.
	case <-time.After(2 * time.Second):
		t.Fatal("idleReaperLoop did not exit within 2s after done channel closed")
	}
}

func TestMetricsCollectorLoop_ExitsOnDone(t *testing.T) {
	defer pkgtestutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	d := &Daemon{
		done:      make(chan struct{}),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		metrics:   NewMetrics(),
		pool:      newLoopsTestPool(),
		procMgr:   process.NewManager(nil, "test"),
		toolCache: &ToolCache{},
		router:    router.New(router.Config{}),
	}
	t.Cleanup(func() { d.pool.Close() })

	exited := make(chan struct{})
	go func() {
		d.metricsCollectorLoop()
		close(exited)
	}()

	close(d.done)

	select {
	case <-exited:
		// Success.
	case <-time.After(2 * time.Second):
		t.Fatal("metricsCollectorLoop did not exit within 2s after done channel closed")
	}
}

func TestCollectMetrics_UpdatesGauges(t *testing.T) {
	defer pkgtestutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	d := &Daemon{
		done:      make(chan struct{}),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		metrics:   NewMetrics(),
		pool:      newLoopsTestPool(),
		procMgr:   process.NewManager(nil, "test"),
		toolCache: &ToolCache{},
		router:    router.New(router.Config{}),
	}
	t.Cleanup(func() { d.pool.Close() })

	// Call collectMetrics directly.
	d.collectMetrics()

	// GoroutineCount should be > 0 (we're running goroutines).
	goroutines := testutil.ToFloat64(d.metrics.GoroutineCount)
	if goroutines <= 0 {
		t.Errorf("GoroutineCount should be > 0, got %v", goroutines)
	}

	// MemAllocBytes should be > 0.
	memAlloc := testutil.ToFloat64(d.metrics.MemAllocBytes)
	if memAlloc <= 0 {
		t.Errorf("MemAllocBytes should be > 0, got %v", memAlloc)
	}

	// MemSysBytes should be > 0.
	memSys := testutil.ToFloat64(d.metrics.MemSysBytes)
	if memSys <= 0 {
		t.Errorf("MemSysBytes should be > 0, got %v", memSys)
	}

	// ProcessCount should be 0 (no processes started).
	procCount := testutil.ToFloat64(d.metrics.ProcessCount)
	if procCount != 0 {
		t.Errorf("ProcessCount should be 0, got %v", procCount)
	}
}

func TestCollectMetrics_NilHubPoolDoesNotPanic(t *testing.T) {
	defer pkgtestutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	d := &Daemon{
		done:      make(chan struct{}),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		metrics:   NewMetrics(),
		pool:      newLoopsTestPool(),
		hubPool:   nil, // Explicitly nil.
		procMgr:   process.NewManager(nil, "test"),
		toolCache: &ToolCache{},
		router:    router.New(router.Config{}),
	}
	t.Cleanup(func() { d.pool.Close() })

	// Should not panic with nil hubPool and nil hubClient.
	d.collectMetrics()
}

func TestSessionReaperLoop_WithSessionManager(t *testing.T) {
	defer pkgtestutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	sm := NewSessionManager(100, 1*time.Millisecond, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))

	d := &Daemon{
		done:     make(chan struct{}),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessions: sm,
	}

	// Add a session that will expire immediately.
	sm.Open(SessionClientInfo{}, "")

	// Wait for the session to expire.
	time.Sleep(5 * time.Millisecond)

	exited := make(chan struct{})
	go func() {
		d.sessionReaperLoop()
		close(exited)
	}()

	// Let the loop run for a brief moment so it can tick and reap.
	// The ticker in sessionReaperLoop is 30s which is too long for a test,
	// so we just verify clean exit.
	close(d.done)

	select {
	case <-exited:
		// Success.
	case <-time.After(2 * time.Second):
		t.Fatal("sessionReaperLoop did not exit within 2s after done channel closed")
	}
}
