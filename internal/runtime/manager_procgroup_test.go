//go:build unix

package runtime

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/backend"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

const (
	procGroupHelperBackend = "procgroup-helper"
	procGroupHelperModeEnv = "FLEXINFER_RUNTIME_PROCGROUP_HELPER"
	procGroupHelperPIDEnv  = "FLEXINFER_RUNTIME_PROCGROUP_PIDFILE"
)

// procGroupHelper is a test backend whose subprocess is this test binary
// re-executed into TestRuntimeProcGroupHelperProcess. It embeds llamacpp for
// the rest of the Backend interface.
type procGroupHelper struct {
	backend.Backend
}

func (procGroupHelper) Name() string                  { return procGroupHelperBackend }
func (procGroupHelper) Aliases() []string             { return nil }
func (procGroupHelper) NeedsVolume() bool             { return false }
func (procGroupHelper) ReadinessProbe() *corev1.Probe { return nil }
func (procGroupHelper) Command() []string {
	return []string{os.Args[0], "-test.run=^TestRuntimeProcGroupHelperProcess$"}
}
func (procGroupHelper) Args(*backend.ModelSpec) []string       { return nil }
func (procGroupHelper) Env(*backend.ModelSpec) []corev1.EnvVar { return nil }

var registerProcGroupHelper sync.Once

func procGroupTestManager(t *testing.T, cfg ManagerConfig) *Manager {
	t.Helper()
	registerProcGroupHelper.Do(func() {
		llama, ok := backend.Get(backend.NameLlamaCpp)
		require.True(t, ok)
		require.NoError(t, backend.Register(procGroupHelper{Backend: llama}))
	})
	cfg.GPUVendor = backend.GPUVendorCPU
	return NewManager(cfg)
}

// TestRuntimeProcGroupHelperProcess is the backend subprocess. Modes:
//   - sleep: run until signalled (default SIGTERM disposition: exit).
//   - parent: like `ollama serve` — spawn a "stubborn" child, then sleep.
//   - stubborn: like a wedged engine worker — ignore SIGTERM, write own PID.
func TestRuntimeProcGroupHelperProcess(t *testing.T) {
	switch os.Getenv(procGroupHelperModeEnv) {
	case "sleep":
		time.Sleep(time.Hour)
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestRuntimeProcGroupHelperProcess$")
		child.Env = append(os.Environ(), procGroupHelperModeEnv+"=stubborn")
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		time.Sleep(time.Hour)
	case "stubborn":
		signal.Ignore(syscall.SIGTERM)
		path := os.Getenv(procGroupHelperPIDEnv)
		_ = os.WriteFile(path+".tmp", []byte(strconv.Itoa(os.Getpid())), 0o644)
		_ = os.Rename(path+".tmp", path) // publish only after SIGTERM is ignored
		time.Sleep(time.Hour)
	}
}

func helperEnv(mode, pidFile string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: procGroupHelperModeEnv, Value: mode},
		{Name: procGroupHelperPIDEnv, Value: pidFile},
	}
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	var pid int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil && pid > 0
	}, 20*time.Second, 10*time.Millisecond, "helper grandchild never reported its PID")
	return pid
}

// pidAlive reports whether pid is a live process; on Linux a zombie awaiting
// reaping by PID 1 counts as gone, matching processGroupAlive.
func pidAlive(pid int) bool {
	if data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		state, _, ok := parseProcStatForTest(string(data))
		return ok && state != "Z" && state != "X"
	}
	return syscall.Kill(pid, 0) == nil
}

func parseProcStatForTest(stat string) (string, int, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return "", 0, false
	}
	f := strings.Fields(stat[i+1:])
	if len(f) < 3 {
		return "", 0, false
	}
	pgrp, err := strconv.Atoi(f[2])
	return f[0], pgrp, err == nil
}

func killLeftovers(t *testing.T, pids ...*int) {
	t.Cleanup(func() {
		for _, p := range pids {
			if *p > 1 {
				_ = syscall.Kill(*p, syscall.SIGKILL)
				_ = syscall.Kill(-*p, syscall.SIGKILL)
			}
		}
	})
}

