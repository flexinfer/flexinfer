package agent

import (
	"context"
	"errors"
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

type fakeLanes struct {
	lanes []string
	err   error
	calls int
}

func (f *fakeLanes) ActiveLanes(_ context.Context, _ string) ([]string, error) {
	f.calls++
	return f.lanes, f.err
}

func laneJoules(p *PowerSampler, lane, gpu string) float64 {
	return promtestutil.ToFloat64(metrics.LaneEnergyJoulesTotal.WithLabelValues(lane, p.agent.nodeName, gpu))
}

// startAttributing starts a sampler over one card with the given lanes Active.
func startAttributing(t *testing.T, root string, lanes *fakeLanes) *PowerSampler {
	t.Helper()
	p := newPowerTestSampler(t, root)
	p.SetLaneSource(lanes)
	require.Empty(t, firstReturn(p.start()))
	require.True(t, p.attributing)
	p.refreshLanes(context.Background())
	return p
}

func TestLaneEnergy_TrapezoidSplitEqually(t *testing.T) {
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	power := filepath.Join(dev, "hwmon", "hwmon3", "power1_average")
	p := startAttributing(t, root, &fakeLanes{lanes: []string{"bge-large-radeonvii", "gte-qwen2-1p5b-radeonvii"}})

	t0 := time.Unix(1_800_000_000, 0)
	writeFile(t, power, "100000000\n")
	p.sample(t0) // first sample: nothing to integrate yet
	assert.Equal(t, 0.0, laneJoules(p, "gte-qwen2-1p5b-radeonvii", "0"))

	writeFile(t, power, "200000000\n")
	p.sample(t0.Add(time.Second)) // (100 + 200) / 2 W × 1 s = 150 J, 75 J each
	assert.InDelta(t, 75.0, laneJoules(p, "bge-large-radeonvii", "0"), 1e-9)
	assert.InDelta(t, 75.0, laneJoules(p, "gte-qwen2-1p5b-radeonvii", "0"), 1e-9)

	p.sample(t0.Add(3 * time.Second)) // 200 W × 2 s = 400 J
	assert.InDelta(t, 275.0, laneJoules(p, "gte-qwen2-1p5b-radeonvii", "0"), 1e-9)
}

func TestLaneEnergy_IdleCardIsAttributedToNobody(t *testing.T) {
	root := t.TempDir()
	writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	lanes := &fakeLanes{}
	p := startAttributing(t, root, lanes)

	t0 := time.Unix(1_800_000_000, 0)
	p.sample(t0)
	p.sample(t0.Add(time.Second))

	lanes.lanes = []string{"orpheus-3b-radeonvii"}
	p.refreshLanes(context.Background())
	p.sample(t0.Add(2 * time.Second)) // 9 W × 1 s, only this step
	assert.InDelta(t, 9.0, laneJoules(p, "orpheus-3b-radeonvii", "0"), 1e-9)
}

func TestLaneEnergy_GapsAreNotIntegrated(t *testing.T) {
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	p := startAttributing(t, root, &fakeLanes{lanes: []string{"lane"}})

	t0 := time.Unix(1_800_000_000, 0)
	p.sample(t0)
	p.sample(t0.Add(powerMaxGapIntervals*time.Second + time.Millisecond)) // stalled loop
	assert.Equal(t, 0.0, laneJoules(p, "lane", "0"))

	// A suspended sample breaks the integral too.
	writeFile(t, filepath.Join(dev, "power", "runtime_status"), "suspended\n")
	p.sample(t0.Add(7 * time.Second))
	writeFile(t, filepath.Join(dev, "power", "runtime_status"), "active\n")
	p.sample(t0.Add(8 * time.Second))
	assert.Equal(t, 0.0, laneJoules(p, "lane", "0"))

	p.sample(t0.Add(9 * time.Second))
	assert.InDelta(t, 9.0, laneJoules(p, "lane", "0"), 1e-9)
}

func TestLaneEnergy_OffWithMoreThanOneCard(t *testing.T) {
	root := t.TempDir()
	writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	writeSysfsCard(t, root, 1, testDiscreteVRAMBytes)
	p := newPowerTestSampler(t, root)
	p.SetLaneSource(&fakeLanes{lanes: []string{"lane"}})
	require.Empty(t, firstReturn(p.start()))
	require.Len(t, p.sensors, 2)
	assert.False(t, p.attributing, "the agent cannot tell which card a lane holds")
}

func TestLaneEnergy_ListFailuresClearTheLaneSet(t *testing.T) {
	root := t.TempDir()
	writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	lanes := &fakeLanes{lanes: []string{"lane"}}
	p := startAttributing(t, root, lanes)

	lanes.err = errors.New("apiserver unavailable")
	for i := 1; i < laneListMaxFailures; i++ {
		p.refreshLanes(context.Background())
		assert.Equal(t, []string{"lane"}, p.active, "a failed listing keeps the last set (%d)", i)
	}
	p.refreshLanes(context.Background())
	assert.Empty(t, p.active)

	lanes.err = nil
	p.refreshLanes(context.Background())
	assert.Equal(t, []string{"lane"}, p.active)
}
