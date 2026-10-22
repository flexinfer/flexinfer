package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/flexinfer/flexinfer/pkg/constants"
)

const (
	testDiscreteVRAMBytes = "25753026560\n" // 24 GiB RX 7900 XTX
	testIGPUVRAMBytes     = "536870912\n"   // 512 MB Raphael iGPU
)

// writeSysfsCard lays out /sys/class/drm/cardN/device the way amdgpu does on a
// healthy, awake card. Callers mutate individual files to model faults.
func writeSysfsCard(t *testing.T, root string, index int, vramBytes string) string {
	t.Helper()
	dev := filepath.Join(root, "class/drm", "card"+itoa(index), "device")
	mkdirAll(t, filepath.Join(dev, "hwmon", "hwmon3"))
	mkdirAll(t, filepath.Join(dev, "power"))
	writeFile(t, filepath.Join(dev, "mem_info_vram_total"), vramBytes)
	writeFile(t, filepath.Join(dev, "mem_info_vram_used"), "1048576\n")
	writeFile(t, filepath.Join(dev, "gpu_busy_percent"), "0\n")
	writeFile(t, filepath.Join(dev, "current_link_speed"), "16.0 GT/s PCIe\n")
	writeFile(t, filepath.Join(dev, "power", "runtime_status"), "active\n")
	writeFile(t, filepath.Join(dev, "hwmon", "hwmon3", "temp1_input"), "27000\n")
	writeFile(t, filepath.Join(dev, "hwmon", "hwmon3", "power1_average"), "9000000\n")
	return dev
}

func itoa(i int) string { return strconv.Itoa(i) }

func newHealthTestAgent(t *testing.T, root string) *Agent {
	t.Helper()
	a := &Agent{
		labelPrefix:    "flexinfer.ai/",
		nodeName:       "gpu-node",
		namespace:      "flexinfer-system",
		sysfsRoot:      root,
		probeTimeout:   200 * time.Millisecond,
		healthSentinel: true,
		health:         newHealthState(),
	}
	a.runCmd = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, exec.ErrNotFound
	}
	return a
}

func TestProbeAMDHealthSysfs_HealthyCard(t *testing.T) {
	root := t.TempDir()
	writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	a := newHealthTestAgent(t, root)

	cards := a.probeAMDHealthSysfs()
	require.Len(t, cards, 1)
	assert.True(t, cards[0].Healthy)
	assert.Equal(t, "AMD", cards[0].Vendor)
	assert.Equal(t, "card0: ok", cards[0].String())
}

func TestProbeAMDHealthSysfs_LinkUnknownIsDegraded(t *testing.T) {
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	writeFile(t, filepath.Join(dev, "current_link_speed"), "Unknown\n")
	a := newHealthTestAgent(t, root)

	cards := a.probeAMDHealthSysfs()
	require.Len(t, cards, 1)
	assert.False(t, cards[0].Healthy)
	assert.Equal(t, HealthReasonPCIeLinkDown, cards[0].Reason)
	assert.Equal(t, "card0: pcie-link-down (current_link_speed=Unknown)", cards[0].String())
	assert.True(t, hardDegraded(cards))
}

func TestProbeAMDHealthSysfs_SuspendedCardIsNotJudged(t *testing.T) {
	// amdgpu runpm: a card in D3cold has no readable PCIe config space, so
	// current_link_speed reads Unknown on a perfectly healthy idle card.
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	writeFile(t, filepath.Join(dev, "current_link_speed"), "Unknown\n")
	writeFile(t, filepath.Join(dev, "power", "runtime_status"), "suspended\n")
	a := newHealthTestAgent(t, root)

	cards := a.probeAMDHealthSysfs()
	require.Len(t, cards, 1)
	assert.True(t, cards[0].Healthy, "suspended card must not be marked degraded")
}

func TestProbeAMDHealthSysfs_IgnoresIntegratedGPU(t *testing.T) {
	// cblevins-7900xtx: the 512 MB Raphael iGPU is a second amdgpu device.
	// Whatever it reports must not take the discrete card out of service.
	root := t.TempDir()
	writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	igpu := writeSysfsCard(t, root, 1, testIGPUVRAMBytes)
	writeFile(t, filepath.Join(igpu, "current_link_speed"), "Unknown\n")
	a := newHealthTestAgent(t, root)

	cards := a.probeAMDHealthSysfs()
	require.Len(t, cards, 1, "only the discrete card is assessed")
	assert.Equal(t, 0, cards[0].Index)
	assert.True(t, cards[0].Healthy)
}

