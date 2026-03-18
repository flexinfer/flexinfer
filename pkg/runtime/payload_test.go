package runtime

import (
	"encoding/json"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	_ "github.com/flexinfer/flexinfer/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildLoadPayloadForModelAddsCompilationCacheAndStartupTimeout(t *testing.T) {
	b, ok := backend.Get("vllm")
	require.True(t, ok)

	enabled := true
	timeout := metav1.Duration{Duration: 90 * time.Second}
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fast-chat",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://Qwen/Qwen3-14B",
			Serverless: &aiv1alpha2.ServerlessSpec{
				ColdStartTimeout: &timeout,
			},
			Cache: &aiv1alpha2.CacheSpec{
				CompilationCache: &aiv1alpha2.CompilationCacheSpec{
					Enabled:  &enabled,
					HostPath: "/var/lib/flexinfer/compile-cache",
				},
			},
		},
	}

	data, err := BuildLoadPayloadForModel(model, b, BuildLoadOptions{
		ModelBasePath: "/models",
		GPUVendor:     backend.GPUVendorAMD,
	})
	require.NoError(t, err)

	var payload LoadPayload
	require.NoError(t, json.Unmarshal(data, &payload))
	require.Contains(t, payload.Config, "startupTimeoutSeconds")
	assert.Equal(t, 90.0, payload.Config["startupTimeoutSeconds"])
	assert.Contains(t, payload.Env, EnvVar{
		Name:  "TORCHINDUCTOR_CACHE_DIR",
		Value: "/var/lib/flexinfer/compile-cache/flexinfer-system/fast-chat/inductor",
	})
}

func TestBuildLoadPayloadForModelUsesGPUProfileDeviceDefaults(t *testing.T) {
	b, ok := backend.Get("llamacpp")
	require.True(t, ok)

	rawConfig := []byte(`{"contextSize":4096}`)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vega-chat",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://TheBloke/Mistral-7B-GGUF",
			Config:  &apiextensionsv1.JSON{Raw: rawConfig},
		},
	}

	data, err := BuildLoadPayloadForModel(model, b, BuildLoadOptions{
		ModelBasePath: "/models",
		GPUVendor:     backend.GPUVendorAMD,
		GPUProfile: &aiv1alpha2.GPUProfileSpec{
			UsableDeviceIndices: []string{"0"},
		},
	})
	require.NoError(t, err)

	var payload LoadPayload
	require.NoError(t, json.Unmarshal(data, &payload))
	assert.Equal(t, "0", payload.Config["hipVisibleDevices"])
	assert.Equal(t, "0", payload.Config["rocrVisibleDevices"])
	assert.Equal(t, "0", payload.Config["gpuDeviceOrdinal"])
}

func TestBuildLoadPayloadForModelAddsNVIDIARuntimeEnvFromProfile(t *testing.T) {
	b, ok := backend.Get("ollama")
	require.True(t, ok)

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "retro-chat",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "ollama",
			Source:  "ollama://tinyllama:latest",
		},
	}

	data, err := BuildLoadPayloadForModel(model, b, BuildLoadOptions{
		ModelBasePath: "/models",
		GPUVendor:     backend.GPUVendorNVIDIA,
		GPUProfile: &aiv1alpha2.GPUProfileSpec{
			UsableDeviceIndices: []string{"0"},
		},
	})
	require.NoError(t, err)

	var payload LoadPayload
	require.NoError(t, json.Unmarshal(data, &payload))
	assert.Contains(t, payload.Env, EnvVar{Name: "CUDA_VISIBLE_DEVICES", Value: "0"})
	assert.Contains(t, payload.Env, EnvVar{Name: "NVIDIA_VISIBLE_DEVICES", Value: "0"})
}

func TestDirectRuntimeLoadEligibility(t *testing.T) {
	tests := []struct {
		name       string
		model      *aiv1alpha2.Model
		wantOK     bool
		wantReason string
	}{
		{
			name:       "nil model",
			model:      nil,
			wantOK:     false,
			wantReason: "model is nil",
		},
		{
			name: "raw pvc source without staged local cache is not eligible",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "pvc://gonzalomo-fluxpony-source/gonzalomoXLFluxPony_v30FluxDAIO.safetensors",
				},
			},
			wantOK:     false,
			wantReason: "raw pvc:// sources require staged Local cache for runtime loads",
		},
		{
			name: "pvc source with local cache but not ready is not eligible",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "pvc://gonzalomo-fluxpony-source/gonzalomoXLFluxPony_v30FluxDAIO.safetensors",
					Cache:  &aiv1alpha2.CacheSpec{Strategy: "Local"},
				},
			},
			wantOK:     false,
			wantReason: "staged Local cache is not ready",
		},
		{
			name: "pvc source with ready local cache is eligible",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "pvc://gonzalomo-fluxpony-source/gonzalomoXLFluxPony_v30FluxDAIO.safetensors",
					Cache:  &aiv1alpha2.CacheSpec{Strategy: "Local"},
				},
				Status: aiv1alpha2.ModelStatus{
					Cache: &aiv1alpha2.CacheStatus{Strategy: "Local", Ready: true},
				},
			},
			wantOK: true,
		},
		{
			name: "hf source is eligible",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://black-forest-labs/FLUX.1-dev",
				},
			},
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := DirectRuntimeLoadEligibility(tt.model)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}

func TestBuildLoadPayloadForModelUsesRuntimeLocalCacheForPVCSources(t *testing.T) {
	b, ok := backend.Get("diffusers")
	require.True(t, ok)

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gonzalomo-fluxpony-imagegen",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "diffusers",
			Source:  "pvc://gonzalomo-fluxpony-source/gonzalomoXLFluxPony_v30FluxDAIO.safetensors",
			Cache:   &aiv1alpha2.CacheSpec{Strategy: "Local"},
		},
		Status: aiv1alpha2.ModelStatus{
			Cache: &aiv1alpha2.CacheStatus{Strategy: "Local", Ready: true},
		},
	}

	data, err := BuildLoadPayloadForModel(model, b, BuildLoadOptions{
		ModelBasePath: "/models",
		GPUVendor:     backend.GPUVendorAMD,
	})
	require.NoError(t, err)

	var payload LoadPayload
	require.NoError(t, json.Unmarshal(data, &payload))
	assert.Equal(t, "/models/flexinfer-system/gonzalomo-fluxpony-imagegen/gonzalomoXLFluxPony_v30FluxDAIO.safetensors", payload.ModelPath)
}
