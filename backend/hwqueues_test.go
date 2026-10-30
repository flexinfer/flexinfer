package backend

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestHWQueuesPerHIPProcess(t *testing.T) {
	cases := map[string]int32{
		"":    6, // ROCclr default of 4 kernel queues + 2
		"1":   3, // the gfx906 cap, measured on the live lanes
		"2":   4,
		" 1 ": 3,
		"0":   6, // not a valid cap; ROCclr keeps its default
		"-3":  6,
		"abc": 6,
	}
	for value, want := range cases {
		assert.Equal(t, want, HWQueuesPerHIPProcess(value), "GPU_MAX_HW_QUEUES=%q", value)
	}
}

func TestDefaultHWQueueEstimate(t *testing.T) {
	capped := []corev1.EnvVar{{Name: "HSA_ENABLE_SDMA", Value: "0"}, {Name: EnvGPUMaxHWQueues, Value: "1"}}
	cases := []struct {
		backend string
		env     []corev1.EnvVar
		want    int32
	}{
		{NameLlamaCpp, capped, 3},
		{NameLlamaCpp, nil, 6},
		{NameOllama, capped, 3},
		{NameDiffusers, capped, 3},
		{NameSNAC, capped, 3},
		{NameVLLM, capped, 6},
		{NameVLLM, nil, 12},
		{NameVLLMOmni, capped, 6},
		{"unknown-backend", capped, 3},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, DefaultHWQueueEstimate(tc.backend, tc.env), "%s env=%v", tc.backend, tc.env)
	}

	// The last entry wins, as when the env list is rendered.
	overridden := append(append([]corev1.EnvVar(nil), capped...), corev1.EnvVar{Name: EnvGPUMaxHWQueues, Value: "4"})
	assert.Equal(t, int32(6), DefaultHWQueueEstimate(NameLlamaCpp, overridden))
}

func TestModelHWQueueEstimate(t *testing.T) {
	profile := &aiv1alpha2.GPUProfileSpec{
		Architecture: "gfx906",
		Vendor:       "amd",
		Env:          []corev1.EnvVar{{Name: EnvGPUMaxHWQueues, Value: "1"}},
	}
	model := func(backendName string) *aiv1alpha2.Model {
		return &aiv1alpha2.Model{Spec: aiv1alpha2.ModelSpec{Backend: backendName, GPU: &aiv1alpha2.GPUSpec{}}}
	}

	assert.Equal(t, int32(3), ModelHWQueueEstimate(model(NameLlamaCpp), profile, GPUVendorAMD, "gfx906"))
	assert.Equal(t, int32(6), ModelHWQueueEstimate(model(NameVLLM), profile, GPUVendorAMD, "gfx906"))

	// spec.config.env overrides the profile env, as on both launch paths.
	uncapped := model(NameLlamaCpp)
	uncapped.Spec.Config = &apiextensionsv1.JSON{Raw: []byte(`{"env":{"GPU_MAX_HW_QUEUES":"2"}}`)}
	assert.Equal(t, int32(4), ModelHWQueueEstimate(uncapped, profile, GPUVendorAMD, "gfx906"))

	// An explicit estimate wins, including zero.
	explicit := model(NameVLLM)
	five, zero := int32(5), int32(0)
	explicit.Spec.GPU.HWQueueEstimate = &five
	assert.Equal(t, int32(5), ModelHWQueueEstimate(explicit, profile, GPUVendorAMD, "gfx906"))
	explicit.Spec.GPU.HWQueueEstimate = &zero
	assert.Equal(t, int32(0), ModelHWQueueEstimate(explicit, profile, GPUVendorAMD, "gfx906"))

	// Without a profile the in-code gfx906 env applies; it sets no cap.
	assert.Equal(t, int32(6), ModelHWQueueEstimate(model(NameLlamaCpp), nil, GPUVendorAMD, "gfx906"))
	assert.Equal(t, int32(0), ModelHWQueueEstimate(nil, profile, GPUVendorAMD, "gfx906"))
}

func TestHWQueueBudget(t *testing.T) {
	_, _, ok := HWQueueBudget(nil)
	assert.False(t, ok)
	_, _, ok = HWQueueBudget(&aiv1alpha2.GPUProfileSpec{ReservedHWQueues: 2})
	assert.False(t, ok, "a reserve without maxHWQueues does not enable admission")

	maxQueues, reserved, ok := HWQueueBudget(&aiv1alpha2.GPUProfileSpec{MaxHWQueues: 24, ReservedHWQueues: 2})
	assert.True(t, ok)
	assert.Equal(t, int32(24), maxQueues)
	assert.Equal(t, int32(2), reserved)
}
