package controllers

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

const (
	diarRuntimeImage = "registry.harbor.lan/flexinfer/runtime@sha256:d5a0d5a0d5a0d5a0d5a0d5a0d5a0d5a0d5a0d5a0d5a0d5a0d5a0d5a0d5a0d5a0"
	diarOtherImage   = "registry.harbor.lan/flexinfer/runtime@sha256:90648c350783694d4858e88a31aefcdf682d171571801905c3ddd64c64c530d7"
	diarEvidence     = ".loom/60-validation-matrix.md#gfx906-diarize-job-2026-11-xx"
	diarManifest     = "pyannote-diarization-radeonvii.yaml"
)

func loadGfx906GPUProfile(t *testing.T) *aiv1alpha2.GPUProfile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "deploy", "gpuprofiles", "gfx906.yaml"))
	require.NoError(t, err)
	profile := &aiv1alpha2.GPUProfile{}
	require.NoError(t, yaml.UnmarshalStrict(raw, profile))
	return profile
}

// diarCertifiedProfile is the gfx906 profile as the KT-DIAR window leaves it:
// the pyannote layer as the runtime image, pyannote bundled, the certificate
// bound to that image.
func diarCertifiedProfile(t *testing.T, certified string) *aiv1alpha2.GPUProfile {
	t.Helper()
	profile := loadGfx906GPUProfile(t).DeepCopy()
	profile.Spec.Runtime.Image = diarRuntimeImage
	if !bundlesBackendExplicitly(profile.Spec.Runtime.BundledBackends, backend.NamePyannote) {
		profile.Spec.Runtime.BundledBackends = append(profile.Spec.Runtime.BundledBackends, backend.NamePyannote)
	}
	if certified != "" {
		aiv1alpha2.SetBackendCapabilityCertificate(profile, backend.NamePyannote, aiv1alpha2.PyannoteCapabilitySpeakerDiarization, certified, diarEvidence)
	}
	return profile
}

func loadDiarizationModel(t *testing.T) *aiv1alpha2.Model {
	t.Helper()
	return loadDeployModelManifest(t, filepath.Join("..", "deploy", "debug", diarManifest))
}

