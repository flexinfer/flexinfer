package controllers

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

const (
	soCertifiedImage = "registry.harbor.lan/flexinfer/runtime@sha256:b06b06fe1c0265ce207f3fc719c181e8edd18eae50bdb8490c122800b17abfe2"
	soOtherImage     = "registry.harbor.lan/flexinfer/runtime@sha256:cd108c9ebf99aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	soEvidence       = ".loom/60-validation-matrix.md#gfx1100-structured-outputs-2026-11-06"
)

func soProfile(certify func(*aiv1alpha2.GPUProfile)) *aiv1alpha2.GPUProfile {
	profile := &aiv1alpha2.GPUProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "gfx1100"},
		Spec: aiv1alpha2.GPUProfileSpec{
			Architecture: "gfx1100",
			Backends: map[string]aiv1alpha2.BackendProfile{
				backend.NameVLLM: {Support: "full"},
			},
		},
	}
	if certify != nil {
		certify(profile)
	}
	return profile
}

func soCertify(image string) func(*aiv1alpha2.GPUProfile) {
	return func(p *aiv1alpha2.GPUProfile) {
		aiv1alpha2.SetBackendCapabilityCertificate(p, backend.NameVLLM, aiv1alpha2.VLLMCapabilityStructuredOutputs, image, soEvidence)
	}
}

func soModel(structuredOutput *bool, config map[string]any) *aiv1alpha2.Model {
	return gpuTestModel("so-canary", func(m *aiv1alpha2.Model) {
		m.Spec.Image = soCertifiedImage
		if structuredOutput != nil {
			m.Spec.Capabilities = &aiv1alpha2.ModelCapabilities{StructuredOutput: structuredOutput}
		}
		if config != nil {
			m.Spec.Config = mustJSONConfig(config)
		}
	})
}

func soBool(v bool) *bool { return &v }

