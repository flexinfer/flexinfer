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
	"fmt"
	"math"
	"path"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/constants"
	"github.com/flexinfer/flexinfer/pkg/gpu"
)

// isMaxwellGPUArch delegates to gpu.IsMaxwellArch.
func isMaxwellGPUArch(gpuArch string) bool {
	return gpu.IsMaxwellArch(gpuArch)
}

// validateVRAMFit checks whether the model's declared VRAM estimate fits the GPU capacity.
// Skips validation if no estimate is provided (backward compatible).
func (r *ModelReconciler) validateVRAMFit(model *aiv1alpha2.Model, b backend.Backend, gpuArch string) error {
	if model.Spec.GPU == nil || model.Spec.GPU.VRAMEstimateMB == nil || *model.Spec.GPU.VRAMEstimateMB <= 0 {
		return nil
	}

	// Resolve max VRAM via the GPUProfile-first cascade. Profile.VRAMMB wins;
	// falls through to BackendGPUCompatibility for nodes without a profile.
	var profileSpec *aiv1alpha2.GPUProfileSpec
	if r.GPUProfiles != nil {
		if cachedProfile, ok := r.GPUProfiles.LookupProfile(gpuArch); ok {
			profileSpec = &cachedProfile.Spec
		} else if cachedSpec, ok := r.GPUProfiles.Lookup(gpuArch); ok {
			profileSpec = cachedSpec
		}
	}
	var maxVRAMMB int
	if profileSpec != nil && profileSpec.VRAMMB > 0 {
		maxVRAMMB = int(profileSpec.VRAMMB)
	}
	if maxVRAMMB == 0 {
		support, found := backend.ResolveBackendGPUSupport(profileSpec, b.Name(), gpuArch)
		if !found || support.MaxVRAMMB <= 0 {
			return nil
		}
		maxVRAMMB = support.MaxVRAMMB
	}

	estimateMB := *model.Spec.GPU.VRAMEstimateMB
	gpuCount := int64(1)
	if model.Spec.GPU.Count != nil && *model.Spec.GPU.Count > 1 {
		gpuCount = int64(*model.Spec.GPU.Count)
	}
	totalVRAMMB := int64(maxVRAMMB) * gpuCount

	// Block if estimate exceeds 95% of total VRAM
	if estimateMB > totalVRAMMB*95/100 {
		return fmt.Errorf("model VRAM estimate (%dMB) exceeds 95%% of available GPU VRAM (%dMB across %d GPU(s)) for %s on %s",
			estimateMB, totalVRAMMB, gpuCount, b.Name(), gpuArch)
	}

	// Warn if estimate exceeds 80% of total VRAM. Throttled per model: the
	// pressure is a static spec property, so emitting it on every reconcile only
	// floods the event stream (and historically rode a reconcile hot loop to
	// thousands of duplicate events/day).
	if estimateMB > totalVRAMMB*80/100 && r.shouldEmitVRAMPressure(model) {
		r.Recorder.Event(model, corev1.EventTypeWarning, "VRAMPressure",
			fmt.Sprintf("model VRAM estimate (%dMB) exceeds 80%% of GPU VRAM (%dMB); performance may be degraded",
				estimateMB, totalVRAMMB))
	}

	return nil
}

// shouldEmitVRAMPressure reports whether the VRAMPressure event for this model
// is outside its cooldown; see shouldEmitThrottledEvent.
func (r *ModelReconciler) shouldEmitVRAMPressure(model *aiv1alpha2.Model) bool {
	return r.shouldEmitThrottledEvent(model, "VRAMPressure", vramPressureEventCooldown)
}

// shouldEmitThrottledEvent reports whether enough time has elapsed since the
// last event with this reason for this model to emit another, recording the
// emit time when it returns true. Keyed by reason and UID, so reasons throttle
// independently and a delete+recreate re-arms immediately.
func (r *ModelReconciler) shouldEmitThrottledEvent(model *aiv1alpha2.Model, reason string, cooldown time.Duration) bool {
	key := reason + "/" + string(model.UID)
	now := time.Now()
	if v, ok := r.throttledEventLastEmit.Load(key); ok {
		if last, ok := v.(time.Time); ok && now.Sub(last) < cooldown {
			return false
		}
	}
	r.throttledEventLastEmit.Store(key, now)
	return true
}

