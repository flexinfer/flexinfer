package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDetectGPUNvidia(t *testing.T) {
	a := &Agent{labelPrefix: "flexinfer.ai/"}
	a.runCmd = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "nvidia-smi" {
			return []byte("24576 MiB, 8.9\n24576 MiB, 8.9\n"), nil
		}
		return nil, exec.ErrNotFound
	}
	labels := make(map[string]string)
	a.detectGPU(context.Background(), labels)

	assert.Equal(t, "NVIDIA", labels["flexinfer.ai/gpu.vendor"])
	assert.Equal(t, "24Gi", labels["flexinfer.ai/gpu.vram"])
	assert.Equal(t, "sm_89", labels["flexinfer.ai/gpu.arch"])
	assert.Equal(t, "true", labels["flexinfer.ai/gpu.int4"])
	assert.Equal(t, "2", labels["flexinfer.ai/gpu.count"])
}

func TestDetectGPUAMD(t *testing.T) {
	a := &Agent{labelPrefix: "flexinfer.ai/"}
	a.runCmd = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "nvidia-smi":
			return nil, exec.ErrNotFound
		case "rocm-smi":
			return []byte(`{"card0": {"vram_total": "65536"}, "card1": {"vram_total": "65536"}}`), nil
		case "rocminfo":
			return []byte("Name: gfx90a"), nil
		default:
			return nil, exec.ErrNotFound
		}
	}
	labels := make(map[string]string)
	a.detectGPU(context.Background(), labels)

	assert.Equal(t, "AMD", labels["flexinfer.ai/gpu.vendor"])
	assert.Equal(t, "64Gi", labels["flexinfer.ai/gpu.vram"])
	assert.Equal(t, "gfx90a", labels["flexinfer.ai/gpu.arch"])
	assert.Equal(t, "true", labels["flexinfer.ai/gpu.int4"])
	assert.Equal(t, "2", labels["flexinfer.ai/gpu.count"])
}

func TestDetectGPUNone(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("8"),
				corev1.ResourceMemory: resource.MustParse("32Gi"),
			},
		},
	}
	a := &Agent{
		labelPrefix: "flexinfer.ai/",
		nodeName:    "test-node",
		kubeClient:  fake.NewSimpleClientset(node),
	}
	// Keep sysfs probing hermetic on linux CI runners.
	a.sysfsRoot = t.TempDir()
	a.runCmd = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, exec.ErrNotFound
	}
	labels := make(map[string]string)
	a.detectGPU(context.Background(), labels)
	_, ok := labels["flexinfer.ai/gpu.vendor"]
	assert.False(t, ok)
}

func TestDetectGPUFromAllocatable_NVIDIA(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
			},
		},
	}
	a := &Agent{
		labelPrefix: "flexinfer.ai/",
		nodeName:    "test-node",
		kubeClient:  fake.NewSimpleClientset(node),
	}
	labels := make(map[string]string)
	a.detectGPUFromAllocatable(context.Background(), labels)

	assert.Equal(t, "NVIDIA", labels["flexinfer.ai/gpu.vendor"])
	assert.Equal(t, "2", labels["flexinfer.ai/gpu.count"])
	// No VRAM or arch available from allocatable
	_, hasVRAM := labels["flexinfer.ai/gpu.vram"]
	assert.False(t, hasVRAM)
}

func TestDetectGPUFromAllocatable_AMD(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceName("amd.com/gpu"): resource.MustParse("1"),
			},
		},
	}
	a := &Agent{
		labelPrefix: "flexinfer.ai/",
		nodeName:    "test-node",
		kubeClient:  fake.NewSimpleClientset(node),
	}
	labels := make(map[string]string)
	a.detectGPUFromAllocatable(context.Background(), labels)

	assert.Equal(t, "AMD", labels["flexinfer.ai/gpu.vendor"])
	assert.Equal(t, "1", labels["flexinfer.ai/gpu.count"])
}

func TestDetectGPUFromAllocatable_NoGPU(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
		},
	}
	a := &Agent{
		labelPrefix: "flexinfer.ai/",
		nodeName:    "test-node",
		kubeClient:  fake.NewSimpleClientset(node),
	}
	labels := make(map[string]string)
	a.detectGPUFromAllocatable(context.Background(), labels)

	_, ok := labels["flexinfer.ai/gpu.vendor"]
	assert.False(t, ok)
}

