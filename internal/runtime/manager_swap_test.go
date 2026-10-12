package runtime

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/backend"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// histogramCount returns the sample count of one labelled histogram series.
func histogramCount(t *testing.T, h *prometheus.HistogramVec, labels ...string) uint64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, h.WithLabelValues(labels...).(prometheus.Histogram).Write(&m))
	return m.GetHistogram().GetSampleCount()
}

// fakeGPU replays a fixed sequence of readings (repeating the last one) and
// counts how often it was queried.
type fakeGPU struct {
	mu       sync.Mutex
	readings []GPUInfo
	calls    int
}

func (f *fakeGPU) query() GPUInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.calls
	f.calls++
	if i >= len(f.readings) {
		i = len(f.readings) - 1
	}
	return f.readings[i]
}

func (f *fakeGPU) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func used(totalMB, usedMB int64) GPUInfo {
	return GPUInfo{VRAMTotalMB: totalMB, VRAMUsedMB: usedMB, VRAMFreeMB: totalMB - usedMB}
}

func TestVRAMTelemetryPlausible(t *testing.T) {
	tests := []struct {
		name       string
		info       GPUInfo
		readyEstMB int64
		wantOK     bool
		wantReason string
	}{
		{"absent", GPUInfo{}, 0, false, "absent"},
		{"used above total", used(16368, 20000), 0, false, "out_of_range"},
		{"negative used", used(16368, -1), 0, false, "out_of_range"},
		{"no estimates to compare", used(16368, 194), 0, true, ""},
		// The live gfx906 case: ~6.5 GB of models believed resident, 194 MB seen.
		{"far below ready estimates", used(16368, 194), 6500, false, "below_loaded_estimate"},
		{"exactly half is plausible", used(16368, 3250), 6500, true, ""},
		{"consistent", used(16368, 7000), 6500, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := vramTelemetryPlausible(tt.info, tt.readyEstMB)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}

func TestCanAdmitTreatsImplausibleReadingAsUnknown(t *testing.T) {
	m := NewManager(ManagerConfig{MultiModel: true, GPUVendor: backend.GPUVendorAMD, GPUArch: "gfx906"})
	m.models["humanizer"] = &LoadedModel{Name: "humanizer", Backend: "llamacpp", State: ModelStateReady, vramEstimateMB: 5000}
	m.models["reranker"] = &LoadedModel{Name: "reranker", Backend: "llamacpp", State: ModelStateReady, vramEstimateMB: 1500}
	// A Loading model has not necessarily allocated yet and must not count.
	m.models["warming"] = &LoadedModel{Name: "warming", Backend: "llamacpp", State: ModelStateLoading, vramEstimateMB: 9000}

	// 194 MB used while 6.5 GB of Ready models are believed resident: the
	// reading would admit almost anything, so it must be treated as unknown.
	// The estimate is chosen so a trusted reading would reject it.
	m.gpuQuery = func() GPUInfo { return used(16368, 194) }
	before := testutil.ToFloat64(VRAMTelemetryUnknownTotal.WithLabelValues("admission", "below_loaded_estimate"))
	ok, reason := m.canAdmit(logr.Discard(), 16000)
	assert.True(t, ok, "implausible telemetry must fail open")
	assert.Contains(t, reason, "below_loaded_estimate")
	assert.Equal(t, before+1, testutil.ToFloat64(VRAMTelemetryUnknownTotal.WithLabelValues("admission", "below_loaded_estimate")))

	// A plausible reading is enforced: 7 GB used of 16 GB leaves no room for 9 GB.
	m.gpuQuery = func() GPUInfo { return used(16368, 7000) }
	ok, reason = m.canAdmit(logr.Discard(), 9000)
	assert.False(t, ok, "plausible telemetry must be enforced, got reason=%s", reason)
	assert.Contains(t, reason, "insufficient VRAM")

	ok, _ = m.canAdmit(logr.Discard(), 4000)
	assert.True(t, ok)
}

func TestWaitVRAMDrain(t *testing.T) {
	tests := []struct {
		name          string
		timeout       time.Duration
		pre           GPUInfo
		readyEstMB    int64
		unloadedEstMB int64
		readings      []GPUInfo
		wantOutcome   string // "" = drain skipped, no samples taken
		wantCalls     int
	}{
		{
			name: "drained once half the estimate is released", timeout: time.Second,
			pre: used(24560, 18000), readyEstMB: 16000, unloadedEstMB: 16000,
			readings:    []GPUInfo{used(24560, 17000), used(24560, 12000), used(24560, 9500)},
			wantOutcome: "drained", wantCalls: 3,
		},
		{
			name: "plateau ends the wait when the estimate is unknown", timeout: time.Second,
			pre:         used(24560, 18000),
			readings:    []GPUInfo{used(24560, 2000), used(24560, 2000), used(24560, 2000)},
			wantOutcome: "plateau", wantCalls: 3,
		},
		{
			name: "plateau also ends a known-estimate wait that never moves", timeout: time.Second,
			pre: used(16368, 400), unloadedEstMB: 2000,
			readings:    []GPUInfo{used(16368, 400), used(16368, 401), used(16368, 401)},
			wantOutcome: "plateau", wantCalls: 3,
		},
		{
			name: "timeout while memory is still trickling down", timeout: 30 * time.Millisecond,
			pre: used(24560, 18000), unloadedEstMB: 16000,
			readings:    trickle(18000, 500),
			wantOutcome: "timeout",
		},
		{
			name: "telemetry lost mid-drain", timeout: time.Second,
			pre: used(24560, 18000), unloadedEstMB: 16000,
			readings:    []GPUInfo{used(24560, 17000), {}},
			wantOutcome: "telemetry_lost", wantCalls: 2,
		},
		{name: "skipped without telemetry", timeout: time.Second, pre: GPUInfo{}, unloadedEstMB: 4000},
		{name: "skipped on implausible telemetry", timeout: time.Second, pre: used(16368, 194), readyEstMB: 6500, unloadedEstMB: 6500},
		{name: "disabled", timeout: 0, pre: used(24560, 18000), unloadedEstMB: 16000, readings: []GPUInfo{used(24560, 18000)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewManager(ManagerConfig{VRAMDrainTimeout: tt.timeout})
			m.vramDrainPoll = time.Millisecond
			gpu := &fakeGPU{readings: tt.readings}
			m.gpuQuery = gpu.query

			var before uint64
			if tt.wantOutcome != "" {
				before = histogramCount(t, VRAMDrainSeconds, tt.wantOutcome)
			}
			timeoutsBefore := testutil.ToFloat64(VRAMDrainTimeoutsTotal)

			m.waitVRAMDrain(logr.Discard(), tt.pre, tt.readyEstMB, tt.unloadedEstMB)

			if tt.wantOutcome == "" {
				assert.Zero(t, gpu.count(), "a skipped drain must not poll telemetry")
				return
			}
			assert.Equal(t, before+1, histogramCount(t, VRAMDrainSeconds, tt.wantOutcome))
			if tt.wantCalls > 0 {
				assert.Equal(t, tt.wantCalls, gpu.count())
			}
			wantTimeouts := timeoutsBefore
			if tt.wantOutcome == "timeout" {
				wantTimeouts++
			}
			assert.Equal(t, wantTimeouts, testutil.ToFloat64(VRAMDrainTimeoutsTotal))
		})
	}
}

// trickle returns n readings that each drop 1 MB, never reaching a target.
func trickle(fromMB int64, n int) []GPUInfo {
	out := make([]GPUInfo, n)
	for i := range out {
		out[i] = used(24560, fromMB-int64(i+1))
	}
	return out
}

func TestResolveStartupTimeout(t *testing.T) {
	cfg := func(v any) map[string]any { return map[string]any{"startupTimeoutSeconds": v} }
	tests := []struct {
		name          string
		def           time.Duration
		config        map[string]any
		want          time.Duration
		wantRequested time.Duration
		wantFloored   bool
	}{
		{"absent keeps default", 300 * time.Second, nil, 300 * time.Second, 0, false},
		{"controller 60s default floored for vllm", 300 * time.Second, cfg(60.0), 300 * time.Second, 60 * time.Second, true},
		{"larger payload value stays", 300 * time.Second, cfg(600.0), 600 * time.Second, 600 * time.Second, false},
		{"equal value is not floored", 60 * time.Second, cfg(60.0), 60 * time.Second, 60 * time.Second, false},
		{"duration string floored", 900 * time.Second, cfg("2m"), 900 * time.Second, 2 * time.Minute, true},
		{"duration string raises", 60 * time.Second, cfg("5m"), 5 * time.Minute, 5 * time.Minute, false},
		{"int seconds", 60 * time.Second, cfg(120), 120 * time.Second, 120 * time.Second, false},
		{"invalid string ignored", 300 * time.Second, cfg("soon"), 300 * time.Second, 0, false},
		{"zero ignored", 300 * time.Second, cfg(0.0), 300 * time.Second, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, requested, floored := resolveStartupTimeout(tt.def, tt.config)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantRequested, requested)
			assert.Equal(t, tt.wantFloored, floored)
		})
	}
}