// validateBackendGPUCompatibility checks if the backend is compatible with the target GPU arch.
// Uses the GPU compatibility matrix for data-driven validation with fallback to architecture-specific checks.
func (r *ModelReconciler) validateBackendGPUCompatibility(model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor, gpuArch string) error {
	// Resolve via the GPUProfile-first cascade. profile.Backends[name].Support
	// wins; falls through to BackendGPUCompatibility for nodes without a
	// profile.
	var profileSpec *aiv1alpha2.GPUProfileSpec
	var profile *aiv1alpha2.GPUProfile
	if r.GPUProfiles != nil {
		if cachedProfile, ok := r.GPUProfiles.LookupProfile(gpuArch); ok {
			profile = cachedProfile
			profileSpec = &cachedProfile.Spec
		} else if cachedSpec, ok := r.GPUProfiles.Lookup(gpuArch); ok {
			profileSpec = cachedSpec
		}
	}
	support, found := backend.ResolveBackendGPUSupport(profileSpec, b.Name(), gpuArch)
	if found {
		switch support.Level {
		case backend.SupportUnsupported:
			return fmt.Errorf("%s backend is not supported on %s GPUs. Use a compatible backend instead", b.Name(), gpuArch)
		case backend.SupportExperimental:
			// Only warn if the resolved image is generic (not arch-specific).
			// Resolve via GPUProfile-first so a profile-declared image is
			// recognized as arch-specific even when the in-code rules would
			// fall back to a generic tag.
			img := backend.ResolveBackendImage(b, profileSpec, gpuVendor, gpuArch)
			isGenericImage := !strings.Contains(img, "gfx906") && !strings.Contains(img, "gfx110")
			if isGenericImage && r.shouldEmitThrottledEvent(model, "ExperimentalGPUSupport", experimentalGPUSupportEventCooldown) {
				r.Recorder.Event(model, corev1.EventTypeWarning, "ExperimentalGPUSupport",
					fmt.Sprintf("%s on %s is experimental: using generic image %s", b.Name(), gpuArch, img))
			}
		}
	}
	if isCanary, since, evidence := aiv1alpha2.GetBackendCanary(profile, b.Name()); isCanary {
		message := fmt.Sprintf("%s on %s is marked as a canary backend", b.Name(), gpuArch)
		if !since.IsZero() {
			message += fmt.Sprintf(" since %s", since.Format("2006-01-02T15:04:05Z07:00"))
		}
		if strings.TrimSpace(evidence) != "" {
			message += fmt.Sprintf("; evidence: %s", evidence)
		}
		r.Recorder.Event(model, corev1.EventTypeWarning, "BackendCanary", message)
	}

	if b.Name() == backend.NameLlamaCpp {
		if err := validateLlamaCppFlashAttention(model); err != nil {
			return err
		}
		selectedImage := resolveModelBackendImage(model, b, profileSpec, gpuVendor, gpuArch)
		if err := validateLlamaCppFeatureCertificates(model, profile, gpuArch, selectedImage); err != nil {
			return err
		}
	}

	// --- Maxwell-specific validation (sm_5x) ---
	if err := r.validateMaxwellSpecifics(model, b, gpuVendor, gpuArch); err != nil {
		return err
	}

	return nil
}

// validateLlamaCppFlashAttention rejects flashAttention: false next to a
// quantized V cache. The backend renders false as --flash-attn off, and
// llama.cpp refuses that context ("quantized V cache was requested, but this
// requires Flash Attention"), so the load would fail on every attempt.
func validateLlamaCppFlashAttention(model *aiv1alpha2.Model) error {
	if model == nil {
		return nil
	}
	spec := &backend.ModelSpec{Config: model.Spec.GetConfigMap()}
	if flashAttention, set := spec.ConfigOptionalBool("flashAttention"); !set || flashAttention {
		return nil
	}
	switch cacheTypeV := strings.ToLower(strings.TrimSpace(model.Spec.ConfigString("cacheTypeV", ""))); cacheTypeV {
	case "", "f32", "f16", "bf16":
		return nil
	default:
		return fmt.Errorf("spec.config.flashAttention=false cannot be combined with the quantized cacheTypeV %q: llama.cpp requires Flash Attention for a quantized V cache; unset flashAttention or use an f16 V cache", cacheTypeV)
	}
}

