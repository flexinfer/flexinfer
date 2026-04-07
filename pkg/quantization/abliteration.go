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
	LoadAutoClass            string   `json:"load_auto_class,omitempty"`
	DecoderLayersPath        string   `json:"decoder_layers_path,omitempty"`
	LMHeadPath               string   `json:"lm_head_path,omitempty"`
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

// abliterationCPUMaxMemoryGB computes the CPU memory budget for accelerate's max_memory dict.
// With device_map=auto, layers that don't fit in GPU+CPU spill to disk offload (mmap from
// NFS-backed PVC), which is 10x slower. For Qwen3.5-27B BF16 (~54GB), gpu=12 + cpu=32 = 44GB
// causes ~10GB disk offload stall. Override via FLEXINFER_ABLITERATION_CPU_MAX_MEMORY_GB=56.
// TODO: derive from node allocatable memory instead of hardcoding a cap.
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

// abliterationGPUMaxMemoryGB returns the GPU memory budget for accelerate's max_memory dict.
// WARNING: Must not exceed physical VRAM. transformers 5.x caching_allocator_warmup calls
// torch.empty(max_memory_bytes) on the GPU -- if this exceeds VRAM, gfx906 returns
// "HIP error: invalid argument" (not OOM). Radeon VII = 16GB, use 12 with headroom.
// Override via FLEXINFER_ABLITERATION_GPU_MAX_MEMORY_GB.
// TODO: read from GPUProfile.spec.vramMB instead of hardcoding.
func abliterationGPUMaxMemoryGB(useGPU bool, gpuArch string) int32 {
	if !useGPU {
		return 0
	}
	if gpuArch == "gfx906" {
		// Radeon VII only has 16 GiB HBM2. Keep headroom so the gfx906
		// mem_get_info shim does not over-advertise VRAM to accelerate and
		// over-place layers onto the GPU during dispatch.
		return 14
	}
	return 20
}

