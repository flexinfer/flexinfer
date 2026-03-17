// Package quantization — abliteration job builder.
// Abliteration removes the "refusal direction" from transformer model weights
// by running contrastive prompts (harmful vs harmless), computing mean activation
// differences at each decoder layer, and orthogonalizing weight matrices against
// this direction. Weights are modified in-place on the PVC before quantization.
package quantization

import (
	"fmt"
	"os"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

const (
	// DefaultAbliterationMemoryGB is the default memory limit for abliteration jobs.
	// 27B BF16 ≈ 54 GB + activation overhead.
	DefaultAbliterationMemoryGB = 56

	// DefaultAbliterationDeadlineSeconds is the default 4-hour deadline.
	DefaultAbliterationDeadlineSeconds = 14400

	// DefaultAbliterationNumSamples is the default number of contrastive prompt pairs.
	DefaultAbliterationNumSamples = 128
)

// BuildAbliterationJob creates a Kubernetes Job that abliterates model weights on the PVC.
// It reuses the GPTQ quantizer ROCm image (which has transformers, torch, accelerate).
func BuildAbliterationJob(params JobParams, ablitSpec *aiv1alpha1.AbliterationSpec) (*batchv1.Job, error) {
	if ablitSpec == nil {
		return nil, fmt.Errorf("abliteration spec is nil")
	}

	memoryGB := int32(DefaultAbliterationMemoryGB)
	if ablitSpec.MaxMemoryGB != nil && *ablitSpec.MaxMemoryGB > 0 {
		memoryGB = *ablitSpec.MaxMemoryGB
	}

	deadline := int64(DefaultAbliterationDeadlineSeconds)
	if ablitSpec.TimeoutSeconds != nil && *ablitSpec.TimeoutSeconds >= 300 {
		deadline = *ablitSpec.TimeoutSeconds
	}

	image := abliterationImage(params.GPUVendor, params.GPUArch)
	// GPUProfile image override takes priority.
	if params.ProfileQuantizerImage != "" {
		image = params.ProfileQuantizerImage
	}
	ablitEnv := abliterationEnv(params.ModelPath, ablitSpec)
	script := abliterationWrapperScript()

	backoffLimit := int32(2)
	pvcVol, pvcMount := modelPVCVolume(params.PVCName)
	wsVol, wsMount := workspaceVolume(fmt.Sprintf("%dGi", memoryGB*2))

	var env []corev1.EnvVar
	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", DefaultGPUQuantizationCPU)),
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
		},
	}

	if ablitSpec.UseGPU {
		gpuResourceName := "nvidia.com/gpu"
		if params.GPUVendor == "amd" {
			gpuResourceName = "amd.com/gpu"
			env = append(env, corev1.EnvVar{
				Name:  "PYTORCH_HIP_ALLOC_CONF",
				Value: "expandable_segments:True",
			})
		}
		gpuResource := corev1.ResourceName(gpuResourceName)
		resources.Requests[gpuResource] = resource.MustParse("1")
		resources.Limits[gpuResource] = resource.MustParse("1")
	}

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{
			{
				Name:            "abliterator",
				Image:           image,
				ImagePullPolicy: corev1.PullAlways,
				Command:         []string{"/bin/bash", "-c"},
				Args:            []string{script},
				Env:             append(env, ablitEnv...),
				VolumeMounts: []corev1.VolumeMount{
					pvcMount,
					wsMount,
				},
				Resources: resources,
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

	jobMeta := metav1.ObjectMeta{
		Name:      fmt.Sprintf("%s-abliterate", params.Name),
		Namespace: params.Namespace,
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "flexinfer",
			"flexinfer.ai/component":       "abliterator",
			"flexinfer.ai/cache":           params.Name,
		},
	}

	return &batchv1.Job{
		ObjectMeta: jobMeta,
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: &deadline,
			BackoffLimit:          &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: podSpec,
			},
		},
	}, nil
}

// abliterationImage returns the container image for abliteration jobs.
// Reuses the GPTQ quantizer image since it already has transformers + torch + accelerate.
func abliterationImage(gpuVendor, gpuArch string) string {
	// Prefer unified runtime image when available.
	if img := runtimeImageForQuantization(); img != "" {
		return img
	}
	if img := os.Getenv("FLEXINFER_ABLITERATOR_IMAGE"); img != "" {
		return img
	}
	if gpuVendor == "amd" {
		return gptqQuantizerROCmImage(gpuArch)
	}
	return gptqQuantizerImage()
}

// abliterationEnv returns environment variables for the abliteration script.
func abliterationEnv(modelPath string, spec *aiv1alpha1.AbliterationSpec) []corev1.EnvVar {
	numSamples := int32(DefaultAbliterationNumSamples)
	if spec.NumSamples != nil && *spec.NumSamples > 0 {
		numSamples = *spec.NumSamples
	}

	targetLayers := "auto"
	if spec.TargetLayers != nil && *spec.TargetLayers != "" {
		targetLayers = *spec.TargetLayers
	}

	weightMatrices := "o_proj,out_proj,down_proj"
	if len(spec.WeightMatrices) > 0 {
		weightMatrices = strings.Join(spec.WeightMatrices, ",")
	}

	skipVision := "true"
	if spec.SkipVisionLayers != nil && !*spec.SkipVisionLayers {
		skipVision = "false"
	}

	deviceMap := "cpu"
	if spec.UseGPU {
		deviceMap = "auto"
	}

	return []corev1.EnvVar{
		{Name: "MODEL_DIR", Value: fmt.Sprintf("/cache/%s", modelPath)},
		{Name: "NUM_SAMPLES", Value: fmt.Sprintf("%d", numSamples)},
		{Name: "TARGET_LAYERS", Value: targetLayers},
		{Name: "WEIGHT_MATRICES", Value: weightMatrices},
		{Name: "SKIP_VISION", Value: skipVision},
		{Name: "DEVICE_MAP", Value: deviceMap},
		{Name: "SAFETENSORS_FAST_GPU", Value: "0"},
		{Name: "HF_SAFETENSORS_MMAP", Value: "0"},
		{Name: "FLEXINFER_TELEMETRY", Value: "true"},
	}
}

// abliterationWrapperScript returns the shell wrapper for abliteration.
// It delegates to the Python script at /opt/flexinfer/scripts/abliterate.py.
func abliterationWrapperScript() string {
	return `set -euo pipefail
START_TS=$(date +%s)

echo "=== FlexInfer Abliteration ==="
echo "Model: ${MODEL_DIR}"
echo "Samples: ${NUM_SAMPLES}"
echo "Target layers: ${TARGET_LAYERS}"
echo "Weight matrices: ${WEIGHT_MATRICES}"
echo "Skip vision: ${SKIP_VISION}"
echo "Device map: ${DEVICE_MAP}"
echo "Start: $(date -u +%Y-%m-%dT%H:%M:%SZ)"

python3 /opt/flexinfer/scripts/abliterate.py

END_TS=$(date +%s)
DURATION=$((END_TS - START_TS))
echo "=== Abliteration finished in ${DURATION}s ==="
`
}