func TestEvaluateBackendCertificate(t *testing.T) {
	key := aiv1alpha2.BackendCapabilityCertificateAnnotationKey(backend.NamePyannote, aiv1alpha2.PyannoteCapabilitySpeakerDiarization)
	require.Equal(t, "flexinfer.ai/backend-certificate-pyannote-speaker-diarization", key)

	lane := loadDiarizationModel(t)
	withConfig := func(edit func(cfg map[string]any)) *aiv1alpha2.Model {
		m := lane.DeepCopy()
		cfg := map[string]any{}
		for k, v := range lane.Spec.GetConfigMap() {
			cfg[k] = v
		}
		edit(cfg)
		m.Spec.Config = mustJSONConfig(cfg)
		return m
	}
	partial := diarCertifiedProfile(t, diarRuntimeImage)
	delete(partial.Annotations, aiv1alpha2.BackendCapabilityCertificateEvidenceAnnotationKey(backend.NamePyannote, aiv1alpha2.PyannoteCapabilitySpeakerDiarization))
	notBundled := diarCertifiedProfile(t, diarRuntimeImage)
	notBundled.Spec.Runtime.BundledBackends = []string{"llamacpp", "ollama", "diffusers"}
	legacyAll := diarCertifiedProfile(t, diarRuntimeImage)
	legacyAll.Spec.Runtime.BundledBackends = nil
	tagged := diarCertifiedProfile(t, "registry.harbor.lan/flexinfer/runtime:rocm-gfx906")
	tagged.Spec.Runtime.Image = "registry.harbor.lan/flexinfer/runtime:rocm-gfx906"

	cases := []struct {
		name          string
		model         *aiv1alpha2.Model
		backend       string
		profile       *aiv1alpha2.GPUProfile
		arch          string
		wantRequired  bool
		wantCertified bool
		wantReason    string
		wantMessage   string
	}{
		{name: "other backends are not gated", model: lane, backend: backend.NameLlamaCpp, profile: loadGfx906GPUProfile(t), arch: "gfx906"},
		{name: "the shipped gfx906 profile bundles pyannote but refuses it uncertified", model: lane, backend: backend.NamePyannote, profile: loadGfx906GPUProfile(t), arch: "gfx906",
			wantRequired: true, wantReason: aiv1alpha2.ReasonBackendNotCertified, wantMessage: "no complete " + key},
		{name: "no profile", model: lane, backend: backend.NamePyannote, arch: "gfx906",
			wantRequired: true, wantReason: aiv1alpha2.ReasonBackendNotCertified, wantMessage: "no GPUProfile for gfx906"},
		{name: "no arch", model: lane, backend: backend.NamePyannote, profile: diarCertifiedProfile(t, diarRuntimeImage),
			wantRequired: true, wantReason: aiv1alpha2.ReasonBackendNotCertified, wantMessage: "no GPU architecture"},
		{name: "bundled but no certificate", model: lane, backend: backend.NamePyannote, profile: diarCertifiedProfile(t, ""), arch: "gfx906",
			wantRequired: true, wantReason: aiv1alpha2.ReasonBackendNotCertified, wantMessage: "no complete " + key},
		{name: "partial triple", model: lane, backend: backend.NamePyannote, profile: partial, arch: "gfx906",
			wantRequired: true, wantReason: aiv1alpha2.ReasonBackendNotCertified, wantMessage: "no complete " + key},
		{name: "certificate for another image", model: lane, backend: backend.NamePyannote, profile: diarCertifiedProfile(t, diarOtherImage), arch: "gfx906",
			wantRequired: true, wantReason: aiv1alpha2.ReasonBackendNotCertified, wantMessage: "but the profile's runtime image is"},
		{name: "certified but not bundled", model: lane, backend: backend.NamePyannote, profile: notBundled, arch: "gfx906",
			wantRequired: true, wantReason: aiv1alpha2.ReasonBackendNotCertified, wantMessage: "does not list pyannote"},
		{name: "an empty bundled list is not an explicit listing", model: lane, backend: backend.NamePyannote, profile: legacyAll, arch: "gfx906",
			wantRequired: true, wantReason: aiv1alpha2.ReasonBackendNotCertified, wantMessage: "does not list pyannote"},
		{name: "tag-pinned runtime image", model: lane, backend: backend.NamePyannote, profile: tagged, arch: "gfx906",
			wantRequired: true, wantReason: aiv1alpha2.ReasonBackendNotCertified, wantMessage: "is not digest-pinned"},
		{name: "dedicated Deployment", model: withConfig(func(cfg map[string]any) { cfg["dedicatedDeployment"] = true }), backend: backend.NamePyannote,
			profile: diarCertifiedProfile(t, diarRuntimeImage), arch: "gfx906",
			wantRequired: true, wantReason: aiv1alpha2.ReasonBackendUnsupported, wantMessage: "runs only inside the persistent runtime pod"},
		{name: "a token Secret name that is not a name", model: withConfig(func(cfg map[string]any) { cfg["hfTokenSecret"] = "../token" }), backend: backend.NamePyannote,
			profile: diarCertifiedProfile(t, diarRuntimeImage), arch: "gfx906",
			wantRequired: true, wantReason: aiv1alpha2.ReasonConfigInvalid, wantMessage: "is not a Secret name"},
		{name: "certified", model: lane, backend: backend.NamePyannote, profile: diarCertifiedProfile(t, diarRuntimeImage), arch: "gfx906",
			wantRequired: true, wantCertified: true, wantReason: aiv1alpha2.ReasonBackendCertified, wantMessage: "evidence: " + diarEvidence},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateBackendCertificate(tc.model, tc.backend, tc.profile, tc.arch)
			assert.Equal(t, tc.wantRequired, got.Required)
			assert.Equal(t, tc.wantCertified, got.Certified)
			assert.Equal(t, tc.wantReason, got.Reason)
			assert.Contains(t, got.Message, tc.wantMessage)
		})
	}
}

func TestApplyBackendCertificateCondition(t *testing.T) {
	lane := loadDiarizationModel(t)

	applyBackendCertificateCondition(lane, evaluateBackendCertificate(lane, backend.NamePyannote, loadGfx906GPUProfile(t), "gfx906"))
	cond := meta.FindStatusCondition(lane.Status.Conditions, aiv1alpha2.ConditionBackendCertificate)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, aiv1alpha2.ReasonBackendNotCertified, cond.Reason)

	applyBackendCertificateCondition(lane, evaluateBackendCertificate(lane, backend.NamePyannote, diarCertifiedProfile(t, diarRuntimeImage), "gfx906"))
	cond = meta.FindStatusCondition(lane.Status.Conditions, aiv1alpha2.ConditionBackendCertificate)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, aiv1alpha2.ReasonBackendCertified, cond.Reason)

	// A backend the gate does not cover carries no condition.
	applyBackendCertificateCondition(lane, evaluateBackendCertificate(lane, backend.NameLlamaCpp, diarCertifiedProfile(t, diarRuntimeImage), "gfx906"))
	assert.Nil(t, meta.FindStatusCondition(lane.Status.Conditions, aiv1alpha2.ConditionBackendCertificate))
}