func validateLlamaCppFeatureCertificates(model *aiv1alpha2.Model, profile *aiv1alpha2.GPUProfile, gpuArch, selectedImage string) error {
	if model == nil {
		return nil
	}
	cfg := model.Spec.GetConfigMap()
	capabilities, err := requestedLlamaCppCapabilities(cfg)
	if err != nil {
		return err
	}
	if len(capabilities) == 0 || gpuArch == "" {
		return nil
	}

	// Runtime images carry their own bundled backend binary. Until that exact
	// runtime artifact receives a certificate, force opt-ins through the
	// dedicated backend image that the certificate can bind and verify.
	if !model.Spec.ConfigBool("dedicatedDeployment", false) {
		return fmt.Errorf("llamacpp certified features on %s require spec.config.dedicatedDeployment=true so the proven backend image is used instead of an uncertified persistent runtime", gpuArch)
	}
	if profile == nil {
		return fmt.Errorf("llamacpp certified features require a GPUProfile with artifact-bound certificates for %s", gpuArch)
	}
	if !strings.Contains(selectedImage, "@sha256:") {
		return fmt.Errorf("llamacpp certified features require a digest-pinned selected image on %s (got %q)", gpuArch, selectedImage)
	}

	for _, capability := range capabilities {
		certifiedImage, since, evidence := aiv1alpha2.GetBackendCapabilityCertificate(profile, backend.NameLlamaCpp, capability)
		key := aiv1alpha2.BackendCapabilityCertificateAnnotationKey(backend.NameLlamaCpp, capability)
		if certifiedImage == "" || since.IsZero() || strings.TrimSpace(evidence) == "" {
			return fmt.Errorf("llamacpp capability %q is not fully certified for %s; GPUProfile annotation %s and its since/evidence companions are required", capability, gpuArch, key)
		}
		if !strings.Contains(certifiedImage, "@sha256:") {
			return fmt.Errorf("llamacpp capability %q certificate for %s is not digest-pinned: %q", capability, gpuArch, certifiedImage)
		}
		if selectedImage != certifiedImage {
			return fmt.Errorf("llamacpp capability %q on %s is certified only for %q, but this model selects %q", capability, gpuArch, certifiedImage, selectedImage)
		}
	}
	return nil
}

