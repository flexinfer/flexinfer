package proxy

import (
	"testing"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------------------------------------------------------------------------
// modelToOpenAI (v1alpha2 Model -> OpenAIModel)
// ---------------------------------------------------------------------------

func TestModelToOpenAI_FullMetadata(t *testing.T) {
	p := setupTestProxy(t)

	priority := int32(200)
	ts := metav1.NewTime(time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC))

	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "qwen3-14b-gptq",
			Namespace:         "default",
			CreationTimestamp: ts,
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/qwen3-14b-gptq",
			GPU: &aiv1alpha2.GPUSpec{
				Shared:   "gpu-group-a",
				Priority: &priority,
			},
			LiteLLM: &aiv1alpha2.LiteLLMSpec{
				ServedModelName: "qwen3-14b",
				Aliases:         []string{"qwen3", "chat-fast"},
			},
			ServiceLabels: []string{"textgen", "chat"},
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseReady,
		},
	}

	result := p.modelToOpenAI(m)

	assert.Equal(t, "qwen3-14b-gptq", result.ID)
	assert.Equal(t, "model", result.Object)
	assert.Equal(t, ts.Unix(), result.Created)
	assert.Equal(t, "flexinfer", result.OwnedBy)

	// Metadata fields
	assert.Equal(t, "vllm", result.Metadata["backend"])
	assert.Equal(t, "HF://org/qwen3-14b-gptq", result.Metadata["source"])
	assert.Equal(t, true, result.Metadata["ready"])
	assert.Equal(t, "v1alpha2", result.Metadata["version"])
	assert.Equal(t, "Ready", result.Metadata["phase"])
	assert.Equal(t, "gpu-group-a", result.Metadata["gpu_shared"])

	// gpu_priority is stored as json.Number or int when going through the map
	// but modelToOpenAI stores the raw int32 dereferenced value.
	assert.Equal(t, int32(200), result.Metadata["gpu_priority"])

	assert.Equal(t, "qwen3-14b", result.Metadata["served_model_name"])
	assert.Equal(t, []string{"qwen3", "chat-fast"}, result.Metadata["aliases"])
	assert.Equal(t, []string{"textgen", "chat"}, result.Metadata["service_labels"])
}

func TestModelToOpenAI_MinimalModel(t *testing.T) {
	p := setupTestProxy(t)

	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "bare-model",
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(time.Unix(1000000, 0)),
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "ollama",
			Source:  "ollama://llama3",
		},
	}

	result := p.modelToOpenAI(m)

	assert.Equal(t, "bare-model", result.ID)
	assert.Equal(t, "model", result.Object)
	assert.Equal(t, int64(1000000), result.Created)
	assert.Equal(t, "flexinfer", result.OwnedBy)

	assert.Equal(t, "ollama", result.Metadata["backend"])
	assert.Equal(t, "ollama://llama3", result.Metadata["source"])
	assert.Equal(t, false, result.Metadata["ready"])
	assert.Equal(t, "v1alpha2", result.Metadata["version"])

	// Optional fields should be absent
	assert.Nil(t, result.Metadata["phase"])        // empty Phase => not set
	assert.Nil(t, result.Metadata["gpu_shared"])   // no GPU spec
	assert.Nil(t, result.Metadata["gpu_priority"]) // no GPU spec
	assert.Nil(t, result.Metadata["served_model_name"])
	assert.Nil(t, result.Metadata["aliases"])
	assert.Nil(t, result.Metadata["service_labels"])
}

func TestModelToOpenAI_ReadyPhase(t *testing.T) {
	p := setupTestProxy(t)

	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ready-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseReady,
		},
	}

	result := p.modelToOpenAI(m)
	assert.Equal(t, true, result.Metadata["ready"])
	assert.Equal(t, "Ready", result.Metadata["phase"])
}

func TestModelToOpenAI_NotReady(t *testing.T) {
	p := setupTestProxy(t)

	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pending-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhasePending,
		},
	}

	result := p.modelToOpenAI(m)
	assert.Equal(t, false, result.Metadata["ready"])
	assert.Equal(t, "Pending", result.Metadata["phase"])
}

// ---------------------------------------------------------------------------
// modelDeploymentToOpenAI (v1alpha1 ModelDeployment -> OpenAIModel)
// ---------------------------------------------------------------------------

