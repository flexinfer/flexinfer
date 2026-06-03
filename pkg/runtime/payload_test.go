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

func TestBuildLoadPayloadForModelAutoEnablesCompilationCacheForSharedAMDModels(t *testing.T) {
	b, ok := backend.Get("vllm")
	require.True(t, ok)

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gemma4-e4b",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://google/gemma-4-E4B-it",
			GPU: &aiv1alpha2.GPUSpec{
				Vendor: aiv1alpha2.GPUVendorAMD,
				Shared: "7900xtx-textgen",
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
	assert.Contains(t, payload.Env, EnvVar{
		Name:  "VLLM_CACHE_ROOT",
		Value: "/var/lib/flexinfer/compile-cache/flexinfer-system/gemma4-e4b/vllm",
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

func TestBuildLoadPayloadForModelUsesGPUProfileVLLMDefaults(t *testing.T) {
	b, ok := backend.Get("vllm")
	require.True(t, ok)

	enforceEager := true
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vega-vllm",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://Qwen/Qwen3-1.7B",
		},
	}

	data, err := BuildLoadPayloadForModel(model, b, BuildLoadOptions{
		ModelBasePath: "/models",
		GPUVendor:     backend.GPUVendorAMD,
		GPUProfile: &aiv1alpha2.GPUProfileSpec{
			Backends: map[string]aiv1alpha2.BackendProfile{
				"vllm": {
					VLLM: &aiv1alpha2.VLLMCapabilities{
						Defaults: &aiv1alpha2.VLLMDefaults{
							EnforceEager: &enforceEager,
							KVCacheDtype: "auto",
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	var payload LoadPayload
	require.NoError(t, json.Unmarshal(data, &payload))
	assert.Equal(t, true, payload.Config["enforceEager"])
	assert.Equal(t, "auto", payload.Config["kvCacheDtype"])
}

func TestBuildLoadPayloadForModelPreservesExplicitVLLMConfig(t *testing.T) {
	b, ok := backend.Get("vllm")
	require.True(t, ok)

	enforceEager := true
	rawConfig := []byte(`{"enforceEager":false,"kvCacheDtype":"fp8_e4m3"}`)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rdna-vllm",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://Qwen/Qwen3-14B",
			Config:  &apiextensionsv1.JSON{Raw: rawConfig},
		},
	}

	data, err := BuildLoadPayloadForModel(model, b, BuildLoadOptions{
		ModelBasePath: "/models",
		GPUVendor:     backend.GPUVendorAMD,
		GPUProfile: &aiv1alpha2.GPUProfileSpec{
			Backends: map[string]aiv1alpha2.BackendProfile{
				"vllm": {
					VLLM: &aiv1alpha2.VLLMCapabilities{
						Defaults: &aiv1alpha2.VLLMDefaults{
							EnforceEager: &enforceEager,
							KVCacheDtype: "auto",
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	var payload LoadPayload
	require.NoError(t, json.Unmarshal(data, &payload))
	assert.Equal(t, false, payload.Config["enforceEager"])
	assert.Equal(t, "fp8_e4m3", payload.Config["kvCacheDtype"])
}

func TestBuildLoadPayloadForModelAddsRuntimeBackendPortForAPI8080Backends(t *testing.T) {
	b, ok := backend.Get("llamacpp")
	require.True(t, ok)

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen3-tools",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://unsloth/Qwen3-1.7B-GGUF",
		},
	}

	data, err := BuildLoadPayloadForModel(model, b, BuildLoadOptions{
		ModelBasePath: "/models",
		GPUVendor:     backend.GPUVendorAMD,
	})
	require.NoError(t, err)

	var payload LoadPayload
	require.NoError(t, json.Unmarshal(data, &payload))
	assert.Equal(t, float64(RuntimeBackendPort), payload.Config["port"])
}

func TestBuildLoadPayloadForModelPreservesExplicitRuntimeBackendPort(t *testing.T) {
	b, ok := backend.Get("llamacpp")
	require.True(t, ok)

	rawConfig := []byte(`{"port":18080}`)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen3-tools",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://unsloth/Qwen3-1.7B-GGUF",
			Config:  &apiextensionsv1.JSON{Raw: rawConfig},
		},
	}

	data, err := BuildLoadPayloadForModel(model, b, BuildLoadOptions{
		ModelBasePath: "/models",
		GPUVendor:     backend.GPUVendorAMD,
	})
	require.NoError(t, err)

	var payload LoadPayload
	require.NoError(t, json.Unmarshal(data, &payload))
	assert.Equal(t, float64(18080), payload.Config["port"])
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
		backend    string
		profile    *aiv1alpha2.GPUProfileSpec
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
			name:    "backend not bundled in selected runtime profile is not eligible",
			backend: "vllm",
			profile: &aiv1alpha2.GPUProfileSpec{
				Architecture: "gfx906",
				Runtime: &aiv1alpha2.RuntimeProfile{
					Image:           "registry.harbor.lan/flexinfer/runtime:rocm-gfx906",
					BundledBackends: []string{"llamacpp", "ollama", "diffusers"},
				},
			},
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://Qwen/Qwen3-1.7B",
				},
			},
			wantOK:     false,
			wantReason: `runtime profile for gfx906 does not bundle backend "vllm"; use the dedicated backend image`,
		},
		{
			name:    "backend bundled in selected runtime profile is eligible",
			backend: "llamacpp",
			profile: &aiv1alpha2.GPUProfileSpec{
				Architecture: "gfx906",
				Runtime: &aiv1alpha2.RuntimeProfile{
					Image:           "registry.harbor.lan/flexinfer/runtime:rocm-gfx906",
					BundledBackends: []string{"llamacpp", "ollama", "diffusers"},
				},
			},
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://rippertnt/Qwen3-1.7B-Q4_K_M-GGUF",
				},
			},
			wantOK: true,
		},
		{
			name:    "profile without bundled backend metadata preserves legacy behavior",
			backend: "vllm",
			profile: &aiv1alpha2.GPUProfileSpec{
				Architecture: "gfx1100",
				Runtime: &aiv1alpha2.RuntimeProfile{
					Image: "registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-serving",
				},
			},
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://Qwen/Qwen3-14B",
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
		{
			name: "hf source with local cache but not ready is not eligible",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://stablediffusionapi/gonzalomoxlfluxpony-v30unitydmd",
					Cache:  &aiv1alpha2.CacheSpec{Strategy: "Local"},
				},
			},
			wantOK:     false,
			wantReason: "staged Local cache is not ready",
		},
		{
			// services/flexinfer#62: a CPU-only model must not occupy the
			// single-slot GPU runtime; it gets a dedicated Deployment instead so
			// it can run concurrently with the node's GPU runtime.
			name:    "cpu-only model is not runtime-eligible (uses dedicated Deployment)",
			backend: "llamacpp",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://rippertnt/Qwen3-1.7B-Q4_K_M-GGUF",
					GPU:    &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorCPU},
				},
			},
			wantOK:     false,
			wantReason: "cpu-only models use a dedicated Deployment, not the single-slot GPU runtime",
		},
		{
			// A GPU (amd) llamacpp model on the same source stays runtime-eligible —
			// the CPU exclusion must not regress GPU models.
			name:    "amd gpu model stays runtime-eligible",
			backend: "llamacpp",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://rippertnt/Qwen3-1.7B-Q4_K_M-GGUF",
					GPU:    &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD},
				},
			},
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := DirectRuntimeLoadEligibility(tt.model, tt.backend, tt.profile)
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

