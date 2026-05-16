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
	"strings"

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

	// Warn if estimate exceeds 80% of total VRAM
	if estimateMB > totalVRAMMB*80/100 {
		r.Recorder.Event(model, corev1.EventTypeWarning, "VRAMPressure",
			fmt.Sprintf("model VRAM estimate (%dMB) exceeds 80%% of GPU VRAM (%dMB); performance may be degraded",
				estimateMB, totalVRAMMB))
	}

	return nil
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

	// --- Maxwell-specific validation (sm_5x) ---
	if err := r.validateMaxwellSpecifics(model, b, gpuVendor, gpuArch); err != nil {
		return err
	}

	return nil
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
		for _, node := range nodes {
			if !isNodeReady(node) {
				continue
			}
			switch vendor {
			case backend.GPUVendorNVIDIA:
				qty, ok := node.Status.Capacity["nvidia.com/gpu"]
				if !ok || qty.Value() < 1 {
					continue
				}
				major := ""
				if node.Labels != nil {
					major = node.Labels["nvidia.com/gpu.compute.major"]
				}
				arch := ""
				if major != "" {
					arch = "sm_" + major
				}
				// Fall back to flexinfer.ai/gpu.arch label (same as AMD detection).
				if arch == "" && node.Labels != nil {
					arch = node.Labels[LabelGPUArch]
				}
				return nodeMatch{vendor: backend.GPUVendorNVIDIA, arch: arch}, true
			case backend.GPUVendorAMD:
				qty, ok := node.Status.Capacity["amd.com/gpu"]
				if !ok || qty.Value() < 1 {
					continue
				}
				arch := ""
				if node.Labels != nil {
					arch = node.Labels["gpu.amd.com/gpu-architecture"]
					if arch == "" {
						// FlexInfer agent sets this label via rocminfo detection.
						arch = node.Labels[LabelGPUArch]
					}
					if arch == "" {
						// ROCm arch label isn't always present; fall back to common node-level labels.
						// Prefer RDNA3 dGPU (GC 11.0.0) when multiple AMD GPUs exist on the same node.
						if node.Labels["amd.com/gpu.family.GC_11_0_0"] != "" {
							arch = "gfx1100"
						} else if node.Labels["amd.com/gpu.family.GC_10_3_6"] != "" {
							arch = "gfx1036"
						} else if modelName := node.Labels["gpu.amd.com/model"]; strings.Contains(modelName, "7900") {
							arch = "gfx1100"
						}
					}
				}
				return nodeMatch{vendor: backend.GPUVendorAMD, arch: arch}, true
			default:
				continue
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
