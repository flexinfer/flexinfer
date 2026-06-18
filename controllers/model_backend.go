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
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/quantization"
)

// freshModelForRender returns the Model re-read from the API server (uncached),
// so Deployment rendering reflects the CURRENT spec rather than a possibly-stale
// informer cache. The cached client has been observed serving a stale
// spec.config for minutes: a maxModelLen edit reached the API server (and the
// cached CR read), yet ensureDeployment kept rendering the old value and never
// rolled the Deployment until a manual `kubectl delete deploy` forced a fresh
// create (reproduced on both the 14B and 35B 5930k lanes, 2026-06-18). Reading
// through r.APIReader (mgr.GetAPIReader, already wired in
// cmd/flexinfer-manager/main.go) bypasses the cache. Falls back to the passed
// model when no APIReader is configured (unit tests) or the read fails.
func (r *ModelReconciler) freshModelForRender(ctx context.Context, model *aiv1alpha2.Model) *aiv1alpha2.Model {
	if r.APIReader == nil {
		return model
	}
	fresh := &aiv1alpha2.Model{}
	if err := r.APIReader.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, fresh); err != nil {
		return model
	}
	return fresh
}

// buildBackendModelSpec converts Model spec to backend.ModelSpec.
func (r *ModelReconciler) buildBackendModelSpec(model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor) *backend.ModelSpec {
	return r.buildBackendModelSpecForArch(model, b, gpuVendor, "")
}

func (r *ModelReconciler) buildBackendModelSpecForArch(model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor, gpuArch string) *backend.ModelSpec {
	modelValue := extractModelFromSource(model.Spec.Source)
	spec := &backend.ModelSpec{
		Model:          modelValue,
		ModelPath:      "",
		GPUVendor:      gpuVendor,
		StartupTimeout: modelColdStartTimeout(model),
	}

	// Parse config into the spec
	if model.Spec.Config != nil {
		spec.Config = model.Spec.GetConfigMap()
	}
	if gpuArch != "" && r.GPUProfiles != nil {
		if profile, ok := r.GPUProfiles.Lookup(gpuArch); ok {
			spec.Config = backend.ApplyVLLMDefaultsFromProfile(spec.Config, profile, b.Name())
		}
	}

	storagePlan := resolveBackendStoragePlan(model, b, spec.Config)
	spec.ModelPath = storagePlan.ModelPath

	return spec
}

func modelColdStartTimeout(model *aiv1alpha2.Model) time.Duration {
	if model == nil || model.Spec.Serverless == nil || model.Spec.Serverless.ColdStartTimeout == nil {
		return 0
	}
	return model.Spec.Serverless.ColdStartTimeout.Duration
}

type backendStoragePlan struct {
	ModelPath          string
	ModelVolumeSubPath string
	HFCacheBasePath    string
}

