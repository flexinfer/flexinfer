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

	// Env is a list of environment variables injected into all inference pods on this architecture.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Quantization declares which quantization formats and images are available.
	// +optional
	Quantization *QuantizationProfile `json:"quantization,omitempty"`

	// Runtime configures the persistent runtime container for this GPU architecture.
	// When set, the controller deploys a DaemonSet with this image on matching nodes
	// instead of creating per-model Deployments.
	// +optional
	Runtime *RuntimeProfile `json:"runtime,omitempty"`
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
}

// QuantizationProfile declares available quantization formats and their container images.
type QuantizationProfile struct {
	// Supported lists quantization formats available on this architecture (e.g. ["gptq", "awq", "gguf"]).
	// +optional
	Supported []string `json:"supported,omitempty"`

	// Images maps quantization format to the container image used for quantization jobs.
	// +optional
	Images map[string]string `json:"images,omitempty"`
}

// RuntimeProfile configures the persistent flexinfer-runtime container for a GPU architecture.
type RuntimeProfile struct {
	// Image is the unified runtime container image for this architecture.
	// Example: "registry.harbor.lan/flexinfer/runtime:rocm-gfx1100"
	Image string `json:"image"`

	// Port is the runtime API port. Defaults to 8080.
	// +optional
	Port *int32 `json:"port,omitempty"`
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
