package controllers

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// certificateGatedBackends maps each backend the controller serves only behind
// a GPUProfile certificate to the capability that certifies it. Unlike the
// opt-in feature certificates (llama.cpp slots and drafts, vLLM structured
// outputs), the certificate here gates the backend itself: without it the
// Model is refused, so an uncertified image never loads it.
var certificateGatedBackends = map[string]string{
	// D5-a: pyannote needs pyannote.audio and torch kernels for the card in
	// the runtime image, which only the KT-DIAR window proves.
	backend.NamePyannote: aiv1alpha2.PyannoteCapabilitySpeakerDiarization,
}

// backendCertificateDecision is the outcome of the backend certificate gate
// for one Model on one GPU architecture.
type backendCertificateDecision struct {
	// Required is true for a certificate-gated backend.
	Required bool
	// Certified is true when the Model may be served.
	Certified bool
	Reason    string
	Message   string
}

// evaluateBackendCertificate applies the gate. A gated backend is a runtime
// subprocess with no image of its own, so the certified artifact is the
// persistent runtime image the GPUProfile declares (spec.runtime.image, kept
// equal to the runtime DaemonSet's image by the runtime_pin_drift check). The
// profile must list the backend in spec.runtime.bundledBackends explicitly
// (an empty list means "all" for legacy profiles and is not enough here), and
// carry a complete, digest-pinned certificate triple bound to that image.
func evaluateBackendCertificate(model *aiv1alpha2.Model, backendName string, profile *aiv1alpha2.GPUProfile, gpuArch string) backendCertificateDecision {
	capability, gated := certificateGatedBackends[backendName]
	if !gated || model == nil {
		return backendCertificateDecision{}
	}
	refuse := func(reason, format string, args ...any) backendCertificateDecision {
		return backendCertificateDecision{Required: true, Reason: reason, Message: fmt.Sprintf(format, args...)}
	}
	notCertified := func(format string, args ...any) backendCertificateDecision {
		return refuse(aiv1alpha2.ReasonBackendNotCertified, format+"; the %s backend is not served until the GPUProfile certifies the runtime image", append(args, backendName)...)
	}
	if backendName == backend.NamePyannote {
		if _, _, err := backend.PyannoteHFTokenSecret(model.Spec.GetConfigMap()); err != nil {
			return refuse(aiv1alpha2.ReasonConfigInvalid, "%v", err)
		}
	}
	if model.Spec.ConfigBool("dedicatedDeployment", false) {
		return refuse(aiv1alpha2.ReasonBackendUnsupported,
			"the %s backend runs only inside the persistent runtime pod and has no image of its own; unset spec.config.dedicatedDeployment", backendName)
	}
	if model.Spec.GPU != nil && model.Spec.GetGPUVendor() == aiv1alpha2.GPUVendorCPU {
		return refuse(aiv1alpha2.ReasonBackendUnsupported,
			"the %s backend is certified per GPU runtime image; a gpu.vendor: cpu Model has no runtime to certify", backendName)
	}
	if gpuArch == "" {
		return notCertified("no GPU architecture resolved for the %s/%s certificate", backendName, capability)
	}
	if profile == nil {
		return notCertified("no GPUProfile for %s carries a %s/%s certificate", gpuArch, backendName, capability)
	}
	runtime := profile.Spec.Runtime
	if runtime == nil || !bundlesBackendExplicitly(runtime.BundledBackends, backendName) {
		return notCertified("GPUProfile %s does not list %s in spec.runtime.bundledBackends", profile.Name, backendName)
	}
	runtimeImage := strings.TrimSpace(runtime.Image)
	if !strings.Contains(runtimeImage, "@sha256:") {
		return notCertified("GPUProfile %s spec.runtime.image is not digest-pinned (%q)", profile.Name, runtimeImage)
	}
	certifiedImage, since, evidence := aiv1alpha2.GetBackendCapabilityCertificate(profile, backendName, capability)
	key := aiv1alpha2.BackendCapabilityCertificateAnnotationKey(backendName, capability)
	if certifiedImage == "" || since.IsZero() || strings.TrimSpace(evidence) == "" {
		return notCertified("GPUProfile %s has no complete %s certificate (the annotation and its -since/-evidence companions are all required)", profile.Name, key)
	}
	if !strings.Contains(certifiedImage, "@sha256:") {
		return notCertified("the %s certificate on GPUProfile %s is not digest-pinned (%q)", key, profile.Name, certifiedImage)
	}
	if certifiedImage != runtimeImage {
		return notCertified("the %s certificate on GPUProfile %s covers %q, but the profile's runtime image is %q", key, profile.Name, certifiedImage, runtimeImage)
	}
	return backendCertificateDecision{
		Required:  true,
		Certified: true,
		Reason:    aiv1alpha2.ReasonBackendCertified,
		Message: fmt.Sprintf("GPUProfile %s certifies %s/%s on runtime image %s since %s (evidence: %s)",
			profile.Name, backendName, capability, certifiedImage, since.UTC().Format("2006-01-02T15:04:05Z"), evidence),
	}
}

func bundlesBackendExplicitly(bundled []string, backendName string) bool {
	for _, name := range bundled {
		if strings.EqualFold(strings.TrimSpace(name), backendName) {
			return true
		}
	}
	return false
}

// backendCertificateDecisionFor resolves the GPUProfile for gpuArch and
// applies the gate.
func (r *ModelReconciler) backendCertificateDecisionFor(model *aiv1alpha2.Model, b backend.Backend, gpuArch string) backendCertificateDecision {
	if _, gated := certificateGatedBackends[b.Name()]; !gated {
		return backendCertificateDecision{}
	}
	var profile *aiv1alpha2.GPUProfile
	if r.GPUProfiles != nil && gpuArch != "" {
		profile, _ = r.GPUProfiles.LookupProfile(gpuArch)
	}
	return evaluateBackendCertificate(model, b.Name(), profile, gpuArch)
}

// applyBackendCertificateCondition records the gate outcome: True when the
// backend may serve, False with the reason when it may not, and no condition
// for backends the gate does not cover.
func applyBackendCertificateCondition(model *aiv1alpha2.Model, decision backendCertificateDecision) {
	if model == nil {
		return
	}
	if !decision.Required {
		meta.RemoveStatusCondition(&model.Status.Conditions, aiv1alpha2.ConditionBackendCertificate)
		return
	}
	setModelCondition(model, aiv1alpha2.ConditionBackendCertificate, decision.Certified, decision.Reason, decision.Message)
}