func TestDetectCPU(t *testing.T) {
	a := &Agent{labelPrefix: "flexinfer.ai/"}
	labels := make(map[string]string)
	a.detectCPU(labels)
	// Value depends on host; just assert key exists
	_, ok := labels["flexinfer.ai/cpu.avx512"]
	assert.True(t, ok)
}

// TestParseNvidiaFreeMemory tests NVIDIA free memory parsing.
func TestParseNvidiaFreeMemory(t *testing.T) {
	a := &Agent{}

	tests := []struct {
		name     string
		input    string
		expected uint64
	}{
		{
			name:     "single GPU",
			input:    "22528\n",
			expected: 22528,
		},
		{
			name:     "dual GPU",
			input:    "22528\n22016\n",
			expected: 44544,
		},
		{
			name:     "empty",
			input:    "",
			expected: 0,
		},
		{
			name:     "whitespace",
			input:    "  22528  \n  22016  \n",
			expected: 44544,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := a.parseNvidiaFreeMemory(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestParseRocmFreeMemory_ROCm5x tests ROCm 5.x format parsing.
func TestParseRocmFreeMemory_ROCm5x(t *testing.T) {
	a := &Agent{}

	// ROCm 5.x format: lowercase keys, values as plain numbers (bytes)
	input := `{"card0": {"vram_free": "25742540800"}}`
	result := a.parseRocmFreeMemory(input)

	// 25742540800 bytes / 1048576 = 24550 MB
	assert.Equal(t, uint64(24550), result)
}

// TestParseRocmFreeMemory_ROCm60 tests ROCm 6.0-6.3 format parsing.
func TestParseRocmFreeMemory_ROCm60(t *testing.T) {
	a := &Agent{}

	input := loadTestData(t, "rocm-smi-6.0-vram.json")
	result := a.parseRocmFreeMemory(string(input))

	// 25742540800 bytes / 1048576 = 24550 MB
	assert.Equal(t, uint64(24550), result)
}

// TestParseRocmFreeMemory_ROCm64 tests ROCm 6.4+ format parsing.
func TestParseRocmFreeMemory_ROCm64(t *testing.T) {
	a := &Agent{}

	input := loadTestData(t, "rocm-smi-6.4-vram.json")
	result := a.parseRocmFreeMemory(string(input))

	// Value is already in MB
	assert.Equal(t, uint64(24550), result)
}

// TestParseRocmFreeMemory_MultiGPU tests multi-GPU VRAM parsing.
func TestParseRocmFreeMemory_MultiGPU(t *testing.T) {
	a := &Agent{}

	// Multi-GPU with ROCm 6.0 format
	input := `{
		"card0": {"VRAM Total Free Memory (B)": "25742540800"},
		"card1": {"VRAM Total Free Memory (B)": "25742540800"}
	}`
	result := a.parseRocmFreeMemory(input)

	// 2 * 24550 MB = 49100 MB
	assert.Equal(t, uint64(49100), result)
}

// TestParseRocm_ROCm5x tests ROCm 5.x total VRAM parsing.
func TestParseRocm_ROCm5x(t *testing.T) {
	a := &Agent{labelPrefix: "flexinfer.ai/"}
	input := loadTestData(t, "rocm-smi-5.7-vram.json")

	labels := make(map[string]string)
	a.parseRocm(string(input), "Name: gfx1100", labels)

	assert.Equal(t, "AMD", labels["flexinfer.ai/gpu.vendor"])
	// 25753026560 bytes / 1073741824 = 23.98 Gi ≈ 23Gi
	assert.Equal(t, "23Gi", labels["flexinfer.ai/gpu.vram"])
	assert.Equal(t, "gfx1100", labels["flexinfer.ai/gpu.arch"])
}

// TestParseRocm_ROCm60 tests ROCm 6.0-6.3 total VRAM parsing.
func TestParseRocm_ROCm60(t *testing.T) {
	a := &Agent{labelPrefix: "flexinfer.ai/"}
	input := loadTestData(t, "rocm-smi-6.0-vram.json")

	labels := make(map[string]string)
	a.parseRocm(string(input), "Name: gfx1100", labels)

	assert.Equal(t, "AMD", labels["flexinfer.ai/gpu.vendor"])
	// 25753026560 bytes / 1073741824 = 23.98 Gi ≈ 23Gi
	assert.Equal(t, "23Gi", labels["flexinfer.ai/gpu.vram"])
	assert.Equal(t, "gfx1100", labels["flexinfer.ai/gpu.arch"])
}

// TestParseRocm_ROCm64 tests ROCm 6.4+ total VRAM parsing.
func TestParseRocm_ROCm64(t *testing.T) {
	a := &Agent{labelPrefix: "flexinfer.ai/"}
	input := loadTestData(t, "rocm-smi-6.4-vram.json")

	labels := make(map[string]string)
	a.parseRocm(string(input), "Name: gfx1100", labels)

	assert.Equal(t, "AMD", labels["flexinfer.ai/gpu.vendor"])
	// 24560 MB / 1024 = 23.98 Gi ≈ 23Gi
	assert.Equal(t, "23Gi", labels["flexinfer.ai/gpu.vram"])
	assert.Equal(t, "gfx1100", labels["flexinfer.ai/gpu.arch"])
}

func TestParseRocm_IgnoresIntegratedGPUCount(t *testing.T) {
	a := &Agent{labelPrefix: "flexinfer.ai/"}

	// Some nodes expose both an iGPU (tiny "VRAM") and a dGPU (real VRAM).
	// Count only the discrete GPU(s) for scheduling + benchmark cache keys.
	input := `{
  "card0": {"GPU Memory Total (MB)": "512", "GPU Memory Free (MB)": "512"},
  "card1": {"GPU Memory Total (MB)": "24560", "GPU Memory Free (MB)": "24550"}
}`

	labels := make(map[string]string)
	a.parseRocm(input, "Name: gfx1036\nName: gfx1100\n", labels)

	assert.Equal(t, "AMD", labels["flexinfer.ai/gpu.vendor"])
	assert.Equal(t, "1", labels["flexinfer.ai/gpu.count"])
	assert.Equal(t, "23Gi", labels["flexinfer.ai/gpu.vram"])
	assert.Equal(t, "gfx1100", labels["flexinfer.ai/gpu.arch"])
}

func TestDetectGPUMetricsAMD_IgnoresIntegratedGPU(t *testing.T) {
	a := &Agent{}
	a.runCmd = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "nvidia-smi":
			return nil, exec.ErrNotFound
		case "chroot":
			return nil, exec.ErrNotFound
		case "rocm-smi":
			if len(args) >= 2 && args[0] == "--showtemp" && args[1] == "--json" {
				return []byte(`{"card0": {"Temperature (Sensor edge) (C)": "40"}, "card1": {"Temperature (Sensor edge) (C)": "45"}}`), nil
			}
			if len(args) >= 3 && args[0] == "--showmeminfo" && args[1] == "vram" && args[2] == "--json" {
				return []byte(`{
  "card0": {"GPU Memory Total (MB)": "512", "GPU Memory Used (MB)": "0", "GPU Memory Free (MB)": "512"},
  "card1": {"GPU Memory Total (MB)": "24560", "GPU Memory Used (MB)": "10", "GPU Memory Free (MB)": "24550"}
}`), nil
			}
			if len(args) >= 2 && args[0] == "--showuse" && args[1] == "--json" {
				return []byte(`{"card0": {"GPU use (%)": "0"}, "card1": {"GPU use (%)": "1"}}`), nil
			}
			return nil, exec.ErrNotFound
		default:
			return nil, exec.ErrNotFound
		}
	}

	metrics := a.DetectGPUMetrics(context.Background())
	require.Len(t, metrics, 1)
	assert.Equal(t, "AMD", metrics[0].Vendor)
	assert.Greater(t, metrics[0].TotalVRAMMB, uint64(20000))
}

func TestExtractAMDArch_PrefersHigherMajor(t *testing.T) {
	out := `
Agents:
  Name: gfx1036
  Name: gfx1100
`
	assert.Equal(t, "gfx1100", extractAMDArch(out))
}

// TestParseMemoryString tests the memory string parser.
func TestParseMemoryString(t *testing.T) {
	a := &Agent{}

	tests := []struct {
		name     string
		key      string
		val      string
		expected uint64
	}{
		{
			name:     "MB explicit",
			key:      "GPU Memory Free (MB)",
			val:      "24550",
			expected: 24550,
		},
		{
			name:     "bytes explicit",
			key:      "VRAM Total Free Memory (B)",
			val:      "25742540800",
			expected: 24550,
		},
		{
			name:     "large number as bytes",
			key:      "vram_free",
			val:      "25742540800",
			expected: 24550,
		},
		{
			name:     "small number as MB",
			key:      "vram_free",
			val:      "24550",
			expected: 24550,
		},
		{
			name:     "empty",
			key:      "vram_free",
			val:      "",
			expected: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := a.parseMemoryString(tc.key, tc.val)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestExtractMemoryValue tests the memory value extraction from GPU data.
func TestExtractMemoryValue(t *testing.T) {
	a := &Agent{}

	tests := []struct {
		name     string
		gpu      map[string]any
		keys     []string
		expected uint64
	}{
		{
			name: "exact match",
			gpu: map[string]any{
				"GPU Memory Free (MB)": "24550",
			},
			keys:     []string{"GPU Memory Free (MB)"},
			expected: 24550,
		},
		{
			name: "case insensitive",
			gpu: map[string]any{
				"gpu memory free (mb)": "24550",
			},
			keys:     []string{"GPU Memory Free (MB)"},
			expected: 24550,
		},
		{
			name: "fallback key",
			gpu: map[string]any{
				"vram_free": "25742540800",
			},
			keys:     []string{"GPU Memory Free (MB)", "vram_free"},
			expected: 24550,
		},
		{
			name:     "no match",
			gpu:      map[string]any{},
			keys:     []string{"vram_free"},
			expected: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := a.extractMemoryValue(tc.gpu, tc.keys)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestDetectFreeVRAM_NVIDIA tests NVIDIA free VRAM detection.
func TestDetectFreeVRAM_NVIDIA(t *testing.T) {
	a := &Agent{labelPrefix: "flexinfer.ai/"}
	a.runCmd = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "nvidia-smi" {
			return loadTestData(t, "nvidia-smi-memory.txt"), nil
		}
		return nil, exec.ErrNotFound
	}

	result := a.detectFreeVRAM(context.Background())
	// 22528 + 22016 = 44544 MB
	assert.Equal(t, uint64(44544), result)
}

// TestDetectFreeVRAM_AMD tests AMD free VRAM detection.
func TestDetectFreeVRAM_AMD(t *testing.T) {
	a := &Agent{labelPrefix: "flexinfer.ai/"}
	a.runCmd = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "nvidia-smi" {
			return nil, exec.ErrNotFound
		}
		if name == "rocm-smi" {
			return loadTestData(t, "rocm-smi-6.0-vram.json"), nil
		}
		return nil, exec.ErrNotFound
	}

	result := a.detectFreeVRAM(context.Background())
	// 25742540800 bytes / 1048576 = 24550 MB
	assert.Equal(t, uint64(24550), result)
}

func TestDetectAMDMetrics_SysfsIncludesUtilization(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sysfs GPU metrics test is linux-only")
	}

	sys := t.TempDir()
	mustWrite := func(path, contents string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	}

	// Minimal sysfs layout:
	// /sys/class/drm/card0/device/{mem_info_vram_total,mem_info_vram_used,gpu_busy_percent,hwmon/hwmon0/temp1_input}
	card0 := filepath.Join(sys, "class/drm/card0")
	dev0 := filepath.Join(card0, "device")
	mustWrite(filepath.Join(dev0, "mem_info_vram_total"), "25753026560\n") // ~24560MB
	mustWrite(filepath.Join(dev0, "mem_info_vram_used"), "1048576000\n")   // 1000MB
	mustWrite(filepath.Join(dev0, "gpu_busy_percent"), "37\n")
	mustWrite(filepath.Join(dev0, "hwmon/hwmon0/temp1_input"), "42000\n")

	// Ensure connectors (card0-DP-1) don't get treated as GPUs.
	_ = os.MkdirAll(filepath.Join(sys, "class/drm/card0-DP-1"), 0o755)

	a := &Agent{labelPrefix: "flexinfer.ai/", sysfsRoot: sys}
	// Force sysfs fallback.
	a.runCmd = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, exec.ErrNotFound
	}

	metrics := a.detectAMDMetrics(context.Background())
	require.Len(t, metrics, 1)
	assert.Equal(t, "AMD", metrics[0].Vendor)
	assert.Equal(t, 0, metrics[0].Index)
	assert.InDelta(t, 42.0, metrics[0].Temperature, 0.001)
	assert.Equal(t, uint64(24560), metrics[0].TotalVRAMMB)
	assert.Equal(t, uint64(1000), metrics[0].UsedVRAMMB)
	assert.Equal(t, uint64(23560), metrics[0].FreeVRAMMB)
	assert.InDelta(t, 37.0, metrics[0].Utilization, 0.001)
}

// loadTestData loads a test data file.
func loadTestData(t *testing.T, filename string) []byte {
	t.Helper()
	path := filepath.Join("testdata", filename)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to load test data: %s", path)
	return data
}