func requestedLlamaCppCapabilities(cfg map[string]any) ([]string, error) {
	if len(cfg) == 0 {
		return nil, nil
	}
	capabilities := make([]string, 0, 2)
	if raw, exists := cfg["slotSavePath"]; exists {
		slotPath, ok := raw.(string)
		if !ok || strings.TrimSpace(slotPath) == "" {
			return nil, fmt.Errorf("spec.config.slotSavePath must be a non-empty string")
		}
		clean := path.Clean(strings.TrimSpace(slotPath))
		if !strings.HasPrefix(clean, "/models/") {
			return nil, fmt.Errorf("spec.config.slotSavePath must be under the persistent /models mount (got %q)", slotPath)
		}
		capabilities = append(capabilities, aiv1alpha2.LlamaCppCapabilityStatefulSlots)
	}

	specType := ""
	if raw, exists := cfg["specType"]; exists {
		value, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("spec.config.specType must be a string")
		}
		specType = strings.TrimSpace(value)
	}
	draftMax, hasDraftMax, err := strictConfigInt(cfg, "draftMax")
	if err != nil {
		return nil, err
	}
	draftMin, hasDraftMin, err := strictConfigInt(cfg, "draftMin")
	if err != nil {
		return nil, err
	}
	// llamacppBuild declares the image's llama.cpp build number. The backend
	// uses it to spell the draft window (b8955 renamed --draft-max/--draft-min),
	// so it must be exact and positive when set.
	build, hasBuild, err := strictConfigInt(cfg, "llamacppBuild")
	if err != nil {
		return nil, err
	}
	if hasBuild && build <= 0 {
		return nil, fmt.Errorf("spec.config.llamacppBuild must be a positive llama.cpp build number (11847 for b11847)")
	}
	renamedDraftFlags := hasBuild && build >= backend.LlamaCppSpecDraftFlagsSinceBuild

	switch specType {
	case "", "none":
		if hasDraftMax || hasDraftMin {
			return nil, fmt.Errorf("spec.config.draftMax and draftMin require spec.config.specType=ngram-simple or draft-mtp")
		}
	case "ngram-simple":
		if renamedDraftFlags {
			// From b8955 ngram-simple is sized by --spec-ngram-simple-size-n/-m
			// and -min-hits; draftMax/draftMin would be silently ignored, so the
			// b8173 envelope cannot carry over.
			return nil, fmt.Errorf("the certified ngram-simple envelope (draftMax=16, draftMin=1) is a llama.cpp b8173 surface: from b%d ngram-simple is sized by --spec-ngram-simple-* and ignores the draft window, so spec.config.llamacppBuild=%d cannot use it", backend.LlamaCppSpecDraftFlagsSinceBuild, build)
		}
		if !hasDraftMax || !hasDraftMin || draftMax != 16 || draftMin != 1 {
			return nil, fmt.Errorf("the certified ngram-simple envelope requires spec.config.draftMax=16 and draftMin=1")
		}
		capabilities = append(capabilities, aiv1alpha2.LlamaCppCapabilityNgramSimpleDraft16)
	case "draft-mtp":
		if hasBuild && !renamedDraftFlags {
			return nil, fmt.Errorf("spec.config.specType=draft-mtp needs llama.cpp b%d or newer, but spec.config.llamacppBuild=%d", backend.LlamaCppSpecDraftFlagsSinceBuild, build)
		}
		if !hasDraftMax && !hasDraftMin {
			capabilities = append(capabilities, aiv1alpha2.LlamaCppCapabilityDraftMTP)
			break
		}
		// An explicit window (rendered as --spec-draft-n-max/--spec-draft-n-min)
		// is a separate envelope that needs its own certificate: the single
		// Qwen3.5-family NextN head is reused for every draft step.
		if !hasDraftMax {
			return nil, fmt.Errorf("an explicit draft-mtp window needs spec.config.draftMax; draftMin alone does not define one")
		}
		if !hasDraftMin {
			draftMin = 0 // llama.cpp's default n_min
		}
		if draftMax < 1 || draftMax > llamaCppDraftMTPWindowMax {
			return nil, fmt.Errorf("spec.config.draftMax for draft-mtp must be between 1 and %d (got %d)", llamaCppDraftMTPWindowMax, draftMax)
		}
		if draftMin < 0 || draftMin > draftMax {
			return nil, fmt.Errorf("spec.config.draftMin for draft-mtp must be between 0 and draftMax=%d (got %d)", draftMax, draftMin)
		}
		capabilities = append(capabilities, aiv1alpha2.LlamaCppCapabilityDraftMTPWindow(draftMax, draftMin))
	default:
		return nil, fmt.Errorf("spec.config.specType %q is not exposed by the certified llama.cpp feature surface", specType)
	}

	return capabilities, nil
}

// llamaCppDraftMTPWindowMax bounds an explicit draft-mtp window. It is a sanity
// limit, not the gate: every window still needs its own certificate.
const llamaCppDraftMTPWindowMax = 16

func strictConfigInt(cfg map[string]any, key string) (int, bool, error) {
	raw, ok := cfg[key]
	if !ok {
		return 0, false, nil
	}
	switch value := raw.(type) {
	case int:
		return value, true, nil
	case int32:
		return int(value), true, nil
	case int64:
		return int(value), true, nil
	case float64:
		if math.Trunc(value) == value && value >= math.MinInt && value <= math.MaxInt {
			return int(value), true, nil
		}
	}
	return 0, false, fmt.Errorf("spec.config.%s must be an integer", key)
}

