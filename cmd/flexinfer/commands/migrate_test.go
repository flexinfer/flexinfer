package commands

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func int32Ptr(i int32) *int32 { return &i }
func int64Ptr(i int64) *int64 { return &i }
func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

func TestInferSource(t *testing.T) {
	tests := []struct {
		name     string
		backend  string
		model    string
		expected string
	}{
		{"ollama model", "ollama", "llama3:8b", "ollama://llama3:8b"},
		{"vllm HF model", "vllm", "Qwen/Qwen2.5-7B-Instruct", "HF://Qwen/Qwen2.5-7B-Instruct"},
		{"vllm bare model", "vllm", "my-model", "HF://my-model"},
		{"vllm-omni HF model", "vllm-omni", "Tongyi-MAI/Z-Image-Turbo", "HF://Tongyi-MAI/Z-Image-Turbo"},
		{"llamacpp HF model", "llamacpp", "TheBloke/Llama-2-7B-GGUF", "HF://TheBloke/Llama-2-7B-GGUF"},
		{"llamacpp local file", "llamacpp", "/models/model.gguf", "file:///models/model.gguf"},
		{"llamacpp bare", "llamacpp", "model.gguf", "model.gguf"},
		{"diffusers HF model", "diffusers", "black-forest-labs/FLUX.1-schnell", "HF://black-forest-labs/FLUX.1-schnell"},
		{"diffusers bare", "diffusers", "my-model", "my-model"},
		{"unknown backend", "custom", "something", "something"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := InferSource(tc.backend, tc.model)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestBuildGPUSpec(t *testing.T) {
	tests := []struct {
		name     string
		md       *aiv1alpha1.ModelDeployment
		wantNil  bool
		wantSpec func(t *testing.T, md *aiv1alpha1.ModelDeployment)
	}{
		{
			name: "no GPU fields",
			md: &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{Backend: "ollama", Model: "test"},
			},
			wantNil: true,
		},
		{
			name: "GPUGroup ref only",
			md: &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend:     "ollama",
					Model:       "test",
					GPUGroupRef: strPtr("my-group"),
				},
			},
			wantSpec: func(t *testing.T, md *aiv1alpha1.ModelDeployment) {
				gpu := BuildGPUSpec(md)
				require.NotNil(t, gpu)
				assert.Equal(t, "my-group", gpu.Shared)
			},
		},
		{
			name: "all GPU fields",
			md: &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend:        "vllm",
					Model:          "test",
					GPUGroupRef:    strPtr("gpu-share"),
					Priority:       int32Ptr(200),
					VRAMEstimateMB: int64Ptr(8000),
				},
			},
			wantSpec: func(t *testing.T, md *aiv1alpha1.ModelDeployment) {
				gpu := BuildGPUSpec(md)
				require.NotNil(t, gpu)
				assert.Equal(t, "gpu-share", gpu.Shared)
				assert.Equal(t, int32(200), *gpu.Priority)
				assert.Equal(t, int64(8000), *gpu.VRAMEstimateMB)
			},
		},
		{
			name: "empty GPUGroupRef ignored",
			md: &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend:     "ollama",
					Model:       "test",
					GPUGroupRef: strPtr(""),
				},
			},
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gpu := BuildGPUSpec(tc.md)
			if tc.wantNil {
				assert.Nil(t, gpu)
			} else if tc.wantSpec != nil {
				tc.wantSpec(t, tc.md)
			}
		})
	}
}

func TestBuildServerlessSpec(t *testing.T) {
	tests := []struct {
		name     string
		md       *aiv1alpha1.ModelDeployment
		wantNil  bool
		wantSpec func(t *testing.T, md *aiv1alpha1.ModelDeployment)
	}{
		{
			name: "no serverless fields",
			md: &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{Backend: "ollama", Model: "test"},
			},
			wantNil: true,
		},
		{
			name: "all serverless fields",
			md: &aiv1alpha1.ModelDeployment{
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend:                 "ollama",
					Model:                   "test",
					MinReplicas:             int32Ptr(0),
					IdleTimeoutSeconds:      int32Ptr(300),
					ColdStartTimeoutSeconds: int32Ptr(120),
				},
			},
			wantSpec: func(t *testing.T, md *aiv1alpha1.ModelDeployment) {
				s := BuildServerlessSpec(md)
				require.NotNil(t, s)
				assert.Equal(t, int32(0), *s.MinReplicas)
				assert.Equal(t, 300*time.Second, s.IdleTimeout.Duration)
				assert.Equal(t, 120*time.Second, s.ColdStartTimeout.Duration)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := BuildServerlessSpec(tc.md)
			if tc.wantNil {
				assert.Nil(t, s)
			} else if tc.wantSpec != nil {
				tc.wantSpec(t, tc.md)
			}
		})
	}
}

func TestBuildConfigMap_VLLM(t *testing.T) {
	md := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "vllm",
			Model:   "Qwen/Qwen2.5-7B-Instruct",
			VLLM: &aiv1alpha1.VLLMSpec{
				MaxModelLen:          int32Ptr(16384),
				GPUMemoryUtilization: strPtr("0.95"),
				Quantization:         "gptq",
				MaxNumSeqs:           int32Ptr(8),
				AttentionBackend:     "TORCH_SDPA",
			},
		},
	}

	cfg := BuildConfigMap(md)
	require.NotNil(t, cfg)
	assert.Equal(t, int32(16384), cfg["maxModelLen"])
	assert.Equal(t, "0.95", cfg["gpuMemoryUtilization"])
	assert.Equal(t, "gptq", cfg["quantization"])
	assert.Equal(t, int32(8), cfg["maxNumSeqs"])
	assert.Equal(t, "TORCH_SDPA", cfg["attentionBackend"])
}

