package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

func TestGFXTargetVersion(t *testing.T) {
	cases := map[string]int{
		"gfx906":             90006,
		"gfx906:sramecc+":    90006,
		"gfx90a":             90010,
		"gfx942":             90402,
		"gfx1100":            110000,
		"gfx1151":            110501,
		" GFX906 ":           90006,
		"gfx1030":            100300,
		"gfx90c:xnack-":      90012,
		"sm_52":              -1,
		"":                   -1,
		"gfx9":               -1,
		"gfxzz6":             -1,
		"gfx1100-something!": -1,
	}
	for arch, want := range cases {
		got, ok := gfxTargetVersion(arch)
		if want < 0 {
			assert.False(t, ok, arch)
			continue
		}
		assert.True(t, ok, arch)
		assert.Equal(t, want, got, arch)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// fakeKFD lays out the KFD sysfs tree read on cblevins-radeonvii on
// 2026-10-30: a CPU node, the Radeon VII (gpu_id 11065), plus a second GPU of
// another architecture, four llama-servers with 3 compute queues each and
// pyannote with 2 compute + 2 SDMA queues.
func fakeKFD(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "topology/nodes/0/gpu_id"), "0\n")
	writeFile(t, filepath.Join(root, "topology/nodes/0/properties"), "cpu_cores_count 12\ngfx_target_version 0\n")
	writeFile(t, filepath.Join(root, "topology/nodes/1/gpu_id"), "11065\n")
	writeFile(t, filepath.Join(root, "topology/nodes/1/properties"), "simd_count 240\ngfx_target_version 90006\nnum_cp_queues 24\n")
	writeFile(t, filepath.Join(root, "topology/nodes/2/gpu_id"), "4242\n")
	writeFile(t, filepath.Join(root, "topology/nodes/2/properties"), "gfx_target_version 110000\n")
	queue := func(pid, id, qType, gpuID string) {
		writeFile(t, filepath.Join(root, "proc", pid, "queues", id, "type"), qType+"\n")
		writeFile(t, filepath.Join(root, "proc", pid, "queues", id, "gpuid"), gpuID+"\n")
	}
	for _, pid := range []string{"1936810", "2099892", "2475805", "710904"} {
		for _, id := range []string{"0", "1", "2"} {
			queue(pid, id, "0", "11065")
		}
	}
	queue("34829", "0", "0", "11065")
	queue("34829", "1", "0", "11065")
	queue("34829", "2", "1", "11065") // SDMA: not a compute queue
	queue("34829", "3", "1", "11065")
	queue("99", "0", "0", "4242") // another GPU
	// A process that exited between the directory listing and the read.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "proc", "123"), 0o755))
	return root
}

func TestObserveKFDComputeQueues(t *testing.T) {
	root := fakeKFD(t)
	got, ok := observeKFDComputeQueues(root, "gfx906")
	require.True(t, ok)
	assert.Equal(t, int32(14), got, "4 llama-servers x 3 + pyannote 2; SDMA and other GPUs excluded")

	got, ok = observeKFDComputeQueues(root, "gfx1100")
	require.True(t, ok)
	assert.Equal(t, int32(1), got)

	_, ok = observeKFDComputeQueues(root, "gfx942")
	assert.False(t, ok, "no GPU node of that architecture")
	_, ok = observeKFDComputeQueues(filepath.Join(root, "missing"), "gfx906")
	assert.False(t, ok)
	_, ok = observeKFDComputeQueues(root, "sm_52")
	assert.False(t, ok)
}

func TestHWQueueEstimateFor(t *testing.T) {
	capped := []corev1.EnvVar{{Name: backend.EnvGPUMaxHWQueues, Value: "1"}}
	assert.Equal(t, int32(3), hwQueueEstimateFor(backend.NameLlamaCpp, LoadRequest{Env: capped}))
	assert.Equal(t, int32(6), hwQueueEstimateFor(backend.NameVLLM, LoadRequest{Env: capped}))
	assert.Equal(t, int32(5), hwQueueEstimateFor(backend.NameLlamaCpp, LoadRequest{Env: capped, Config: map[string]any{pkgrt.HWQueueEstimateConfigKey: float64(5)}}))
	assert.Equal(t, int32(0), hwQueueEstimateFor(backend.NameLlamaCpp, LoadRequest{Config: map[string]any{pkgrt.HWQueueEstimateConfigKey: float64(0)}}))
	assert.Equal(t, int32(3), hwQueueEstimateFor(backend.NameLlamaCpp, LoadRequest{Env: capped, Config: map[string]any{pkgrt.HWQueueEstimateConfigKey: float64(-1)}}),
		"a negative estimate is ignored")

	// The runtime's own environment applies unless the request overrides it.
	t.Setenv(backend.EnvGPUMaxHWQueues, "2")
	assert.Equal(t, int32(4), hwQueueEstimateFor(backend.NameLlamaCpp, LoadRequest{}))
	assert.Equal(t, int32(3), hwQueueEstimateFor(backend.NameLlamaCpp, LoadRequest{Env: capped}))
}