func TestBuildLoadPayloadForModelUsesRuntimeLocalCacheForHFSources(t *testing.T) {
	b, ok := backend.Get("diffusers")
	require.True(t, ok)

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gonzalomo-fluxpony-imagegen",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "diffusers",
			Source:  "HF://stablediffusionapi/gonzalomoxlfluxpony-v30unitydmd",
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
	assert.Equal(t, "/models/flexinfer-system/gonzalomo-fluxpony-imagegen", payload.ModelPath)
}

func TestBuildLoadPayloadForModelAppendsGGUFFileForRuntimeLocalCache(t *testing.T) {
	b, ok := backend.Get("llamacpp")
	require.True(t, ok)

	rawConfig := []byte(`{"ggufFile":"qwen3-1.7b-q4_k_m.gguf"}`)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen3-1p7b-tools-radeonvii",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://rippertnt/Qwen3-1.7B-Q4_K_M-GGUF",
			Config:  &apiextensionsv1.JSON{Raw: rawConfig},
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
	assert.Equal(t, "/models/flexinfer-system/qwen3-1p7b-tools-radeonvii/qwen3-1.7b-q4_k_m.gguf", payload.ModelPath)
}

func TestBuildLoadPayloadForModelPreservesFileSourcePath(t *testing.T) {
	b, ok := backend.Get("llamacpp")
	require.True(t, ok)

	const modelPath = "/models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf"
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen3-8b-radeonvii-soak",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "file://" + modelPath,
			Cache:   &aiv1alpha2.CacheSpec{Strategy: "None"},
		},
		Status: aiv1alpha2.ModelStatus{
			Cache: &aiv1alpha2.CacheStatus{Strategy: "None", Ready: true},
		},
	}

	data, err := BuildLoadPayloadForModel(model, b, BuildLoadOptions{
		ModelBasePath: "/models",
		GPUVendor:     backend.GPUVendorAMD,
	})
	require.NoError(t, err)

	var payload LoadPayload
	require.NoError(t, json.Unmarshal(data, &payload))
	assert.Equal(t, modelPath, payload.ModelPath)
	assert.Equal(t, modelPath, payload.Model)
}

func TestBuildLoadPayloadForModelRewritesLocalCacheAbsoluteVAEPath(t *testing.T) {
	b, ok := backend.Get("diffusers")
	require.True(t, ok)

	rawConfig := []byte(`{"vaeRepo":"madebyollin/sdxl-vae-fp16-fix","vaePath":"/models/.vae/sdxl-vae-fp16-fix"}`)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gonzalomo-fluxpony-imagegen",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "diffusers",
			Source:  "HF://stablediffusionapi/gonzalomoxlfluxpony-v30unitydmd",
			Config:  &apiextensionsv1.JSON{Raw: rawConfig},
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
	assert.Equal(t, "/models/flexinfer-system/gonzalomo-fluxpony-imagegen/.vae/sdxl-vae-fp16-fix", payload.Config["vaePath"])
}

func TestBuildLoadPayloadForModelResolvesRelativeVAEPathAgainstModelRoot(t *testing.T) {
	b, ok := backend.Get("diffusers")
	require.True(t, ok)

	rawConfig := []byte(`{"vaeRepo":"madebyollin/sdxl-vae-fp16-fix","vaePath":".vae/sdxl-vae-fp16-fix"}`)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gonzalomo-fluxpony-imagegen",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "diffusers",
			Source:  "HF://stablediffusionapi/gonzalomoxlfluxpony-v30unitydmd",
			Config:  &apiextensionsv1.JSON{Raw: rawConfig},
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
	assert.Equal(t, "/models/flexinfer-system/gonzalomo-fluxpony-imagegen/.vae/sdxl-vae-fp16-fix", payload.Config["vaePath"])
}
