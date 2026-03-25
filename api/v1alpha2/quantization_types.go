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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// QuantizationFormat identifies the quantization format to produce.
// +kubebuilder:validation:Enum=GGUF;AWQ;GPTQ;EXL2;FP8
type QuantizationFormat string

const (
	QuantizationFormatGGUF QuantizationFormat = "GGUF"
	QuantizationFormatAWQ  QuantizationFormat = "AWQ"
	QuantizationFormatGPTQ QuantizationFormat = "GPTQ"
	QuantizationFormatEXL2 QuantizationFormat = "EXL2"
	QuantizationFormatFP8  QuantizationFormat = "FP8"
)

// CalibrationSpec configures calibration parameters for AWQ/GPTQ quantization.
// +kubebuilder:object:generate=true
type CalibrationSpec struct {
	// MaxSeqLen is the maximum sequence length for calibration samples.
	// Defaults to 4096.
	// +kubebuilder:validation:Minimum=128
	// +kubebuilder:validation:Maximum=32768
	// +optional
	MaxSeqLen *int32 `json:"maxSeqLen,omitempty"`

	// MaxSamples is the number of calibration samples to use.
	// Defaults to 256.
	// +kubebuilder:validation:Minimum=8
	// +kubebuilder:validation:Maximum=2048
	// +optional
	MaxSamples *int32 `json:"maxSamples,omitempty"`

	// NParallelCalibSamples controls how many calibration samples are processed
	// in parallel during AWQ quantization. Lower values reduce peak VRAM usage.
	// Defaults to 16 for models >10B params on <=24GB VRAM.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=256
	// +optional
	NParallelCalibSamples *int32 `json:"nParallelCalibSamples,omitempty"`

	// Dataset is the HuggingFace dataset used for calibration samples.
	// Defaults to "mit-han-lab/pile-val-backup".
	// +optional
	Dataset *string `json:"dataset,omitempty"`
}

// QuantizationSpec configures post-download quantization of model weights.
// When set on a ModelCache, the controller creates a quantization Job after
// the download completes.
// +kubebuilder:object:generate=true
type QuantizationSpec struct {
	// Format is the target quantization format.
	// +kubebuilder:validation:Required
	Format QuantizationFormat `json:"format"`

	// GGUFType is the GGUF quantization level (e.g., Q4_K_M, Q5_K_M, Q8_0).
	// Only used when Format is GGUF.
	// +optional
	GGUFType string `json:"ggufType,omitempty"`

	// Bits is the quantization bit width for AWQ/GPTQ formats.
	// +optional
	Bits *int32 `json:"bits,omitempty"`

	// GroupSize is the quantization group size for AWQ/GPTQ formats.
	// +optional
	GroupSize *int32 `json:"groupSize,omitempty"`

	// UseGPU enables GPU-accelerated quantization (required for AWQ/GPTQ).
	// GGUF quantization runs on CPU only.
	// +optional
	UseGPU bool `json:"useGPU,omitempty"`

	// MaxMemoryGB limits the memory available to the quantization job.
	// Defaults to 32GB for GGUF, 48GB for AWQ/GPTQ.
	// +optional
	MaxMemoryGB *int32 `json:"maxMemoryGB,omitempty"`

	// TimeoutSeconds overrides the default 2-hour deadline for quantization jobs.
	// +kubebuilder:validation:Minimum=300
	// +kubebuilder:validation:Maximum=43200
	// +optional
	TimeoutSeconds *int64 `json:"timeoutSeconds,omitempty"`

	// Sym enables symmetric quantization for GPTQ (default true).
	// sym=true is required for ExLlama kernels (best ROCm performance).
	// Ignored for non-GPTQ formats.
	// +optional
	Sym *bool `json:"sym,omitempty"`

	// DescAct enables activation reordering (desc_act) for GPTQ (default false).
	// desc_act=false gives faster inference; true gives slightly better quality.
	// Ignored for non-GPTQ formats.
	// +optional
	DescAct *bool `json:"descAct,omitempty"`

	// Calibration configures calibration parameters for AWQ/GPTQ quantization.
	// Ignored for GGUF and EXL2 formats.
	// +optional
	Calibration *CalibrationSpec `json:"calibration,omitempty"`

	// GPUMemoryFraction caps the fraction of GPU VRAM available to the
	// quantization process (e.g. "0.80"). Defaults to "0.80".
	// Lower values leave more headroom for GPU driver overhead (important on ROCm
	// where HIP/GTT allocations bypass container cgroup limits).
	// Only used for GPTQ format.
	// +optional
	GPUMemoryFraction *string `json:"gpuMemoryFraction,omitempty"`

	// DynamicExclusion controls which module patterns are excluded from
	// quantization (kept at full precision). Defaults to "auto".
	//   - "auto": auto-detect hybrid architectures and exclude attention/expert/
	//     vision/MTP modules (matches official Qwen GPTQ-Int4 approach).
	//   - "none": quantize all modules to the target bit width (pure INT4).
	//     Produces smaller models that fit on smaller VRAM cards.
	// Only used for GPTQ format.
	// +kubebuilder:validation:Enum=auto;none
	// +optional
	DynamicExclusion *string `json:"dynamicExclusion,omitempty"`

	// NodeSelector overrides spec.nodeSelector for quantization jobs.
	// Useful when quantization needs a different node (e.g., more RAM).
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

// QuantizationStatus records the result of quantization.
// +kubebuilder:object:generate=true
type QuantizationStatus struct {
	// Format is the quantization format that was applied.
	Format string `json:"format,omitempty"`

	// Type is the specific quantization type (e.g., Q4_K_M for GGUF).
	Type string `json:"type,omitempty"`

	// OriginalSizeBytes is the size of the model before quantization.
	OriginalSizeBytes int64 `json:"originalSizeBytes,omitempty"`

	// CompressedSizeBytes is the size of the model after quantization.
	CompressedSizeBytes int64 `json:"compressedSizeBytes,omitempty"`

	// CompressionRatio is the ratio of original to compressed size (e.g., "3.75").
	CompressionRatio string `json:"compressionRatio,omitempty"`

	// QuantizationTime is the wall-clock duration of the quantization job.
	QuantizationTime string `json:"quantizationTime,omitempty"`

	// StartedAt is the timestamp when the quantization job started.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// CompletedAt is the timestamp when the quantization job completed.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// CalibrationParams records the calibration parameters that were actually used.
	// +optional
	CalibrationParams *CalibrationSpec `json:"calibrationParams,omitempty"`

	// Progress is the current job progress percentage (0-100), read from structured telemetry.
	// +optional
	Progress *int32 `json:"progress,omitempty"`

	// ProgressDetail is a human-readable description of current progress (e.g., "layer 152/336").
	// +optional
	ProgressDetail string `json:"progressDetail,omitempty"`

	// FailureMessage contains the last lines of pod logs on failure.
	// +optional
	FailureMessage string `json:"failureMessage,omitempty"`
}
