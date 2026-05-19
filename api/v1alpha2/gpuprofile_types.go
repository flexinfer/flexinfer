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

package v1alpha2

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GPUProfileSpec declares the capabilities, images, and environment variables
// for a specific GPU architecture. This replaces hardcoded per-arch logic
// scattered across backend/, controllers/, and pkg/quantization/.
type GPUProfileSpec struct {
	// Architecture is the GPU microarchitecture identifier (e.g. "gfx1100", "gfx906", "sm_52").
	Architecture string `json:"architecture"`

	// Vendor is the GPU vendor: "amd" or "nvidia".
	Vendor string `json:"vendor"`

	// VRAMMB is the usable VRAM in megabytes for inference workloads.
	// For nodes with mixed GPUs (e.g. dGPU + iGPU), this reflects only the usable device.
	VRAMMB int64 `json:"vramMB"`

	// DeviceCount is the total number of GPU resources reported by the K8s device plugin
	// on the target node. This may be higher than the number of usable devices when an
	// iGPU is present (e.g. cblevins-7900xtx reports 2 GPUs but only device 0 is usable).
	// +optional
	DeviceCount *int32 `json:"deviceCount,omitempty"`

	// UsableDeviceIndices lists the HIP/CUDA device indices that are valid for inference.
	// When set, the controller injects HIP_VISIBLE_DEVICES or CUDA_VISIBLE_DEVICES accordingly.
	// Example: ["0"] on a node where device 1 is an unusable iGPU.
	// +optional
	UsableDeviceIndices []string `json:"usableDeviceIndices,omitempty"`

	// Features declares hardware capability flags for this GPU architecture.
	// +optional
	Features GPUFeatures `json:"features,omitempty"`

	// Backends maps backend name (e.g. "vllm", "diffusers") to its support profile.
	// +optional
	Backends map[string]BackendProfile `json:"backends,omitempty"`

	// Env is a list of environment variables injected into runtime pods and
	// GPU workload jobs (quantization, abliteration, finetune) on this architecture.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// MaxGPUMemoryGB is the max GPU memory budget for accelerate device_map (e.g., 12 for gfx906 16GB VRAM).
	// Used as ABLITERATION_GPU_MAX_MEMORY_GB env var in abliteration jobs.
	// +optional
	MaxGPUMemoryGB *int32 `json:"maxGPUMemoryGB,omitempty"`

	// MaxCPUMemoryGB is the max CPU memory budget for offloading (e.g., 56 for 62GiB node).
	// Used as ABLITERATION_CPU_MAX_MEMORY_GB env var in abliteration jobs.
	// +optional
	MaxCPUMemoryGB *int32 `json:"maxCPUMemoryGB,omitempty"`

	// ContainerMemoryGB is the K8s container memory limit for quantization/abliteration jobs.
	// Replaces hardcoded DefaultGPUQuantizationMemoryGB.
	// +optional
	ContainerMemoryGB *int32 `json:"containerMemoryGB,omitempty"`

	// GPUDriverMemoryMB is the estimated system RAM consumed by the GPU driver
	// (HIP/GTT on ROCm, CUDA driver on NVIDIA) outside the container's cgroup.
	// When set, the controller inflates job memory requests/limits by this amount
	// so the K8s scheduler correctly accounts for total node memory consumption.
	// Example: 12288 for gfx1100 (~12 GiB HIP/GTT overhead during quantization).
	// +optional
	GPUDriverMemoryMB *int32 `json:"gpuDriverMemoryMB,omitempty"`

	// Quantization declares which quantization formats and images are available.
	// +optional
	Quantization *QuantizationProfile `json:"quantization,omitempty"`

	// Runtime configures the persistent runtime container for this GPU architecture.
	// When set, the controller deploys a DaemonSet with this image on matching nodes
	// instead of creating per-model Deployments.
	// +optional
	Runtime *RuntimeProfile `json:"runtime,omitempty"`

	// ImagePullPolicy overrides the default pull policy for all images on this GPU architecture.
	// If not set, ImagePullPolicyForImage() logic is used.
	// +optional
	ImagePullPolicy *corev1.PullPolicy `json:"imagePullPolicy,omitempty"`
}