func TestBuildConfigMap_LlamaCpp(t *testing.T) {
	md := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "llamacpp",
			Model:   "test",
			LlamaCpp: &aiv1alpha1.LlamaCppSpec{
				ContextSize:    int32Ptr(4096),
				NGPULayers:     int32Ptr(999),
				FlashAttention: boolPtr(true),
			},
		},
	}

	cfg := BuildConfigMap(md)
	require.NotNil(t, cfg)
	assert.Equal(t, int32(4096), cfg["contextSize"])
	assert.Equal(t, int32(999), cfg["nGPULayers"])
	assert.Equal(t, true, cfg["flashAttention"])
}

func TestBuildConfigMap_NoBackendSpec(t *testing.T) {
	md := &aiv1alpha1.ModelDeployment{
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "ollama",
			Model:   "llama3:8b",
		},
	}

	cfg := BuildConfigMap(md)
	assert.Nil(t, cfg)
}

func TestConvertModelDeploymentToModel(t *testing.T) {
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen3-14b-gptq",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:                 "vllm",
			Model:                   "JunHowie/Qwen3-14B-GPTQ-Int4",
			MinReplicas:             int32Ptr(0),
			IdleTimeoutSeconds:      int32Ptr(300),
			ColdStartTimeoutSeconds: int32Ptr(120),
			GPUGroupRef:             strPtr("7900xtx-models"),
			Priority:                int32Ptr(200),
			VRAMEstimateMB:          int64Ptr(10240),
			ModelCacheRef:           strPtr("qwen3-cache"),
			VLLM: &aiv1alpha1.VLLMSpec{
				MaxModelLen:          int32Ptr(16384),
				GPUMemoryUtilization: strPtr("0.95"),
				MaxNumSeqs:           int32Ptr(8),
			},
			LiteLLM: &aiv1alpha1.LiteLLMSpec{
				Enabled:         boolPtr(true),
				ServedModelName: "qwen3-14b",
				Aliases:         []string{"qwen3", "qwen"},
				CopilotAlias:    "copilot-qwen",
			},
			ServiceLabels: []string{"textgen", "code"},
			NodeSelector:  map[string]string{"kubernetes.io/hostname": "cblevins-7900xtx"},
		},
	}

	model, err := ConvertModelDeploymentToModel(md)
	require.NoError(t, err)
	require.NotNil(t, model)

	// TypeMeta
	assert.Equal(t, "ai.flexinfer/v1alpha2", model.TypeMeta.APIVersion)
	assert.Equal(t, "Model", model.TypeMeta.Kind)

	// ObjectMeta
	assert.Equal(t, "qwen3-14b-gptq", model.Name)
	assert.Equal(t, "flexinfer-system", model.Namespace)

	// Backend and source
	assert.Equal(t, "vllm", model.Spec.Backend)
	assert.Equal(t, "HF://JunHowie/Qwen3-14B-GPTQ-Int4", model.Spec.Source)

	// GPU spec
	require.NotNil(t, model.Spec.GPU)
	assert.Equal(t, "7900xtx-models", model.Spec.GPU.Shared)
	assert.Equal(t, int32(200), *model.Spec.GPU.Priority)
	assert.Equal(t, int64(10240), *model.Spec.GPU.VRAMEstimateMB)

	// Serverless spec
	require.NotNil(t, model.Spec.Serverless)
	assert.Equal(t, int32(0), *model.Spec.Serverless.MinReplicas)
	assert.Equal(t, 300*time.Second, model.Spec.Serverless.IdleTimeout.Duration)
	assert.Equal(t, 120*time.Second, model.Spec.Serverless.ColdStartTimeout.Duration)

	// Cache spec
	require.NotNil(t, model.Spec.Cache)
	assert.Equal(t, "qwen3-cache", model.Spec.Cache.PVCName)

	// Config (vLLM)
	require.NotNil(t, model.Spec.Config)

	// LiteLLM
	require.NotNil(t, model.Spec.LiteLLM)
	assert.Equal(t, true, *model.Spec.LiteLLM.Enabled)
	assert.Equal(t, "qwen3-14b", model.Spec.LiteLLM.ServedModelName)
	assert.Equal(t, []string{"qwen3", "qwen"}, model.Spec.LiteLLM.Aliases)
	assert.Equal(t, "copilot-qwen", model.Spec.LiteLLM.CopilotAlias)

	// Service labels
	assert.Equal(t, []string{"textgen", "code"}, model.Spec.ServiceLabels)

	// Node selector
	assert.Equal(t, "cblevins-7900xtx", model.Spec.NodeSelector["kubernetes.io/hostname"])
}

func TestConvertModelDeploymentToModel_Minimal(t *testing.T) {
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "ollama",
			Model:   "llama3:8b",
		},
	}

	model, err := ConvertModelDeploymentToModel(md)
	require.NoError(t, err)
	require.NotNil(t, model)

	assert.Equal(t, "ollama", model.Spec.Backend)
	assert.Equal(t, "ollama://llama3:8b", model.Spec.Source)
	assert.Nil(t, model.Spec.GPU)
	assert.Nil(t, model.Spec.Serverless)
	assert.Nil(t, model.Spec.Cache)
	assert.Nil(t, model.Spec.Config)
	assert.Nil(t, model.Spec.LiteLLM)
}
