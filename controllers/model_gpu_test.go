/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// fakeGPUBackend implements backend.Backend for testing GPU validation logic.

type fakeGPUBackend struct {
	backend.BaseBackend
	name  string
	image string
}

func (f *fakeGPUBackend) Name() string { return f.name }

func (f *fakeGPUBackend) Image(_ backend.GPUVendor, _ string) string {
	if f.image != "" {
		return f.image
	}
	return "registry.example.com/" + f.name + ":latest"
}

func (f *fakeGPUBackend) Port() int32 { return 8000 }

func (f *fakeGPUBackend) Args(_ *backend.ModelSpec) []string { return nil }

func (f *fakeGPUBackend) Env(_ *backend.ModelSpec) []corev1.EnvVar { return nil }

func (f *fakeGPUBackend) ReadinessProbe() *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/health",
				Port: intstr.FromInt32(8000),
			},
		},
	}
}

// helpers

func gpuInt64Ptr(v int64) *int64 { return &v }
func gpuInt32Ptr(v int32) *int32 { return &v }

// mustJSONConfig marshals a map into an apiextensionsv1.JSON pointer.
func mustJSONConfig(m map[string]any) *apiextensionsv1.JSON {
	raw, _ := json.Marshal(m)
	return &apiextensionsv1.JSON{Raw: raw}
}

// gpuTestModel creates a minimal Model with the given options applied.
func gpuTestModel(name string, opts ...func(*aiv1alpha2.Model)) *aiv1alpha2.Model {
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
		},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// gpuTestReconciler creates a ModelReconciler with a FakeEventRecorder.
func gpuTestReconciler(opts ...func(*ModelReconciler)) *ModelReconciler {
	r := &ModelReconciler{
		Recorder: &FakeEventRecorder{},
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func gpuRecorderEvents(r *ModelReconciler) []FakeEvent {
	return r.Recorder.(*FakeEventRecorder).Events
}

// readyNode creates a corev1.Node that is Ready.
func readyNode(name string, labels map[string]string, capacity corev1.ResourceList) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Status: corev1.NodeStatus{
			Capacity: capacity,
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
}

// notReadyNode creates a corev1.Node that is NotReady.
func notReadyNode(name string, labels map[string]string, capacity corev1.ResourceList) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Status: corev1.NodeStatus{
			Capacity: capacity,
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionFalse,
				},
			},
		},
	}
}

func gpuTestScheme() *k8sruntime.Scheme {
	s := k8sruntime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = aiv1alpha2.AddToScheme(s)
	return s
}

func TestValidateVRAMFit(t *testing.T) {
	// Use the "vllm" backend name so LookupGPUArchSupport finds
	// gfx1100 → MaxVRAMMB = 24576 in BackendGPUCompatibility.
	b := &fakeGPUBackend{name: "vllm"}

	tests := []struct {
		name        string
		gpu         *aiv1alpha2.GPUSpec
		gpuArch     string
		wantErr     bool
		wantWarning bool
		desc        string
	}{
		{
			name:    "nil GPU spec",
			gpu:     nil,
			gpuArch: "gfx1100",
			wantErr: false,
			desc:    "nil GPU spec skips validation",
		},
		{
			name:    "nil VRAMEstimate",
			gpu:     &aiv1alpha2.GPUSpec{},
			gpuArch: "gfx1100",
			wantErr: false,
			desc:    "nil VRAMEstimate skips validation",
		},
		{
			name:    "zero VRAMEstimate",
			gpu:     &aiv1alpha2.GPUSpec{VRAMEstimateMB: gpuInt64Ptr(0)},
			gpuArch: "gfx1100",
			wantErr: false,
			desc:    "zero VRAMEstimate skips validation",
		},
		{
			name:    "estimate well under capacity (50%)",
			gpu:     &aiv1alpha2.GPUSpec{VRAMEstimateMB: gpuInt64Ptr(12000)},
			gpuArch: "gfx1100",
			wantErr: false,
			desc:    "50% utilization passes without warning",
		},
		{
			name:        "estimate at 85% triggers warning",
			gpu:         &aiv1alpha2.GPUSpec{VRAMEstimateMB: gpuInt64Ptr(21000)},
			gpuArch:     "gfx1100",
			wantErr:     false,
			wantWarning: true,
			desc:        "85% utilization passes with VRAMPressure warning",
		},
		{
			name:    "estimate over 95% returns error",
			gpu:     &aiv1alpha2.GPUSpec{VRAMEstimateMB: gpuInt64Ptr(24000)},
			gpuArch: "gfx1100",
			wantErr: true,
			desc:    "exceeding 95% returns error",
		},
		{
			name:    "unknown arch with no matrix entry skips validation",
			gpu:     &aiv1alpha2.GPUSpec{VRAMEstimateMB: gpuInt64Ptr(99999)},
			gpuArch: "unknown-arch",
			wantErr: false,
			desc:    "unknown arch returns nil (no VRAM data to validate against)",
		},
		{
			name: "multi-GPU doubles total VRAM warning band",
			gpu: &aiv1alpha2.GPUSpec{
				VRAMEstimateMB: gpuInt64Ptr(40000),
				Count:          gpuInt32Ptr(2),
			},
			gpuArch:     "gfx1100",
			wantErr:     false,
			wantWarning: true,
			desc:        "2 GPUs give 49152 MB total, 40000 is ~81% -> warning but no error",
		},
		{
			name: "multi-GPU exceeds 95% of total",
			gpu: &aiv1alpha2.GPUSpec{
				VRAMEstimateMB: gpuInt64Ptr(47000),
				Count:          gpuInt32Ptr(2),
			},
			gpuArch: "gfx1100",
			wantErr: true,
			desc:    "47000 > 95% of 49152 -> error",
		},
		{
			name:    "negative VRAMEstimate treated as zero",
			gpu:     &aiv1alpha2.GPUSpec{VRAMEstimateMB: gpuInt64Ptr(-100)},
			gpuArch: "gfx1100",
			wantErr: false,
			desc:    "negative estimate skips validation like zero",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gpuTestReconciler()
			model := gpuTestModel("test-vram", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = tt.gpu
			})

			err := r.validateVRAMFit(model, b, tt.gpuArch)

			if tt.wantErr {
				require.Error(t, err, tt.desc)
				assert.Contains(t, err.Error(), "exceeds 95%")
			} else {
				require.NoError(t, err, tt.desc)
			}

			events := gpuRecorderEvents(r)
			if tt.wantWarning {
				require.NotEmpty(t, events, "expected a VRAMPressure warning event")
				assert.Equal(t, "VRAMPressure", events[0].Reason)
				assert.Equal(t, corev1.EventTypeWarning, events[0].EventType)
			} else if !tt.wantErr {
				assert.Empty(t, events, "expected no events for %s", tt.name)
			}
		})
	}

	t.Run("GPUProfile VRAM override takes precedence", func(t *testing.T) {
		profileR := &GPUProfileReconciler{}
		profileR.profiles.Store("custom-arch", &aiv1alpha2.GPUProfileSpec{
			VRAMMB: 8000,
		})
		r := gpuTestReconciler(func(r *ModelReconciler) {
			r.GPUProfiles = profileR
		})
		model := gpuTestModel("test-profile", func(m *aiv1alpha2.Model) {
			m.Spec.GPU = &aiv1alpha2.GPUSpec{
				VRAMEstimateMB: gpuInt64Ptr(7800), // 97.5% of 8000 -> exceeds 95%
			}
		})

		err := r.validateVRAMFit(model, b, "custom-arch")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds 95%")
	})
}

