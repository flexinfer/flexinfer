package controllers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/internal/proxy"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

// R6: the proxy's direct-load fast path and the controller's reconcile path
// must send the same load payload. Before they shared a builder the proxy
// omitted the LoRA options, so a proxy-won load started the lane without its
// adapters under a SpecHash the controller had never rendered.
func TestRuntimeLoadPayloadParityBetweenControllerAndProxy(t *testing.T) {
	const ns = "flexinfer-system"
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen35-9b-rp-lora-xtx", Namespace: ns, Generation: 7},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://Qwen/Qwen3.5-9B",
			Config: &apiextensionsv1.JSON{Raw: []byte(`{
				"maxModelLen": 32768,
				"maxNumSeqs": 4,
				"gpuMemoryUtilization": "0.9",
				"speculativeConfig": "{\"method\":\"qwen3_5_mtp\",\"num_speculative_tokens\":1}",
				"env": {"FLEXINFER_QWEN35_MTP_HEAD": "checkpoint", "VLLM_ROCM_USE_AITER": "0"}
			}`)},
			GPU:          &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD, Count: ptr.To(int32(1)), VRAMEstimateMB: ptr.To(int64(21000))},
			NodeSelector: map[string]string{"kubernetes.io/hostname": "cblevins-7900xtx"},
			Serverless:   &aiv1alpha2.ServerlessSpec{ColdStartTimeout: &metav1.Duration{Duration: 25 * time.Minute}},
		},
	}
	adapter := func(name, modelRef string, maxRank int) *aiv1alpha2.LoRAAdapter {
		return &aiv1alpha2.LoRAAdapter{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       aiv1alpha2.LoRAAdapterSpec{ModelRef: modelRef, AdapterName: name, MaxRank: ptr.To(maxRank)},
		}
	}
	profile := &aiv1alpha2.GPUProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "gfx1100", Namespace: ns},
		Spec: aiv1alpha2.GPUProfileSpec{
			Architecture:        "gfx1100",
			Vendor:              "amd",
			VRAMMB:              24576,
			UsableDeviceIndices: []string{"0"},
			Env: []corev1.EnvVar{
				{Name: "HSA_OVERRIDE_GFX_VERSION", Value: "11.0.0"},
				{Name: "VLLM_ROCM_USE_AITER", Value: "1"},
			},
		},
	}

	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, aiv1alpha2.AddToScheme(s))
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(
		model, profile,
		adapter("rp-voice", model.Name, 32),
		adapter("rp-terse", model.Name, 64),
		adapter("other-lane", "qwen38-27b-autoround-workhorse", 128),
	).Build()

	b, ok := backend.Get(model.Spec.Backend)
	require.True(t, ok)

	// Controller: the reconcile path, with the GPUProfile cache it reads.
	profiles := &GPUProfileReconciler{}
	profiles.Prime(profile)
	r := &ModelReconciler{Client: cl, Scheme: s, GPUProfiles: profiles}
	controllerPayload, err := r.buildRuntimeLoadPayload(context.Background(), model, b, backend.GPUVendorAMD, "gfx1100")
	require.NoError(t, err)
	controllerBody, err := controllerPayload.Marshal()
	require.NoError(t, err)

	// Proxy: the direct-load fast path's renderer, with the profile it lists.
	proxyBody, err := proxy.DirectLoadPayload(context.Background(), cl, model, b, profile.Spec.DeepCopy())
	require.NoError(t, err)

	require.Equal(t, string(controllerBody), string(proxyBody), "proxy and controller must send byte-identical load payloads")

	// And the shared payload carries what the proxy used to drop.
	var decoded pkgrt.LoadPayload
	require.NoError(t, json.Unmarshal(proxyBody, &decoded))
	require.Equal(t, int64(7), decoded.Generation)
	require.NotEmpty(t, decoded.SpecHash)
	require.Equal(t, true, decoded.Config["enableLora"])
	require.EqualValues(t, 4, decoded.Config["maxLoras"], "two adapters still get the 4-slot minimum")
	require.EqualValues(t, 64, decoded.Config["maxLoraRank"], "the largest rank targeting this Model, not the other lane's")
	require.Equal(t, `{"method":"qwen3_5_mtp","num_speculative_tokens":1}`, decoded.Config["speculativeConfig"])
	require.EqualValues(t, 1500, decoded.Config["startupTimeoutSeconds"])
	env := map[string]string{}
	for _, e := range decoded.Env {
		env[e.Name] = e.Value
	}
	require.Equal(t, "checkpoint", env["FLEXINFER_QWEN35_MTP_HEAD"])
	require.Equal(t, "0", env["VLLM_ROCM_USE_AITER"], "Model env overrides the GPUProfile env")
	require.Equal(t, "11.0.0", env["HSA_OVERRIDE_GFX_VERSION"])
}