func TestAdmitHWQueues(t *testing.T) {
	newManager := func(observed int32, observedOK bool) *Manager {
		m := NewManager(ManagerConfig{GPUVendor: backend.GPUVendorAMD, GPUArch: "gfx906", MultiModel: true})
		m.hwQueueQuery = func() (int32, bool) { return observed, observedOK }
		for _, name := range []string{"bge-large", "bge-reranker", "gte-qwen2", "orpheus"} {
			m.models[name] = &LoadedModel{Name: name, Backend: backend.NameLlamaCpp, State: ModelStateReady, hwQueues: 3}
		}
		return m
	}
	budget := &pkgrt.HWQueueBudget{Max: 24, Reserved: 2}

	// The live plane: 12 estimated + 2 reserved = 14 = KFD. Three more
	// llama.cpp lanes fit (23), a fourth does not (26).
	m := newManager(14, true)
	ok, reason := m.admitHWQueues(logr.Discard(), "qwen3-embedding", 3, budget)
	assert.True(t, ok, reason)
	m.models["a"] = &LoadedModel{Name: "a", hwQueues: 3}
	m.models["b"] = &LoadedModel{Name: "b", hwQueues: 3}
	ok, reason = m.admitHWQueues(logr.Discard(), "c", 3, nil)
	assert.True(t, ok, "18 + 2 reserved + 3 = 23 fits; the remembered budget applies: %s", reason)
	m.models["c"] = &LoadedModel{Name: "c", hwQueues: 3}
	ok, reason = m.admitHWQueues(logr.Discard(), "d", 3, nil)
	assert.False(t, ok)
	assert.Contains(t, reason, "insufficient hardware compute queues")
	assert.Contains(t, reason, "max=24")

	// A reload of a tracked model does not count its own old instance.
	ok, _ = m.admitHWQueues(logr.Discard(), "c", 3, nil)
	assert.True(t, ok)

	// KFD sees a tenant the runtime does not (a dedicated pod): 14 estimated
	// but 22 observed leaves room for nothing beyond 2 queues.
	m = newManager(22, true)
	ok, reason = m.admitHWQueues(logr.Discard(), "x", 3, budget)
	assert.False(t, ok)
	assert.Contains(t, reason, "(kfd;")
	ok, _ = m.admitHWQueues(logr.Discard(), "x", 2, budget)
	assert.True(t, ok)

	// Without KFD telemetry the estimate alone decides.
	m = newManager(0, false)
	ok, reason = m.admitHWQueues(logr.Discard(), "x", 10, budget)
	assert.True(t, ok, reason)
	assert.Contains(t, reason, "KFD observed=unavailable")
	ok, _ = m.admitHWQueues(logr.Discard(), "x", 11, budget)
	assert.False(t, ok)

	// No budget (gfx1100, or before the controller sends one): always admit.
	m = newManager(40, true)
	ok, _ = m.admitHWQueues(logr.Discard(), "x", 30, nil)
	assert.True(t, ok)
	ok, _ = m.admitHWQueues(logr.Discard(), "x", 30, &pkgrt.HWQueueBudget{})
	assert.True(t, ok)
}

func TestHWQueueGauges(t *testing.T) {
	m := NewManager(ManagerConfig{GPUVendor: backend.GPUVendorAMD, GPUArch: "gfx906-gauge-test", MultiModel: true})
	m.hwQueueQuery = func() (int32, bool) { return 14, true }
	m.models["a"] = &LoadedModel{Name: "a", Backend: backend.NameLlamaCpp, hwQueues: 3}
	m.models["b"] = &LoadedModel{Name: "b", Backend: backend.NameLlamaCpp, hwQueues: 3}
	ok, _ := m.admitHWQueues(logr.Discard(), "c", 3, &pkgrt.HWQueueBudget{Max: 24, Reserved: 2})
	require.True(t, ok)
	gauge := func(name, label string) float64 {
		if name == "budget" {
			return testutil.ToFloat64(HWQueueBudgetRT.WithLabelValues("amd", "gfx906-gauge-test", label))
		}
		return testutil.ToFloat64(HWQueuesRT.WithLabelValues("amd", "gfx906-gauge-test", label))
	}
	assert.Equal(t, 6.0, gauge("queues", "estimated"))
	assert.Equal(t, 14.0, gauge("queues", "observed"))
	assert.Equal(t, 24.0, gauge("budget", "max"))
	assert.Equal(t, 2.0, gauge("budget", "reserved"))

	m.mu.Lock()
	delete(m.models, "b")
	m.refreshLoadedModelsLocked()
	m.mu.Unlock()
	assert.Equal(t, 3.0, gauge("queues", "estimated"))
}

func TestLoadRefusesOverQueueBudgetWith503(t *testing.T) {
	m := NewManager(ManagerConfig{GPUVendor: backend.GPUVendorAMD, GPUArch: "gfx906", MultiModel: true, RequestDrain: true})
	m.hwQueueQuery = func() (int32, bool) { return 22, true }
	m.gpuQuery = func() GPUInfo { return GPUInfo{VRAMTotalMB: 16384, VRAMUsedMB: 2000, VRAMFreeMB: 14384} }
	payload := LoadRequest{
		Backend:  backend.NameLlamaCpp,
		Model:    "test/queues",
		Env:      []corev1.EnvVar{{Name: backend.EnvGPUMaxHWQueues, Value: "1"}},
		HWQueues: &pkgrt.HWQueueBudget{Max: 24, Reserved: 2},
	}
	err := m.Load(context.Background(), "waiting", payload)
	require.ErrorIs(t, err, ErrInsufficientHWQueues)
	assert.NotErrorIs(t, err, ErrInsufficientVRAM)
	assert.Empty(t, m.Status().ActiveModels)

	body, err := json.Marshal(payload)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	NewServer(m).Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/models/waiting/load", bytes.NewReader(body)))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), ErrInsufficientHWQueues.Error())
}
