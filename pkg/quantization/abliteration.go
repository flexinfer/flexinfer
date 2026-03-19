// Package quantization — abliteration job builder.
// Abliteration removes the "refusal direction" from transformer model weights
// by running contrastive prompts (harmful vs harmless), computing mean activation
// differences at each decoder layer, and orthogonalizing weight matrices against
// this direction. Weights are modified in-place on the PVC before quantization.
package quantization

import (
	"encoding/json"
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

type abliterationModelPolicy struct {
	Name                     string   `json:"name"`
	MatchModelTypes          []string `json:"match_model_types,omitempty"`
	MatchPathSubstrings      []string `json:"match_path_substrings,omitempty"`
	TokenizerFixMistralRegex *bool    `json:"tokenizer_fix_mistral_regex,omitempty"`
	SaveFormat               string   `json:"save_format,omitempty"`
	SaveMaxShardSize         string   `json:"save_max_shard_size,omitempty"`
}

// memoryRequestForLimitGB keeps large single-node jobs schedulable by requesting
// less than their peak memory limit. We still enforce the full limit at runtime.
func memoryRequestForLimitGB(limitGB int32) int32 {
	if limitGB <= 0 {
		return 1
	}
	requestGB := (limitGB * 4) / 5
	if requestGB < 8 {
		requestGB = limitGB
	}
	if requestGB > limitGB {
		requestGB = limitGB
	}
	return requestGB
}

func abliterationCPUMaxMemoryGB(limitGB int32) int32 {
	if limitGB <= 0 {
		return 12
	}
	cpuGB := limitGB - 36
	if cpuGB < 12 {
		cpuGB = 12
	}
	if cpuGB > 32 {
		cpuGB = 32
	}
	return cpuGB
}

func abliterationGPUMaxMemoryGB(useGPU bool) int32 {
	if !useGPU {
		return 0
	}
	return 20
}

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
	// Prefer GPUProfile-specific runtime images first so per-arch/per-node
	// immutable digests can override the global fallback cleanly.
	if params.ProfileQuantizerImage != "" {
		image = params.ProfileQuantizerImage
	} else if img := runtimeImageForQuantization(); img != "" {
		image = img
	}
	ablitEnv := abliterationEnv(params.ModelPath, ablitSpec)
	script := abliterationWrapperScript()

	backoffLimit := int32(2)
	pvcVol, pvcMount := modelPVCVolume(params.PVCName)
	wsVol, wsMount := workspaceVolume(fmt.Sprintf("%dGi", memoryGB*2))

	var env []corev1.EnvVar
	memoryRequestGB := memoryRequestForLimitGB(memoryGB)
	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", DefaultGPUQuantizationCPU)),
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryRequestGB)),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
		},
	}

	if ablitSpec.UseGPU {
		gpuResourceName := "nvidia.com/gpu"
		if params.GPUVendor == "amd" {
			gpuResourceName = "amd.com/gpu"
			env = append(env, rocmAllocatorEnv())
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
	maxMemoryGB := int32(DefaultAbliterationMemoryGB)
	if spec.NumSamples != nil && *spec.NumSamples > 0 {
		numSamples = *spec.NumSamples
	}
	if spec.MaxMemoryGB != nil && *spec.MaxMemoryGB > 0 {
		maxMemoryGB = *spec.MaxMemoryGB
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

	deviceMap := os.Getenv("FLEXINFER_ABLITERATION_DEVICE_MAP")
	if deviceMap == "" {
		deviceMap = "cpu"
		if spec.UseGPU {
			deviceMap = "auto"
		}
	}

	progressInterval := os.Getenv("FLEXINFER_ABLITERATION_PROGRESS_INTERVAL")
	if progressInterval == "" {
		progressInterval = "10"
	}
	promptMaxLength := os.Getenv("FLEXINFER_ABLITERATION_PROMPT_MAX_LENGTH")
	if promptMaxLength == "" {
		promptMaxLength = "256"
	}
	saveFormat := os.Getenv("FLEXINFER_ABLITERATION_SAVE_FORMAT")
	if saveFormat == "" {
		saveFormat = "auto"
	}
	activationCaptureMode := os.Getenv("FLEXINFER_ABLITERATION_ACTIVATION_CAPTURE_MODE")
	if activationCaptureMode == "" {
		activationCaptureMode = "hooks"
	}
	memoryTrimInterval := os.Getenv("FLEXINFER_ABLITERATION_MEMORY_TRIM_INTERVAL")
	if memoryTrimInterval == "" {
		memoryTrimInterval = "1"
	}
	forwardUseCache := os.Getenv("FLEXINFER_ABLITERATION_FORWARD_USE_CACHE")
	if forwardUseCache == "" {
		forwardUseCache = "false"
	}
	saveMaxShardSize := os.Getenv("FLEXINFER_ABLITERATION_SAVE_MAX_SHARD_SIZE")
	if saveMaxShardSize == "" {
		saveMaxShardSize = "1GB"
	}
	saveImpl := os.Getenv("FLEXINFER_ABLITERATION_SAVE_IMPL")
	if saveImpl == "" {
		saveImpl = "streaming"
	}
	resume := os.Getenv("FLEXINFER_ABLITERATION_RESUME")
	if resume == "" {
		resume = "true"
	}
	cpuMaxMemoryGB := os.Getenv("FLEXINFER_ABLITERATION_CPU_MAX_MEMORY_GB")
	if cpuMaxMemoryGB == "" {
		cpuMaxMemoryGB = fmt.Sprintf("%d", abliterationCPUMaxMemoryGB(maxMemoryGB))
	}
	gpuMaxMemoryGB := os.Getenv("FLEXINFER_ABLITERATION_GPU_MAX_MEMORY_GB")
	if gpuMaxMemoryGB == "" {
		gpuMaxMemoryGB = fmt.Sprintf("%d", abliterationGPUMaxMemoryGB(spec.UseGPU))
	}
	offloadDir := os.Getenv("FLEXINFER_ABLITERATION_OFFLOAD_DIR")
	if offloadDir == "" {
		offloadDir = "/workspace/abliteration-offload"
	}
	modelPolicies := os.Getenv("FLEXINFER_ABLITERATION_MODEL_POLICIES")
	if modelPolicies == "" {
		modelPolicies = defaultAbliterationModelPoliciesJSON()
	}

	return []corev1.EnvVar{
		{Name: "MODEL_DIR", Value: fmt.Sprintf("/cache/%s", modelPath)},
		{Name: "NUM_SAMPLES", Value: fmt.Sprintf("%d", numSamples)},
		{Name: "TARGET_LAYERS", Value: targetLayers},
		{Name: "WEIGHT_MATRICES", Value: weightMatrices},
		{Name: "SKIP_VISION", Value: skipVision},
		{Name: "DEVICE_MAP", Value: deviceMap},
		{Name: "ABLITERATION_PROGRESS_INTERVAL", Value: progressInterval},
		{Name: "ABLITERATION_PROMPT_MAX_LENGTH", Value: promptMaxLength},
		{Name: "ABLITERATION_SAVE_FORMAT", Value: saveFormat},
		{Name: "ABLITERATION_ACTIVATION_CAPTURE_MODE", Value: activationCaptureMode},
		{Name: "ABLITERATION_MEMORY_TRIM_INTERVAL", Value: memoryTrimInterval},
		{Name: "ABLITERATION_FORWARD_USE_CACHE", Value: forwardUseCache},
		{Name: "ABLITERATION_SAVE_MAX_SHARD_SIZE", Value: saveMaxShardSize},
		{Name: "ABLITERATION_SAVE_IMPL", Value: saveImpl},
		{Name: "ABLITERATION_RESUME", Value: resume},
		{Name: "ABLITERATION_CPU_MAX_MEMORY_GB", Value: cpuMaxMemoryGB},
		{Name: "ABLITERATION_GPU_MAX_MEMORY_GB", Value: gpuMaxMemoryGB},
		{Name: "ABLITERATION_OFFLOAD_DIR", Value: offloadDir},
		{Name: "ABLITERATION_MODEL_POLICIES", Value: modelPolicies},
		{Name: "SAFETENSORS_FAST_GPU", Value: "0"},
		{Name: "HF_SAFETENSORS_MMAP", Value: "0"},
		{Name: "FLEXINFER_TELEMETRY", Value: "true"},
	}
}

func defaultAbliterationModelPoliciesJSON() string {
	trueVal := true
	policies := []abliterationModelPolicy{
		{
			Name:                     "qwen3.5-save-safetensors",
			MatchModelTypes:          []string{"qwen3_5", "qwen3_5_text"},
			MatchPathSubstrings:      []string{"qwen35", "qwen3.5"},
			TokenizerFixMistralRegex: &trueVal,
			SaveFormat:               "safetensors",
			SaveMaxShardSize:         "1GB",
		},
	}
	data, err := json.Marshal(policies)
	if err != nil {
		panic(fmt.Sprintf("marshal default abliteration model policies: %v", err))
	}
	return string(data)
}

// abliterationWrapperScript returns the shell wrapper for abliteration.
// It delegates to the Python script at /opt/flexinfer/scripts/abliterate.py.
func abliterationWrapperScript() string {
	return `set -euo pipefail
START_TS=$(date +%s)

dump_checkpoint() {
  local checkpoint="${MODEL_DIR}/.abliteration-checkpoint.json"
  if [ -f "${checkpoint}" ]; then
    echo "=== Last abliteration checkpoint ==="
    cat "${checkpoint}" || true
  fi
}

trap 'rc=$?; if [ $rc -ne 0 ]; then dump_checkpoint; fi; exit $rc' EXIT

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