func TestValidateBackendGPUCompatibility(t *testing.T) {
	tests := []struct {
		name        string
		backendName string
		image       string
		gpuVendor   backend.GPUVendor
		gpuArch     string
		wantErr     bool
		wantWarning bool
		errContains string
		desc        string
	}{
		{
			name:        "unsupported arch returns error",
			backendName: "vllm",
			gpuVendor:   backend.GPUVendorNVIDIA,
			gpuArch:     "sm_52",
			wantErr:     true,
			errContains: "not supported",
			desc:        "vllm on Maxwell (sm_5x) is unsupported in the matrix",
		},
		{
			name:        "experimental with generic image emits warning",
			backendName: "comfyui",
			image:       "registry.example.com/comfyui:generic",
			gpuVendor:   backend.GPUVendorAMD,
			gpuArch:     "gfx906",
			wantErr:     false,
			wantWarning: true,
			desc:        "comfyui on gfx906 is experimental; generic image triggers warning",
		},
		{
			name:        "experimental with arch-specific gfx906 image suppresses warning",
			backendName: "comfyui",
			image:       "registry.example.com/comfyui:rocm-gfx906",
			gpuVendor:   backend.GPUVendorAMD,
			gpuArch:     "gfx906",
			wantErr:     false,
			wantWarning: false,
			desc:        "arch-specific image (contains gfx906) suppresses the experimental warning",
		},
		{
			name:        "experimental with gfx110 image also suppresses warning",
			backendName: "diffusers",
			image:       "registry.example.com/diffusers:rocm-gfx1100",
			gpuVendor:   backend.GPUVendorAMD,
			gpuArch:     "gfx906",
			wantErr:     false,
			wantWarning: false,
			desc:        "image containing gfx110 also suppresses the experimental warning",
		},
		{
			name:        "supported arch passes cleanly",
			backendName: "vllm",
			gpuVendor:   backend.GPUVendorAMD,
			gpuArch:     "gfx1100",
			wantErr:     false,
			wantWarning: false,
			desc:        "vllm on gfx1100 is fully supported",
		},
		{
			name:        "unknown arch not in matrix passes",
			backendName: "vllm",
			gpuVendor:   backend.GPUVendorAMD,
			gpuArch:     "gfx9999",
			wantErr:     false,
			wantWarning: false,
			desc:        "unknown arch not found in matrix is not blocked",
		},
		{
			name:        "unknown backend not in matrix passes",
			backendName: "custom-backend",
			gpuVendor:   backend.GPUVendorAMD,
			gpuArch:     "gfx1100",
			wantErr:     false,
			wantWarning: false,
			desc:        "backend with no matrix entries passes through",
		},
		{
			name:        "Maxwell NVIDIA delegates to validateMaxwellSpecifics",
			backendName: "llamacpp",
			gpuVendor:   backend.GPUVendorNVIDIA,
			gpuArch:     "sm_52",
			wantErr:     false,
			wantWarning: false,
			desc:        "llamacpp on Maxwell is supported; validateMaxwellSpecifics checks FP16",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gpuTestReconciler()
			model := gpuTestModel("compat-test", func(m *aiv1alpha2.Model) {
				m.Spec.Backend = tt.backendName
			})
			b := &fakeGPUBackend{name: tt.backendName, image: tt.image}

			err := r.validateBackendGPUCompatibility(model, b, tt.gpuVendor, tt.gpuArch)

			if tt.wantErr {
				require.Error(t, err, tt.desc)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err, tt.desc)
			}

			events := gpuRecorderEvents(r)
			if tt.wantWarning {
				require.NotEmpty(t, events, "expected warning event for %s", tt.name)
				assert.Equal(t, "ExperimentalGPUSupport", events[0].Reason)
				assert.Equal(t, corev1.EventTypeWarning, events[0].EventType)
			} else if !tt.wantErr {
				assert.Empty(t, events, "expected no events for %s", tt.name)
			}
		})
	}

	t.Run("GPUProfile override takes precedence over matrix", func(t *testing.T) {
		profileR := &GPUProfileReconciler{}
		profileR.profiles.Store("gfx1100", &aiv1alpha2.GPUProfileSpec{
			VRAMMB: 24576,
			Backends: map[string]aiv1alpha2.BackendProfile{
				"vllm": {Support: "unsupported"},
			},
		})
		r := gpuTestReconciler(func(r *ModelReconciler) {
			r.GPUProfiles = profileR
		})
		model := gpuTestModel("profile-override")
		b := &fakeGPUBackend{name: "vllm"}

		err := r.validateBackendGPUCompatibility(model, b, backend.GPUVendorAMD, "gfx1100")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not supported")
	})

	t.Run("GPUProfile canary annotation emits BackendCanary event", func(t *testing.T) {
		profile := &aiv1alpha2.GPUProfile{
			ObjectMeta: metav1.ObjectMeta{Name: "gfx1100"},
			Spec: aiv1alpha2.GPUProfileSpec{
				Architecture: "gfx1100",
				Backends: map[string]aiv1alpha2.BackendProfile{
					"vllm": {Support: "full"},
				},
			},
		}
		aiv1alpha2.SetBackendCanary(profile, "vllm", ".loom/60-validation-matrix.md#gfx1100-vllm")
		profileR := &GPUProfileReconciler{}
		profileR.profileObjects.Store("gfx1100", profile)
		r := gpuTestReconciler(func(r *ModelReconciler) {
			r.GPUProfiles = profileR
		})
		model := gpuTestModel("canary-event")
		b := &fakeGPUBackend{name: "vllm"}

		err := r.validateBackendGPUCompatibility(model, b, backend.GPUVendorAMD, "gfx1100")
		require.NoError(t, err)

		events := gpuRecorderEvents(r)
		require.Len(t, events, 1)
		assert.Equal(t, "BackendCanary", events[0].Reason)
		assert.Equal(t, corev1.EventTypeWarning, events[0].EventType)
		assert.Contains(t, events[0].Message, "vllm on gfx1100 is marked as a canary backend")
		assert.Contains(t, events[0].Message, ".loom/60-validation-matrix.md#gfx1100-vllm")
	})
}

