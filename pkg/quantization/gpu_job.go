package quantization

import (
	"fmt"
	"os"
	"strconv"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// GPUMemoryConfig holds memory sizing from GPUProfile or defaults.
type GPUMemoryConfig struct {
	// ContainerMemoryGB is the K8s container memory limit.
	ContainerMemoryGB int32
	// MaxGPUMemoryGB is the GPU memory budget for accelerate device_map.
	MaxGPUMemoryGB int32
	// MaxCPUMemoryGB is the CPU memory budget for offloading.
	MaxCPUMemoryGB int32
	// GPUDriverMemoryMB is the out-of-cgroup GPU driver overhead (HIP/GTT on ROCm).
	// When > 0, job memory requests/limits are inflated by this amount so the K8s
	// scheduler reserves enough node RAM for the true total footprint.
	GPUDriverMemoryMB int32
	// GPUVramMB is the usable GPU VRAM from GPUProfile. Passed to quantization
	// scripts as GPU_VRAM_MB so they can bypass broken hipMemGetInfo (gfx906).
	GPUVramMB int64
}

// DefaultGPUMemoryConfig returns the default memory configuration.
func DefaultGPUMemoryConfig() GPUMemoryConfig {
	return GPUMemoryConfig{
		ContainerMemoryGB: DefaultGPUQuantizationMemoryGB,
		MaxGPUMemoryGB:    0, // 0 = use arch-specific heuristic as fallback
		MaxCPUMemoryGB:    0, // 0 = use arch-specific heuristic as fallback
	}
}

// GPUMemoryConfigFromProfile creates a GPUMemoryConfig from a GPUProfile, with defaults.
func GPUMemoryConfigFromProfile(profile *aiv1alpha2.GPUProfileSpec) GPUMemoryConfig {
	cfg := DefaultGPUMemoryConfig()
	if profile != nil {
		if profile.ContainerMemoryGB != nil {
			cfg.ContainerMemoryGB = *profile.ContainerMemoryGB
		}
		if profile.MaxGPUMemoryGB != nil {
			cfg.MaxGPUMemoryGB = *profile.MaxGPUMemoryGB
		}
		if profile.MaxCPUMemoryGB != nil {
			cfg.MaxCPUMemoryGB = *profile.MaxCPUMemoryGB
		}
		if profile.GPUDriverMemoryMB != nil {
			cfg.GPUDriverMemoryMB = *profile.GPUDriverMemoryMB
		}
		cfg.GPUVramMB = profile.VRAMMB
	}
	return cfg
}

// quantizationCPUCores returns the CPU core count for quantization jobs.
// Reads FLEXINFER_GPTQ_CPU_CORES env var, falls back to DefaultGPUQuantizationCPU.
func quantizationCPUCores() int {
	if v := os.Getenv("FLEXINFER_GPTQ_CPU_CORES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return DefaultGPUQuantizationCPU
}

const (
	// DefaultAWQImage is the default image used for AWQ quantization jobs.
	DefaultAWQImage = "ghcr.io/flexinfer/quantizer:awq"

	// DefaultGPTQImage is the default image used for GPTQ quantization jobs (CUDA).
	DefaultGPTQImage = "ghcr.io/flexinfer/quantizer:gptq"

	// DefaultGPTQROCmImage is the default image used for GPTQ quantization on ROCm (gfx1100).
	DefaultGPTQROCmImage = "registry.harbor.lan/flexinfer/quantizer:gptq-rocm-gfx1100"

	// DefaultGPTQROCmGFX906Image is the unified runtime image for Radeon VII (gfx906).
	// Based on mixa3607/pytorch-gfx906 (ROCm 6.3.3 + PyTorch 2.9), which restores
	// GPU compute broken in ROCm 6.4+. Includes GPTQModel, bitsandbytes, diffusers.
	DefaultGPTQROCmGFX906Image = "registry.harbor.lan/flexinfer/runtime:unified-gfx906-v3"

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

	// Account for GPU driver memory (HIP/GTT) that lives outside the cgroup.
	// Inflate both request and limit so the K8s scheduler reserves enough node
	// RAM for the true total footprint (in-cgroup process + out-of-cgroup driver).
	schedulingMemoryGB := memoryGB
	if params.MemoryConfig.GPUDriverMemoryMB > 0 {
		schedulingMemoryGB += params.MemoryConfig.GPUDriverMemoryMB / 1024
	}

	// Set memory allocator config for AMD GPUs to reduce fragmentation.
	var env []corev1.EnvVar
	if params.GPUVendor == "amd" {
		env = append(env, rocmAllocatorEnv())
	}
	env = append(env, extraEnv...)

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{
			{
				Name:            "quantizer",
				Image:           image,
				ImagePullPolicy: ImagePullPolicyForImage(image),
				Command:         []string{"/bin/bash", "-c"},
				Args:            []string{script},
				Env:             env,
				VolumeMounts: []corev1.VolumeMount{
					pvcMount,
					wsMount,
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", quantizationCPUCores())),
						corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryRequestForLimitGB(schedulingMemoryGB))),
						gpuResource:           resource.MustParse("1"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", schedulingMemoryGB)),
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