func TestEvaluateStructuredOutput(t *testing.T) {
	dedicated := map[string]any{"dedicatedDeployment": true}
	key := aiv1alpha2.BackendCapabilityCertificateAnnotationKey(backend.NameVLLM, aiv1alpha2.VLLMCapabilityStructuredOutputs)
	require.Equal(t, "flexinfer.ai/backend-certificate-vllm-structured-outputs", key)

	cases := []struct {
		name        string
		model       *aiv1alpha2.Model
		backend     string
		profile     *aiv1alpha2.GPUProfile
		arch        string
		image       string
		wantReq     bool
		wantRender  bool
		wantReason  string
		wantMessage string
	}{
		{
			name:    "no capabilities block is not a request",
			model:   soModel(nil, dedicated),
			backend: backend.NameVLLM, profile: soProfile(soCertify(soCertifiedImage)), arch: "gfx1100", image: soCertifiedImage,
		},
		{
			name:    "structuredOutput false is not a request",
			model:   soModel(soBool(false), dedicated),
			backend: backend.NameVLLM, profile: soProfile(soCertify(soCertifiedImage)), arch: "gfx1100", image: soCertifiedImage,
		},
		{
			name:    "non-vllm backend is unsupported",
			model:   soModel(soBool(true), dedicated),
			backend: backend.NameLlamaCpp, profile: soProfile(soCertify(soCertifiedImage)), arch: "gfx1100", image: soCertifiedImage,
			wantReq: true, wantReason: aiv1alpha2.ReasonStructuredOutputUnsupported, wantMessage: "only for the vllm backend",
		},
		{
			name:    "runtime-pod lane is unsupported",
			model:   soModel(soBool(true), nil),
			backend: backend.NameVLLM, profile: soProfile(soCertify(soCertifiedImage)), arch: "gfx1100", image: soCertifiedImage,
			wantReq: true, wantReason: aiv1alpha2.ReasonStructuredOutputUnsupported, wantMessage: "dedicatedDeployment=true",
		},
		{
			name:    "unresolved arch is not certified",
			model:   soModel(soBool(true), dedicated),
			backend: backend.NameVLLM, profile: soProfile(soCertify(soCertifiedImage)), arch: "", image: soCertifiedImage,
			wantReq: true, wantReason: aiv1alpha2.ReasonStructuredOutputNotCertified, wantMessage: "no GPU architecture",
		},
		{
			name:    "missing GPUProfile is not certified",
			model:   soModel(soBool(true), dedicated),
			backend: backend.NameVLLM, profile: nil, arch: "gfx1100", image: soCertifiedImage,
			wantReq: true, wantReason: aiv1alpha2.ReasonStructuredOutputNotCertified, wantMessage: "no GPUProfile for gfx1100",
		},
		{
			name:    "tag-only selected image is not certified",
			model:   soModel(soBool(true), dedicated),
			backend: backend.NameVLLM, profile: soProfile(soCertify(soCertifiedImage)), arch: "gfx1100", image: "registry.harbor.lan/flexinfer/runtime:rocm-gfx1100",
			wantReq: true, wantReason: aiv1alpha2.ReasonStructuredOutputNotCertified, wantMessage: "not digest-pinned",
		},
		{
			name:    "no certificate is not certified",
			model:   soModel(soBool(true), dedicated),
			backend: backend.NameVLLM, profile: soProfile(nil), arch: "gfx1100", image: soCertifiedImage,
			wantReq: true, wantReason: aiv1alpha2.ReasonStructuredOutputNotCertified, wantMessage: key,
		},
		{
			name:  "partial certificate without evidence is not certified",
			model: soModel(soBool(true), dedicated),
			profile: soProfile(func(p *aiv1alpha2.GPUProfile) {
				soCertify(soCertifiedImage)(p)
				delete(p.Annotations, aiv1alpha2.BackendCapabilityCertificateEvidenceAnnotationKey(backend.NameVLLM, aiv1alpha2.VLLMCapabilityStructuredOutputs))
			}),
			backend: backend.NameVLLM, arch: "gfx1100", image: soCertifiedImage,
			wantReq: true, wantReason: aiv1alpha2.ReasonStructuredOutputNotCertified, wantMessage: "no complete",
		},
		{
			name:  "partial certificate with a malformed since is not certified",
			model: soModel(soBool(true), dedicated),
			profile: soProfile(func(p *aiv1alpha2.GPUProfile) {
				soCertify(soCertifiedImage)(p)
				p.Annotations[aiv1alpha2.BackendCapabilityCertificateSinceAnnotationKey(backend.NameVLLM, aiv1alpha2.VLLMCapabilityStructuredOutputs)] = "yesterday"
			}),
			backend: backend.NameVLLM, arch: "gfx1100", image: soCertifiedImage,
			wantReq: true, wantReason: aiv1alpha2.ReasonStructuredOutputNotCertified, wantMessage: "no complete",
		},
		{
			name:    "tag-only certificate is not certified",
			model:   soModel(soBool(true), dedicated),
			backend: backend.NameVLLM, profile: soProfile(soCertify("registry.harbor.lan/flexinfer/runtime:rocm-gfx1100")), arch: "gfx1100", image: soCertifiedImage,
			wantReq: true, wantReason: aiv1alpha2.ReasonStructuredOutputNotCertified, wantMessage: "certificate on GPUProfile gfx1100 is not digest-pinned",
		},
		{
			name:    "certificate for another image is not certified",
			model:   soModel(soBool(true), dedicated),
			backend: backend.NameVLLM, profile: soProfile(soCertify(soOtherImage)), arch: "gfx1100", image: soCertifiedImage,
			wantReq: true, wantReason: aiv1alpha2.ReasonStructuredOutputNotCertified, wantMessage: "but this Model selects",
		},
		{
			name:    "certificate bound to the selected image renders",
			model:   soModel(soBool(true), dedicated),
			backend: backend.NameVLLM, profile: soProfile(soCertify(soCertifiedImage)), arch: "gfx1100", image: soCertifiedImage,
			wantReq: true, wantRender: true, wantReason: aiv1alpha2.ReasonStructuredOutputCertified, wantMessage: "evidence: " + soEvidence,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateStructuredOutput(tc.model, tc.backend, tc.profile, tc.arch, tc.image)
			require.Equal(t, tc.wantReq, got.Requested)
			require.Equal(t, tc.wantRender, got.Render)
			require.Equal(t, tc.wantReason, got.Reason)
			if tc.wantMessage != "" {
				require.Contains(t, got.Message, tc.wantMessage)
			}
			if tc.wantReq && !tc.wantRender {
				require.Contains(t, got.Message, "rendered")
			}
		})
	}
}

