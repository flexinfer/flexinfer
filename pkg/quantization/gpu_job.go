package quantization

import (
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// DefaultAWQImage is the default image used for AWQ quantization jobs.
	DefaultAWQImage = "ghcr.io/flexinfer/quantizer:awq"

	// DefaultGPTQImage is the default image used for GPTQ quantization jobs (CUDA).
	DefaultGPTQImage = "ghcr.io/flexinfer/quantizer:gptq"

	// DefaultGPTQROCmImage is the default image used for GPTQ quantization on ROCm (gfx1100).
	DefaultGPTQROCmImage = "registry.harbor.lan/flexinfer/quantizer:gptq-rocm-gfx1100"

	// DefaultGPTQROCmGFX906Image is the GPTQ quantizer image for Radeon VII (gfx906).
	// Uses ROCm 6.2.3 + PyTorch 2.3 (last version with full gfx906 kernel support).
	DefaultGPTQROCmGFX906Image = "registry.harbor.lan/flexinfer/quantizer:gptq-rocm-gfx906"

	// DefaultGPUQuantizationMemoryGB is the default memory limit for AWQ/GPTQ jobs.
	DefaultGPUQuantizationMemoryGB = 48

	// DefaultGPUQuantizationCPU is the default CPU request for AWQ/GPTQ jobs.
	DefaultGPUQuantizationCPU = 8

	// DefaultCalibrationMaxSeqLen is the default max sequence length for calibration.
	DefaultCalibrationMaxSeqLen = 4096

	// DefaultCalibrationMaxSamples is the default number of calibration samples.
	DefaultCalibrationMaxSamples = 256

	// DefaultNParallelCalibSamples is the default parallel calibration batch size.
	// Keeps VRAM usage in check for 14B+ models on 24GB cards.
	DefaultNParallelCalibSamples = 16

	// DefaultCalibrationDataset is the HuggingFace dataset for calibration samples.
	DefaultCalibrationDataset = "mit-han-lab/pile-val-backup"

	// DefaultGPUMemoryFraction caps GPU VRAM usage during quantization.
	// 0.80 leaves 20% headroom for ROCm driver GTT overhead.
	DefaultGPUMemoryFraction = "0.80"

	// DefaultAWQBits is the default bit width for AWQ.
	DefaultAWQBits = 4

	// DefaultGPTQBits is the default bit width for GPTQ.
	DefaultGPTQBits = 4

	// DefaultQuantizationGroupSize is the default group size for AWQ/GPTQ.
	DefaultQuantizationGroupSize = 128
)

func buildGPUQuantizationJob(params JobParams, image, script string, memoryGB int32, extraEnv []corev1.EnvVar) (*batchv1.Job, error) {
	deadline := effectiveDeadline(params.Spec)
	backoffLimit := int32(2)
	pvcVol, pvcMount := modelPVCVolume(params.PVCName)
	wsVol, wsMount := workspaceVolume(fmt.Sprintf("%dGi", memoryGB*2))

	gpuResourceName := "nvidia.com/gpu"
	if params.GPUVendor == "amd" {
		gpuResourceName = "amd.com/gpu"
	}
	gpuResource := corev1.ResourceName(gpuResourceName)

	// Set memory allocator config for AMD GPUs to reduce fragmentation.
	var env []corev1.EnvVar
	if params.GPUVendor == "amd" {
		env = append(env, corev1.EnvVar{
			Name:  "PYTORCH_HIP_ALLOC_CONF",
			Value: "expandable_segments:True",
		})
	}
	env = append(env, extraEnv...)

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{
			{
				Name:            "quantizer",
				Image:           image,
				ImagePullPolicy: corev1.PullAlways,
				Command:         []string{"/bin/bash", "-c"},
				Args:            []string{script},
				Env:             env,
				VolumeMounts: []corev1.VolumeMount{
					pvcMount,
					wsMount,
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", DefaultGPUQuantizationCPU)),
						corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
						gpuResource:           resource.MustParse("1"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
						gpuResource:           resource.MustParse("1"),
					},
				},
			},
		},
		Volumes: []corev1.Volume{
			pvcVol,
			wsVol,
		},
	}

	if len(params.NodeSelector) > 0 {
		podSpec.NodeSelector = params.NodeSelector
	}
	if len(params.Tolerations) > 0 {
		podSpec.Tolerations = params.Tolerations
	}

	return &batchv1.Job{
		ObjectMeta: defaultJobMeta(params),
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: &deadline,
			BackoffLimit:          &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: podSpec,
			},
		},
	}, nil
}