// The floor uses each backend's own StartupTimeout constant: a controller
// payload carrying the 60 s coldStartTimeout default must not shorten them.
func TestStartupTimeoutFloorUsesBackendDefaults(t *testing.T) {
	want := map[string]time.Duration{
		backend.NameVLLM:      300 * time.Second,
		backend.NameDiffusers: 900 * time.Second,
		backend.NameSNAC:      300 * time.Second,
		backend.NameLlamaCpp:  60 * time.Second,
		backend.NameOllama:    60 * time.Second,
		backend.NameMLCLLM:    90 * time.Second,
	}
	for name, wantTimeout := range want {
		t.Run(name, func(t *testing.T) {
			b, ok := backend.Get(name)
			require.True(t, ok)
			require.Equal(t, wantTimeout, b.StartupTimeout(), "backend default changed; update this table")
			got, _, _ := resolveStartupTimeout(b.StartupTimeout(), map[string]any{"startupTimeoutSeconds": 60.0})
			assert.Equal(t, wantTimeout, got)
		})
	}
}

func TestLoadedModelsGaugeTracksBackends(t *testing.T) {
	m := NewManager(ManagerConfig{MultiModel: true})
	m.mu.Lock()
	m.models["a"] = &LoadedModel{Name: "a", Backend: "gauge-test-llamacpp"}
	m.models["b"] = &LoadedModel{Name: "b", Backend: "gauge-test-llamacpp"}
	m.models["c"] = &LoadedModel{Name: "c", Backend: "gauge-test-snac"}
	m.refreshLoadedModelsLocked()
	m.mu.Unlock()
	assert.Equal(t, 2.0, testutil.ToFloat64(LoadedModels.WithLabelValues("gauge-test-llamacpp")))
	assert.Equal(t, 1.0, testutil.ToFloat64(LoadedModels.WithLabelValues("gauge-test-snac")))

	m.mu.Lock()
	delete(m.models, "c")
	delete(m.models, "a")
	m.refreshLoadedModelsLocked()
	m.mu.Unlock()
	assert.Equal(t, 1.0, testutil.ToFloat64(LoadedModels.WithLabelValues("gauge-test-llamacpp")))
	assert.Equal(t, 0.0, testutil.ToFloat64(LoadedModels.WithLabelValues("gauge-test-snac")), "emptied backend must read 0, not keep its last value")
}

