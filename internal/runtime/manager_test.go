package runtime

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/backend"
	_ "github.com/flexinfer/flexinfer/backend" // register all backends
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
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

func TestUnloadDoesNotBlockStatusWhileWaitingForBackendExit(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestRuntimeManagerHelperProcess")
	cmd.Env = append(os.Environ(), "FLEXINFER_RUNTIME_HELPER_PROCESS=1")
	require.NoError(t, cmd.Start())
	time.Sleep(100 * time.Millisecond)

	m := NewManager(ManagerConfig{
		ShutdownTimeout: 150 * time.Millisecond,
		GPUVendor:       backend.GPUVendorAMD,
		GPUArch:         "gfx906",
	})
	loaded := &LoadedModel{
		Name:    "stubborn-model",
		Backend: "llamacpp",
		Model:   "test/model",
		State:   ModelStateReady,
		PID:     cmd.Process.Pid,
		cmd:     cmd,
		done:    make(chan error, 1),
	}
	m.active = loaded
	go func() {
		err := cmd.Wait()
		loaded.done <- err
		close(loaded.done)
	}()

	unloaded := make(chan error, 1)
	go func() {
		unloaded <- m.Unload(context.Background(), loaded.Name)
	}()

	time.Sleep(25 * time.Millisecond)
	statusReturned := make(chan RuntimeStatus, 1)
	go func() {
		statusReturned <- m.Status()
	}()

	select {
	case status := <-statusReturned:
		assert.Equal(t, "gfx906", status.GPUArch)
		assert.NotNil(t, status.ActiveModel)
		assert.Equal(t, "Stopping", status.ActiveModel.State)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Status blocked while unload waited for backend shutdown")
	}

	select {
	case err := <-unloaded:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Unload did not finish after shutdown timeout")
	}
}

func TestRuntimeManagerHelperProcess(t *testing.T) {
	if os.Getenv("FLEXINFER_RUNTIME_HELPER_PROCESS") != "1" {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	for range signals {
		// Keep running until SIGKILL so tests can exercise shutdown timeout.
	}
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

func TestResolveExecutableFallsBackToPathForStaleAbsolutePath(t *testing.T) {
	binDir := t.TempDir()
	want := filepath.Join(binDir, "llama-server")
	require.NoError(t, os.WriteFile(want, []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, ok := resolveExecutable("/opt/src/llama.cpp/build/bin/llama-server")

	require.True(t, ok)
	assert.Equal(t, want, got)
}

func TestResolveExecutablePreservesExistingAbsolutePath(t *testing.T) {
	binDir := t.TempDir()
	want := filepath.Join(binDir, "llama-server")
	require.NoError(t, os.WriteFile(want, []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", t.TempDir()+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, ok := resolveExecutable(want)

	require.True(t, ok)
	assert.Equal(t, want, got)
}

func TestRuntimeBackendPortMovesAPI8080BackendsToBackendPort(t *testing.T) {
	b, ok := backend.Get("llamacpp")
	require.True(t, ok)
	spec := &backend.ModelSpec{}

	got := runtimeBackendPort(b, spec)

	assert.Equal(t, int32(8000), got)
	assert.Equal(t, float64(8000), spec.Config["port"])
}

func TestRuntimeBackendPortPreservesExplicitPort(t *testing.T) {
	b, ok := backend.Get("llamacpp")
	require.True(t, ok)
	spec := &backend.ModelSpec{
		Config: map[string]any{"port": float64(18080)},
	}

	got := runtimeBackendPort(b, spec)

	assert.Equal(t, int32(18080), got)
	assert.Equal(t, float64(18080), spec.Config["port"])
}

func TestOverlayEnvVarsReplacesByName(t *testing.T) {
	base := []corev1.EnvVar{
		{Name: "A", Value: "one"},
		{Name: "B", Value: "two"},
	}
	overlay := []corev1.EnvVar{
		{Name: "B", Value: "override"},
		{Name: "C", Value: "three"},
	}

	got := overlayEnvVars(base, overlay)

	assert.Equal(t, []corev1.EnvVar{
		{Name: "A", Value: "one"},
		{Name: "B", Value: "override"},
		{Name: "C", Value: "three"},
	}, got)
}
