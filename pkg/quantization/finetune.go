// Package quantization — finetune job builder.
// Finetuning runs LoRA/QLoRA or full finetuning via Unsloth after abliteration
// (if configured) and before quantization. The finetuned weights are saved
// in-place on the PVC so the quantization step sees the updated model.
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
	// DefaultFinetuneMemoryGB is the default memory limit for finetune jobs.
	DefaultFinetuneMemoryGB = 56

	// DefaultFinetuneDeadlineSeconds is the default 6-hour deadline.
	DefaultFinetuneDeadlineSeconds = 21600

	// DefaultFinetuneEpochs is the default number of training epochs.
	DefaultFinetuneEpochs = 3

	// DefaultFinetuneBatchSize is the default per-device training batch size.
	DefaultFinetuneBatchSize = 4

	// DefaultFinetuneLearningRate is the default learning rate.
	DefaultFinetuneLearningRate = "2e-4"

	// DefaultFinetuneMaxSeqLen is the default max sequence length for training.
	DefaultFinetuneMaxSeqLen = 2048

	// DefaultFinetuneLoRARank is the default LoRA rank.
	DefaultFinetuneLoRARank = 16

	// DefaultFinetuneLoRAAlpha is the default LoRA alpha.
	DefaultFinetuneLoRAAlpha = 32
)