// A7: the gate never certifies LM-Format-Enforcer (deprecated in vLLM 0.33)
// or any backend KT-SO did not prove, even when a complete certificate binds
// the selected image.
func TestEvaluateStructuredOutputRefusesUncertifiedBackends(t *testing.T) {
	model := soModel(soBool(true), map[string]any{"dedicatedDeployment": true})
	profile := soProfile(soCertify(soCertifiedImage))

	auto := evaluateStructuredOutputBackend(model, backend.NameVLLM, profile, "gfx1100", soCertifiedImage, backend.VLLMStructuredOutputsBackendAuto)
	require.True(t, auto.Render)
	require.Equal(t, backend.VLLMStructuredOutputsBackendAuto, auto.Backend)
	require.Equal(t, auto, evaluateStructuredOutput(model, backend.NameVLLM, profile, "gfx1100", soCertifiedImage))

	for _, tc := range []struct {
		soBackend   string
		wantMessage string
	}{
		{backend.VLLMStructuredOutputsBackendLMFormatEnforcer, "LM-Format-Enforcer) is never certified: vLLM 0.33 deprecates it"},
		{"lm_format_enforcer", "is never certified"},
		{"xgrammar", `proves backend "auto" only`},
	} {
		t.Run(tc.soBackend, func(t *testing.T) {
			got := evaluateStructuredOutputBackend(model, backend.NameVLLM, profile, "gfx1100", soCertifiedImage, tc.soBackend)
			require.True(t, got.Requested)
			require.False(t, got.Render)
			require.Empty(t, got.Backend)
			require.Equal(t, aiv1alpha2.ReasonStructuredOutputNotCertified, got.Reason)
			require.Contains(t, got.Message, tc.wantMessage)
			require.Contains(t, got.Message, "--structured-outputs-config not rendered")
		})
	}
}

func TestApplyStructuredOutputCondition(t *testing.T) {
	model := soModel(soBool(true), map[string]any{"dedicatedDeployment": true})

	applyStructuredOutputCondition(model, evaluateStructuredOutput(model, backend.NameVLLM, soProfile(nil), "gfx1100", soCertifiedImage))
	cond := meta.FindStatusCondition(model.Status.Conditions, aiv1alpha2.ConditionStructuredOutput)
	require.NotNil(t, cond)
	require.Equal(t, metav1.ConditionFalse, cond.Status)
	require.Equal(t, aiv1alpha2.ReasonStructuredOutputNotCertified, cond.Reason)

	applyStructuredOutputCondition(model, evaluateStructuredOutput(model, backend.NameVLLM, soProfile(soCertify(soCertifiedImage)), "gfx1100", soCertifiedImage))
	cond = meta.FindStatusCondition(model.Status.Conditions, aiv1alpha2.ConditionStructuredOutput)
	require.NotNil(t, cond)
	require.Equal(t, metav1.ConditionTrue, cond.Status)
	require.Equal(t, aiv1alpha2.ReasonStructuredOutputCertified, cond.Reason)

	// Dropping the opt-in removes the condition instead of leaving a stale one.
	model.Spec.Capabilities.StructuredOutput = soBool(false)
	applyStructuredOutputCondition(model, evaluateStructuredOutput(model, backend.NameVLLM, soProfile(soCertify(soCertifiedImage)), "gfx1100", soCertifiedImage))
	require.Nil(t, meta.FindStatusCondition(model.Status.Conditions, aiv1alpha2.ConditionStructuredOutput))
}