// validateMaxwellSpecifics handles Maxwell GPU (sm_5x) specific validation:
// FP16 rejection and backend-specific library requirements.
func (r *ModelReconciler) validateMaxwellSpecifics(model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor, gpuArch string) error {
	if !isMaxwellGPUArch(gpuArch) || gpuVendor != backend.GPUVendorNVIDIA {
		return nil
	}

	// Reject FP16 models on Maxwell — Maxwell lacks native FP16 support.
	src := strings.ToLower(model.Spec.Source)
	if strings.Contains(src, "f16") || strings.Contains(src, "fp16") {
		return fmt.Errorf("FP16 models are not supported on Maxwell GPUs (no native FP16). Use q4f32_1, q0f32, or GGUF quantized models instead")
	}

	if b.Name() == backend.NameMLCLLM {
		// MLC-LLM on Maxwell should use a pre-compiled model library and avoid JIT.
		cfg := model.Spec.GetConfigMap()
		if cfg != nil {
			if v, ok := cfg["modelLibPath"]; ok {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					return nil
				}
			}
		}

		spec := r.buildBackendModelSpec(model, b, gpuVendor)
		modelPath := spec.ModelPath
		if modelPath == "" {
			modelPath = spec.Model
		}
		modelPath = strings.TrimRight(modelPath, "/")
		if strings.HasPrefix(modelPath, "/models/") && modelPath != "/models" {
			return nil
		}

		return fmt.Errorf("mlc-llm on Maxwell GPUs requires config.modelLibPath (pre-compiled library). See docs/user/backends-maxwell.md")
	}

	return nil
}

// emitVLLMOptInEvents emits informational events when the user opts into
// experimental vLLM features (V1 engine, flash attention, AITER).
func (r *ModelReconciler) emitVLLMOptInEvents(model *aiv1alpha2.Model) {
	cfg := model.Spec.GetConfigMap()
	if cfg == nil {
		return
	}
	if v, ok := cfg["vllmEngineVersion"]; ok {
		if s, ok := v.(string); ok && s == "v1" {
			r.Recorder.Event(model, corev1.EventTypeNormal, "V1EngineOptIn",
				"vLLM V1 engine enabled via spec.config.vllmEngineVersion=v1 (experimental)")
		}
	}
	if v, ok := cfg["enableFlashAttention"]; ok {
		enabled := false
		switch val := v.(type) {
		case bool:
			enabled = val
		case string:
			enabled = val == "true" || val == "1"
		}
		if enabled {
			r.Recorder.Event(model, corev1.EventTypeNormal, "FlashAttentionOptIn",
				"Triton flash attention enabled via spec.config.enableFlashAttention=true (experimental)")
		}
	}
}