func TestProbeAMDHealthSysfs_DeviceIOErrorIsDegraded(t *testing.T) {
	root := t.TempDir()
	writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	a := newHealthTestAgent(t, root)

	// Model the hung-SMU signature: hwmon reads fail with "Remote I/O error".
	orig := readSysfsFile
	t.Cleanup(func() { readSysfsFile = orig })
	readSysfsFile = func(name string) ([]byte, error) {
		if filepath.Base(name) == "temp1_input" {
			return nil, &os.PathError{Op: "read", Path: name, Err: syscall.EIO}
		}
		return os.ReadFile(name)
	}

	cards := a.probeAMDHealthSysfs()
	require.Len(t, cards, 1)
	assert.False(t, cards[0].Healthy)
	assert.Equal(t, HealthReasonDeviceIOError, cards[0].Reason)
	assert.Contains(t, cards[0].Detail, "hwmon/hwmon3/temp1_input")
	assert.Contains(t, cards[0].Detail, "input/output error")
}

func TestIsDeviceIOError(t *testing.T) {
	assert.False(t, isDeviceIOError(nil))
	assert.False(t, isDeviceIOError(os.ErrNotExist), "a missing attribute is not a fault")
	assert.False(t, isDeviceIOError(&os.PathError{Err: syscall.ENOENT}))
	assert.True(t, isDeviceIOError(&os.PathError{Err: syscall.EIO}))
	assert.True(t, isDeviceIOError(&os.PathError{Err: syscall.ENXIO}))
	assert.True(t, isDeviceIOError(&os.PathError{Err: syscall.ENODEV}))
}

func TestExec_TimeoutMarksProbeTimeout(t *testing.T) {
	root := t.TempDir()
	writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	a := newHealthTestAgent(t, root)
	a.runCmd = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		<-ctx.Done() // rocm-smi stuck in state D never returns on its own
		return nil, ctx.Err()
	}

	start := time.Now()
	_, err := a.exec(context.Background(), "chroot", "/host", "rocm-smi", "--showtemp", "--json")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errProbeTimeout), "got %v", err)
	assert.Less(t, time.Since(start), 5*time.Second, "the probe timeout must bound the call")

	health := a.evaluateGPUHealth(context.Background(), a.probeAMDHealthSysfs(), time.Now())
	require.True(t, health.Known)
	assert.True(t, health.Degraded)
	assert.Contains(t, health.Reason, "card0: probe-timeout")
	assert.Contains(t, health.Reason, "rocm-smi did not return within")
}

func TestExec_SkipsGPUToolsAfterHardDegrade(t *testing.T) {
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	writeFile(t, filepath.Join(dev, "current_link_speed"), "Unknown\n")
	a := newHealthTestAgent(t, root)
	calls := 0
	a.runCmd = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls++
		return []byte("{}"), nil
	}
	a.kubeClient = fake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu-node"}})

	require.NoError(t, a.ProbeAndLabel(context.Background()))
	assert.Equal(t, 0, calls, "no vendor tool may be executed against a card that is off the bus")

	// Non-GPU commands still run.
	_, err := a.exec(context.Background(), "chroot", "/host", "ls")
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestExec_NvidiaFallenOffBus(t *testing.T) {
	a := newHealthTestAgent(t, t.TempDir())
	a.lastVendor = "NVIDIA"
	a.runCmd = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("Unable to determine the device handle for GPU 0000:01:00.0: GPU has fallen off the bus.\n"), errors.New("exit status 255")
	}

	_, _ = a.exec(context.Background(), "nvidia-smi", "--query-gpu=memory.total", "--format=csv")
	health := a.evaluateGPUHealth(context.Background(), nil, time.Now())
	require.True(t, health.Known)
	assert.True(t, health.Degraded)
	assert.Contains(t, health.Reason, "card0: fallen-off-bus")
	require.Len(t, health.Cards, 1)
	assert.Equal(t, "NVIDIA", health.Cards[0].Vendor)
}

func TestEvaluateGPUHealth_RecoveryNeedsCleanStreak(t *testing.T) {
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	a := newHealthTestAgent(t, root)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

	h := a.evaluateGPUHealth(ctx, a.probeAMDHealthSysfs(), t0)
	assert.False(t, h.Degraded)
	assert.Equal(t, t0, h.Since)

	writeFile(t, filepath.Join(dev, "current_link_speed"), "Unknown\n")
	h = a.evaluateGPUHealth(ctx, a.probeAMDHealthSysfs(), t0.Add(30*time.Second))
	assert.True(t, h.Degraded, "a single bad read flips to degraded")
	assert.Equal(t, t0.Add(30*time.Second), h.Since)
	assert.Equal(t, "card0: pcie-link-down (current_link_speed=Unknown)", h.Reason)

	writeFile(t, filepath.Join(dev, "current_link_speed"), "16.0 GT/s PCIe\n")
	for i := 1; i < healthRecoveryStreak; i++ {
		h = a.evaluateGPUHealth(ctx, a.probeAMDHealthSysfs(), t0.Add(time.Duration(30+30*i)*time.Second))
		assert.True(t, h.Degraded, "clean cycle %d must not clear the verdict yet", i)
		assert.NotEmpty(t, h.Reason, "the last reason stays visible while degraded")
	}
	recoveredAt := t0.Add(time.Duration(30+30*healthRecoveryStreak) * time.Second)
	h = a.evaluateGPUHealth(ctx, a.probeAMDHealthSysfs(), recoveredAt)
	assert.False(t, h.Degraded)
	assert.Empty(t, h.Reason)
	assert.Equal(t, recoveredAt, h.Since)
}

