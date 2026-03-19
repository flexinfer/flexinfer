package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/backend"
	_ "github.com/flexinfer/flexinfer/backend" // register all backends
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func init() {
	log.SetLogger(zap.New(zap.UseDevMode(true)))
}

func TestNewManager(t *testing.T) {
	m := NewManager(ManagerConfig{
		GPUVendor:       backend.GPUVendorAMD,
		GPUArch:         "gfx1100",
		ShutdownTimeout: 10 * time.Second,
		ModelBasePath:   "/tmp/test-models",
	})

	assert.NotNil(t, m)
	assert.Equal(t, backend.GPUVendorAMD, m.gpuVendor)
	assert.Equal(t, "gfx1100", m.gpuArch)
	assert.Equal(t, 10*time.Second, m.shutdownTimeout)
	assert.Equal(t, "/tmp/test-models", m.modelBasePath)
}

func TestManagerDefaults(t *testing.T) {
	m := NewManager(ManagerConfig{})

	assert.Equal(t, 30*time.Second, m.shutdownTimeout)
	assert.Equal(t, 5*time.Second, m.healthCheckInterval)
	assert.Equal(t, "/models", m.modelBasePath)
}

func TestStatusEmpty(t *testing.T) {
	m := NewManager(ManagerConfig{
		GPUVendor: backend.GPUVendorAMD,
		GPUArch:   "gfx1100",
	})

	status := m.Status()
	assert.Equal(t, "amd", status.GPUVendor)
	assert.Equal(t, "gfx1100", status.GPUArch)
	assert.Nil(t, status.ActiveModel)
}

func TestActiveNil(t *testing.T) {
	m := NewManager(ManagerConfig{})
	assert.Nil(t, m.Active())
}

func TestLoadUnknownBackend(t *testing.T) {
	m := NewManager(ManagerConfig{
		GPUVendor: backend.GPUVendorAMD,
		GPUArch:   "gfx1100",
	})

	ctx := context.Background()
	err := m.Load(ctx, "test-model", LoadRequest{
		Backend: "nonexistent-backend",
		Model:   "test/model",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown backend")
}

func TestLoadRejectsComfyUIInUnifiedRuntime(t *testing.T) {
	m := NewManager(ManagerConfig{
		GPUVendor: backend.GPUVendorAMD,
		GPUArch:   "gfx1100",
	})

	ctx := context.Background()
	err := m.Load(ctx, "test-comfy", LoadRequest{
		Backend: "comfy",
		Model:   "test/model",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not bundled in flexinfer-runtime images")
	assert.Nil(t, m.Active())
}

func TestUnloadNotLoaded(t *testing.T) {
	m := NewManager(ManagerConfig{})
	ctx := context.Background()

	err := m.Unload(ctx, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not loaded")
}

func TestShutdownEmpty(t *testing.T) {
	m := NewManager(ManagerConfig{})
	ctx := context.Background()

	err := m.Shutdown(ctx)
	assert.NoError(t, err)
}

func TestInferCommand(t *testing.T) {
	tests := []struct {
		backend      string
		expectedExec string
		expectedArgs []string
	}{
		{"vllm", "python", []string{"-m", "vllm.entrypoints.openai.api_server"}},
		{"vllm-omni", "python", []string{"-m", "vllm.entrypoints.openai.api_server"}},
		{"llamacpp", "llama-server", nil},
		{"ollama", "ollama", nil},
		{"diffusers", "python", []string{"/opt/flexinfer/server-diffusers.py"}},
		{"unknown", "unknown", nil},
	}

	for _, tt := range tests {
		t.Run(tt.backend, func(t *testing.T) {
			exec, args := inferCommand(tt.backend)
			assert.Equal(t, tt.expectedExec, exec)
			assert.Equal(t, tt.expectedArgs, args)
		})
	}
}