func TestModelDeploymentToOpenAI_Deprecated(t *testing.T) {
	p := setupTestProxy(t)

	one := int32(1)
	gpuGroup := "my-gpu-group"
	ts := metav1.NewTime(time.Date(2024, 12, 1, 8, 0, 0, 0, time.UTC))

	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "legacy-model",
			Namespace:         "default",
			CreationTimestamp: ts,
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:     "mlc-llm",
			Replicas:    &one,
			GPUGroupRef: &gpuGroup,
		},
		Status: aiv1alpha1.ModelDeploymentStatus{
			Phase: aiv1alpha1.ModelDeploymentPhaseRunning,
			Conditions: []metav1.Condition{
				{
					Type:   aiv1alpha1.ConditionTypeReady,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	result := p.modelDeploymentToOpenAI(md)

	assert.Equal(t, "legacy-model", result.ID)
	assert.Equal(t, "model", result.Object)
	assert.Equal(t, ts.Unix(), result.Created)
	assert.Equal(t, "flexinfer", result.OwnedBy)

	// v1alpha1-specific metadata
	assert.Equal(t, "mlc-llm", result.Metadata["backend"])
	assert.Equal(t, true, result.Metadata["ready"])
	assert.Equal(t, true, result.Metadata["scaled"])
	assert.Equal(t, "v1alpha1", result.Metadata["version"])
	assert.Equal(t, true, result.Metadata["deprecated"])
	assert.Equal(t, "Running", result.Metadata["phase"])
	assert.Equal(t, "my-gpu-group", result.Metadata["gpu_group"])
}

func TestModelDeploymentToOpenAI_WithAliases(t *testing.T) {
	p := setupTestProxy(t)

	one := int32(1)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aliased-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:  "vllm",
			Replicas: &one,
			LiteLLM: &aiv1alpha1.LiteLLMSpec{
				ServedModelName: "my-served-name",
				Aliases:         []string{"alias-a", "alias-b"},
			},
			ServiceLabels: []string{"textgen", "code"},
		},
		Status: aiv1alpha1.ModelDeploymentStatus{
			Phase: aiv1alpha1.ModelDeploymentPhaseRunning,
			Conditions: []metav1.Condition{
				{
					Type:   aiv1alpha1.ConditionTypeReady,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	result := p.modelDeploymentToOpenAI(md)

	assert.Equal(t, "aliased-model", result.ID)
	assert.Equal(t, true, result.Metadata["deprecated"])
	assert.Equal(t, "v1alpha1", result.Metadata["version"])
	assert.Equal(t, "my-served-name", result.Metadata["served_model_name"])
	assert.Equal(t, []string{"alias-a", "alias-b"}, result.Metadata["aliases"])
	assert.Equal(t, []string{"textgen", "code"}, result.Metadata["service_labels"])
}

func TestModelDeploymentToOpenAI_ScaledToZero(t *testing.T) {
	p := setupTestProxy(t)

	zero := int32(0)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "idle-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:  "ollama",
			Replicas: &zero,
		},
		Status: aiv1alpha1.ModelDeploymentStatus{
			Phase: aiv1alpha1.ModelDeploymentPhaseIdle,
		},
	}

	result := p.modelDeploymentToOpenAI(md)

	assert.Equal(t, false, result.Metadata["ready"])
	assert.Equal(t, false, result.Metadata["scaled"])
	assert.Equal(t, "Idle", result.Metadata["phase"])
	assert.Equal(t, true, result.Metadata["deprecated"])
}

func TestModelDeploymentToOpenAI_NilReplicas(t *testing.T) {
	p := setupTestProxy(t)

	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nil-replicas",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "vllm",
			// Replicas intentionally nil
		},
	}

	result := p.modelDeploymentToOpenAI(md)

	// nil replicas treated as 0 -> scaled=false
	assert.Equal(t, false, result.Metadata["scaled"])
}

func TestModelDeploymentToOpenAI_NilGPUGroupRef(t *testing.T) {
	p := setupTestProxy(t)

	one := int32(1)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "no-gpu-group",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:  "ollama",
			Replicas: &one,
			// GPUGroupRef intentionally nil
		},
	}

	result := p.modelDeploymentToOpenAI(md)

	// gpu_group should not be present
	assert.Nil(t, result.Metadata["gpu_group"])
}

func TestModelDeploymentToOpenAI_EmptyGPUGroupRef(t *testing.T) {
	p := setupTestProxy(t)

	one := int32(1)
	empty := ""
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "empty-gpu-group",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:     "ollama",
			Replicas:    &one,
			GPUGroupRef: &empty,
		},
	}

	result := p.modelDeploymentToOpenAI(md)

	// Empty GPUGroupRef should not produce a gpu_group key
	assert.Nil(t, result.Metadata["gpu_group"])
}