func TestValidateLlamaCppFlashAttentionOffNeedsUnquantizedVCache(t *testing.T) {
	b := &fakeGPUBackend{name: backend.NameLlamaCpp}
	r := gpuTestReconciler(func(*ModelReconciler) {})

	tests := []struct {
		name        string
		gpuVendor   backend.GPUVendor
		gpuArch     string
		config      map[string]any
		errContains string
	}{
		{name: "off with quantized V cache", gpuVendor: backend.GPUVendorAMD, gpuArch: "gfx906",
			config: map[string]any{"flashAttention": false, "cacheTypeK": "q4_0", "cacheTypeV": "q4_0"}, errContains: `quantized cacheTypeV "q4_0"`},
		{name: "string off with quantized V cache", gpuVendor: backend.GPUVendorAMD, gpuArch: "gfx906",
			config: map[string]any{"flashAttention": "false", "cacheTypeV": "Q8_0"}, errContains: `quantized cacheTypeV "q8_0"`},
		{name: "CPU off with quantized V cache", gpuVendor: backend.GPUVendorCPU,
			config: map[string]any{"flashAttention": false, "cacheTypeV": "q4_0"}, errContains: "requires Flash Attention"},
		{name: "off with quantized K cache only", gpuVendor: backend.GPUVendorAMD, gpuArch: "gfx906",
			config: map[string]any{"flashAttention": false, "cacheTypeK": "q4_0"}},
		{name: "off with f16 V cache", gpuVendor: backend.GPUVendorAMD, gpuArch: "gfx906",
			config: map[string]any{"flashAttention": false, "cacheTypeV": "f16"}},
		{name: "unset with quantized V cache", gpuVendor: backend.GPUVendorAMD, gpuArch: "gfx906",
			config: map[string]any{"cacheTypeK": "q4_0", "cacheTypeV": "q4_0"}},
		{name: "on with quantized V cache", gpuVendor: backend.GPUVendorAMD, gpuArch: "gfx906",
			config: map[string]any{"flashAttention": true, "cacheTypeV": "q4_0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := gpuTestModel("llamacpp-flash-attention", func(model *aiv1alpha2.Model) {
				model.Spec.Backend = backend.NameLlamaCpp
				model.Spec.Config = mustJSONConfig(tt.config)
			})
			err := r.validateBackendGPUCompatibility(model, b, tt.gpuVendor, tt.gpuArch)
			if tt.errContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateLlamaCppFeatureCertificates(t *testing.T) {
	const certifiedImage = "registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim@sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407"
	const evidence = ".loom/iteration-gfx906-llamacpp-stateful-spec-2026-07-15.md"
	newProfile := func() *aiv1alpha2.GPUProfile {
		profile := &aiv1alpha2.GPUProfile{
			ObjectMeta: metav1.ObjectMeta{Name: "gfx906"},
			Spec: aiv1alpha2.GPUProfileSpec{
				Architecture: "gfx906",
				Backends: map[string]aiv1alpha2.BackendProfile{
					backend.NameLlamaCpp: {Support: "full", Image: certifiedImage},
				},
			},
		}
		aiv1alpha2.SetBackendCapabilityCertificate(profile, backend.NameLlamaCpp, aiv1alpha2.LlamaCppCapabilityStatefulSlots, certifiedImage, evidence)
		aiv1alpha2.SetBackendCapabilityCertificate(profile, backend.NameLlamaCpp, aiv1alpha2.LlamaCppCapabilityNgramSimpleDraft16, certifiedImage, evidence)
		return profile
	}
	newModel := func(config map[string]any) *aiv1alpha2.Model {
		return gpuTestModel("gfx906-certified-llamacpp", func(model *aiv1alpha2.Model) {
			model.Spec.Backend = backend.NameLlamaCpp
			model.Spec.Config = mustJSONConfig(config)
		})
	}
	newReconciler := func(profile *aiv1alpha2.GPUProfile) *ModelReconciler {
		profiles := &GPUProfileReconciler{}
		profiles.Prime(profile)
		return gpuTestReconciler(func(r *ModelReconciler) { r.GPUProfiles = profiles })
	}
	b := &fakeGPUBackend{name: backend.NameLlamaCpp}

	t.Run("complete artifact-bound certificates pass", func(t *testing.T) {
		model := newModel(map[string]any{
			"dedicatedDeployment": true,
			"slotSavePath":        "/models/.flexinfer/slots/qwen3-8b",
			"specType":            "ngram-simple",
			"draftMax":            16,
			"draftMin":            1,
		})
		err := newReconciler(newProfile()).validateBackendGPUCompatibility(model, b, backend.GPUVendorAMD, "gfx906")
		require.NoError(t, err)
	})

	t.Run("missing capability certificate fails closed", func(t *testing.T) {
		profile := newProfile()
		aiv1alpha2.ClearBackendCapabilityCertificate(profile, backend.NameLlamaCpp, aiv1alpha2.LlamaCppCapabilityStatefulSlots)
		model := newModel(map[string]any{
			"dedicatedDeployment": true,
			"slotSavePath":        "/models/.flexinfer/slots/qwen3-8b",
		})
		err := newReconciler(profile).validateBackendGPUCompatibility(model, b, backend.GPUVendorAMD, "gfx906")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not fully certified")
	})

	t.Run("persistent runtime path is rejected until its artifact is certified", func(t *testing.T) {
		model := newModel(map[string]any{
			"slotSavePath": "/models/.flexinfer/slots/qwen3-8b",
		})
		err := newReconciler(newProfile()).validateBackendGPUCompatibility(model, b, backend.GPUVendorAMD, "gfx906")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dedicatedDeployment=true")
	})

	t.Run("per-model image drift is rejected", func(t *testing.T) {
		model := newModel(map[string]any{
			"dedicatedDeployment": true,
			"slotSavePath":        "/models/.flexinfer/slots/qwen3-8b",
		})
		model.Spec.Image = "registry.harbor.lan/library/llamacpp:mutable"
		err := newReconciler(newProfile()).validateBackendGPUCompatibility(model, b, backend.GPUVendorAMD, "gfx906")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "digest-pinned selected image")
	})

	t.Run("CPU path does not require a GPU certificate", func(t *testing.T) {
		model := newModel(map[string]any{
			"slotSavePath": "/models/.flexinfer/slots/cpu-model",
		})
		err := validateLlamaCppFeatureCertificates(model, nil, "", "ghcr.io/ggml-org/llama.cpp:server")
		require.NoError(t, err)
	})
}

func TestValidateLlamaCppDraftMTPCertificate(t *testing.T) {
	const profileImage = "registry.harbor.lan/library/llamacpp@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const mtpImage = "registry.harbor.lan/library/llamacpp@sha256:95c1d3901faba5fbbfa9ac173c4b40c479bcc604f1a41f873a975ce0cee0f2a1"
	const evidence = ".loom/60-validation-matrix.md#gfx1100-llamacpp-b11847-2026-10-24"
	newProfile := func(certify bool) *aiv1alpha2.GPUProfile {
		profile := &aiv1alpha2.GPUProfile{
			ObjectMeta: metav1.ObjectMeta{Name: "gfx1100"},
			Spec: aiv1alpha2.GPUProfileSpec{
				Architecture: "gfx1100",
				Backends: map[string]aiv1alpha2.BackendProfile{
					backend.NameLlamaCpp: {Support: "full", Image: profileImage},
				},
			},
		}
		if certify {
			aiv1alpha2.SetBackendCapabilityCertificate(profile, backend.NameLlamaCpp, aiv1alpha2.LlamaCppCapabilityDraftMTP, mtpImage, evidence)
		}
		return profile
	}
	// Mirrors deploy/debug/qwen38-27b-gguf-spec-probe.yaml: a per-model digest
	// pin on the dedicated backend image, not the profile's default artifact.
	newModel := func(image string) *aiv1alpha2.Model {
		return gpuTestModel("qwen38-27b-gguf-spec-probe", func(model *aiv1alpha2.Model) {
			model.Spec.Backend = backend.NameLlamaCpp
			model.Spec.Image = image
			model.Spec.Config = mustJSONConfig(map[string]any{
				"dedicatedDeployment": true,
				"specType":            "draft-mtp",
			})
		})
	}
	validate := func(profile *aiv1alpha2.GPUProfile, model *aiv1alpha2.Model) error {
		profiles := &GPUProfileReconciler{}
		profiles.Prime(profile)
		r := gpuTestReconciler(func(r *ModelReconciler) { r.GPUProfiles = profiles })
		return r.validateBackendGPUCompatibility(model, &fakeGPUBackend{name: backend.NameLlamaCpp}, backend.GPUVendorAMD, "gfx1100")
	}

	t.Run("certificate on the selected artifact passes", func(t *testing.T) {
		require.NoError(t, validate(newProfile(true), newModel(mtpImage)))
	})

	t.Run("uncertified profile fails closed", func(t *testing.T) {
		err := validate(newProfile(false), newModel(mtpImage))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not fully certified")
		assert.Contains(t, err.Error(), "flexinfer.ai/backend-certificate-llamacpp-draft-mtp")
	})

	t.Run("profile default artifact does not inherit the certificate", func(t *testing.T) {
		err := validate(newProfile(true), newModel(""))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is certified only for")
	})
}

func TestRequestedLlamaCppCapabilities(t *testing.T) {
	tests := []struct {
		name        string
		config      map[string]any
		want        []string
		errContains string
	}{
		{name: "no opt-ins", config: map[string]any{}},
		{
			name: "stateful and proven speculation envelope",
			config: map[string]any{
				"slotSavePath": "/models/.flexinfer/slots/test",
				"specType":     "ngram-simple",
				"draftMax":     float64(16),
				"draftMin":     float64(1),
			},
			want: []string{aiv1alpha2.LlamaCppCapabilityStatefulSlots, aiv1alpha2.LlamaCppCapabilityNgramSimpleDraft16},
		},
		{name: "slot path must persist", config: map[string]any{"slotSavePath": "/tmp/slots"}, errContains: "persistent /models mount"},
		{name: "slot path must be a string", config: map[string]any{"slotSavePath": true}, errContains: "non-empty string"},
		{name: "draft limits require speculation", config: map[string]any{"draftMax": 16}, errContains: "require spec.config.specType"},
		{name: "speculation requires proven limits", config: map[string]any{"specType": "ngram-simple", "draftMax": 8, "draftMin": 1}, errContains: "draftMax=16"},
		{name: "draft bounds must be integers", config: map[string]any{"specType": "ngram-simple", "draftMax": 16.5, "draftMin": 1}, errContains: "must be an integer"},
		{name: "uncertified speculation type", config: map[string]any{"specType": "ngram-cache"}, errContains: "not exposed"},
		{
			name:   "MTP speculation on the default draft window",
			config: map[string]any{"specType": "draft-mtp"},
			want:   []string{aiv1alpha2.LlamaCppCapabilityDraftMTP},
		},
		{
			name: "stateful and MTP speculation",
			config: map[string]any{
				"slotSavePath": "/models/.flexinfer/slots/test",
				"specType":     " draft-mtp ",
			},
			want: []string{aiv1alpha2.LlamaCppCapabilityStatefulSlots, aiv1alpha2.LlamaCppCapabilityDraftMTP},
		},
		{
			name:   "MTP explicit window is its own envelope",
			config: map[string]any{"specType": "draft-mtp", "draftMax": 2},
			want:   []string{"draft-mtp-draft2-min0"},
		},
		{
			name:   "MTP window keeps an explicit draftMin",
			config: map[string]any{"specType": "draft-mtp", "draftMax": float64(3), "draftMin": float64(1), "llamacppBuild": float64(11847)},
			want:   []string{aiv1alpha2.LlamaCppCapabilityDraftMTPWindow(3, 1)},
		},
		{name: "MTP draftMin alone is not a window", config: map[string]any{"specType": "draft-mtp", "draftMin": 0}, errContains: "needs spec.config.draftMax"},
		{name: "MTP window must be positive", config: map[string]any{"specType": "draft-mtp", "draftMax": 0}, errContains: "between 1 and 16"},
		{name: "MTP window is bounded", config: map[string]any{"specType": "draft-mtp", "draftMax": 17}, errContains: "between 1 and 16"},
		{name: "MTP draftMin cannot exceed draftMax", config: map[string]any{"specType": "draft-mtp", "draftMax": 2, "draftMin": 3}, errContains: "between 0 and draftMax=2"},
		{name: "MTP needs a renamed-flag build when one is declared", config: map[string]any{"specType": "draft-mtp", "llamacppBuild": 8173}, errContains: "needs llama.cpp b8955"},
		{
			name:   "ngram-simple envelope on its declared b8173 build",
			config: map[string]any{"specType": "ngram-simple", "draftMax": 16, "draftMin": 1, "llamacppBuild": 8173},
			want:   []string{aiv1alpha2.LlamaCppCapabilityNgramSimpleDraft16},
		},
		{name: "ngram-simple envelope refuses renamed-flag builds", config: map[string]any{"specType": "ngram-simple", "draftMax": 16, "draftMin": 1, "llamacppBuild": 11335}, errContains: "--spec-ngram-simple"},
		{name: "build must be positive", config: map[string]any{"llamacppBuild": 0}, errContains: "positive llama.cpp build number"},
		{name: "build must be a number", config: map[string]any{"llamacppBuild": "b11847"}, errContains: "llamacppBuild must be an integer"},
		{name: "combined speculation types stay uncertified", config: map[string]any{"specType": "draft-mtp,ngram-simple"}, errContains: "not exposed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := requestedLlamaCppCapabilities(tt.config)
			if tt.errContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateMaxwellSpecifics(t *testing.T) {
	tests := []struct {
		name        string
		backendName string
		gpuVendor   backend.GPUVendor
		gpuArch     string
		source      string
		config      map[string]any
		wantErr     bool
		errContains string
	}{
		{
			name:        "non-Maxwell arch skips all checks",
			backendName: "vllm",
			gpuVendor:   backend.GPUVendorNVIDIA,
			gpuArch:     "sm_89",
			source:      "HF://org/model-fp16",
			wantErr:     false,
		},
		{
			name:        "non-NVIDIA vendor skips all checks",
			backendName: "vllm",
			gpuVendor:   backend.GPUVendorAMD,
			gpuArch:     "sm_52",
			source:      "HF://org/model-fp16",
			wantErr:     false,
		},
		{
			name:        "FP16 in source rejected on Maxwell",
			backendName: "llamacpp",
			gpuVendor:   backend.GPUVendorNVIDIA,
			gpuArch:     "sm_52",
			source:      "HF://org/model-FP16-GGUF",
			wantErr:     true,
			errContains: "FP16 models are not supported on Maxwell",
		},
		{
			name:        "f16 in source rejected on Maxwell",
			backendName: "llamacpp",
			gpuVendor:   backend.GPUVendorNVIDIA,
			gpuArch:     "sm_52",
			source:      "HF://org/llama-3-8b-f16.gguf",
			wantErr:     true,
			errContains: "FP16 models are not supported on Maxwell",
		},
		{
			name:        "MLC-LLM with modelLibPath passes",
			backendName: "mlc-llm",
			gpuVendor:   backend.GPUVendorNVIDIA,
			gpuArch:     "sm_52",
			source:      "HF://org/model",
			config:      map[string]any{"modelLibPath": "/libs/model.so"},
			wantErr:     false,
		},
		{
			name:        "MLC-LLM without modelLibPath and no model path errors",
			backendName: "mlc-llm",
			gpuVendor:   backend.GPUVendorNVIDIA,
			gpuArch:     "sm_52",
			source:      "HF://org/model",
			config:      map[string]any{},
			wantErr:     true,
			errContains: "mlc-llm on Maxwell GPUs requires config.modelLibPath",
		},
		{
			name:        "MLC-LLM with model path under /models/ passes",
			backendName: "mlc-llm",
			gpuVendor:   backend.GPUVendorNVIDIA,
			gpuArch:     "sm_52",
			source:      "pvc://models-pvc/q4f32_1",
			wantErr:     false,
		},
		{
			name:        "non-MLC backend on Maxwell with safe source passes",
			backendName: "ollama",
			gpuVendor:   backend.GPUVendorNVIDIA,
			gpuArch:     "sm_52",
			source:      "ollama://llama3:8b-q4_0",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gpuTestReconciler()
			model := gpuTestModel("maxwell-test", func(m *aiv1alpha2.Model) {
				m.Spec.Backend = tt.backendName
				m.Spec.Source = tt.source
				if tt.config != nil {
					m.Spec.Config = mustJSONConfig(tt.config)
				}
			})
			b := &fakeGPUBackend{name: tt.backendName}

			err := r.validateMaxwellSpecifics(model, b, tt.gpuVendor, tt.gpuArch)

			if tt.wantErr {
				require.Error(t, err, "expected error for %s", tt.name)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err, "expected no error for %s", tt.name)
			}
		})
	}
}

func TestEmitVLLMOptInEvents(t *testing.T) {
	tests := []struct {
		name        string
		config      map[string]any
		wantReasons []string
	}{
		{
			name:        "v1 engine opt-in emits event",
			config:      map[string]any{"vllmEngineVersion": "v1"},
			wantReasons: []string{"V1EngineOptIn"},
		},
		{
			name:        "flash attention true (bool) emits event",
			config:      map[string]any{"enableFlashAttention": true},
			wantReasons: []string{"FlashAttentionOptIn"},
		},
		{
			name:        "flash attention true (string) emits event",
			config:      map[string]any{"enableFlashAttention": "true"},
			wantReasons: []string{"FlashAttentionOptIn"},
		},
		{
			name:        "nil config does not panic",
			config:      nil,
			wantReasons: nil,
		},
		{
			name:        "no matching keys emits no events",
			config:      map[string]any{"maxModelLen": 4096, "gpuMemoryUtilization": 0.95},
			wantReasons: nil,
		},
		{
			name:        "both v1 engine and flash attention emit two events",
			config:      map[string]any{"vllmEngineVersion": "v1", "enableFlashAttention": true},
			wantReasons: []string{"V1EngineOptIn", "FlashAttentionOptIn"},
		},
		{
			name:        "flash attention false (bool) emits no event",
			config:      map[string]any{"enableFlashAttention": false},
			wantReasons: nil,
		},
		{
			name:        "flash attention 1 (string) emits event",
			config:      map[string]any{"enableFlashAttention": "1"},
			wantReasons: []string{"FlashAttentionOptIn"},
		},
		{
			name:        "vllm engine version v2 does not emit event",
			config:      map[string]any{"vllmEngineVersion": "v2"},
			wantReasons: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gpuTestReconciler()
			model := gpuTestModel("vllm-optin", func(m *aiv1alpha2.Model) {
				if tt.config != nil {
					m.Spec.Config = mustJSONConfig(tt.config)
				}
			})

			r.emitVLLMOptInEvents(model)

			events := gpuRecorderEvents(r)
			if len(tt.wantReasons) == 0 {
				assert.Empty(t, events, "expected no events for %s", tt.name)
			} else {
				require.Len(t, events, len(tt.wantReasons), "expected %d events for %s", len(tt.wantReasons), tt.name)
				for i, reason := range tt.wantReasons {
					assert.Equal(t, reason, events[i].Reason)
					assert.Equal(t, corev1.EventTypeNormal, events[i].EventType)
				}
			}
		})
	}
}

func TestDetectGPU(t *testing.T) {
	scheme := gpuTestScheme()

	tests := []struct {
		name       string
		model      *aiv1alpha2.Model
		nodes      []corev1.Node
		wantVendor backend.GPUVendor
		wantArch   string
		wantErr    bool
		errType    string // "noMatching" or "ambiguous"
	}{
		{
			name: "CPU-only (nil GPU spec)",
			model: gpuTestModel("cpu-model", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = nil
			}),
			nodes:      nil,
			wantVendor: backend.GPUVendorCPU,
			wantArch:   "",
		},
		{
			name: "CPU vendor explicitly set",
			model: gpuTestModel("cpu-explicit", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorCPU}
			}),
			nodes:      nil,
			wantVendor: backend.GPUVendorCPU,
			wantArch:   "",
		},
		{
			name: "NVIDIA GPU found with compute.major label",
			model: gpuTestModel("nvidia-model", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorNVIDIA}
			}),
			nodes: []corev1.Node{
				readyNode("gpu-node-1", map[string]string{
					"nvidia.com/gpu.compute.major": "8",
				}, corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				}),
			},
			wantVendor: backend.GPUVendorNVIDIA,
			wantArch:   "sm_8",
		},
		{
			name: "AMD GPU found with gpu-architecture label",
			model: gpuTestModel("amd-model", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD}
			}),
			nodes: []corev1.Node{
				readyNode("amd-node-1", map[string]string{
					"gpu.amd.com/gpu-architecture": "gfx1100",
				}, corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				}),
			},
			wantVendor: backend.GPUVendorAMD,
			wantArch:   "gfx1100",
		},
		{
			name: "auto with both NVIDIA and AMD returns ambiguous error",
			model: gpuTestModel("ambiguous-model", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAuto}
			}),
			nodes: []corev1.Node{
				readyNode("nvidia-node", map[string]string{}, corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				}),
				readyNode("amd-node", map[string]string{}, corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				}),
			},
			wantErr: true,
			errType: "ambiguous",
		},
		{
			name: "nodeSelector filtering only matches labeled nodes",
			model: gpuTestModel("selector-model", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD}
				m.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "target-node"}
			}),
			nodes: []corev1.Node{
				readyNode("other-node", map[string]string{
					"kubernetes.io/hostname":       "other-node",
					"gpu.amd.com/gpu-architecture": "gfx1100",
				}, corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				}),
				readyNode("target-node", map[string]string{
					"kubernetes.io/hostname":       "target-node",
					"gpu.amd.com/gpu-architecture": "gfx906",
				}, corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				}),
			},
			wantVendor: backend.GPUVendorAMD,
			wantArch:   "gfx906",
		},
		{
			name: "AMD label cascade: flexinfer.ai/gpu.arch fallback",
			model: gpuTestModel("amd-flexinfer-label", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD}
			}),
			nodes: []corev1.Node{
				readyNode("amd-node-flex", map[string]string{
					LabelGPUArch: "gfx906",
				}, corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				}),
			},
			wantVendor: backend.GPUVendorAMD,
			wantArch:   "gfx906",
		},
		{
			name: "AMD label cascade: family label GC_11_0_0",
			model: gpuTestModel("amd-family-label", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD}
			}),
			nodes: []corev1.Node{
				readyNode("amd-node-family", map[string]string{
					"amd.com/gpu.family.GC_11_0_0": "1",
				}, corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				}),
			},
			wantVendor: backend.GPUVendorAMD,
			wantArch:   "gfx1100",
		},
		{
			name: "AMD label cascade: model label with 7900",
			model: gpuTestModel("amd-model-label", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD}
			}),
			nodes: []corev1.Node{
				readyNode("amd-node-model", map[string]string{
					"gpu.amd.com/model": "Radeon RX 7900 XTX",
				}, corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				}),
			},
			wantVendor: backend.GPUVendorAMD,
			wantArch:   "gfx1100",
		},
		{
			name: "no GPU nodes returns noMatchingNodesError",
			model: gpuTestModel("no-nodes", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorNVIDIA}
			}),
			nodes:   []corev1.Node{},
			wantErr: true,
			errType: "noMatching",
		},
		{
			name: "NVIDIA with compute.major 5 returns sm_5",
			model: gpuTestModel("nvidia-maxwell", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorNVIDIA}
			}),
			nodes: []corev1.Node{
				readyNode("maxwell-node", map[string]string{
					"nvidia.com/gpu.compute.major": "5",
				}, corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				}),
			},
			wantVendor: backend.GPUVendorNVIDIA,
			wantArch:   "sm_5",
		},
		{
			name: "not-ready nodes are skipped",
			model: gpuTestModel("skip-not-ready", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorNVIDIA}
			}),
			nodes: []corev1.Node{
				notReadyNode("bad-node", map[string]string{
					"nvidia.com/gpu.compute.major": "8",
				}, corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				}),
			},
			wantErr: true,
			errType: "noMatching",
		},
		{
			name: "auto with only NVIDIA returns NVIDIA",
			model: gpuTestModel("auto-nvidia", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAuto}
			}),
			nodes: []corev1.Node{
				readyNode("nvidia-only", map[string]string{
					"nvidia.com/gpu.compute.major": "7",
				}, corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				}),
			},
			wantVendor: backend.GPUVendorNVIDIA,
			wantArch:   "sm_7",
		},
		{
			name: "auto with only AMD returns AMD",
			model: gpuTestModel("auto-amd", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAuto}
			}),
			nodes: []corev1.Node{
				readyNode("amd-only", map[string]string{
					"gpu.amd.com/gpu-architecture": "gfx1100",
				}, corev1.ResourceList{
					"amd.com/gpu": resource.MustParse("1"),
				}),
			},
			wantVendor: backend.GPUVendorAMD,
			wantArch:   "gfx1100",
		},
		{
			name: "NVIDIA node without compute.major uses flexinfer.ai/gpu.arch fallback",
			model: gpuTestModel("nvidia-flexinfer-arch", func(m *aiv1alpha2.Model) {
				m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorNVIDIA}
			}),
			nodes: []corev1.Node{
				readyNode("nvidia-flex", map[string]string{
					LabelGPUArch: "sm_89",
				}, corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("1"),
				}),
			},
			wantVendor: backend.GPUVendorNVIDIA,
			wantArch:   "sm_89",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var nodeList []corev1.Node
			if tt.nodes != nil {
				nodeList = tt.nodes
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithLists(&corev1.NodeList{Items: nodeList}).
				Build()

			r := &ModelReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: &FakeEventRecorder{},
			}

			vendor, arch, err := r.detectGPU(context.Background(), tt.model)

			if tt.wantErr {
				require.Error(t, err)
				switch tt.errType {
				case "noMatching":
					assert.True(t, isNoMatchingNodesError(err), "expected noMatchingNodesError, got: %v", err)
				case "ambiguous":
					assert.True(t, isAmbiguousGPUVendorError(err), "expected ambiguousGPUVendorError, got: %v", err)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantVendor, vendor, "vendor mismatch")
				assert.Equal(t, tt.wantArch, arch, "arch mismatch")
			}
		})
	}
}