// resolveBackendStoragePlan centralizes cache/storage path decisions so backend
// and source quirks are handled in one place.
func resolveBackendStoragePlan(model *aiv1alpha2.Model, b backend.Backend, config map[string]any) backendStoragePlan {
	plan := backendStoragePlan{}
	source := model.Spec.Source
	modelValue := extractModelFromSource(source)
	strategy := cacheStrategy(model)
	pvcStageMode := pvcSourceStageMode(model)

	backendName := ""
	needsVolume := false
	if b != nil {
		backendName = b.Name()
		needsVolume = b.NeedsVolume()
	}

	// HF sources can use the mounted model volume as a persistent hub cache.
	if strings.HasPrefix(source, "HF://") && needsVolume {
		plan.HFCacheBasePath = "/models/.cache/huggingface"
	}

	// SharedPVC + HF sources are prefetched into /models/<modelName>.
	if strings.HasPrefix(source, "HF://") && strategy == "SharedPVC" && model.Status.Cache != nil && model.Status.Cache.PVCName != "" {
		plan.ModelPath = "/models/" + model.Name
		// diffusers expects model_index.json at mount root.
		if backendName == backend.NameDiffusers {
			plan.ModelVolumeSubPath = model.Name
		}
	}

	// pvc://<pvc>/<subpath> is mounted at /models.
	if strings.HasPrefix(source, "pvc://") {
		if pvcStageMode == pvcSourceCacheModeLocal {
			subPath := strings.TrimLeft(modelValue, "/")
			if subPath != "" {
				plan.ModelPath = "/models/" + subPath
			} else {
				plan.ModelPath = "/models"
			}
		} else if strings.HasPrefix(modelValue, "/") {
			plan.ModelPath = "/models" + modelValue
		} else {
			plan.ModelPath = "/models"
		}
	}

	// file:// paths are already in-container paths.
	if strings.HasPrefix(source, "file://") {
		plan.ModelPath = modelValue
	}

	// Backends that load a single GGUF file need a concrete file path under the
	// staged HF directory. llama.cpp always requires it; vLLM uses it when the
	// user specifies ggufFile to select a specific variant from multi-GGUF repos.
	if (backendName == backend.NameLlamaCpp || backendName == backend.NameVLLM) &&
		strings.HasPrefix(source, "HF://") &&
		(strategy == "SharedPVC" || strategy == "Local") &&
		model.Status.Cache != nil &&
		(strategy == "Local" || model.Status.Cache.PVCName != "") {
		if ggufFile := resolveGGUFFile(config); ggufFile != "" {
			if strategy == "Local" {
				plan.ModelPath = "/models/" + ggufFile
			} else {
				plan.ModelPath = "/models/" + model.Name + "/" + ggufFile
			}
		}
	}

	// When quantization completed, redirect model path to the quantized output subdirectory.
	// Use CompletedAt (always set from job.Status.CompletionTime) rather than Type
	// (parsed from pod termination-log metadata, which may be unavailable if the pod
	// was cleaned up before the controller read it).
	if model.Spec.Quantize != nil &&
		model.Status.Cache != nil &&
		model.Status.Cache.Quantization != nil &&
		model.Status.Cache.Quantization.CompletedAt != nil {
		quantizedSubdir := quantizedOutputDir(model.Spec.Quantize)
		if quantizedSubdir != "" {
			plan.ModelPath = "/models/" + model.Name + "/" + quantizedSubdir
		}
	}

	return plan
}

