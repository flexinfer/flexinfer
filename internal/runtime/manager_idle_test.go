package runtime

import (
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/backend"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestGamingShouldRevert(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name         string
		clientActive bool
		idleSince    time.Time
		now          time.Time
		timeout      time.Duration
		wantRevert   bool
	}{
		{"disabled timeout never reverts", false, base, base.Add(time.Hour), 0, false},
		{"active client never reverts", true, base, base.Add(time.Hour), 10 * time.Minute, false},
		{"idle below timeout holds", false, base, base.Add(5 * time.Minute), 10 * time.Minute, false},
		{"idle at timeout reverts", false, base, base.Add(10 * time.Minute), 10 * time.Minute, true},
		{"idle past timeout reverts", false, base, base.Add(20 * time.Minute), 10 * time.Minute, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			revert, newIdle := gamingShouldRevert(tt.clientActive, tt.idleSince, tt.now, tt.timeout)
			assert.Equal(t, tt.wantRevert, revert)
			if tt.clientActive {
				// A live client resets the idle clock to now.
				assert.Equal(t, tt.now, newIdle)
			} else {
				assert.Equal(t, tt.idleSince, newIdle)
			}
		})
	}
}

func TestNewManagerIdleGuardDefaults(t *testing.T) {
	m := NewManager(ManagerConfig{GamingIdleTimeout: 20 * time.Minute})
	assert.Equal(t, 20*time.Minute, m.gamingIdleTimeout)
	assert.NotNil(t, m.gamingClientActive, "default client probe must be wired")
	assert.NotNil(t, m.nowFn, "clock must be wired")
	assert.Nil(t, m.gamingIdleCancel, "no idle guard until gaming mode")

	// Disabled by default.
	assert.Equal(t, time.Duration(0), NewManager(ManagerConfig{}).gamingIdleTimeout)
}

func TestSetModeGauge(t *testing.T) {
	m := NewManager(ManagerConfig{})
	// Initialized to inference.
	assert.Equal(t, float64(1), testutil.ToFloat64(NodeModeGauge.WithLabelValues(string(ModeInference))))
	assert.Equal(t, float64(0), testutil.ToFloat64(NodeModeGauge.WithLabelValues(string(ModeGaming))))

	m.setModeGauge(ModeGaming)
	assert.Equal(t, float64(1), testutil.ToFloat64(NodeModeGauge.WithLabelValues(string(ModeGaming))))
	assert.Equal(t, float64(0), testutil.ToFloat64(NodeModeGauge.WithLabelValues(string(ModeInference))))

	m.setModeGauge(ModeInference)
	assert.Equal(t, float64(1), testutil.ToFloat64(NodeModeGauge.WithLabelValues(string(ModeInference))))
	assert.Equal(t, float64(0), testutil.ToFloat64(NodeModeGauge.WithLabelValues(string(ModeGaming))))
}

// The idle guard goroutine start/stop plumbing (no timer wait: the loop's first
// tick is >=15s away, so it never fires during the test — we assert lifecycle).
func TestGamingIdleGuardLifecycle(t *testing.T) {
	m := NewManager(ManagerConfig{GamingIdleTimeout: time.Hour})
	m.gamingClientActive = func() bool { return true } // never revert even if it ticked

	m.startGamingIdleGuard()
	m.mu.RLock()
	started := m.gamingIdleCancel != nil
	m.mu.RUnlock()
	assert.True(t, started, "startGamingIdleGuard must set the cancel func")

	m.stopGamingIdleGuard()
	m.mu.RLock()
	stopped := m.gamingIdleCancel == nil
	m.mu.RUnlock()
	assert.True(t, stopped, "stopGamingIdleGuard must clear the cancel func")
}

// Guard against a rename of the port constant the probe watches.
func TestSunshinePortWatched(t *testing.T) {
	assert.Equal(t, int32(47989), backend.PortSunshine)
}
