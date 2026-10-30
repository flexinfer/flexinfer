package runtime

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

func TestNewLoadPayloadForModelCarriesHWQueueBudget(t *testing.T) {
	b, ok := backend.Get(backend.NameLlamaCpp)
	require.True(t, ok)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "bge-large-radeonvii", Namespace: "flexinfer-system", Generation: 3},
		Spec: aiv1alpha2.ModelSpec{
			Backend: backend.NameLlamaCpp,
			Source:  "HF://CompendiumLabs/bge-large-en-v1.5-gguf",
			GPU:     &aiv1alpha2.GPUSpec{Shared: "radeonvii-models"},
		},
	}
	profile := func(maxQueues, reserved int32) *aiv1alpha2.GPUProfileSpec {
		return &aiv1alpha2.GPUProfileSpec{
			Architecture:     "gfx906",
			Vendor:           "amd",
			Env:              []corev1.EnvVar{{Name: backend.EnvGPUMaxHWQueues, Value: "1"}},
			MaxHWQueues:      maxQueues,
			ReservedHWQueues: reserved,
		}
	}
	build := func(p *aiv1alpha2.GPUProfileSpec) *LoadPayload {
		payload, err := NewLoadPayloadForModel(model, b, BuildLoadOptions{ModelBasePath: "/models", GPUVendor: backend.GPUVendorAMD, GPUProfile: p})
		require.NoError(t, err)
		return payload
	}

	budgeted := build(profile(24, 2))
	require.NotNil(t, budgeted.HWQueues)
	assert.Equal(t, HWQueueBudget{Max: 24, Reserved: 2}, *budgeted.HWQueues)
	assert.NotContains(t, budgeted.Config, HWQueueEstimateConfigKey, "no explicit estimate: the runtime derives it")

	// Admission metadata, not launch configuration: adding or editing the
	// budget leaves the launch hash alone, so no loaded lane reads it as a
	// spec change.
	plain := build(profile(0, 0))
	assert.Nil(t, plain.HWQueues)
	assert.Equal(t, plain.SpecHash, budgeted.SpecHash)
	assert.Equal(t, plain.SpecHash, build(profile(20, 4)).SpecHash)

	data, err := budgeted.Marshal()
	require.NoError(t, err)
	var decoded LoadPayload
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NotNil(t, decoded.HWQueues)
	assert.Equal(t, int32(24), decoded.HWQueues.Max)

	// An explicit Model estimate rides in the config, like vramEstimateMB.
	five := int32(5)
	model.Spec.GPU.HWQueueEstimate = &five
	explicit := build(profile(24, 2))
	assert.EqualValues(t, 5, explicit.Config[HWQueueEstimateConfigKey])
	assert.NotContains(t, model.Spec.GetConfigMap(), HWQueueEstimateConfigKey)
}
