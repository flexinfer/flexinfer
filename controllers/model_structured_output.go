package controllers

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// structuredOutputsBackendAuto is the only structured-output backend the D4
// certificate covers: vLLM picks xgrammar or guidance per request. Another
// backend would be another envelope with its own kill-test.
const structuredOutputsBackendAuto = "auto"

// structuredOutputDecision is the outcome of the D4 certificate gate for one
// Model on one GPU architecture and selected image.
type structuredOutputDecision struct {
	// Requested is true when spec.capabilities.structuredOutput is true.
	Requested bool
	// Render is true when --structured-outputs-config may be rendered.
	Render  bool
	Reason  string
	Message string
}

// structuredOutputRequested reports whether the Model opts into the certified
// structured-output envelope.
func structuredOutputRequested(model *aiv1alpha2.Model) bool {
	if model == nil || model.Spec.Capabilities == nil || model.Spec.Capabilities.StructuredOutput == nil {
		return false
	}
	return *model.Spec.Capabilities.StructuredOutput
}

// evaluateStructuredOutput applies the vllm/structured-outputs certificate
// gate. It mirrors validateLlamaCppFeatureCertificates (dedicated Deployment,
// digest-pinned selected image, complete certificate triple bound to that
// exact image) but never fails the reconcile: an uncertified opt-in renders
// nothing and is reported through the StructuredOutput condition, so the lane
// keeps serving exactly as it would without the opt-in.
func evaluateStructuredOutput(model *aiv1alpha2.Model, backendName string, profile *aiv1alpha2.GPUProfile, gpuArch, selectedImage string) structuredOutputDecision {
	if !structuredOutputRequested(model) {
		return structuredOutputDecision{}
	}
	notCertified := func(format string, args ...any) structuredOutputDecision {
		return structuredOutputDecision{
			Requested: true,
			Reason:    aiv1alpha2.ReasonStructuredOutputNotCertified,
			Message:   fmt.Sprintf(format, args...) + "; --structured-outputs-config not rendered",
		}
	}
	if backendName != backend.NameVLLM {
		return structuredOutputDecision{
			Requested: true,
			Reason:    aiv1alpha2.ReasonStructuredOutputUnsupported,
			Message:   fmt.Sprintf("spec.capabilities.structuredOutput is certified only for the vllm backend, not %q; nothing rendered", backendName),
		}
	}
	// Runtime images bundle their own vLLM, and runtime-pod loads build their
	// launch from the load payload, which cannot carry the certified flag.
	// The certificate binds the dedicated backend image the controller can
	// verify, as for the llama.cpp certified features.
	if !model.Spec.ConfigBool("dedicatedDeployment", false) {
		return structuredOutputDecision{
			Requested: true,
			Reason:    aiv1alpha2.ReasonStructuredOutputUnsupported,
			Message:   "spec.capabilities.structuredOutput requires spec.config.dedicatedDeployment=true so the certified image is launched instead of a persistent runtime; nothing rendered",
		}
	}
	if gpuArch == "" {
		return notCertified("no GPU architecture resolved for the structured-output certificate")
	}
	if profile == nil {
		return notCertified("no GPUProfile for %s carries a vllm/%s certificate", gpuArch, aiv1alpha2.VLLMCapabilityStructuredOutputs)
	}
	if !strings.Contains(selectedImage, "@sha256:") {
		return notCertified("the selected image on %s is not digest-pinned (%q)", gpuArch, selectedImage)
	}
	certifiedImage, since, evidence := aiv1alpha2.GetBackendCapabilityCertificate(profile, backend.NameVLLM, aiv1alpha2.VLLMCapabilityStructuredOutputs)
	key := aiv1alpha2.BackendCapabilityCertificateAnnotationKey(backend.NameVLLM, aiv1alpha2.VLLMCapabilityStructuredOutputs)
	if certifiedImage == "" || since.IsZero() || strings.TrimSpace(evidence) == "" {
		return notCertified("GPUProfile %s has no complete %s certificate (the annotation and its -since/-evidence companions are all required)", profile.Name, key)
	}
	if !strings.Contains(certifiedImage, "@sha256:") {
		return notCertified("the %s certificate on GPUProfile %s is not digest-pinned (%q)", key, profile.Name, certifiedImage)
	}
	if certifiedImage != selectedImage {
		return notCertified("the %s certificate on GPUProfile %s covers %q, but this Model selects %q", key, profile.Name, certifiedImage, selectedImage)
	}
	return structuredOutputDecision{
		Requested: true,
		Render:    true,
		Reason:    aiv1alpha2.ReasonStructuredOutputCertified,
		Message: fmt.Sprintf("--structured-outputs-config backend %s rendered: GPUProfile %s certifies %s since %s (evidence: %s)",
			structuredOutputsBackendAuto, profile.Name, certifiedImage, since.UTC().Format("2006-01-02T15:04:05Z"), evidence),
	}
}

// structuredOutputDecisionFor resolves the GPUProfile and the image this
// Model would launch, then applies the gate.
func (r *ModelReconciler) structuredOutputDecisionFor(model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor, gpuArch string) structuredOutputDecision {
	if !structuredOutputRequested(model) {
		return structuredOutputDecision{}
	}
	var profile *aiv1alpha2.GPUProfile
	var profileSpec *aiv1alpha2.GPUProfileSpec
	if r.GPUProfiles != nil && gpuArch != "" {
		profile, _ = r.GPUProfiles.LookupProfile(gpuArch)
		profileSpec, _ = r.GPUProfiles.Lookup(gpuArch)
	}
	image := resolveModelBackendImage(model, b, profileSpec, gpuVendor, gpuArch)
	return evaluateStructuredOutput(model, b.Name(), profile, gpuArch, image)
}

// applyStructuredOutputCondition records the gate outcome on the Model status:
// True when the flag renders, False with the reason when it does not, and no
// condition at all when the Model does not ask for structured outputs.
func applyStructuredOutputCondition(model *aiv1alpha2.Model, decision structuredOutputDecision) {
	if model == nil {
		return
	}
	if !decision.Requested {
		meta.RemoveStatusCondition(&model.Status.Conditions, aiv1alpha2.ConditionStructuredOutput)
		return
	}
	setModelCondition(model, aiv1alpha2.ConditionStructuredOutput, decision.Render, decision.Reason, decision.Message)
}