// TestDetectGPU_AutoNoGPUNodes verifies auto mode with only CPU nodes.
func TestDetectGPU_AutoNoGPUNodes(t *testing.T) {
	scheme := gpuTestScheme()

	model := gpuTestModel("auto-no-gpu", func(m *aiv1alpha2.Model) {
		m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAuto}
	})

	nodes := []corev1.Node{
		readyNode("cpu-node", map[string]string{}, corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("8"),
			corev1.ResourceMemory: resource.MustParse("32Gi"),
		}),
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithLists(&corev1.NodeList{Items: nodes}).
		Build()

	r := &ModelReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: &FakeEventRecorder{},
	}

	_, _, err := r.detectGPU(context.Background(), model)
	require.Error(t, err)
	assert.True(t, isNoMatchingNodesError(err), "expected noMatchingNodesError for auto with no GPU nodes")
}

// TestDetectGPU_NodeSelectorNoMatch verifies that nodeSelector filtering
// that eliminates all GPU nodes produces a noMatchingNodesError.
func TestDetectGPU_NodeSelectorNoMatch(t *testing.T) {
	scheme := gpuTestScheme()

	model := gpuTestModel("selector-no-match", func(m *aiv1alpha2.Model) {
		m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD}
		m.Spec.NodeSelector = map[string]string{"zone": "us-west-2a"}
	})

	nodes := []corev1.Node{
		readyNode("amd-east", map[string]string{
			"zone":                         "us-east-1b",
			"gpu.amd.com/gpu-architecture": "gfx1100",
		}, corev1.ResourceList{
			"amd.com/gpu": resource.MustParse("1"),
		}),
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithLists(&corev1.NodeList{Items: nodes}).
		Build()

	r := &ModelReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: &FakeEventRecorder{},
	}

	_, _, err := r.detectGPU(context.Background(), model)
	require.Error(t, err)
	assert.True(t, isNoMatchingNodesError(err), "expected noMatchingNodesError when nodeSelector filters out all GPU nodes")
}

