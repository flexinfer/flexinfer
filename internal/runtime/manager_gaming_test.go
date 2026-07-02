package runtime

import (
	"context"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newGamingManager returns a manager already in gaming mode with fast restart
// backoff and the restart hook stubbed to record invocations.
func newGamingManager(t *testing.T, restart func(ctx context.Context) error) *Manager {
	t.Helper()
	m := NewManager(ManagerConfig{})
	m.gamingRestartBase = 5 * time.Millisecond
	m.gamingRestartMax = 20 * time.Millisecond
	m.gamingRestart = restart
	m.mu.Lock()
	m.mode = ModeGaming
	m.mu.Unlock()
	return m
}

// startCrashingGamingBackend registers a real subprocess as the gaming model
// and returns it once started. The command mimics the 2026-07-01 outage where
// the Sunshine wrapper died shortly after start (useradd exited 4).
func startCrashingGamingBackend(t *testing.T, m *Manager, script string) *LoadedModel {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	require.NoError(t, cmd.Start())
	loaded := &LoadedModel{
		Name:     gamingModelName,
		Backend:  backend.NameSunshine,
		State:    ModelStateReady,
		PID:      cmd.Process.Pid,
		LoadedAt: time.Now(),
		cmd:      cmd,
		done:     make(chan error, 1),
	}
	m.mu.Lock()
	m.models[loaded.Name] = loaded
	m.mu.Unlock()
	return loaded
}

// Regression for the 2026-07-01 outage: the Sunshine backend crashed while the
// node was in gaming mode, the runtime kept reporting mode=gaming, the
// GamingSession reconciler's idempotent SetMode(gaming) no-op'd, and Sunshine
// stayed dead until a pod restart. The runtime must mark the model Failed,
// report the session degraded, and re-drive the backend itself.
func TestGamingBackendCrashTriggersSupervisedRestart(t *testing.T) {
	restarts := make(chan struct{}, 8)
	m := newGamingManager(t, func(ctx context.Context) error {
		restarts <- struct{}{}
		return nil
	})
	loaded := startCrashingGamingBackend(t, m, "exit 4")

	go m.monitorProcess(context.Background(), loaded)

	select {
	case <-restarts:
	case <-time.After(2 * time.Second):
		t.Fatal("gaming backend was not restarted after subprocess crash")
	}

	m.mu.RLock()
	state := loaded.State
	errMsg := loaded.Error
	m.mu.RUnlock()
	assert.Equal(t, ModelStateFailed, state)
	assert.Contains(t, errMsg, "exit status 4")
	assert.Equal(t, ModeGaming, m.Mode(), "mode must remain gaming while supervised")
}

// A clean exit (status 0) of the gaming backend is still a crash while the
// node is in gaming mode — nothing but an unload should stop Sunshine.
func TestGamingBackendCleanExitIsCrashWhileGaming(t *testing.T) {
	restarts := make(chan struct{}, 8)
	m := newGamingManager(t, func(ctx context.Context) error {
		restarts <- struct{}{}
		return nil
	})
	loaded := startCrashingGamingBackend(t, m, "exit 0")

	go m.monitorProcess(context.Background(), loaded)

	select {
	case <-restarts:
	case <-time.After(2 * time.Second):
		t.Fatal("gaming backend was not restarted after clean unexpected exit")
	}

	m.mu.RLock()
	state := loaded.State
	errMsg := loaded.Error
	m.mu.RUnlock()
	assert.Equal(t, ModelStateFailed, state)
	assert.Equal(t, "backend exited unexpectedly", errMsg)
}

// While the crashed backend awaits restart, the runtime must report the gaming
// session degraded so the GamingSession controller shows Degraded, not Active.
func TestGamingDegradedReporting(t *testing.T) {
	m := NewManager(ManagerConfig{})

	// Inference mode: never degraded.
	degraded, _ := m.GamingDegraded()
	assert.False(t, degraded)

	// Gaming mode with no gaming model loaded: degraded.
	m.mu.Lock()
	m.mode = ModeGaming
	m.mu.Unlock()
	degraded, detail := m.GamingDegraded()
	assert.True(t, degraded)
	assert.Contains(t, detail, "not loaded")

	// Loading and Ready are healthy.
	for _, state := range []ModelState{ModelStateLoading, ModelStateReady} {
		m.mu.Lock()
		m.models[gamingModelName] = &LoadedModel{Name: gamingModelName, Backend: backend.NameSunshine, State: state}
		m.mu.Unlock()
		degraded, _ = m.GamingDegraded()
		assert.False(t, degraded, "state %s must not be degraded", state)
	}

	// Failed: degraded with the crash detail.
	m.mu.Lock()
	m.models[gamingModelName] = &LoadedModel{
		Name: gamingModelName, Backend: backend.NameSunshine,
		State: ModelStateFailed, Error: "exit status 4",
	}
	m.gamingRestartAttempt = 2
	m.mu.Unlock()
	degraded, detail = m.GamingDegraded()
	assert.True(t, degraded)
	assert.Contains(t, detail, "exit status 4")
	assert.Contains(t, detail, "attempt 2")
}

// The restart delay grows exponentially from base to max across a crash loop,
// and resets to base after the backend ran stably before crashing.
func TestNextGamingRestartDelayBackoff(t *testing.T) {
	m := NewManager(ManagerConfig{})
	base := time.Unix(1_700_000_000, 0)
	m.nowFn = func() time.Time { return base }

	want := []time.Duration{
		2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, time.Minute, time.Minute,
	}
	for i, w := range want {
		got := m.nextGamingRestartDelayLocked(time.Time{})
		assert.Equal(t, w, got, "attempt %d", i+1)
	}

	// A crash after a stable run restarts the backoff from the base delay.
	stableLoadedAt := base.Add(-gamingStableUptime)
	assert.Equal(t, 2*time.Second, m.nextGamingRestartDelayLocked(stableLoadedAt))
	assert.Equal(t, 1, m.gamingRestartAttempt)

	// A quick crash right after continues the loop instead of resetting.
	recentLoadedAt := base.Add(-time.Second)
	assert.Equal(t, 4*time.Second, m.nextGamingRestartDelayLocked(recentLoadedAt))
}

// Failed restart attempts (e.g. the backend binary cannot even start) keep
// retrying with backoff until one succeeds.
func TestGamingRestartRetriesUntilSuccess(t *testing.T) {
	var calls atomic.Int32
	success := make(chan struct{})
	m := newGamingManager(t, nil)
	m.gamingRestart = func(ctx context.Context) error {
		if calls.Add(1) < 3 {
			return assert.AnError
		}
		close(success)
		return nil
	}

	go m.superviseGamingRestart(context.Background(), time.Millisecond)

	select {
	case <-success:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not retry until a restart succeeded")
	}
	assert.Equal(t, int32(3), calls.Load())
}

// The supervisor must not restart the gaming backend after the node has left
// gaming mode (e.g. the GamingSession was deleted during the backoff window).
func TestGamingRestartSupervisorAbortsAfterModeSwitch(t *testing.T) {
	var calls atomic.Int32
	m := newGamingManager(t, func(ctx context.Context) error {
		calls.Add(1)
		return nil
	})
	m.mu.Lock()
	m.mode = ModeInference
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.superviseGamingRestart(context.Background(), time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not exit after mode switch")
	}
	assert.Equal(t, int32(0), calls.Load(), "no restart once the node left gaming mode")
}

// Supervision defaults are wired at construction.
func TestNewManagerGamingSupervisionDefaults(t *testing.T) {
	m := NewManager(ManagerConfig{})
	assert.Equal(t, defaultGamingRestartBase, m.gamingRestartBase)
	assert.Equal(t, defaultGamingRestartMax, m.gamingRestartMax)
	assert.NotNil(t, m.gamingRestart, "restart hook must be wired")
	assert.Equal(t, 0, m.gamingRestartAttempt)
}
