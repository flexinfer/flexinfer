package quantization

import (
	"fmt"
	"os"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

const (
	// DefaultGGUFType is the default quantization level for GGUF.
	// Q4_K_M provides a good balance of quality and compression.
	DefaultGGUFType = "Q4_K_M"

	// DefaultGGUFImage is the container image for GGUF quantization.
	DefaultGGUFImage = "ghcr.io/flexinfer/quantizer:gguf"

	// DefaultGGUFMemoryGB is the default memory limit for GGUF jobs.
	DefaultGGUFMemoryGB = 32

	// DefaultGGUFCPU is the default CPU request for GGUF jobs.
	DefaultGGUFCPU = 8

	// DefaultActiveDeadlineSeconds is the max runtime for quantization jobs.
	// GPTQ 27B + NFS save can take 2.5h+; we set a 4-hour deadline.
	DefaultActiveDeadlineSeconds int64 = 14400
)

// GGUFJobBuilder generates Kubernetes Jobs for GGUF quantization.
// GGUF conversion runs entirely on CPU using llama.cpp tools:
// 1. convert_hf_to_gguf.py — converts HuggingFace format to FP16 GGUF
// 2. llama-quantize — quantizes FP16 GGUF to the target type (Q4_K_M, etc.)
type GGUFJobBuilder struct{}

// Format returns the GGUF quantization format.
func (b *GGUFJobBuilder) Format() aiv1alpha1.QuantizationFormat {
	return aiv1alpha1.QuantizationFormatGGUF
}

// Validate checks that the quantization spec is valid for GGUF.
func (b *GGUFJobBuilder) Validate(spec *aiv1alpha1.QuantizationSpec) error {
	if spec.Format != aiv1alpha1.QuantizationFormatGGUF {
		return fmt.Errorf("GGUFJobBuilder only handles GGUF format, got %q", spec.Format)
	}
	if spec.GGUFType != "" && !IsValidGGUFType(spec.GGUFType) {
		return fmt.Errorf("invalid GGUF type %q; valid types: %s", spec.GGUFType, strings.Join(ValidGGUFTypes, ", "))
	}
	return nil
}

// BuildJob creates a batch/v1.Job that quantizes a model to GGUF format.
func (b *GGUFJobBuilder) BuildJob(params JobParams) (*batchv1.Job, error) {
	if err := b.Validate(params.Spec); err != nil {
		return nil, err
	}

	ggufType := params.Spec.GGUFType
	if ggufType == "" {
		ggufType = DefaultGGUFType
	}

	memoryGB := int32(DefaultGGUFMemoryGB)
	if params.Spec.MaxMemoryGB != nil {
		memoryGB = *params.Spec.MaxMemoryGB
	}

	image := quantizerImage()
	deadline := effectiveDeadline(params.Spec)
	backoffLimit := int32(2)

	ggufEnv := b.buildEnv(params.ModelPath, ggufType)
	script := b.ggufWrapperScript()

	pvcVol, pvcMount := modelPVCVolume(params.PVCName)
	wsVol, wsMount := workspaceVolume(fmt.Sprintf("%dGi", memoryGB*2))

	job := &batchv1.Job{
		ObjectMeta: defaultJobMeta(params),
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: &deadline,
			BackoffLimit:          &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "quantizer",
							Image:   image,
							Command: []string{"/bin/sh", "-c"},
							Args:    []string{script},
							Env:     ggufEnv,
							VolumeMounts: []corev1.VolumeMount{
								pvcMount,
								wsMount,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", DefaultGGUFCPU)),
									corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						pvcVol,
						wsVol,
					},
				},
			},
		},
	}

	if len(params.NodeSelector) > 0 {
		job.Spec.Template.Spec.NodeSelector = params.NodeSelector
	}
	if len(params.Tolerations) > 0 {
		job.Spec.Template.Spec.Tolerations = params.Tolerations
	}

	return job, nil
}

// buildEnv returns environment variables for the GGUF quantization script.
func (b *GGUFJobBuilder) buildEnv(modelPath, ggufType string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "MODEL_DIR", Value: fmt.Sprintf("/cache/%s", modelPath)},
		{Name: "GGUF_TYPE", Value: ggufType},
		{Name: "FLEXINFER_TELEMETRY", Value: "true"},
	}
}

// ggufWrapperScript returns the shell wrapper for GGUF quantization.
// It delegates to the script at /opt/flexinfer/scripts/quantize_gguf.sh.
func (b *GGUFJobBuilder) ggufWrapperScript() string {
	return `/opt/flexinfer/scripts/quantize_gguf.sh`
}

// quantizerImage returns the container image for GGUF quantization.
// Supports override via environment variable.
func quantizerImage() string {
	if img := os.Getenv("FLEXINFER_QUANTIZER_GGUF_IMAGE"); img != "" {
		return img
	}
	return DefaultGGUFImage
}
