package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectGPU(t *testing.T) {
	agent := &Agent{labelPrefix: "flexinfer.ai/"}
	labels := make(map[string]string)
	agent.detectGPU(labels)

	assert.Equal(t, "NVIDIA", labels["flexinfer.ai/gpu.vendor"])
	assert.Equal(t, "24Gi", labels["flexinfer.ai/gpu.vram"])
	assert.Equal(t, "sm_89", labels["flexinfer.ai/gpu.arch"])
	assert.Equal(t, "true", labels["flexinfer.ai/gpu.int4"])
	assert.Equal(t, "1", labels["flexinfer.ai/gpu.count"])
}

func TestDetectGPUEnvOverride(t *testing.T) {
	t.Setenv("GPU_VENDOR", "AMD")
	t.Setenv("GPU_VRAM", "16Gi")
	t.Setenv("GPU_ARCH", "gfx90a")
	t.Setenv("GPU_INT4", "false")
	t.Setenv("GPU_COUNT", "4")

	agent := &Agent{labelPrefix: "flexinfer.ai/"}
	labels := make(map[string]string)
	agent.detectGPU(labels)

	assert.Equal(t, "AMD", labels["flexinfer.ai/gpu.vendor"])
	assert.Equal(t, "16Gi", labels["flexinfer.ai/gpu.vram"])
	assert.Equal(t, "gfx90a", labels["flexinfer.ai/gpu.arch"])
	assert.Equal(t, "false", labels["flexinfer.ai/gpu.int4"])
	assert.Equal(t, "4", labels["flexinfer.ai/gpu.count"])
}

func TestDetectCPU(t *testing.T) {
	agent := &Agent{labelPrefix: "flexinfer.ai/"}
	labels := make(map[string]string)
	agent.detectCPU(labels)

	assert.Equal(t, "false", labels["flexinfer.ai/cpu.avx512"])
}

func TestDetectCPUEnvOverride(t *testing.T) {
	t.Setenv("CPU_AVX512", "true")
	agent := &Agent{labelPrefix: "flexinfer.ai/"}
	labels := make(map[string]string)
	agent.detectCPU(labels)

	assert.Equal(t, "true", labels["flexinfer.ai/cpu.avx512"])
}
