package agent

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
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