// BuildAbliterationJob creates a Kubernetes Job that abliterates model weights on the PVC.
// It reuses the GPTQ quantizer ROCm image (which has transformers, torch, accelerate).
func BuildAbliterationJob(params JobParams, ablitSpec *aiv1alpha1.AbliterationSpec) (*batchv1.Job, error) {
	if ablitSpec == nil {
		return nil, fmt.Errorf("abliteration spec is nil")
	}

	// Container memory priority: spec > GPUProfile > hardcoded default.
	memoryGB := int32(DefaultAbliterationMemoryGB)
	if params.MemoryConfig.ContainerMemoryGB > 0 {
		memoryGB = params.MemoryConfig.ContainerMemoryGB
	}
	if ablitSpec.MaxMemoryGB != nil && *ablitSpec.MaxMemoryGB > 0 {
		memoryGB = *ablitSpec.MaxMemoryGB
	}

	deadline := int64(DefaultAbliterationDeadlineSeconds)
	if ablitSpec.TimeoutSeconds != nil && *ablitSpec.TimeoutSeconds >= 300 {
		deadline = *ablitSpec.TimeoutSeconds
	}

	image := ResolveImage(ImageFormatAbliteration, params.ProfileQuantizerImage, params.GPUVendor, params.GPUArch)
	ablitEnv := abliterationEnv(params.ModelPath, params.GPUArch, ablitSpec, params.MemoryConfig)
	script := abliterationWrapperScript()

	backoffLimit := int32(2)
	pvcVol, pvcMount := modelPVCVolume(params.PVCName)
	wsVol, wsMount := workspaceVolume(fmt.Sprintf("%dGi", memoryGB*2))

	// Account for GPU driver memory (HIP/GTT) that lives outside the cgroup.
	schedulingMemoryGB := memoryGB
	if params.MemoryConfig.GPUDriverMemoryMB > 0 {
		schedulingMemoryGB += params.MemoryConfig.GPUDriverMemoryMB / 1024
	}

	var env []corev1.EnvVar
	memoryRequestGB := memoryRequestForLimitGB(schedulingMemoryGB)
	resources := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", quantizationCPUCores())),
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryRequestGB)),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", schedulingMemoryGB)),
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
	env = mergeEnvVars(env, ablitEnv)
	env = mergeEnvVars(env, params.ProfileEnv)

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{
			{
				Name:            "abliterator",
				Image:           image,
				ImagePullPolicy: ImagePullPolicyForImage(image),
				Command:         []string{"/bin/bash", "-c"},
				Args:            []string{script},
				Env:             env,
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

// abliterationEnv returns environment variables for the abliteration script.
func abliterationEnv(modelPath, gpuArch string, spec *aiv1alpha1.AbliterationSpec, memCfg GPUMemoryConfig) []corev1.EnvVar {
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

	skipGDN := "true"
	if spec.SkipGDNLayers != nil && !*spec.SkipGDNLayers {
		skipGDN = "false"
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
	// CPU memory priority: env var > GPUProfile > heuristic from container limit.
	cpuMaxMemoryGB := os.Getenv("FLEXINFER_ABLITERATION_CPU_MAX_MEMORY_GB")
	if cpuMaxMemoryGB == "" {
		if memCfg.MaxCPUMemoryGB > 0 {
			cpuMaxMemoryGB = fmt.Sprintf("%d", memCfg.MaxCPUMemoryGB)
		} else {
			cpuMaxMemoryGB = fmt.Sprintf("%d", abliterationCPUMaxMemoryGB(maxMemoryGB))
		}
	}
	// GPU memory priority: env var > GPUProfile > arch-based heuristic.
	gpuMaxMemoryGB := os.Getenv("FLEXINFER_ABLITERATION_GPU_MAX_MEMORY_GB")
	if gpuMaxMemoryGB == "" {
		if memCfg.MaxGPUMemoryGB > 0 {
			gpuMaxMemoryGB = fmt.Sprintf("%d", memCfg.MaxGPUMemoryGB)
		} else {
			gpuMaxMemoryGB = fmt.Sprintf("%d", abliterationGPUMaxMemoryGB(spec.UseGPU, gpuArch))
		}
	}
	offloadDir := os.Getenv("FLEXINFER_ABLITERATION_OFFLOAD_DIR")
	if offloadDir == "" {
		offloadDir = "/workspace/abliteration-offload"
	}
	skipCachingAllocatorWarmup := os.Getenv("FLEXINFER_ABLITERATION_SKIP_CACHING_ALLOCATOR_WARMUP")
	if skipCachingAllocatorWarmup == "" {
		if spec.UseGPU && gpuArch == "gfx906" {
			skipCachingAllocatorWarmup = "true"
		} else {
			skipCachingAllocatorWarmup = "false"
		}
	}
	safeShardedLoad := os.Getenv("FLEXINFER_ABLITERATION_SAFE_SHARDED_LOAD")
	if safeShardedLoad == "" {
		if spec.UseGPU && gpuArch == "gfx906" {
			safeShardedLoad = "true"
		} else {
			safeShardedLoad = "false"
		}
	}
	modelPolicies := os.Getenv("FLEXINFER_ABLITERATION_MODEL_POLICIES")
	if modelPolicies == "" {
		modelPolicies = defaultAbliterationModelPoliciesJSON()
	}

	normThreshold := "100"
	if spec.NormThreshold != nil && *spec.NormThreshold != "" {
		normThreshold = *spec.NormThreshold
	}

	ablitateLmHead := "true"
	if spec.AblitateLmHead != nil && !*spec.AblitateLmHead {
		ablitateLmHead = "false"
	}

	return []corev1.EnvVar{
		{Name: "MODEL_DIR", Value: fmt.Sprintf("/cache/%s", modelPath)},
		{Name: "NUM_SAMPLES", Value: fmt.Sprintf("%d", numSamples)},
		{Name: "TARGET_LAYERS", Value: targetLayers},
		{Name: "WEIGHT_MATRICES", Value: weightMatrices},
		{Name: "SKIP_VISION", Value: skipVision},
		{Name: "SKIP_GDN_LAYERS", Value: skipGDN},
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
		{Name: "ABLITERATION_SKIP_CACHING_ALLOCATOR_WARMUP", Value: skipCachingAllocatorWarmup},
		{Name: "ABLITERATION_SAFE_SHARDED_LOAD", Value: safeShardedLoad},
		{Name: "ABLITERATION_MODEL_POLICIES", Value: modelPolicies},
		{Name: "ABLITERATION_NORM_THRESHOLD", Value: normThreshold},
		{Name: "ABLITERATION_ABLITERATE_LM_HEAD", Value: ablitateLmHead},
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
		{
			Name:                "gemma4-text",
			MatchModelTypes:     []string{"gemma4", "gemma4_text"},
			MatchPathSubstrings: []string{"gemma4", "gemma-4"},
			SaveFormat:          "safetensors",
			SaveMaxShardSize:    "1GB",
			LoadAutoClass:       "AutoModelForImageTextToText",
			DecoderLayersPath:   "model.language_model.layers",
			LMHeadPath:          "language_model.lm_head",
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
LOGFILE=/tmp/abliterate-output.log
# Persist full log to PVC for post-mortem analysis (survives pod GC)
PVC_LOGDIR="${MODEL_DIR}/.flexinfer-logs"
PVC_LOGFILE="${PVC_LOGDIR}/abliterate-$(date +%Y%m%d-%H%M%S).log"

# Structured JSON event emitter for Loki/OTEL queryability.
# Events go to stdout (Promtail → Loki) and are queryable via:
#   {namespace="flexinfer-system"} | json | event="abliteration_start"
emit_event() {
    local event="$1"; shift
    local ts
    ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    local json="{\"ts\":\"${ts}\",\"component\":\"abliterator\",\"event\":\"${event}\""
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

cleanup_on_failure() {
  local rc=$?
  # Persist log to PVC before anything else
  if [ -f "${LOGFILE}" ]; then
    mkdir -p "${PVC_LOGDIR}"
    cp "${LOGFILE}" "${PVC_LOGFILE}" 2>/dev/null || true
    # Keep only last 3 log files to avoid PVC bloat
    ls -t "${PVC_LOGDIR}"/abliterate-*.log 2>/dev/null | tail -n +4 | xargs rm -f 2>/dev/null || true
  fi
  if [ $rc -ne 0 ]; then
    emit_event "abliteration_error" "exit_code" "${rc}" "model" "${MODEL_DIR}"
    # Write error context to termination-log for controller capture
    {
      echo "exit_code=${rc}"
      local checkpoint="${MODEL_DIR}/.abliteration-checkpoint.json"
      if [ -f "${checkpoint}" ]; then
        echo "---checkpoint---"
        cat "${checkpoint}" 2>/dev/null || true
      fi
      echo "---output_tail---"
      tail -80 "${LOGFILE}" 2>/dev/null || echo "(no log output captured)"
    } > /dev/termination-log 2>/dev/null || true
  fi
  exit $rc
}

trap cleanup_on_failure EXIT

# Tee all output so cleanup can capture tail on failure
exec > >(tee -a "${LOGFILE}") 2>&1

emit_event "abliteration_start" "model" "${MODEL_DIR}" "samples" "${NUM_SAMPLES}" "target_layers" "${TARGET_LAYERS}" "weight_matrices" "${WEIGHT_MATRICES}" "device_map" "${DEVICE_MAP}"
echo "=== FlexInfer Abliteration ==="
echo "Model: ${MODEL_DIR}"
echo "Samples: ${NUM_SAMPLES}"
echo "Target layers: ${TARGET_LAYERS}"
echo "Weight matrices: ${WEIGHT_MATRICES}"
echo "Skip vision: ${SKIP_VISION}"
echo "Device map: ${DEVICE_MAP}"
echo "Start: $(date -u +%Y-%m-%dT%H:%M:%SZ)"

# Wait for the downloader stage to finish rebuilding the source weights before
# we try to load the model. Spec-change retries can briefly recreate the
# downloader and abliteration Jobs in the same reconcile window.
DOWNLOAD_MARKER="${MODEL_DIR}/.download_complete"
DOWNLOAD_READY="false"
for attempt in $(seq 1 180); do
    WEIGHT_COUNT=$(find "${MODEL_DIR}" -maxdepth 1 \( -name '*.safetensors' -o -name '*.bin' -o -name '*.pt' \) 2>/dev/null | wc -l | tr -d ' ')
    if [ -f "${DOWNLOAD_MARKER}" ] && [ "${WEIGHT_COUNT}" -gt 0 ]; then
        DOWNLOAD_READY="true"
        break
    fi
    if [ "${attempt}" -eq 1 ] || [ $((attempt % 6)) -eq 0 ]; then
        MARKER_STATE="missing"
        if [ -f "${DOWNLOAD_MARKER}" ]; then
            MARKER_STATE="present"
        fi
        emit_event "abliteration_waiting_for_download" "attempt" "${attempt}" "marker" "${MARKER_STATE}" "weight_files" "${WEIGHT_COUNT}"
        echo "Waiting for source weights to finish downloading (attempt ${attempt}/180, marker=${MARKER_STATE}, weight_files=${WEIGHT_COUNT})"
    fi
    sleep 10
done
if [ "${DOWNLOAD_READY}" != "true" ]; then
    msg="Timed out waiting for downloaded source weights in ${MODEL_DIR}"
    echo "${msg}"
    emit_event "abliteration_error" "model" "${MODEL_DIR}" "detail" "${msg}"
    exit 1
fi

# Short-circuit: if abliteration already completed (status file + weight files exist),
# re-emit the metadata and exit 0. This handles the case where the Job is recreated
# (TTL GC + controller restart) after a successful abliteration.
ABLIT_STATUS="${MODEL_DIR}/.abliteration-status.json"
if [ -f "${ABLIT_STATUS}" ]; then
    ABLIT_COMPLETE=$(python3 -c "import json; d=json.load(open('${ABLIT_STATUS}')); print('yes' if d.get('status')=='complete' else 'no')" 2>/dev/null || echo "no")
    WEIGHT_COUNT=$(find "${MODEL_DIR}" -maxdepth 1 \( -name '*.safetensors' -o -name '*.bin' -o -name '*.pt' \) 2>/dev/null | wc -l | tr -d ' ')
    if [ "${ABLIT_COMPLETE}" = "yes" ] && [ "${WEIGHT_COUNT}" -gt 0 ]; then
        emit_event "abliteration_cached" "model" "${MODEL_DIR}" "weight_files" "${WEIGHT_COUNT}"
        echo "Abliteration already complete (${WEIGHT_COUNT} weight files present)"
        echo "Status: $(cat ${ABLIT_STATUS})"
        # Re-emit termination metadata for controller capture
        cat "${ABLIT_STATUS}" > /dev/termination-log 2>/dev/null || true
        exit 0
    fi
    echo "WARNING: Status file exists but abliteration may be incomplete (status=${ABLIT_COMPLETE}, weights=${WEIGHT_COUNT})"
fi

# Monkey-patch torch.cuda.mem_get_info for gfx906 (hipMemGetInfo not supported
# on Vega20 — VMM not available). Without this, device_map=auto crashes during
# caching_allocator_warmup in transformers 5.x. Returns hardcoded VRAM size
# so accelerate can still distribute the model across GPU+CPU.
# Record status file mtime before running (to detect if abliterate.py updated it)
ABLIT_STATUS_MTIME_BEFORE=""
if [ -f "${ABLIT_STATUS}" ]; then
    ABLIT_STATUS_MTIME_BEFORE=$(stat -c %Y "${ABLIT_STATUS}" 2>/dev/null || stat -f %m "${ABLIT_STATUS}" 2>/dev/null || echo "")
fi

python3 -c "
import json
import os
import torch.cuda
_orig = torch.cuda.mem_get_info
def _patched(device=None):
    try:
        return _orig(device)
    except RuntimeError:
        vram_gb = int(os.environ.get('ABLITERATION_GPU_MAX_MEMORY_GB', '16'))
        total = vram_gb * 1024**3
        used = int(torch.cuda.memory_allocated(device) if torch.cuda.is_available() else 0)
        return (max(total - used, 0), total)
torch.cuda.mem_get_info = _patched
if os.environ.get('ABLITERATION_SKIP_CACHING_ALLOCATOR_WARMUP', 'false').lower() == 'true':
    import transformers.modeling_utils as modeling_utils
    def _skip_caching_allocator_warmup(*args, **kwargs):
        return None
    modeling_utils.caching_allocator_warmup = _skip_caching_allocator_warmup
    print('Patched transformers.caching_allocator_warmup to no-op')
if os.environ.get('ABLITERATION_SAFE_SHARDED_LOAD', 'false').lower() == 'true':
    import gc
    from transformers import AutoConfig, AutoModelForCausalLM
    from transformers.modeling_utils import get_checkpoint_shard_files, load_state_dict
    _orig_from_pretrained = AutoModelForCausalLM.from_pretrained
    def _safe_sharded_from_pretrained(model_path, *args, **kwargs):
        device_map = kwargs.get('device_map')
        if not device_map or device_map == 'cpu':
            return _orig_from_pretrained(model_path, *args, **kwargs)
        trust_remote_code = kwargs.get('trust_remote_code', True)
        dtype = kwargs.get('torch_dtype')
        config = AutoConfig.from_pretrained(model_path, trust_remote_code=trust_remote_code)
        if getattr(config, 'text_config', None) is not None and not hasattr(config, 'vocab_size'):
            cfg_path = os.path.join(model_path, 'config.json')
            try:
                with open(cfg_path, 'r', encoding='utf-8') as handle:
                    raw_cfg = json.load(handle)
                text_cfg = dict(raw_cfg.get('text_config') or {})
                if text_cfg:
                    for key in ('bos_token_id', 'eos_token_id', 'pad_token_id'):
                        if key in raw_cfg and key not in text_cfg:
                            text_cfg[key] = raw_cfg[key]
                    model_type = text_cfg.get('model_type') or raw_cfg.get('model_type', '')
                    if model_type == 'qwen3_5':
                        model_type = 'qwen3_5_text'
                        text_cfg['architectures'] = ['Qwen3_5ForCausalLM']
                    config = AutoConfig.for_model(model_type, **{key: value for key, value in text_cfg.items() if key != 'model_type'})
                    print(f'Safe sharded load patch: extracted text config model_type={model_type}', flush=True)
            except Exception as exc:
                print(f'WARN: safe sharded load patch could not extract text config: {exc}', flush=True)
        build_start = __import__('time').time()
        print(f'Safe sharded load patch: constructing model from config dtype={dtype}', flush=True)
        model = AutoModelForCausalLM.from_config(config, trust_remote_code=trust_remote_code, torch_dtype=dtype)
        _elapsed = __import__('time').time() - build_start
        print(f'Safe sharded load patch: model constructed in {_elapsed:.1f}s', flush=True)
        if hasattr(model, 'tie_weights'):
            print('Safe sharded load patch: tying weights', flush=True)
            model.tie_weights()
        index_filename = ''
        for candidate in ('model.safetensors.index.json', 'pytorch_model.bin.index.json'):
            candidate_path = os.path.join(model_path, candidate)
            if os.path.exists(candidate_path):
                index_filename = candidate_path
                break
        if not index_filename:
            print('Safe sharded load patch: no shard index found, falling back to transformers from_pretrained', flush=True)
            return _orig_from_pretrained(model_path, *args, **kwargs)
        shard_files, _ = get_checkpoint_shard_files(model_path, index_filename, local_files_only=True)
        print(f'Safe sharded load patch: loading {len(shard_files)} checkpoint shards on CPU before dispatch', flush=True)
        load_start = __import__('time').time()
        for index, shard_file in enumerate(shard_files, start=1):
            if index == 1 or index % 25 == 0 or index == len(shard_files):
                print(f'Safe sharded load patch: loading shard {index}/{len(shard_files)}', flush=True)
            state_dict = load_state_dict(shard_file, map_location='cpu')
            # VLM checkpoints use 'model.language_model.' prefix for text
            # weights, but ForCausalLM expects 'model.' directly.  Remap keys
            # so load_state_dict(strict=False) actually matches.
            remapped = {}
            needs_remap = False
            for key, value in state_dict.items():
                if 'language_model.' in key:
                    remapped[key.replace('model.language_model.', 'model.')] = value
                    needs_remap = True
                else:
                    remapped[key] = value
            if needs_remap:
                if index == 1:
                    print('Safe sharded load patch: remapping VLM language_model keys -> model keys', flush=True)
                state_dict = remapped
            del remapped
            model.load_state_dict(state_dict, strict=False)
            del state_dict
            gc.collect()
        _elapsed = __import__('time').time() - load_start
        print(f'Safe sharded load patch: loaded all shards in {_elapsed:.1f}s', flush=True)
        from accelerate import dispatch_model, infer_auto_device_map
        # Prevent sub-layer splitting for decoder layers.  GDN linear_attn
        # modules register dt_bias/A_log as parameters (not submodules).
        # Granular dispatch puts child projections on one device while these
        # orphan params stay on CPU, causing forward-pass device mismatches.
        no_split = list(getattr(model, '_no_split_modules', None) or [])
        for _mod in model.modules():
            _cls = type(_mod).__name__
            if 'DecoderLayer' in _cls and _cls not in no_split:
                no_split.append(_cls)
                print(f'Safe sharded load patch: added {_cls} to no_split_module_classes', flush=True)
                break
        inferred_map = infer_auto_device_map(model, max_memory=kwargs.get('max_memory'), no_split_module_classes=no_split if no_split else None)
        gpu_layers = sum(1 for value in inferred_map.values() if value != 'cpu')
        cpu_layers = sum(1 for value in inferred_map.values() if value == 'cpu')
        gpu_targets = [f'{key}->{value}' for key, value in inferred_map.items() if value != 'cpu']
        cpu_targets = [f'{key}->{value}' for key, value in inferred_map.items() if value == 'cpu']
        print(f'Safe sharded load patch: dispatching model gpu_layers={gpu_layers} cpu_layers={cpu_layers}', flush=True)
        if gpu_targets:
            print(f'Safe sharded load patch: sample gpu targets: {gpu_targets[:8]}', flush=True)
        if cpu_targets:
            print(f'Safe sharded load patch: sample cpu targets: {cpu_targets[:8]}', flush=True)
        dispatch_start = __import__('time').time()
        try:
            model = dispatch_model(
                model,
                device_map=inferred_map,
                offload_dir=kwargs.get('offload_folder'),
                offload_buffers=kwargs.get('offload_buffers', False),
            )
        except Exception as exc:
            _elapsed = __import__('time').time() - dispatch_start
            print(
                f'Safe sharded load patch: dispatch failed after {_elapsed:.1f}s: {type(exc).__name__}: {exc}',
                flush=True,
            )
            raise
        print('Safe sharded load patch: dispatch complete', flush=True)
        # Post-dispatch fixup: move orphan params/buffers that accelerate missed.
        # infer_auto_device_map only tracks submodules. Registered parameters
        # and buffers (like GDN dt_bias, A_log) stay on CPU even when their
        # parent module was dispatched to GPU. Fix by walking the module tree
        # and moving any misplaced tensors to match the module execution device.
        import torch
        _fixed = 0
        for name, module in model.named_modules():
            exec_device = getattr(module, '_hf_hook', None)
            if exec_device is not None:
                exec_device = getattr(exec_device, 'execution_device', None)
            if exec_device is None:
                continue
            for pname, param in list(module.named_parameters(recurse=False)):
                if param.is_meta or param.device == exec_device:
                    continue
                module._parameters[pname] = param.to(exec_device)
                _fixed += 1
            for bname, buf in list(module.named_buffers(recurse=False)):
                if buf is None or buf.is_meta or buf.device == exec_device:
                    continue
                module._buffers[bname] = buf.to(exec_device)
                _fixed += 1
        if _fixed > 0:
            print(f'Safe sharded load patch: fixed {_fixed} orphan params/buffers to match dispatch device', flush=True)
        return model
    AutoModelForCausalLM.from_pretrained = _safe_sharded_from_pretrained
    print('Patched AutoModelForCausalLM.from_pretrained for gfx906 safe sharded load')
exec(open('/opt/flexinfer/scripts/abliterate.py').read())
"

END_TS=$(date +%s)
DURATION=$((END_TS - START_TS))

# Only emit abliteration_complete if actual work was done (status file updated).
# If the Python script short-circuited via its own caching, emit abliteration_cached instead.
ABLIT_STATUS_MTIME_AFTER=""
if [ -f "${ABLIT_STATUS}" ]; then
    ABLIT_STATUS_MTIME_AFTER=$(stat -c %Y "${ABLIT_STATUS}" 2>/dev/null || stat -f %m "${ABLIT_STATUS}" 2>/dev/null || echo "")
fi
if [ -n "${ABLIT_STATUS_MTIME_AFTER}" ] && [ "${ABLIT_STATUS_MTIME_BEFORE}" = "${ABLIT_STATUS_MTIME_AFTER}" ]; then
    emit_event "abliteration_cached" "model" "${MODEL_DIR}" "duration_sec" "${DURATION}" "detail" "python_internal_cache"
    echo "=== Abliteration skipped (already complete) in ${DURATION}s ==="
else
    emit_event "abliteration_complete" "model" "${MODEL_DIR}" "duration_sec" "${DURATION}" "samples" "${NUM_SAMPLES}"
    echo "=== Abliteration finished in ${DURATION}s ==="
fi
`
}