// TestDetectGPU_AMDFamilyGC1036 verifies the GC_10_3_6 family label maps to gfx1036.
func TestDetectGPU_AMDFamilyGC1036(t *testing.T) {
	scheme := gpuTestScheme()

	model := gpuTestModel("amd-gc1036", func(m *aiv1alpha2.Model) {
		m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD}
	})

	nodes := []corev1.Node{
		readyNode("amd-1036-node", map[string]string{
			"amd.com/gpu.family.GC_10_3_6": "1",
		}, corev1.ResourceList{
			"amd.com/gpu": resource.MustParse("1"),
		}),
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithLists(&corev1.NodeList{Items: nodes}).
		Build()

	r := &ModelReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: &FakeEventRecorder{},
	}

	vendor, arch, err := r.detectGPU(context.Background(), model)
	require.NoError(t, err)
	assert.Equal(t, backend.GPUVendorAMD, vendor)
	assert.Equal(t, "gfx1036", arch)
}

// TestDetectGPU_SkipsDegradedGPUNodes verifies the controller passes over a
// node the agent's GPU health sentinel marked degraded and lands on a healthy
// twin when one matches.
func TestDetectGPU_SkipsDegradedGPUNodes(t *testing.T) {
	scheme := gpuTestScheme()
	model := gpuTestModel("prefers-healthy", func(m *aiv1alpha2.Model) {
		m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD}
	})

	degraded := readyNode("xtx-degraded", map[string]string{
		"gpu.amd.com/gpu-architecture": "gfx1100",
		"flexinfer.ai/gpu.health":      "degraded",
	}, corev1.ResourceList{"amd.com/gpu": resource.MustParse("2")})
	degraded.Annotations = map[string]string{
		"flexinfer.ai/gpu.health-reason": "card0: pcie-link-down (current_link_speed=Unknown)",
	}
	healthy := readyNode("5930k-healthy", map[string]string{
		"gpu.amd.com/gpu-architecture": "gfx1100",
		"flexinfer.ai/gpu.health":      "ok",
	}, corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")})

	r := &ModelReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithLists(&corev1.NodeList{Items: []corev1.Node{degraded, healthy}}).
			Build(),
		Scheme:   scheme,
		Recorder: &FakeEventRecorder{},
	}

	vendor, arch, err := r.detectGPU(context.Background(), model)
	require.NoError(t, err)
	assert.Equal(t, backend.GPUVendorAMD, vendor)
	assert.Equal(t, "gfx1100", arch)
}