func TestEvaluateGPUHealth_GPUMissing(t *testing.T) {
	root := t.TempDir()
	writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	a := newHealthTestAgent(t, root)
	ctx := context.Background()

	h := a.evaluateGPUHealth(ctx, a.probeAMDHealthSysfs(), time.Now())
	require.False(t, h.Degraded)

	require.NoError(t, os.RemoveAll(filepath.Join(root, "class/drm/card0")))
	h = a.evaluateGPUHealth(ctx, a.probeAMDHealthSysfs(), time.Now())
	require.True(t, h.Known)
	assert.True(t, h.Degraded)
	assert.Contains(t, h.Reason, "card0: gpu-missing")
	assert.Contains(t, h.Reason, "0 of 1 discrete cards visible")
}

func TestEvaluateGPUHealth_NoEvidenceIsUnknown(t *testing.T) {
	a := newHealthTestAgent(t, t.TempDir())
	h := a.evaluateGPUHealth(context.Background(), nil, time.Now())
	assert.False(t, h.Known, "a node with no GPU evidence gets no verdict")

	labels := map[string]string{}
	annotations := map[string]string{}
	applyGPUHealth(h, labels, annotations)
	assert.Empty(t, labels)
	assert.Empty(t, annotations)
}

func TestProbeAndLabel_PublishesGPUHealth(t *testing.T) {
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	// Labels a previous (tool-backed) cycle applied; a degraded cycle must
	// leave them in place, since the merge patch only touches what it sets.
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   "gpu-node",
		Labels: map[string]string{"flexinfer.ai/gpu.vendor": "AMD", "flexinfer.ai/gpu.arch": "gfx1100"},
	}}
	client := fake.NewSimpleClientset(node)
	a := newHealthTestAgent(t, root)
	a.kubeClient = client
	ctx := context.Background()

	require.NoError(t, a.ProbeAndLabel(ctx))
	got, err := client.CoreV1().Nodes().Get(ctx, "gpu-node", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, constants.GPUHealthOK, got.Labels[constants.NodeLabelGPUHealth])
	assert.Equal(t, "", got.Annotations[constants.NodeAnnotationGPUHealthReason])
	assert.NotEmpty(t, got.Annotations[constants.NodeAnnotationGPUHealthSince])

	writeFile(t, filepath.Join(dev, "current_link_speed"), "Unknown\n")
	require.NoError(t, a.ProbeAndLabel(ctx))
	got, err = client.CoreV1().Nodes().Get(ctx, "gpu-node", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, constants.GPUHealthDegraded, got.Labels[constants.NodeLabelGPUHealth])
	assert.Equal(t, "card0: pcie-link-down (current_link_speed=Unknown)", got.Annotations[constants.NodeAnnotationGPUHealthReason])
	assert.Equal(t, "gfx1100", got.Labels["flexinfer.ai/gpu.arch"], "existing hardware labels survive a degraded cycle")
}

func TestProbeAndLabel_SentinelDisabledPublishesNothing(t *testing.T) {
	root := t.TempDir()
	dev := writeSysfsCard(t, root, 0, testDiscreteVRAMBytes)
	writeFile(t, filepath.Join(dev, "current_link_speed"), "Unknown\n")
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu-node"}}
	client := fake.NewSimpleClientset(node)
	a := newHealthTestAgent(t, root)
	a.kubeClient = client
	a.healthSentinel = false

	require.NoError(t, a.ProbeAndLabel(context.Background()))
	got, err := client.CoreV1().Nodes().Get(context.Background(), "gpu-node", metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotContains(t, got.Labels, constants.NodeLabelGPUHealth)
	assert.NotContains(t, got.Annotations, constants.NodeAnnotationGPUHealthReason)
}

func TestProbeToolName(t *testing.T) {
	assert.Equal(t, "rocm-smi", probeToolName("rocm-smi", []string{"--showtemp"}))
	assert.Equal(t, "rocm-smi", probeToolName("chroot", []string{"/host", "rocm-smi", "--showtemp"}))
	assert.Equal(t, "nvidia-smi", probeToolName("/usr/bin/nvidia-smi", nil))
	assert.Equal(t, "chroot", probeToolName("chroot", []string{"/host"}))
	assert.True(t, isGPUTool("rocminfo"))
	assert.False(t, isGPUTool("ls"))
}
