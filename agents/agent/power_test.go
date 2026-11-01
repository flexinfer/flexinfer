package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-logr/logr"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/flexinfer/flexinfer/pkg/metrics"
)

// newPowerTestSampler builds a sampler over a fake sysfs tree on a node name
// unique to the test, so gauge assertions do not see other tests' series.
func newPowerTestSampler(t *testing.T, root string) *PowerSampler {
	t.Helper()
	a := newHealthTestAgent(t, root)
	a.nodeName = "power-" + strings.ReplaceAll(t.Name(), "/", "-")
	return a.NewPowerSampler(time.Second, logr.Discard())
}

func powerGauge(t *testing.T, p *PowerSampler, gpu string) float64 {
	t.Helper()
	return promtestutil.ToFloat64(metrics.GPUPowerWatts.WithLabelValues(gpu, p.agent.nodeName))
}

func powerSeries(p *PowerSampler) int {
	n := 0
	for _, s := range p.sensors {
		if metrics.GPUPowerWatts.DeleteLabelValues(itoa(s.index), p.agent.nodeName) {
			n++
		}
	}
	return n
}

func samplerActive(p *PowerSampler) float64 {
	return promtestutil.ToFloat64(metrics.GPUPowerSamplerActive.WithLabelValues(p.agent.nodeName))
}

func TestPowerSamplingEnabled(t *testing.T) {
	assert.True(t, PowerSamplingEnabled("cblevins-5930k", "cblevins-5930k,cblevins-radeonvii"))
	assert.True(t, PowerSamplingEnabled("cblevins-radeonvii", " cblevins-5930k , cblevins-radeonvii "))
	assert.False(t, PowerSamplingEnabled("cblevins-7900xtx", "cblevins-5930k,cblevins-radeonvii"))
	assert.False(t, PowerSamplingEnabled("cblevins-7900xtx", ""), "empty list samples nowhere")
	assert.False(t, PowerSamplingEnabled("cblevins-7900xtx", "*"), "no wildcard")
	assert.False(t, PowerSamplingEnabled("", ","))
}

func TestNewPowerSampler_IntervalBounds(t *testing.T) {
	a := newHealthTestAgent(t, t.TempDir())
	assert.Equal(t, DefaultPowerInterval, a.NewPowerSampler(0, logr.Discard()).Interval())
	assert.Equal(t, minPowerInterval, a.NewPowerSampler(10*time.Millisecond, logr.Discard()).Interval())
	assert.Equal(t, 2*time.Second, a.NewPowerSampler(2*time.Second, logr.Discard()).Interval())
}

func TestPowerSampler_ReadsPowerAverage(t *testing.T) {
	root := t.TempDir()
	writeSysfsCard(t, root, 1, testDiscreteVRAMBytes) // power1_average = 9 W
	p := newPowerTestSampler(t, root)

	reason, _ := p.start()
	require.Empty(t, reason)
	require.Len(t, p.sensors, 1)
	assert.Equal(t, 1, p.sensors[0].index, "gpu label is the DRM card index, like flexinfer_gpu_healthy")
	assert.Equal(t, 1.0, samplerActive(p))

	p.sample(time.Now())
	assert.InDelta(t, 9.0, powerGauge(t, p, "1"), 1e-9)
}

func TestPowerSampler_FallsBackToPowerInput(t *testing.T) {
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	hwmon := filepath.Join(dev, "hwmon", "hwmon3")
	require.NoError(t, os.Remove(filepath.Join(hwmon, "power1_average")))
	writeFile(t, filepath.Join(hwmon, "power1_input"), "294500000\n")
	p := newPowerTestSampler(t, root)

	require.Empty(t, firstReturn(p.start()))
	assert.Equal(t, "power1_input", filepath.Base(p.sensors[0].path))
	p.sample(time.Now())
	assert.InDelta(t, 294.5, powerGauge(t, p, "0"), 1e-9)
}

func TestPowerSampler_SkipsIGPUAndCardsWithoutSensor(t *testing.T) {
	root := t.TempDir()
	writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	writeSysfsCard(t, root, 1, testIGPUVRAMBytes) // Raphael iGPU: filtered as non-discrete
	p := newPowerTestSampler(t, root)

	require.Empty(t, firstReturn(p.start()))
	require.Len(t, p.sensors, 1)
	assert.Equal(t, 0, p.sensors[0].index)
}

func TestPowerSampler_NoSensorStops(t *testing.T) {
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	require.NoError(t, os.Remove(filepath.Join(dev, "hwmon", "hwmon3", "power1_average")))
	p := newPowerTestSampler(t, root)

	p.Run(context.Background()) // returns at once
	assert.True(t, p.Stopped())
	assert.Equal(t, 0.0, samplerActive(p))
}

func TestPowerSampler_RequiresSentinel(t *testing.T) {
	root := t.TempDir()
	writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	p := newPowerTestSampler(t, root)
	p.agent.healthSentinel = false

	reason, _ := p.start()
	assert.Equal(t, PowerStopNoSentinel, reason)
}

