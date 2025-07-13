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
}

func TestDetectCPU(t *testing.T) {
	agent := &Agent{labelPrefix: "flexinfer.ai/"}
	labels := make(map[string]string)
	agent.detectCPU(labels)

	assert.Equal(t, "false", labels["flexinfer.ai/cpu.avx512"])
}