// TestBackendCertificateDecisionUsesTheProfileCache runs the gate the way
// reconcile does, through the GPUProfile cache.
func TestBackendCertificateDecisionUsesTheProfileCache(t *testing.T) {
	lane := loadDiarizationModel(t)
	b, ok := backend.Get(lane.Spec.Backend)
	require.True(t, ok)

	shipped := &GPUProfileReconciler{}
	shipped.Prime(loadGfx906GPUProfile(t))
	got := (&ModelReconciler{GPUProfiles: shipped}).backendCertificateDecisionFor(lane, b, "gfx906")
	assert.True(t, got.Required)
	assert.False(t, got.Certified, "the gfx906 profile on master must refuse pyannote until KT-DIAR certifies the runtime image")

	certified := &GPUProfileReconciler{}
	certified.Prime(diarCertifiedProfile(t, diarRuntimeImage))
	got = (&ModelReconciler{GPUProfiles: certified}).backendCertificateDecisionFor(lane, b, "gfx906")
	assert.True(t, got.Certified, got.Message)
}

// TestDiarizationManifestContract pins what the window plan relies on: the
// D5-a lane is a scale-to-zero pyannote lane on the Radeon VII below the
// whisper.cpp lane, declares the measured 3 queues, reads the HF token by
// Secret reference and serves only the diarization path.
func TestDiarizationManifestContract(t *testing.T) {
	lane := loadDiarizationModel(t)
	assert.Equal(t, backend.NamePyannote, lane.Spec.Backend)
	assert.Equal(t, "HF://"+backend.DefaultPyannotePipeline, lane.Spec.Source)
	assert.Equal(t, backend.PyannoteDiarizationPath, lane.Annotations["flexinfer.ai/serve-paths"])
	require.NotNil(t, lane.Spec.GPU)
	require.NotNil(t, lane.Spec.GPU.HWQueueEstimate)
	assert.Equal(t, int32(3), *lane.Spec.GPU.HWQueueEstimate)
	assert.Equal(t, "radeonvii-models", lane.Spec.GPU.Shared)
	require.NotNil(t, lane.Spec.GPU.Priority)
	assert.Less(t, *lane.Spec.GPU.Priority, int32(111), "below the whisper.cpp lane, so a Humanizer burst evicts diarization first")
	assert.Equal(t, "cblevins-radeonvii", lane.Spec.NodeSelector["kubernetes.io/hostname"])
	name, key, err := backend.PyannoteHFTokenSecret(lane.Spec.GetConfigMap())
	require.NoError(t, err)
	assert.Equal(t, "flexinfer-hf-token", name)
	assert.Equal(t, "HF_TOKEN", key)
	assert.Nil(t, lane.Spec.Cache, "pyannote pulls from the hub; a Local cache block deadlocks runtime eligibility")
	assert.False(t, lane.Spec.ConfigBool("dedicatedDeployment", false))
}

// TestHWQueueBoundAdmitsTheD5aVoiceSet: with the warm plane, the whisper.cpp
// lane and the pyannote lane all Active the Radeon VII holds 18 of 24 queues,
// within the budget both before the pyannote Deployment retires
// (reservedHWQueues 2) and after (0), and a Humanizer burst evicts the voice
// lanes, never gte.
func TestHWQueueBoundAdmitsTheD5aVoiceSet(t *testing.T) {
	now := time.Now()
	diar := loadDiarizationModel(t)
	for _, reserved := range []int32{2, 0} {
		profile := gfx906QueueProfile()
		profile.ReservedHWQueues = reserved
		queues := gfx906QueueBound(profile)
		group := radeonviiGroup(now, "whisper-large-v3-turbo-radeonvii", diar.Name)
		whisper := radeonviiLane("whisper-large-v3-turbo-radeonvii", backend.NameWhisperCpp, 111, 1500, false, aiv1alpha2.ModelPhaseIdle, timePtr(now))
		pyannote := radeonviiLane(diar.Name, backend.NamePyannote, *diar.Spec.GPU.Priority, *diar.Spec.GPU.VRAMEstimateMB, false, aiv1alpha2.ModelPhaseIdle, timePtr(now))
		pyannote.Spec.GPU.HWQueueEstimate = diar.Spec.GPU.HWQueueEstimate
		group = append(group, whisper, pyannote)

		bounded := chooseSharedGroupLeadersBounded(group, now, true, profile.VRAMMB, queues, nil, false)
		got := names(bounded)
		for _, want := range []string{"bge-large-radeonvii", "bge-reranker-radeonvii", "gte-qwen2-1p5b-radeonvii", "orpheus-3b-radeonvii", whisper.Name, pyannote.Name} {
			assert.True(t, got[want], "reserved %d: %s not elected (%v)", reserved, want, got)
		}
		assert.Equal(t, int32(18), queues.sum(bounded), "reserved %d", reserved)
		assert.LessOrEqual(t, queues.sum(bounded), queues.limit)

		burst := append(radeonviiGroup(now, "humanizer"), whisper, pyannote)
		got = names(chooseSharedGroupLeadersBounded(burst, now, true, profile.VRAMMB, queues, nil, false))
		assert.True(t, got["gte-qwen2-1p5b-radeonvii"], "reserved %d: a Humanizer burst must keep gte", reserved)
		assert.False(t, got[pyannote.Name], "reserved %d: a Humanizer burst evicts diarization", reserved)
	}
}