func TestPowerSampler_StopsWhenSentinelDegraded(t *testing.T) {
	root := t.TempDir()
	writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	p := newPowerTestSampler(t, root)
	require.Empty(t, firstReturn(p.start()))
	p.sample(time.Now())
	require.InDelta(t, 9.0, powerGauge(t, p, "0"), 1e-9)

	p.agent.health.mu.Lock()
	p.agent.health.degraded = true
	p.agent.health.reason = "card0: pcie-link-down (current_link_speed=Unknown)"
	p.agent.health.mu.Unlock()

	p.sample(time.Now())
	assert.True(t, p.Stopped())
	assert.Equal(t, 0.0, samplerActive(p))
	assert.Equal(t, 0, powerSeries(p), "a stopped sampler leaves no frozen power reading behind")

	// Latched: a recovered verdict does not re-arm the sampler.
	p.agent.health.mu.Lock()
	p.agent.health.degraded = false
	p.agent.health.mu.Unlock()
	p.sample(time.Now())
	assert.True(t, p.Stopped())
	assert.Equal(t, 0, powerSeries(p))
}

func TestPowerSampler_StopsOnHardSysfsVerdictMidCycle(t *testing.T) {
	root := t.TempDir()
	writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	p := newPowerTestSampler(t, root)
	require.Empty(t, firstReturn(p.start()))

	// ProbeAndLabel sets skipTools before evaluateGPUHealth flips degraded.
	p.agent.health.mu.Lock()
	p.agent.health.skipTools = true
	p.agent.health.mu.Unlock()

	p.sample(time.Now())
	assert.True(t, p.Stopped())
}

func TestPowerSampler_StopsOnDeviceIOError(t *testing.T) {
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	p := newPowerTestSampler(t, root)
	require.Empty(t, firstReturn(p.start()))

	power := filepath.Join(dev, "hwmon", "hwmon3", "power1_average")
	orig := readSysfsFile
	t.Cleanup(func() { readSysfsFile = orig })
	readSysfsFile = func(name string) ([]byte, error) {
		if name == power {
			return nil, &os.PathError{Op: "read", Path: name, Err: syscall.EIO}
		}
		return orig(name)
	}

	p.sample(time.Now())
	assert.True(t, p.Stopped(), "a wedged SMU stops the sampler on the first failed read")
	assert.Equal(t, 0.0, samplerActive(p))
}

func TestPowerSampler_StopsAfterRepeatedReadErrors(t *testing.T) {
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	p := newPowerTestSampler(t, root)
	require.Empty(t, firstReturn(p.start()))

	power := filepath.Join(dev, "hwmon", "hwmon3", "power1_average")
	writeFile(t, power, "garbage\n")
	for i := 1; i < powerMaxConsecutiveErrors; i++ {
		p.sample(time.Now())
		require.False(t, p.Stopped(), "read %d of %d", i, powerMaxConsecutiveErrors)
	}

	// A good read resets the streak.
	writeFile(t, power, "15000000\n")
	p.sample(time.Now())
	assert.InDelta(t, 15.0, powerGauge(t, p, "0"), 1e-9)

	require.NoError(t, os.Remove(power))
	for i := 0; i < powerMaxConsecutiveErrors; i++ {
		p.sample(time.Now())
	}
	assert.True(t, p.Stopped())
	assert.Equal(t, 0.0, samplerActive(p))
}

func TestPowerSampler_RejectsImplausibleReading(t *testing.T) {
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	p := newPowerTestSampler(t, root)
	require.Empty(t, firstReturn(p.start()))
	p.sample(time.Now())

	writeFile(t, filepath.Join(dev, "hwmon", "hwmon3", "power1_average"), "4294967295000000\n")
	p.sample(time.Now())
	assert.InDelta(t, 9.0, powerGauge(t, p, "0"), 1e-9, "an implausible reading does not overwrite the last good one")
	assert.Equal(t, 1, p.sensors[0].errors)
}

func TestPowerSampler_SkipsSuspendedCard(t *testing.T) {
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	p := newPowerTestSampler(t, root)
	require.Empty(t, firstReturn(p.start()))

	writeFile(t, filepath.Join(dev, "power", "runtime_status"), "suspended\n")
	power := filepath.Join(dev, "hwmon", "hwmon3", "power1_average")
	orig := readSysfsFile
	t.Cleanup(func() { readSysfsFile = orig })
	readSysfsFile = func(name string) ([]byte, error) {
		if name == power {
			t.Fatalf("read %s on a runtime-suspended card", name)
		}
		return orig(name)
	}
	for i := 0; i < powerMaxConsecutiveErrors+1; i++ {
		p.sample(time.Now())
	}
	assert.False(t, p.Stopped())
}

func TestPowerSampler_RunStopsWithContext(t *testing.T) {
	root := t.TempDir()
	writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	p := newPowerTestSampler(t, root)
	p.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	require.Eventually(t, func() bool { return powerGauge(t, p, "0") == 9.0 }, time.Second, 5*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
	assert.False(t, p.Stopped())
}

func firstReturn(reason, _ string) string { return reason }