func TestUnloadKillsBackendProcessGroup(t *testing.T) {
	m := procGroupTestManager(t, ManagerConfig{ShutdownTimeout: 300 * time.Millisecond})
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	var leader, grandchild int
	killLeftovers(t, &leader, &grandchild)

	require.NoError(t, m.Load(context.Background(), "pg-model", LoadRequest{
		Backend: procGroupHelperBackend,
		Env:     helperEnv("parent", pidFile),
	}))
	m.mu.RLock()
	loaded := m.models["pg-model"]
	leader, pgid := loaded.PID, loaded.pgid
	m.mu.RUnlock()
	require.Equal(t, leader, pgid, "backend must lead its own process group")
	require.NotEqual(t, syscall.Getpgrp(), pgid, "backend must not share the runtime's process group")

	grandchild = waitForPIDFile(t, pidFile)
	gcGroup, err := syscall.Getpgid(grandchild)
	require.NoError(t, err)
	assert.Equal(t, pgid, gcGroup, "grandchildren (e.g. `ollama runner`) inherit the backend's group")

	before := testutil.ToFloat64(BackendGroupLeftoversTotal.WithLabelValues(procGroupHelperBackend, "killed"))
	start := time.Now()
	require.NoError(t, m.Unload(context.Background(), "pg-model"))

	assert.False(t, pidAlive(leader), "direct child must be gone after Unload")
	assert.False(t, pidAlive(grandchild), "SIGTERM-ignoring grandchild must be gone after Unload")
	alive, _ := processGroupAlive(pgid)
	assert.False(t, alive, "process group must be empty after Unload")
	assert.Equal(t, before+1, testutil.ToFloat64(BackendGroupLeftoversTotal.WithLabelValues(procGroupHelperBackend, "killed")))
	assert.Less(t, time.Since(start), 300*time.Millisecond+defaultGroupKillGrace+time.Second,
		"group termination must stay bounded by shutdownTimeout plus the kill grace")
	_, stillLoaded := m.Model("pg-model")
	assert.False(t, stillLoaded)
}

// A single-slot swap must sample VRAM before the incumbent stops, and only
// drain-poll once the incumbent's whole process group is gone.
func TestLoadDrainsVRAMAfterIncumbentGroupIsGone(t *testing.T) {
	m := procGroupTestManager(t, ManagerConfig{ShutdownTimeout: 2 * time.Second, VRAMDrainTimeout: 2 * time.Second})
	m.vramDrainPoll = 5 * time.Millisecond
	var first, second int
	killLeftovers(t, &first, &second)

	require.NoError(t, m.Load(context.Background(), "incumbent", LoadRequest{
		Backend: procGroupHelperBackend,
		Config:  map[string]any{"vramEstimateMB": float64(4000)},
		Env:     helperEnv("sleep", ""),
	}))
	m.mu.RLock()
	first = m.models["incumbent"].pgid
	m.mu.RUnlock()
	require.Positive(t, first)

	var mu sync.Mutex
	var samples []bool // incumbent group alive at each telemetry sample
	m.gpuQuery = func() GPUInfo {
		alive, _ := processGroupAlive(first)
		mu.Lock()
		defer mu.Unlock()
		samples = append(samples, alive)
		if len(samples) == 1 {
			return used(16368, 5000) // pre-unload: incumbent resident
		}
		return used(16368, 900) // released
	}

	drainedBefore := histogramCount(t, VRAMDrainSeconds, "drained")
	cancelledBefore := histogramCount(t, ModelSwapDurationSeconds, "incumbent", procGroupHelperBackend, "cancelled")
	require.NoError(t, m.Load(context.Background(), "next", LoadRequest{
		Backend: procGroupHelperBackend,
		Env:     helperEnv("sleep", ""),
	}))
	m.mu.RLock()
	second = m.models["next"].pgid
	_, incumbentLoaded := m.models["incumbent"]
	m.mu.RUnlock()

	mu.Lock()
	got := append([]bool(nil), samples...)
	mu.Unlock()
	require.GreaterOrEqual(t, len(got), 2, "expected a pre-unload sample and at least one drain sample")
	assert.True(t, got[0], "pre-unload sample must be taken while the incumbent runs")
	for i, alive := range got[1:] {
		assert.False(t, alive, "drain sample %d taken while the incumbent's group was still alive", i+1)
	}
	assert.False(t, incumbentLoaded, "single-slot load must unload the incumbent")
	assert.Equal(t, drainedBefore+1, histogramCount(t, VRAMDrainSeconds, "drained"))
	assert.Equal(t, cancelledBefore+1, histogramCount(t, ModelSwapDurationSeconds, "incumbent", procGroupHelperBackend, "cancelled"),
		"unloading a model that never reached Ready records a cancelled swap")

	require.NoError(t, m.Unload(context.Background(), "next"))
}