// GPUFeatures declares hardware capability flags.
type GPUFeatures struct {
	FP16           bool `json:"fp16,omitempty"`
	BF16           bool `json:"bf16,omitempty"`
	FP8            bool `json:"fp8,omitempty"`
	FlashAttention bool `json:"flashAttention,omitempty"`
	INT4           bool `json:"int4,omitempty"`
	INT8           bool `json:"int8,omitempty"`
}

// BackendProfile describes a backend's support level and optional image override for this arch.
type BackendProfile struct {
	// Support level: "full", "experimental", or "unsupported".
	Support string `json:"support"`

	// Image overrides the default container image for this backend on this architecture.
	// +optional
	Image string `json:"image,omitempty"`

	// VLLM declares vLLM-specific feature capability per architecture.
	// Only meaningful when Support != "unsupported" and the backend key is "vllm".
	// Wave 1 schema-only addition (2026-05-15): consumers come in a follow-up slice.
	// +optional
	VLLM *VLLMCapabilities `json:"vllm,omitempty"`
}

// VLLMCapabilities declares per-arch vLLM feature support and engine-arg defaults.
// Each capability field is one of "supported", "experimental", or "unsupported".
type VLLMCapabilities struct {
	// V1Engine reports vLLM V1 engine support. V0 was removed upstream in vLLM >=0.18.
	// +optional
	V1Engine string `json:"v1Engine,omitempty"`

	// PiecewiseGraphs reports V1 piecewise CUDA-graph capture support.
	// gfx1100 as of 2026-05: "experimental" pending upstream vllm-project/vllm#39010 / #41622.
	// +optional
	PiecewiseGraphs string `json:"piecewiseGraphs,omitempty"`

	// FlashAttention reports which FA backend is supported.
	// One of "ck", "triton", "aotriton", or "none".
	// gfx1100 today: "ck" with AOTriton experimental.
	// gfx906 today: "none" (no FA kernel exists for Vega20).
	// +optional
	FlashAttention string `json:"flashAttention,omitempty"`

	// FusedMoETriton reports vLLM-native FusedMoE Triton kernel support
	// (covers gemma4, mixtral, qwen3-moe, etc.).
	// gfx1100 BF16: validated via PR #38826 in vLLM 0.19.0.
	// gfx1100 INT4: experimental (weight-loader path unproven).
	// +optional
	FusedMoETriton string `json:"fusedMoETriton,omitempty"`

	// FP8KVEmulation reports INT8-with-FP8-scale KV cache emulation support
	// for arches without native FP8 hardware (RDNA3, Vega20).
	// +optional
	FP8KVEmulation string `json:"fp8KVEmulation,omitempty"`

	// MarlinINT4 reports Marlin INT4 GEMM kernel support. CUDA-only upstream;
	// ROCm variants (Conch, rocm_aiter_marlin) ship in newer vLLM versions.
	// turboquant-vllm Tq4 backend is the current INT4 path on AMD, tracked separately.
	// +optional
	MarlinINT4 string `json:"marlinINT4,omitempty"`

	// AudioTranscription reports OpenAI-compatible /v1/audio/transcriptions
	// endpoint support for Whisper-family models (vLLM `--task transcription`).
	// gfx1100: gated on the Slice 1 kill-test in
	// .loom/asr-diarization-7900xtx-plan-2026-05-18.md (initial value "experimental").
	// gfx906: "unsupported" — no FlashAttention kernel on Vega20 and the gfx906
	// vLLM runtime is currently paused (Track B in gfx1100-gfx906-next-round-plan.md).
	// Doc-only at first; controller enforcement (refuse `task: transcription` model
	// CRs on profiles where this is "unsupported") lands in a follow-up slice.
	// +optional
	AudioTranscription string `json:"audioTranscription,omitempty"`

	// Defaults specifies default engine args injected when this profile is selected.
	// Wave 1 lands the field; controller consumers come in a follow-up slice.
	// +optional
	Defaults *VLLMDefaults `json:"defaults,omitempty"`
}

