package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	a := &Agent{labelPrefix: "flexinfer.ai/"}
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
		gpu      map[string]interface{}
		keys     []string
		expected uint64
	}{
		{
			name: "exact match",
			gpu: map[string]interface{}{
				"GPU Memory Free (MB)": "24550",
			},
			keys:     []string{"GPU Memory Free (MB)"},
			expected: 24550,
		},
		{
			name: "case insensitive",
			gpu: map[string]interface{}{
				"gpu memory free (mb)": "24550",
			},
			keys:     []string{"GPU Memory Free (MB)"},
			expected: 24550,
		},
		{
			name: "fallback key",
			gpu: map[string]interface{}{
				"vram_free": "25742540800",
			},
			keys:     []string{"GPU Memory Free (MB)", "vram_free"},
			expected: 24550,
		},
		{
			name:     "no match",
			gpu:      map[string]interface{}{},
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

// loadTestData loads a test data file.
func loadTestData(t *testing.T, filename string) []byte {
	t.Helper()
	path := filepath.Join("testdata", filename)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to load test data: %s", path)
	return data
}