// TestDetectGPU_ReportsGPUDegradedWhenOnlyDegradedNodesMatch verifies a lane
// pinned to a degraded card gets a no-matching-nodes error flagged as GPU
// degraded, naming the node and the agent's reason, so the Schedulable
// condition can say GPUDegraded and the Deployment is parked.
func TestDetectGPU_ReportsGPUDegradedWhenOnlyDegradedNodesMatch(t *testing.T) {
	scheme := gpuTestScheme()
	model := gpuTestModel("xtx-warm-lane", func(m *aiv1alpha2.Model) {
		m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD}
		m.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "cblevins-7900xtx"}
	})

	degraded := readyNode("cblevins-7900xtx", map[string]string{
		"kubernetes.io/hostname":       "cblevins-7900xtx",
		"gpu.amd.com/gpu-architecture": "gfx1100",
		"flexinfer.ai/gpu.health":      "degraded",
	}, corev1.ResourceList{"amd.com/gpu": resource.MustParse("2")})
	degraded.Annotations = map[string]string{
		"flexinfer.ai/gpu.health-reason": "card0: device-io-error (hwmon/hwmon3/temp1_input: remote I/O error)",
	}
	other := readyNode("cblevins-5930k", map[string]string{
		"kubernetes.io/hostname":       "cblevins-5930k",
		"gpu.amd.com/gpu-architecture": "gfx1100",
	}, corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")})

	r := &ModelReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithLists(&corev1.NodeList{Items: []corev1.Node{degraded, other}}).
			Build(),
		Scheme:   scheme,
		Recorder: &FakeEventRecorder{},
	}

	_, _, err := r.detectGPU(context.Background(), model)
	require.Error(t, err)
	assert.True(t, isNoMatchingNodesError(err))
	assert.True(t, isGPUDegradedError(err), "error must be flagged as GPU degraded: %v", err)
	assert.Contains(t, err.Error(), "cblevins-7900xtx: card0: device-io-error")
	assert.Contains(t, err.Error(), "flexinfer.ai/gpu.health=degraded")

	// A selector miss with no degraded candidate keeps the plain reason.
	plain := gpuTestModel("nowhere", func(m *aiv1alpha2.Model) {
		m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD}
		m.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "missing"}
	})
	_, _, err = r.detectGPU(context.Background(), plain)
	require.Error(t, err)
	assert.True(t, isNoMatchingNodesError(err))
	assert.False(t, isGPUDegradedError(err))
}

