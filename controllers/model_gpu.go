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

// shouldEmitVRAMPressure reports whether enough time has elapsed since the last
// VRAMPressure event for this model to emit another, recording the emit time
// when it returns true. Keyed by UID so a delete+recreate re-arms immediately.
func (r *ModelReconciler) shouldEmitVRAMPressure(model *aiv1alpha2.Model) bool {
	key := string(model.UID)
	now := time.Now()
	if v, ok := r.vramPressureLastEmit.Load(key); ok {
		if last, ok := v.(time.Time); ok && now.Sub(last) < vramPressureEventCooldown {
			return false
		}
	}
	r.vramPressureLastEmit.Store(key, now)
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
			if isGenericImage {
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

	switch specType {
	case "", "none":
		if hasDraftMax || hasDraftMin {
			return nil, fmt.Errorf("spec.config.draftMax and draftMin require spec.config.specType=ngram-simple")
		}
	case "ngram-simple":
		if !hasDraftMax || !hasDraftMin || draftMax != 16 || draftMin != 1 {
			return nil, fmt.Errorf("the certified ngram-simple envelope requires spec.config.draftMax=16 and draftMin=1")
		}
		capabilities = append(capabilities, aiv1alpha2.LlamaCppCapabilityNgramSimpleDraft16)
	default:
		return nil, fmt.Errorf("spec.config.specType %q is not exposed by the certified llama.cpp feature surface", specType)
	}

	return capabilities, nil
}

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

	findFirst := func(vendor backend.GPUVendor) (nodeMatch, bool) {
		for i := range nodes {
			node := &nodes[i]
			if !isNodeReady(*node) {
				continue
			}
			switch vendor {
			case backend.GPUVendorNVIDIA:
				if arch, ok := nvidiaGPUArchFromNode(node); ok {
					return nodeMatch{vendor: backend.GPUVendorNVIDIA, arch: arch}, true
				}
			case backend.GPUVendorAMD:
				if arch, ok := amdGPUArchFromNode(node); ok {
					return nodeMatch{vendor: backend.GPUVendorAMD, arch: arch}, true
				}
			}
		}
		return nodeMatch{}, false
	}

	switch model.Spec.GetGPUVendor() {
	case aiv1alpha2.GPUVendorNVIDIA:
		if match, ok := findFirst(backend.GPUVendorNVIDIA); ok {
			return match.vendor, match.arch, nil
		}
		return backend.GPUVendorUnknown, "", &noMatchingNodesError{reason: fmt.Sprintf("no NVIDIA GPU nodes match selector for model %s", model.Name)}
	case aiv1alpha2.GPUVendorAMD:
		if match, ok := findFirst(backend.GPUVendorAMD); ok {
			return match.vendor, match.arch, nil
		}
		return backend.GPUVendorUnknown, "", &noMatchingNodesError{reason: fmt.Sprintf("no AMD GPU nodes match selector for model %s", model.Name)}
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

	return backend.GPUVendorUnknown, "", &noMatchingNodesError{reason: fmt.Sprintf("no GPU nodes match selector for model %s", model.Name)}
}