// detectGPU detects the GPU vendor and architecture from nodes.
func (r *ModelReconciler) detectGPU(ctx context.Context, model *aiv1alpha2.Model) (backend.GPUVendor, string, error) {
	if model.Spec.GetGPUCount() == 0 || model.Spec.GetGPUVendor() == aiv1alpha2.GPUVendorCPU {
		return backend.GPUVendorCPU, "", nil
	}

	nodeList := &corev1.NodeList{}
	if err := r.List(ctx, nodeList); err != nil {
		return backend.GPUVendorUnknown, "", err
	}

	nodes := nodeList.Items
	if len(model.Spec.NodeSelector) > 0 {
		filtered := make([]corev1.Node, 0, len(nodes))
		for _, node := range nodes {
			matches := true
			for k, v := range model.Spec.NodeSelector {
				if node.Labels == nil || node.Labels[k] != v {
					matches = false
					break
				}
			}
			if matches {
				filtered = append(filtered, node)
			}
		}
		nodes = filtered
	}

	type nodeMatch struct {
		vendor backend.GPUVendor
		arch   string
	}
	isNodeReady := func(node corev1.Node) bool {
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady {
				return condition.Status == corev1.ConditionTrue
			}
		}
		return false
	}

	// Nodes that would have matched but carry the agent's degraded verdict.
	// They are reported in the error so the Schedulable condition says why the
	// lane is parked (GPUDegraded) rather than the generic NoMatchingNodes.
	var degraded []string
	noteDegraded := func(node *corev1.Node) {
		reason := strings.TrimSpace(node.Annotations[constants.NodeAnnotationGPUHealthReason])
		if reason == "" {
			reason = "no reason recorded"
		}
		degraded = append(degraded, node.Name+": "+reason)
	}

	findFirst := func(vendor backend.GPUVendor) (nodeMatch, bool) {
		for i := range nodes {
			node := &nodes[i]
			if !isNodeReady(*node) {
				continue
			}
			var match nodeMatch
			matched := false
			switch vendor {
			case backend.GPUVendorNVIDIA:
				if arch, ok := nvidiaGPUArchFromNode(node); ok {
					match, matched = nodeMatch{vendor: backend.GPUVendorNVIDIA, arch: arch}, true
				}
			case backend.GPUVendorAMD:
				if arch, ok := amdGPUArchFromNode(node); ok {
					match, matched = nodeMatch{vendor: backend.GPUVendorAMD, arch: arch}, true
				}
			}
			if !matched {
				continue
			}
			if nodeGPUDegraded(node) {
				noteDegraded(node)
				continue
			}
			return match, true
		}
		return nodeMatch{}, false
	}

	noMatch := func(vendorWord string) error {
		if len(degraded) > 0 {
			return &noMatchingNodesError{
				reason: fmt.Sprintf("no healthy %sGPU nodes match selector for model %s: %s=%s on %s",
					vendorWord, model.Name, constants.NodeLabelGPUHealth, constants.GPUHealthDegraded, strings.Join(degraded, "; ")),
				gpuDegraded: true,
			}
		}
		return &noMatchingNodesError{reason: fmt.Sprintf("no %sGPU nodes match selector for model %s", vendorWord, model.Name)}
	}

	switch model.Spec.GetGPUVendor() {
	case aiv1alpha2.GPUVendorNVIDIA:
		if match, ok := findFirst(backend.GPUVendorNVIDIA); ok {
			return match.vendor, match.arch, nil
		}
		return backend.GPUVendorUnknown, "", noMatch("NVIDIA ")
	case aiv1alpha2.GPUVendorAMD:
		if match, ok := findFirst(backend.GPUVendorAMD); ok {
			return match.vendor, match.arch, nil
		}
		return backend.GPUVendorUnknown, "", noMatch("AMD ")
	default: // auto
		nvidiaMatch, nvidiaOK := findFirst(backend.GPUVendorNVIDIA)
		amdMatch, amdOK := findFirst(backend.GPUVendorAMD)

		// Tighten vendor selection: when both vendors match, force the user to pick.
		// This avoids surprising behavior on mixed-vendor clusters where "auto" would
		// otherwise prefer NVIDIA.
		if nvidiaOK && amdOK {
			return backend.GPUVendorUnknown, "", &ambiguousGPUVendorError{
				reason: fmt.Sprintf(
					"gpu.vendor is %q but both NVIDIA and AMD GPU nodes match selector for model %s; set spec.gpu.vendor explicitly",
					aiv1alpha2.GPUVendorAuto,
					model.Name,
				),
			}
		}

		if nvidiaOK {
			return nvidiaMatch.vendor, nvidiaMatch.arch, nil
		}
		if amdOK {
			return amdMatch.vendor, amdMatch.arch, nil
		}
	}

	return backend.GPUVendorUnknown, "", noMatch("")
}

// nodeGPUDegraded reports whether the node agent's GPU health sentinel has
// marked the node's card unusable (flexinfer.ai/gpu.health=degraded).
func nodeGPUDegraded(node *corev1.Node) bool {
	return node != nil && node.Labels != nil &&
		node.Labels[constants.NodeLabelGPUHealth] == constants.GPUHealthDegraded
}
