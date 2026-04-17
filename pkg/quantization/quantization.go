// Package quantization provides Job builders for model weight quantization.
// Each quantization format (GGUF, AWQ, GPTQ, etc.) has a dedicated builder
// that generates a Kubernetes batch/v1.Job spec.
package quantization

import (
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// JobBuilder generates a Kubernetes Job for a specific quantization format.
type JobBuilder interface {
	// BuildJob creates the batch/v1.Job spec for quantization.
	BuildJob(params JobParams) (*batchv1.Job, error)

	// Format returns the quantization format this builder handles.
	Format() aiv1alpha2.QuantizationFormat

	// Validate checks that the quantization spec is valid for this format.
	Validate(spec *aiv1alpha2.QuantizationSpec) error
}

// JobParams contains the inputs needed to build a quantization job.
type JobParams struct {
	// Name is the base name for the job (usually the ModelCache name).
	Name string

	// Namespace is the Kubernetes namespace.
	Namespace string

	// PVCName is the PersistentVolumeClaim containing the model.
	PVCName string

	// ModelPath is the subdirectory within the PVC where the model is stored.
	ModelPath string

	// Spec is the quantization configuration from the ModelCache.
	Spec *aiv1alpha2.QuantizationSpec

	// GPUVendor selects the GPU resource name: "nvidia" (default) or "amd".
	GPUVendor string

	// GPUArch is the GPU microarchitecture (e.g. "gfx1100", "gfx906", "sm_80").
	// Used to select architecture-specific quantizer images on ROCm.
	GPUArch string

	// NodeSelector constrains which nodes the quantization job runs on.
	NodeSelector map[string]string

	// Tolerations allows the quantization pod to schedule on tainted nodes
	// (e.g. dedicated=gpu nodes).
	Tolerations []corev1.Toleration

	// ProfileQuantizerImage is an optional image override from a GPUProfile CR.
	// When set, it takes priority over env var and hardcoded defaults.
	ProfileQuantizerImage string

	// ProfileEnv is an optional set of GPUProfile-derived environment variables.
	// Job builders merge these last so per-architecture quirks can override
	// generic defaults without hardcoding them in each builder.
	ProfileEnv []corev1.EnvVar

	// MemoryConfig holds GPUProfile-derived memory sizing for container limits
	// and GPU/CPU memory budgets. When zero-valued, builders fall back to their
	// own defaults (DefaultGPUQuantizationMemoryGB, etc.).
	MemoryConfig GPUMemoryConfig
}

// FormatBackendCompatibility maps quantization formats to compatible backends.
var FormatBackendCompatibility = map[aiv1alpha2.QuantizationFormat][]string{
	aiv1alpha2.QuantizationFormatGGUF:              {"llamacpp", "ollama"},
	aiv1alpha2.QuantizationFormatAWQ:               {"vllm"},
	aiv1alpha2.QuantizationFormatGPTQ:              {"vllm"},
	aiv1alpha2.QuantizationFormatEXL2:              {"exllamav2"},
	aiv1alpha2.QuantizationFormatFP8:               {"vllm"},
	aiv1alpha2.QuantizationFormatCompressedTensors: {"vllm"},
}

// ValidGGUFTypes lists the supported GGUF quantization levels.
var ValidGGUFTypes = []string{
	"Q2_K", "Q3_K_S", "Q3_K_M", "Q3_K_L",
	"Q4_0", "Q4_K_S", "Q4_K_M",
	"Q5_0", "Q5_K_S", "Q5_K_M",
	"Q6_K", "Q8_0",
}

// IsValidGGUFType checks if a GGUF type string is recognized.
func IsValidGGUFType(t string) bool {
	for _, valid := range ValidGGUFTypes {
		if t == valid {
			return true
		}
	}
	return false
}

// GetBuilder returns the appropriate JobBuilder for the given format.
func GetBuilder(format aiv1alpha2.QuantizationFormat) (JobBuilder, error) {
	switch format {
	case aiv1alpha2.QuantizationFormatGGUF:
		return &GGUFJobBuilder{}, nil
	case aiv1alpha2.QuantizationFormatAWQ:
		return &AWQJobBuilder{}, nil
	case aiv1alpha2.QuantizationFormatGPTQ:
		return &GPTQJobBuilder{}, nil
	case aiv1alpha2.QuantizationFormatEXL2:
		return &EXL2JobBuilder{}, nil
	case aiv1alpha2.QuantizationFormatFP8:
		return &FP8JobBuilder{}, nil
	case aiv1alpha2.QuantizationFormatCompressedTensors:
		return &CompressedTensorsJobBuilder{}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, format)
	}
}

// defaultJobMeta creates the common ObjectMeta for quantization jobs.
func defaultJobMeta(params JobParams) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      fmt.Sprintf("%s-quantize", params.Name),
		Namespace: params.Namespace,
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "flexinfer",
			"flexinfer.ai/component":       "quantizer",
			"flexinfer.ai/cache":           params.Name,
			"flexinfer.ai/format":          string(params.Spec.Format),
		},
	}
}

// modelPVCVolume creates the volume and mount for the model PVC.
func modelPVCVolume(pvcName string) (corev1.Volume, corev1.VolumeMount) {
	vol := corev1.Volume{
		Name: "model-cache",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		},
	}
	mount := corev1.VolumeMount{
		Name:      "model-cache",
		MountPath: "/cache",
	}
	return vol, mount
}

// workspaceVolume creates an emptyDir volume for intermediate quantization files.
func workspaceVolume(sizeLimit string) (corev1.Volume, corev1.VolumeMount) {
	vol := corev1.Volume{
		Name: "workspace",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				SizeLimit: resourcePtr(sizeLimit),
			},
		},
	}
	mount := corev1.VolumeMount{
		Name:      "workspace",
		MountPath: "/workspace",
	}
	return vol, mount
}

// effectiveDeadline returns the job deadline from spec override or the default.
func effectiveDeadline(spec *aiv1alpha2.QuantizationSpec) int64 {
	if spec != nil && spec.TimeoutSeconds != nil && *spec.TimeoutSeconds >= 300 {
		return *spec.TimeoutSeconds
	}
	return DefaultActiveDeadlineSeconds
}

func resourcePtr(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}