// BuildFinetuneJob creates a Kubernetes Job that finetunes model weights on the PVC.
func BuildFinetuneJob(params JobParams, spec *aiv1alpha1.FinetuneSpec) (*batchv1.Job, error) {
	if spec == nil {
		return nil, fmt.Errorf("finetune spec is nil")
	}

	memoryGB := int32(DefaultFinetuneMemoryGB)
	if spec.MaxMemoryGB != nil && *spec.MaxMemoryGB > 0 {
		memoryGB = *spec.MaxMemoryGB
	}

	deadline := int64(DefaultFinetuneDeadlineSeconds)
	if spec.TimeoutSeconds != nil && *spec.TimeoutSeconds >= 300 {
		deadline = *spec.TimeoutSeconds
	}

	image := finetuneImage(params.GPUVendor, params.GPUArch)
	// Prefer GPUProfile-specific runtime images first so per-arch/per-node
	// immutable digests can override the global fallback cleanly.
	if params.ProfileQuantizerImage != "" {
		image = params.ProfileQuantizerImage
	} else if img := runtimeImageForQuantization(); img != "" {
		image = img
	}
	ftEnv := finetuneEnv(params.ModelPath, spec)
	script := finetuneWrapperScript()

	backoffLimit := int32(2)
	pvcVol, pvcMount := modelPVCVolume(params.PVCName)
	wsVol, wsMount := workspaceVolume(fmt.Sprintf("%dGi", memoryGB*2))

	volumes := []corev1.Volume{pvcVol, wsVol}
	mounts := []corev1.VolumeMount{pvcMount, wsMount}

	// Mount dataset PVC if specified.
	if spec.Dataset.PVCName != nil && *spec.Dataset.PVCName != "" {
		dsVol := corev1.Volume{
			Name: "dataset",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: *spec.Dataset.PVCName,
					ReadOnly:  true,
				},
			},
		}
		dsMount := corev1.VolumeMount{
			Name:      "dataset",
			MountPath: "/datasets",
			ReadOnly:  true,
		}
		if spec.Dataset.PVCSubPath != nil && *spec.Dataset.PVCSubPath != "" {
			dsMount.SubPath = *spec.Dataset.PVCSubPath
		}
		volumes = append(volumes, dsVol)
		mounts = append(mounts, dsMount)
	}

	useGPU := spec.UseGPU == nil || *spec.UseGPU // default true
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

	if useGPU {
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
				Name:            "finetuner",
				Image:           image,
				ImagePullPolicy: corev1.PullAlways,
				Command:         []string{"/bin/bash", "-c"},
				Args:            []string{script},
				Env:             append(env, ftEnv...),
				VolumeMounts:    mounts,
				Resources:       resources,
			},
		},
		Volumes: volumes,
	}

	if len(params.NodeSelector) > 0 {
		podSpec.NodeSelector = params.NodeSelector
	}
	if len(params.Tolerations) > 0 {
		podSpec.Tolerations = params.Tolerations
	}

	jobMeta := metav1.ObjectMeta{
		Name:      fmt.Sprintf("%s-finetune", params.Name),
		Namespace: params.Namespace,
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "flexinfer",
			"flexinfer.ai/component":       "finetuner",
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

// finetuneImage returns the container image for finetune jobs.
func finetuneImage(gpuVendor, gpuArch string) string {
	// Prefer unified runtime image when available.
	if img := runtimeImageForQuantization(); img != "" {
		return img
	}
	if img := os.Getenv("FLEXINFER_FINETUNE_IMAGE"); img != "" {
		return img
	}
	if gpuVendor == "amd" {
		return gptqQuantizerROCmImage(gpuArch)
	}
	return gptqQuantizerImage()
}

// finetuneEnv returns environment variables for the finetune script.
func finetuneEnv(modelPath string, spec *aiv1alpha1.FinetuneSpec) []corev1.EnvVar {
	mode := "qlora"
	if spec.Mode != nil {
		mode = string(*spec.Mode)
	}

	epochs := int32(DefaultFinetuneEpochs)
	if spec.Epochs != nil && *spec.Epochs > 0 {
		epochs = *spec.Epochs
	}

	batchSize := int32(DefaultFinetuneBatchSize)
	if spec.BatchSize != nil && *spec.BatchSize > 0 {
		batchSize = *spec.BatchSize
	}

	lr := DefaultFinetuneLearningRate
	if spec.LearningRate != nil && *spec.LearningRate != "" {
		lr = *spec.LearningRate
	}

	maxSeqLen := int32(DefaultFinetuneMaxSeqLen)
	if spec.MaxSeqLen != nil && *spec.MaxSeqLen > 0 {
		maxSeqLen = *spec.MaxSeqLen
	}

	loraRank := int32(DefaultFinetuneLoRARank)
	loraAlpha := int32(DefaultFinetuneLoRAAlpha)
	loraDropout := "0.05"
	var targetModules string
	if spec.LoRA != nil {
		if spec.LoRA.Rank != nil && *spec.LoRA.Rank > 0 {
			loraRank = *spec.LoRA.Rank
		}
		if spec.LoRA.Alpha != nil {
			loraAlpha = *spec.LoRA.Alpha
		}
		if spec.LoRA.Dropout != nil && *spec.LoRA.Dropout != "" {
			loraDropout = *spec.LoRA.Dropout
		}
		if len(spec.LoRA.TargetModules) > 0 {
			targetModules = strings.Join(spec.LoRA.TargetModules, ",")
		}
	}

	mergeAdapter := "true"
	if spec.MergeAdapter != nil && !*spec.MergeAdapter {
		mergeAdapter = "false"
	}

	gradCheckpoint := "true"
	if spec.GradientCheckpointing != nil && !*spec.GradientCheckpointing {
		gradCheckpoint = "false"
	}

	datasetSource := ""
	if spec.Dataset.HuggingFace != nil && *spec.Dataset.HuggingFace != "" {
		datasetSource = *spec.Dataset.HuggingFace
	}

	datasetSplit := "train"
	if spec.Dataset.Split != nil && *spec.Dataset.Split != "" {
		datasetSplit = *spec.Dataset.Split
	}

	maxSamples := "0"
	if spec.Dataset.MaxSamples != nil && *spec.Dataset.MaxSamples > 0 {
		maxSamples = fmt.Sprintf("%d", *spec.Dataset.MaxSamples)
	}

	datasetPVCPath := ""
	if spec.Dataset.PVCName != nil && *spec.Dataset.PVCName != "" {
		datasetPVCPath = "/datasets"
	}

	return []corev1.EnvVar{
		{Name: "MODEL_DIR", Value: fmt.Sprintf("/cache/%s", modelPath)},
		{Name: "MODE", Value: mode},
		{Name: "EPOCHS", Value: fmt.Sprintf("%d", epochs)},
		{Name: "BATCH_SIZE", Value: fmt.Sprintf("%d", batchSize)},
		{Name: "LEARNING_RATE", Value: lr},
		{Name: "MAX_SEQ_LEN", Value: fmt.Sprintf("%d", maxSeqLen)},
		{Name: "LORA_RANK", Value: fmt.Sprintf("%d", loraRank)},
		{Name: "LORA_ALPHA", Value: fmt.Sprintf("%d", loraAlpha)},
		{Name: "LORA_DROPOUT", Value: loraDropout},
		{Name: "TARGET_MODULES", Value: targetModules},
		{Name: "MERGE_ADAPTER", Value: mergeAdapter},
		{Name: "GRAD_CHECKPOINT", Value: gradCheckpoint},
		{Name: "DATASET_SOURCE", Value: datasetSource},
		{Name: "DATASET_SPLIT", Value: datasetSplit},
		{Name: "MAX_SAMPLES", Value: maxSamples},
		{Name: "DATASET_PVC_PATH", Value: datasetPVCPath},
		{Name: "FLEXINFER_TELEMETRY", Value: "true"},
	}
}

// finetuneWrapperScript returns the shell wrapper for finetuning.
// It delegates to the Python script at /opt/flexinfer/scripts/finetune.py.
func finetuneWrapperScript() string {
	return `set -euo pipefail
START_TS=$(date +%s)
LOGFILE=/tmp/finetune-output.log
# Persist full log to PVC for post-mortem analysis (survives pod GC)
PVC_LOGDIR="${MODEL_DIR}/.flexinfer-logs"
PVC_LOGFILE="${PVC_LOGDIR}/finetune-$(date +%Y%m%d-%H%M%S).log"

# Structured JSON event emitter for Loki/OTEL queryability.
# Events go to stdout (Promtail → Loki) and are queryable via:
#   {namespace="flexinfer-system"} | json | event="finetune_start"
emit_event() {
    local event="$1"; shift
    local ts
    ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    local json="{\"ts\":\"${ts}\",\"component\":\"finetuner\",\"event\":\"${event}\""
    while [ $# -ge 2 ]; do
        local key="$1" val="$2"; shift 2
        case "${val}" in
            ''|*[!0-9.]*) json="${json},\"${key}\":\"${val}\"" ;;
            *)            json="${json},\"${key}\":${val}" ;;
        esac
    done
    json="${json}}"
    echo "${json}"
}

cleanup() {
    local ec=$?
    # Persist log to PVC before anything else
    if [ -f "${LOGFILE}" ]; then
        mkdir -p "${PVC_LOGDIR}"
        cp "${LOGFILE}" "${PVC_LOGFILE}" 2>/dev/null || true
        # Keep only last 3 log files to avoid PVC bloat
        ls -t "${PVC_LOGDIR}"/finetune-*.log 2>/dev/null | tail -n +4 | xargs rm -f 2>/dev/null || true
    fi
    if [ $ec -ne 0 ]; then
        emit_event "finetune_error" "exit_code" "${ec}" "model" "${MODEL_DIR}" "mode" "${MODE}"
        {
            echo "exit_code=${ec}"
            echo "---"
            tail -80 "${LOGFILE}" 2>/dev/null || echo "(no log output captured)"
        } > /dev/termination-log 2>/dev/null || true
    fi
}
trap cleanup EXIT

# Tee all output so cleanup can capture tail on failure
exec > >(tee -a "${LOGFILE}") 2>&1

emit_event "finetune_start" "model" "${MODEL_DIR}" "mode" "${MODE}" "epochs" "${EPOCHS}" "batch_size" "${BATCH_SIZE}" "lr" "${LEARNING_RATE}" "lora_rank" "${LORA_RANK}"
echo "=== FlexInfer Finetune ==="
echo "Model: ${MODEL_DIR}"
echo "Mode: ${MODE}"
echo "Epochs: ${EPOCHS}"
echo "Batch size: ${BATCH_SIZE}"
echo "LR: ${LEARNING_RATE}"
echo "Max seq len: ${MAX_SEQ_LEN}"
echo "LoRA rank: ${LORA_RANK}, alpha: ${LORA_ALPHA}"
echo "Merge adapter: ${MERGE_ADAPTER}"
echo "Gradient checkpointing: ${GRAD_CHECKPOINT}"
echo "Dataset: ${DATASET_SOURCE:-$DATASET_PVC_PATH}"
echo "Start: $(date -u +%Y-%m-%dT%H:%M:%SZ)"

PYTHON_BIN=python3

if python3 -c "import torch,sys; sys.exit(0 if getattr(torch.version, 'hip', None) else 1)" 2>/dev/null; then
    echo "ROCm runtime detected; preparing isolated Unsloth AMD environment"
    UNSLOTH_VENV=/workspace/unsloth-amd-venv
    rm -rf "${UNSLOTH_VENV}"
    if python3 -m venv "${UNSLOTH_VENV}" && \
       . "${UNSLOTH_VENV}/bin/activate" && \
       python -m pip install --no-cache-dir --upgrade pip setuptools wheel && \
       python -m pip install --no-cache-dir --upgrade \
         torch==2.8.0 pytorch-triton-rocm torchvision torchaudio \
         torchao==0.13.0 xformers \
         --index-url https://download.pytorch.org/whl/rocm6.4 && \
       python -m pip install --no-cache-dir --no-deps unsloth unsloth-zoo && \
       python -m pip install --no-cache-dir --no-deps \
         git+https://github.com/unslothai/unsloth-zoo.git && \
       python -m pip install --no-cache-dir \
         "unsloth[amd] @ git+https://github.com/unslothai/unsloth" && \
       python -c "import torch, unsloth; print('Unsloth AMD ready:', torch.__version__, getattr(torch.version, 'hip', None))"; then
        PYTHON_BIN=python
    else
        echo "WARN: Unsloth AMD environment setup failed; falling back to base runtime"
        deactivate 2>/dev/null || true
        rm -rf "${UNSLOTH_VENV}"
        PYTHON_BIN=python3
    fi
else
    # Remove packages that frequently break the runtime image in-place.
    # torchvision is unnecessary for text finetuning and is often ABI-mismatched.
    pip uninstall -y torchvision 2>/dev/null || true

    pip install --no-cache-dir --quiet "unsloth[cu124-ampere-torch250]" 2>/dev/null || \
    pip install --no-cache-dir --quiet unsloth 2>/dev/null || \
    echo "WARN: unsloth install failed, falling back to transformers SFTTrainer"
    CURRENT_TF=$(python3 -c "import transformers; print(transformers.__version__)" 2>/dev/null || echo "0")
    echo "transformers ${CURRENT_TF}"
    if ! python3 -c "import transformers.models.qwen3.modeling_qwen3" 2>/dev/null; then
        echo "Qwen3 import failed; reinstalling compatible transformers stack"
        pip uninstall -y torchao 2>/dev/null || true
        pip install --no-cache-dir --quiet "transformers>=5.0" 2>/dev/null || true
    fi
fi

${PYTHON_BIN} /opt/flexinfer/scripts/finetune.py

END_TS=$(date +%s)
DURATION=$((END_TS - START_TS))
emit_event "finetune_complete" "model" "${MODEL_DIR}" "mode" "${MODE}" "duration_sec" "${DURATION}"
echo "=== Finetune finished in ${DURATION}s ==="
`
}