// quantizedOutputDir returns the output subdirectory name for a given quantization spec.
func quantizedOutputDir(spec *aiv1alpha2.QuantizationSpec) string {
	if spec == nil {
		return ""
	}
	switch spec.Format {
	case aiv1alpha2.QuantizationFormatAWQ:
		bits := int32(quantization.DefaultAWQBits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		groupSize := int32(quantization.DefaultQuantizationGroupSize)
		if spec.GroupSize != nil {
			groupSize = *spec.GroupSize
		}
		return fmt.Sprintf("awq-w%d-g%d", bits, groupSize)
	case aiv1alpha2.QuantizationFormatGPTQ:
		bits := int32(quantization.DefaultGPTQBits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		groupSize := int32(quantization.DefaultQuantizationGroupSize)
		if spec.GroupSize != nil {
			groupSize = *spec.GroupSize
		}
		return fmt.Sprintf("gptq-w%d-g%d", bits, groupSize)
	case aiv1alpha2.QuantizationFormatCompressedTensors:
		bits := int32(quantization.DefaultCompressedTensorsBits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		groupSize := int32(quantization.DefaultCompressedTensorsGroupSize)
		if spec.GroupSize != nil {
			groupSize = *spec.GroupSize
		}
		return quantization.CompressedTensorsOutputSubdir(int(bits), int(groupSize))
	default:
		return ""
	}
}

func resolveGGUFFile(config map[string]any) string {
	if config == nil {
		return ""
	}

	ggufFile := ""
	if v, ok := config["ggufFile"]; ok {
		if s, ok := v.(string); ok {
			ggufFile = s
		}
	}
	if strings.TrimSpace(ggufFile) == "" {
		if v, ok := config["modelFile"]; ok {
			if s, ok := v.(string); ok {
				ggufFile = s
			}
		}
	}

	ggufFile = strings.TrimLeft(strings.TrimSpace(ggufFile), "/")
	// Best-effort safety: ignore traversal attempts.
	if ggufFile != "" && !strings.Contains(ggufFile, "..") {
		return ggufFile
	}
	return ""
}

// extractModelFromSource parses the model name from the source URI.
func extractModelFromSource(source string) string {
	// Handle different source formats:
	// HF://org/model -> org/model
	// ollama://model:tag -> model:tag
	// file:///path/to/model -> /path/to/model
	// pvc://name/path -> /path

	if strings.HasPrefix(source, "HF://") {
		return strings.TrimPrefix(source, "HF://")
	}
	if strings.HasPrefix(source, "ollama://") {
		return strings.TrimPrefix(source, "ollama://")
	}
	if strings.HasPrefix(source, "file://") {
		return strings.TrimPrefix(source, "file://")
	}
	if strings.HasPrefix(source, "pvc://") {
		parts := strings.SplitN(strings.TrimPrefix(source, "pvc://"), "/", 2)
		if len(parts) == 2 {
			return "/" + parts[1]
		}
	}
	return source
}

// getVolumeSource returns the appropriate volume source based on cache strategy.
func (r *ModelReconciler) getVolumeSource(model *aiv1alpha2.Model) corev1.VolumeSource {
	if pvcName, _, ok := parsePVCSource(model.Spec.Source); ok {
		switch pvcSourceStageMode(model) {
		case pvcSourceCacheModeSharedPVC:
			cacheName, _ := cachePVCName(model)
			return corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: cacheName,
				},
			}
		case pvcSourceCacheModeLocal:
			return corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: resolveLocalCachePath(model),
					Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
				},
			}
		}
		return corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		}
	}

	strategy := "SharedPVC" // default
	if model.Spec.Cache != nil && model.Spec.Cache.Strategy != "" {
		strategy = model.Spec.Cache.Strategy
	} else if model.Spec.IsShared() {
		strategy = "Memory" // Use RAM cache for shared GPU to enable fast swapping
	}

	switch strategy {
	case "Memory":
		// Use emptyDir with memory medium for fast model swapping
		return corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				Medium: corev1.StorageMediumMemory,
			},
		}
	case "Local":
		// Use hostPath for NVMe-backed local model storage
		return corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: resolveLocalCachePath(model),
				Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
			},
		}
	case "None":
		// No persistent volume, download each time
		return corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		}
	default: // SharedPVC
		pvcName := model.Name + "-cache"
		if model.Spec.Cache != nil && model.Spec.Cache.PVCName != "" {
			pvcName = model.Spec.Cache.PVCName
		}
		return corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		}
	}
}

func cacheStrategy(model *aiv1alpha2.Model) string {
	if model.Spec.Cache != nil && model.Spec.Cache.Strategy != "" {
		return model.Spec.Cache.Strategy
	}
	if model.Spec.IsShared() {
		return "Memory"
	}
	return "SharedPVC"
}

func shouldStagePVCSourceToCache(model *aiv1alpha2.Model) bool {
	return pvcSourceStageMode(model) != pvcSourceCacheModeNone
}

type pvcSourceCacheMode string

const (
	pvcSourceCacheModeNone      pvcSourceCacheMode = "none"
	pvcSourceCacheModeSharedPVC pvcSourceCacheMode = "shared-pvc"
	pvcSourceCacheModeLocal     pvcSourceCacheMode = "local"
)

func pvcSourceStageMode(model *aiv1alpha2.Model) pvcSourceCacheMode {
	if !strings.HasPrefix(model.Spec.Source, "pvc://") {
		return pvcSourceCacheModeNone
	}
	if model.Spec.Cache == nil {
		return pvcSourceCacheModeNone
	}
	if model.Spec.Cache.Strategy == "" {
		return pvcSourceCacheModeSharedPVC
	}
	switch model.Spec.Cache.Strategy {
	case "SharedPVC":
		return pvcSourceCacheModeSharedPVC
	case "Local":
		return pvcSourceCacheModeLocal
	default:
		return pvcSourceCacheModeNone
	}
}