func TestSwapDurationObservedOncePerLoad(t *testing.T) {
	readyBefore := histogramCount(t, ModelSwapDurationSeconds, "swap-once", "llamacpp", "ready")
	failedBefore := histogramCount(t, ModelSwapDurationSeconds, "swap-once", "llamacpp", "failed")
	lm := &LoadedModel{Name: "swap-once", Backend: "llamacpp", requestedAt: time.Now().Add(-2 * time.Second)}
	lm.observeSwapLocked("ready")
	lm.observeSwapLocked("failed") // e.g. a later crash after Ready
	assert.Equal(t, readyBefore+1, histogramCount(t, ModelSwapDurationSeconds, "swap-once", "llamacpp", "ready"))
	assert.Equal(t, failedBefore, histogramCount(t, ModelSwapDurationSeconds, "swap-once", "llamacpp", "failed"))

	// Entries built without a request time (tests, legacy paths) never observe.
	bareBefore := histogramCount(t, ModelSwapDurationSeconds, "swap-bare", "llamacpp", "ready")
	bare := &LoadedModel{Name: "swap-bare", Backend: "llamacpp"}
	bare.observeSwapLocked("ready")
	assert.Equal(t, bareBefore, histogramCount(t, ModelSwapDurationSeconds, "swap-bare", "llamacpp", "ready"))
}