// VLLMDefaults specifies per-arch default vLLM engine args.
type VLLMDefaults struct {
	// CudagraphMode is the default --cudagraph-mode engine arg.
	// One of "NONE", "PIECEWISE", or "FULL".
	// gfx1100 Wave 1: "NONE" until upstream piecewise capture bugs close.
	// +optional
	CudagraphMode string `json:"cudagraphMode,omitempty"`

	// EnforceEager forces enforce_eager=true at the engine level.
	// +optional
	EnforceEager *bool `json:"enforceEager,omitempty"`

	// KVCacheDtype default. One of "auto", "fp8", "fp8_e4m3", or "fp8_e5m2".
	// Model CRs may override.
	// +optional
	KVCacheDtype string `json:"kvCacheDtype,omitempty"`
}

// QuantizationProfile declares available quantization formats and their container images.
type QuantizationProfile struct {
	// Supported lists quantization formats available on this architecture (e.g. ["gptq", "awq", "gguf"]).
	// +optional
	Supported []string `json:"supported,omitempty"`

	// Images maps quantization format to the container image used for quantization jobs.
	// +optional
	Images map[string]string `json:"images,omitempty"`

	// ImagePullPolicy overrides the pull policy for quantization job images.
	// Takes precedence over GPUProfileSpec.ImagePullPolicy for quantization jobs.
	// +optional
	ImagePullPolicy *corev1.PullPolicy `json:"imagePullPolicy,omitempty"`
}

// RuntimeProfile configures the persistent flexinfer-runtime container for a GPU architecture.
type RuntimeProfile struct {
	// Image is the unified runtime container image for this architecture.
	// Example: "registry.harbor.lan/flexinfer/runtime:rocm-gfx1100"
	Image string `json:"image"`

	// BundledBackends lists backend names that can be launched inside this
	// persistent runtime image. When set, controller/proxy direct runtime loads
	// are allowed only for these backends; other backends fall back to their
	// dedicated GPUProfile backend image.
	// +optional
	BundledBackends []string `json:"bundledBackends,omitempty"`

	// Port is the runtime API port. Defaults to 8080.
	// +optional
	Port *int32 `json:"port,omitempty"`

	// ImagePullPolicy overrides the pull policy for runtime images.
	// Takes precedence over GPUProfileSpec.ImagePullPolicy for runtime pods.
	// +optional
	ImagePullPolicy *corev1.PullPolicy `json:"imagePullPolicy,omitempty"`
}

// GPUProfileStatus holds observed state from the controller.
type GPUProfileStatus struct {
	// Cached indicates whether this profile has been loaded into the controller's in-memory cache.
	// +optional
	Cached bool `json:"cached,omitempty"`

	// LastCachedTime is when the profile was last loaded into cache.
	// +optional
	LastCachedTime *metav1.Time `json:"lastCachedTime,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=gpup;gpuprofile,scope=Namespaced
//+kubebuilder:printcolumn:name="Architecture",type="string",JSONPath=".spec.architecture"
//+kubebuilder:printcolumn:name="Vendor",type="string",JSONPath=".spec.vendor"
//+kubebuilder:printcolumn:name="VRAM",type="integer",JSONPath=".spec.vramMB"
//+kubebuilder:printcolumn:name="Cached",type="boolean",JSONPath=".status.cached"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// GPUProfile declares GPU architecture capabilities, container images, and
// environment variables. The controller caches profiles and exposes them to
// Model, ModelCache, and ModelDeployment reconcilers for image/env selection.
type GPUProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GPUProfileSpec   `json:"spec,omitempty"`
	Status GPUProfileStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// GPUProfileList contains a list of GPUProfile
type GPUProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GPUProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GPUProfile{}, &GPUProfileList{})
}