// TestNodeGPUHealthChange_PredicateAndFanOut covers the Node watch: only a
// gpu.health label flip admits the event, and the fan-out enqueues GPU
// Models but not CPU ones.
func TestNodeGPUHealthChange_PredicateAndFanOut(t *testing.T) {
	old := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n", Labels: map[string]string{"flexinfer.ai/gpu.health": "ok"}}}
	flipped := old.DeepCopy()
	flipped.Labels["flexinfer.ai/gpu.health"] = "degraded"
	heartbeat := old.DeepCopy()
	heartbeat.Annotations = map[string]string{"flexinfer.ai/gpu.util": "3.00"}

	assert.True(t, nodeGPUHealthChange.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: flipped}))
	assert.False(t, nodeGPUHealthChange.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: heartbeat}))
	assert.False(t, nodeGPUHealthChange.Create(event.CreateEvent{Object: flipped}))
	assert.False(t, nodeGPUHealthChange.Delete(event.DeleteEvent{Object: flipped}))

	scheme := gpuTestScheme()
	gpuModel := gpuTestModel("gpu-lane", func(m *aiv1alpha2.Model) {
		m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD}
	})
	cpuModel := gpuTestModel("cpu-embed", func(m *aiv1alpha2.Model) {
		m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorCPU}
	})
	r := &ModelReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(gpuModel, cpuModel).
			Build(),
		Scheme: scheme,
	}
	requests := r.requestsForNodeGPUHealth(context.Background(), flipped)
	require.Len(t, requests, 1)
	assert.Equal(t, "gpu-lane", requests[0].Name)
}