// writeSysfsDevice fakes an amdgpu render node's sysfs device directory.
func writeSysfsDevice(t *testing.T, sysRoot, render string, totalB, usedB int64, tempMilli int64) {
	t.Helper()
	dir := filepath.Join(sysRoot, "class", "drm", render, "device")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	if totalB > 0 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "mem_info_vram_total"), []byte(itoa(totalB)+"\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "mem_info_vram_used"), []byte(itoa(usedB)+"\n"), 0o644))
	}
	if tempMilli > 0 {
		hw := filepath.Join(dir, "hwmon", "hwmon3")
		require.NoError(t, os.MkdirAll(hw, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(hw, "temp1_input"), []byte(itoa(tempMilli)+"\n"), 0o644))
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func TestReadSysfsVRAM(t *testing.T) {
	const mib = 1024 * 1024
	// 7900xtx-like host: the dGPU on renderD128 and a 512 MB iGPU on renderD129,
	// plus an NVIDIA render node without amdgpu counters.
	sysRoot := t.TempDir()
	writeSysfsDevice(t, sysRoot, "renderD128", 24560*mib, 17557*mib, 41000)
	writeSysfsDevice(t, sysRoot, "renderD129", 512*mib, 16*mib, 0)
	writeSysfsDevice(t, sysRoot, "renderD130", 0, 0, 0)

	devRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(devRoot, "dri"), 0o755))
	for _, n := range []string{"card0", "renderD128", "renderD129", "renderD130"} {
		require.NoError(t, os.WriteFile(filepath.Join(devRoot, "dri", n), nil, 0o644))
	}

	env := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}

	info, ok := readSysfsVRAM(sysRoot, devRoot, env(nil))
	require.True(t, ok)
	assert.Equal(t, GPUInfo{VRAMTotalMB: 24560, VRAMUsedMB: 17557, VRAMFreeMB: 7003, Temperature: 41}, info)

	info, ok = readSysfsVRAM(sysRoot, devRoot, env(map[string]string{"HIP_VISIBLE_DEVICES": "1"}))
	require.True(t, ok)
	assert.Equal(t, int64(512), info.VRAMTotalMB, "HIP index 1 is the second amdgpu device")

	// ROCR filters first, HIP indexes into the filtered list.
	info, ok = readSysfsVRAM(sysRoot, devRoot, env(map[string]string{"ROCR_VISIBLE_DEVICES": "1,0", "HIP_VISIBLE_DEVICES": "1"}))
	require.True(t, ok)
	assert.Equal(t, int64(24560), info.VRAMTotalMB)

	// UUID-style lists cannot be mapped and are ignored.
	info, ok = readSysfsVRAM(sysRoot, devRoot, env(map[string]string{"ROCR_VISIBLE_DEVICES": "GPU-abc"}))
	require.True(t, ok)
	assert.Equal(t, int64(24560), info.VRAMTotalMB)

	_, ok = readSysfsVRAM(sysRoot, devRoot, env(map[string]string{"ROCR_VISIBLE_DEVICES": "3"}))
	assert.False(t, ok, "an index past the visible devices must fall back to rocm-smi")

	// Inside a pod only the granted render node is present under /dev/dri, so
	// device 0 is that node even though sysfs shows every card on the host.
	podDev := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(podDev, "dri"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(podDev, "dri", "renderD129"), nil, 0o644))
	info, ok = readSysfsVRAM(sysRoot, podDev, env(nil))
	require.True(t, ok)
	assert.Equal(t, int64(512), info.VRAMTotalMB)

	// Without /dev/dri, every sysfs render node is a candidate.
	info, ok = readSysfsVRAM(sysRoot, filepath.Join(t.TempDir(), "missing"), env(nil))
	require.True(t, ok)
	assert.Equal(t, int64(24560), info.VRAMTotalMB)

	_, ok = readSysfsVRAM(t.TempDir(), t.TempDir(), env(nil))
	assert.False(t, ok, "no amdgpu counters means fall back to rocm-smi")
}