// TestStructuredOutputDeploymentRender renders the production workhorse
// manifest through ensureDeployment. The flag appears only for an opted-in
// copy whose image the GPUProfile certifies; the manifest as shipped never
// renders it, certificate or not, so D4 is live-neutral at merge.
func TestStructuredOutputDeploymentRender(t *testing.T) {
	workhorse := loadDeployModelManifest(t, filepath.Join("..", "deploy", "models", "qwen38-27b-autoround-workhorse.yaml"))
	require.Equal(t, soCertifiedImage, workhorse.Spec.Image, "fixture image must be the workhorse's pinned digest")
	optedIn := workhorse.DeepCopy()
	optedIn.Spec.Capabilities = &aiv1alpha2.ModelCapabilities{StructuredOutput: soBool(true)}

	cases := []struct {
		name    string
		model   *aiv1alpha2.Model
		profile *aiv1alpha2.GPUProfile
		want    bool
	}{
		{name: "shipped workhorse, certified profile", model: workhorse, profile: soProfile(soCertify(soCertifiedImage)), want: false},
		{name: "opted in, no certificate", model: optedIn, profile: soProfile(nil), want: false},
		{name: "opted in, certificate for another image", model: optedIn, profile: soProfile(soCertify(soOtherImage)), want: false},
		{name: "opted in, certificate for its image", model: optedIn, profile: soProfile(soCertify(soCertifiedImage)), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := renderStructuredOutputArgs(t, tc.model, tc.profile)
			count := 0
			for i, arg := range args {
				if arg == "--structured-outputs-config" {
					count++
					require.Less(t, i+1, len(args))
					require.Equal(t, `{"backend":"auto"}`, args[i+1])
				}
			}
			if tc.want {
				require.Equal(t, 1, count, "args: %s", strings.Join(args, " "))
			} else {
				require.Zero(t, count, "args: %s", strings.Join(args, " "))
			}
		})
	}
}

func renderStructuredOutputArgs(t *testing.T, model *aiv1alpha2.Model, profile *aiv1alpha2.GPUProfile) []string {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha2.AddToScheme(s))
	c := fake.NewClientBuilder().WithScheme(s).Build()
	profiles := &GPUProfileReconciler{}
	profiles.Prime(profile)
	r := &ModelReconciler{Client: c, Scheme: s, GPUProfiles: profiles}
	b, ok := backend.Get(model.Spec.Backend)
	require.True(t, ok)
	ctx := context.Background()
	require.NoError(t, r.ensureDeployment(ctx, model, b, backend.GPUVendorAMD, "gfx1100", 1))
	deployment := &appsv1.Deployment{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, deployment))
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if len(container.Args) > 0 {
			return container.Args
		}
	}
	t.Fatalf("rendered Deployment has no container args")
	return nil
}

// TestStructuredOutputCanaryManifest renders the KT-SO paste manifest. Before
// the verdict (no certificate) it must render no flag and report
// NotCertified; once deploy/gpuprofiles/gfx1100.yaml carries the certificate
// for its image, the same manifest renders the flag and reports Certified.
func TestStructuredOutputCanaryManifest(t *testing.T) {
	canary := loadDeployModelManifest(t, filepath.Join("..", "deploy", "debug", "qwen38-27b-so-canary.yaml"))
	require.True(t, structuredOutputRequested(canary))
	require.Equal(t, soCertifiedImage, canary.Spec.Image)

	for _, tc := range []struct {
		name    string
		profile *aiv1alpha2.GPUProfile
		want    bool
		reason  string
	}{
		{"before the verdict", soProfile(nil), false, aiv1alpha2.ReasonStructuredOutputNotCertified},
		{"after the certificate", soProfile(soCertify(soCertifiedImage)), true, aiv1alpha2.ReasonStructuredOutputCertified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision := evaluateStructuredOutput(canary, backend.NameVLLM, tc.profile, "gfx1100", canary.Spec.Image)
			require.Equal(t, tc.want, decision.Render)
			require.Equal(t, tc.reason, decision.Reason)
			args := strings.Join(renderStructuredOutputArgs(t, canary, tc.profile), " ")
			require.Equal(t, tc.want, strings.Contains(args, `--structured-outputs-config {"backend":"auto"}`), args)
			require.Contains(t, args, `--speculative-config {"method":"mtp","num_speculative_tokens":2`)
		})
	}
}