func cachePVCName(model *aiv1alpha2.Model) (string, bool) {
	if model.Spec.Cache != nil && model.Spec.Cache.PVCName != "" {
		return model.Spec.Cache.PVCName, false
	}
	return model.Name + "-cache", true
}

func cacheStorageClass(model *aiv1alpha2.Model) string {
	if model.Spec.Cache != nil && model.Spec.Cache.StorageClass != "" {
		return model.Spec.Cache.StorageClass
	}
	return "longhorn"
}

func cacheSize(model *aiv1alpha2.Model) string {
	if model.Spec.Cache != nil && model.Spec.Cache.Size != "" {
		return model.Spec.Cache.Size
	}
	return "50Gi"
}

// compilationCacheMountPath is where compilation caches are mounted inside the container.
const compilationCacheMountPath = "/cache/compile"

// resolveCompilationCache determines whether compilation cache should be injected
// and returns the hostPath directory for this model. Returns ("", false) if disabled.
func resolveCompilationCache(model *aiv1alpha2.Model) (hostPath string, enabled bool) {
	// Check explicit CRD configuration
	if model.Spec.Cache != nil && model.Spec.Cache.CompilationCache != nil {
		cc := model.Spec.Cache.CompilationCache
		if cc.Enabled != nil && !*cc.Enabled {
			return "", false
		}
		basePath := "/var/lib/flexinfer/compile-cache"
		if cc.HostPath != "" {
			basePath = cc.HostPath
		}
		return filepath.Join(basePath, model.Namespace, model.Name), true
	}

	// Auto-enable for shared AMD GPU models (the common swap case)
	if model.Spec.IsShared() && model.Spec.GPU != nil &&
		(model.Spec.GPU.Vendor == "amd" || model.Spec.GPU.Vendor == "auto") {
		return filepath.Join("/var/lib/flexinfer/compile-cache", model.Namespace, model.Name), true
	}

	return "", false
}

// resolveLocalCachePath returns the hostPath directory for this model's local cache.
func resolveLocalCachePath(model *aiv1alpha2.Model) string {
	basePath := "/var/lib/flexinfer/models"
	if model.Spec.Cache != nil && model.Spec.Cache.HostPath != "" {
		basePath = model.Spec.Cache.HostPath
	}
	return filepath.Join(basePath, model.Namespace, model.Name)
}

func parsePVCSource(source string) (pvcName string, subPath string, ok bool) {
	if !strings.HasPrefix(source, "pvc://") {
		return "", "", false
	}
	rest := strings.TrimPrefix(source, "pvc://")
	if rest == "" {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	if parts[0] == "" {
		return "", "", false
	}
	pvcName = parts[0]
	if len(parts) == 2 {
		subPath = parts[1]
	}
	return pvcName, subPath, true
}

func hfCacheEnvVars(basePath string) []corev1.EnvVar {
	basePath = strings.TrimRight(basePath, "/")
	return []corev1.EnvVar{
		{Name: "HF_HOME", Value: basePath},
		{Name: "HF_HUB_CACHE", Value: basePath + "/hub"},
		{Name: "HUGGINGFACE_HUB_CACHE", Value: basePath + "/hub"},
		{Name: "TRANSFORMERS_CACHE", Value: basePath + "/transformers"},
	}
}

func mergeEnv(existing []corev1.EnvVar, additional []corev1.EnvVar) []corev1.EnvVar {
	if len(additional) == 0 {
		return existing
	}
	out := make([]corev1.EnvVar, 0, len(existing)+len(additional))
	indexByName := make(map[string]int, len(existing))
	for _, e := range existing {
		indexByName[e.Name] = len(out)
		out = append(out, e)
	}
	for _, e := range additional {
		if idx, ok := indexByName[e.Name]; ok {
			out[idx] = e
			continue
		}
		indexByName[e.Name] = len(out)
		out = append(out, e)
	}
	return out
}
