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

	// DefaultAWQBits is the default bit width for AWQ.
	DefaultAWQBits = 4

	// DefaultGPTQBits is the default bit width for GPTQ.
	DefaultGPTQBits = 4

	// DefaultQuantizationGroupSize is the default group size for AWQ/GPTQ.
	DefaultQuantizationGroupSize = 128
)

// AWQJobBuilder generates Kubernetes Jobs for AWQ quantization.
type AWQJobBuilder struct{}

// Format returns the AWQ quantization format.
func (b *AWQJobBuilder) Format() aiv1alpha1.QuantizationFormat {
	return aiv1alpha1.QuantizationFormatAWQ
}

// Validate checks that the quantization spec is valid for AWQ.
func (b *AWQJobBuilder) Validate(spec *aiv1alpha1.QuantizationSpec) error {
	if spec.Format != aiv1alpha1.QuantizationFormatAWQ {
		return fmt.Errorf("AWQJobBuilder only handles AWQ format, got %q", spec.Format)
	}
	if !spec.UseGPU {
		return fmt.Errorf("AWQ quantization requires useGPU=true")
	}

	bits := DefaultAWQBits
	if spec.Bits != nil {
		bits = int(*spec.Bits)
	}
	if bits != 4 {
		return fmt.Errorf("AWQ currently supports only 4-bit quantization, got %d", bits)
	}

	groupSize := DefaultQuantizationGroupSize
	if spec.GroupSize != nil {
		groupSize = int(*spec.GroupSize)
	}
	if groupSize <= 0 {
		return fmt.Errorf("AWQ groupSize must be > 0, got %d", groupSize)
	}

	return nil
}

// BuildJob creates a batch/v1.Job that quantizes a model to AWQ format.
func (b *AWQJobBuilder) BuildJob(params JobParams) (*batchv1.Job, error) {
	if err := b.Validate(params.Spec); err != nil {
		return nil, err
	}

	memoryGB := int32(DefaultGPUQuantizationMemoryGB)
	if params.Spec.MaxMemoryGB != nil {
		memoryGB = *params.Spec.MaxMemoryGB
	}

	bits := DefaultAWQBits
	if params.Spec.Bits != nil {
		bits = int(*params.Spec.Bits)
	}
	groupSize := DefaultQuantizationGroupSize
	if params.Spec.GroupSize != nil {
		groupSize = int(*params.Spec.GroupSize)
	}

	return buildGPUQuantizationJob(
		params,
		awqQuantizerImage(),
		b.buildScript(params.ModelPath, bits, groupSize, params.Spec.Calibration),
		memoryGB,
	)
}

// buildScript generates the shell script for AWQ quantization.
func (b *AWQJobBuilder) buildScript(modelPath string, bits, groupSize int, calib *aiv1alpha1.CalibrationSpec) string {
	maxSeqLen := int32(DefaultCalibrationMaxSeqLen)
	maxSamples := int32(DefaultCalibrationMaxSamples)
	var nParallel *int32
	if calib != nil {
		if calib.MaxSeqLen != nil {
			maxSeqLen = *calib.MaxSeqLen
		}
		if calib.MaxSamples != nil {
			maxSamples = *calib.MaxSamples
		}
		nParallel = calib.NParallelCalibSamples
	}

	// Build the n_parallel_calib_samples kwarg line.
	// When nil, AutoAWQ processes all samples on GPU (default behavior).
	// When set, AutoAWQ offloads to CPU RAM between batches (higher CPU memory).
	nParallelKwarg := ""
	nParallelLog := "None"
	if nParallel != nil {
		nParallelKwarg = fmt.Sprintf("    n_parallel_calib_samples=%d,\n", *nParallel)
		nParallelLog = fmt.Sprintf("%d", *nParallel)
	}

	return fmt.Sprintf(`set -euo pipefail

MODEL_DIR="/cache/%s"
BITS=%d
GROUP_SIZE=%d
TYPE="W${BITS}_G${GROUP_SIZE}"
OUT_DIR="${MODEL_DIR}/awq-w${BITS}-g${GROUP_SIZE}"
START_TS=$(date +%%s)

cleanup() { rm -rf "${OUT_DIR}"; echo "Cleaned up partial output"; }
trap cleanup EXIT

echo "=== AWQ Quantization ==="
echo "Model: ${MODEL_DIR}"
echo "Type: ${TYPE}"
echo "Calibration: maxSeqLen=%d maxSamples=%d nParallel=%s"
echo "Start: $(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)"

ORIGINAL_SIZE=$(du -sb "${MODEL_DIR}" | cut -f1)
echo "Original size: ${ORIGINAL_SIZE} bytes"

rm -rf "${OUT_DIR}"
mkdir -p "${OUT_DIR}"

export MODEL_DIR OUT_DIR BITS GROUP_SIZE
python3 - <<'PY'
import os
from awq import AutoAWQForCausalLM
from transformers import AutoTokenizer

model_dir = os.environ["MODEL_DIR"]
out_dir = os.environ["OUT_DIR"]
bits = int(os.environ["BITS"])
group_size = int(os.environ["GROUP_SIZE"])

model = AutoAWQForCausalLM.from_pretrained(model_dir, safetensors=True, device_map=None)
tokenizer = AutoTokenizer.from_pretrained(model_dir, trust_remote_code=True)
model.quantize(
    tokenizer,
    quant_config={
        "w_bit": bits,
        "q_group_size": group_size,
        "zero_point": True,
        "version": "GEMM",
    },
    max_calib_seq_len=%d,
    max_calib_samples=%d,
%s)
model.save_quantized(out_dir)
tokenizer.save_pretrained(out_dir)
PY

trap - EXIT

# Remove FP16 source weight files to reclaim disk space.
# Quantized output is already saved in OUT_DIR; source weights are no longer needed.
# Re-quantization requires a fresh download (delete .flexinfer_cached + prefetch job).
echo "Cleaning up FP16 source weight files..."
find "${MODEL_DIR}" -maxdepth 1 \( -name '*.safetensors' -o -name '*.bin' -o -name '*.pt' \) -delete
echo "FP16 source files removed"

COMPRESSED_SIZE=$(du -sb "${OUT_DIR}" | cut -f1)
echo "Compressed size: ${COMPRESSED_SIZE} bytes"
END_TS=$(date +%%s)
DURATION_SEC=$((END_TS - START_TS))

cat > "${MODEL_DIR}/.quantization-status.json" << METADATA
{
  "format": "AWQ",
  "type": "${TYPE}",
  "originalSizeBytes": ${ORIGINAL_SIZE},
  "compressedSizeBytes": ${COMPRESSED_SIZE},
  "quantizationTimeSeconds": ${DURATION_SEC},
  "outputDir": "awq-w${BITS}-g${GROUP_SIZE}"
}
METADATA

cat > /dev/termination-log << TERMINATION
{
  "type": "${TYPE}",
  "originalSizeBytes": ${ORIGINAL_SIZE},
  "compressedSizeBytes": ${COMPRESSED_SIZE},
  "quantizationTimeSeconds": ${DURATION_SEC}
}
TERMINATION

echo "=== Quantization complete ==="
echo "Output: ${OUT_DIR}"
echo "End: $(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)"
`, modelPath, bits, groupSize, maxSeqLen, maxSamples, nParallelLog, maxSeqLen, maxSamples, nParallelKwarg)
}

// GPTQJobBuilder generates Kubernetes Jobs for GPTQ quantization.
type GPTQJobBuilder struct{}

// Format returns the GPTQ quantization format.
func (b *GPTQJobBuilder) Format() aiv1alpha1.QuantizationFormat {
	return aiv1alpha1.QuantizationFormatGPTQ
}

// Validate checks that the quantization spec is valid for GPTQ.
func (b *GPTQJobBuilder) Validate(spec *aiv1alpha1.QuantizationSpec) error {
	if spec.Format != aiv1alpha1.QuantizationFormatGPTQ {
		return fmt.Errorf("GPTQJobBuilder only handles GPTQ format, got %q", spec.Format)
	}
	if !spec.UseGPU {
		return fmt.Errorf("GPTQ quantization requires useGPU=true")
	}

	bits := DefaultGPTQBits
	if spec.Bits != nil {
		bits = int(*spec.Bits)
	}
	switch bits {
	case 4, 8:
	default:
		return fmt.Errorf("GPTQ currently supports 4-bit or 8-bit quantization, got %d", bits)
	}

	groupSize := DefaultQuantizationGroupSize
	if spec.GroupSize != nil {
		groupSize = int(*spec.GroupSize)
	}
	if groupSize <= 0 {
		return fmt.Errorf("GPTQ groupSize must be > 0, got %d", groupSize)
	}

	return nil
}

// BuildJob creates a batch/v1.Job that quantizes a model to GPTQ format.
func (b *GPTQJobBuilder) BuildJob(params JobParams) (*batchv1.Job, error) {
	if err := b.Validate(params.Spec); err != nil {
		return nil, err
	}

	memoryGB := int32(DefaultGPUQuantizationMemoryGB)
	if params.Spec.MaxMemoryGB != nil {
		memoryGB = *params.Spec.MaxMemoryGB
	}

	bits := DefaultGPTQBits
	if params.Spec.Bits != nil {
		bits = int(*params.Spec.Bits)
	}
	groupSize := DefaultQuantizationGroupSize
	if params.Spec.GroupSize != nil {
		groupSize = int(*params.Spec.GroupSize)
	}

	sym := true
	if params.Spec.Sym != nil {
		sym = *params.Spec.Sym
	}
	descAct := false
	if params.Spec.DescAct != nil {
		descAct = *params.Spec.DescAct
	}

	image := gptqQuantizerImage()
	if params.GPUVendor == "amd" {
		image = gptqQuantizerROCmImage(params.GPUArch)
	}

	return buildGPUQuantizationJob(
		params,
		image,
		b.buildScript(params.ModelPath, bits, groupSize, sym, descAct, memoryGB, params.Spec.Calibration),
		memoryGB,
	)
}

// buildScript generates the shell script for GPTQ quantization using GPTQModel.
func (b *GPTQJobBuilder) buildScript(modelPath string, bits, groupSize int, sym, descAct bool, memoryGB int32, calib *aiv1alpha1.CalibrationSpec) string {
	maxSeqLen := int32(DefaultCalibrationMaxSeqLen)
	maxSamples := int32(DefaultCalibrationMaxSamples)
	if calib != nil {
		if calib.MaxSeqLen != nil {
			maxSeqLen = *calib.MaxSeqLen
		}
		if calib.MaxSamples != nil {
			maxSamples = *calib.MaxSamples
		}
	}

	symPy := "True"
	if !sym {
		symPy = "False"
	}
	descActPy := "False"
	if descAct {
		descActPy = "True"
	}

	return fmt.Sprintf(`set -euo pipefail

MODEL_DIR="/cache/%s"
BITS=%d
GROUP_SIZE=%d
MAX_MEMORY_GB=%d
TYPE="W${BITS}_G${GROUP_SIZE}"
OUT_DIR="${MODEL_DIR}/gptq-w${BITS}-g${GROUP_SIZE}"
START_TS=$(date +%%s)

cleanup() { rm -rf "${OUT_DIR}"; echo "Cleaned up partial output"; }
trap cleanup EXIT

# Auto-detect gfx900 (Radeon VII reports as gfx900, needs gfx906 ISA override).
# Must be set BEFORE any HIP/PyTorch call so the driver loads correct ISA.
if command -v rocminfo &>/dev/null; then
    GPU_GFX=$(rocminfo 2>/dev/null | grep -oP 'gfx\d+' | head -1 || true)
    if [ "${GPU_GFX}" = "gfx900" ]; then
        export HSA_OVERRIDE_GFX_VERSION=9.0.6
        echo "Detected ${GPU_GFX} (Radeon VII), set HSA_OVERRIDE_GFX_VERSION=9.0.6"
    else
        echo "Detected GPU: ${GPU_GFX:-unknown}"
    fi
fi

echo "=== GPTQ Quantization (GPTQModel) ==="
echo "Model: ${MODEL_DIR}"
echo "Type: ${TYPE}"
echo "Calibration: maxSeqLen=%d maxSamples=%d sym=%s descAct=%s"
echo "Container memory limit: ${MAX_MEMORY_GB}Gi"
echo "Start: $(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)"

ORIGINAL_SIZE=$(du -sb "${MODEL_DIR}" | cut -f1)
echo "Original size: ${ORIGINAL_SIZE} bytes"

rm -rf "${OUT_DIR}"
mkdir -p "${OUT_DIR}"
mkdir -p /workspace/offload

export MODEL_DIR OUT_DIR BITS GROUP_SIZE MAX_MEMORY_GB
python3 - <<'PY'
import json
import os
import torch
from datasets import load_dataset
from gptqmodel import GPTQModel, QuantizeConfig
from transformers import AutoTokenizer

model_dir = os.environ["MODEL_DIR"]
out_dir = os.environ["OUT_DIR"]
bits = int(os.environ["BITS"])
group_size = int(os.environ["GROUP_SIZE"])
max_memory_gb = int(os.environ["MAX_MEMORY_GB"])
max_seq_len = %d
max_samples = %d

# VLM config extraction: models like Qwen3.5 have a composite VLM config
# (model_type=qwen3_5) wrapping text_config (model_type=qwen3_5_text).
# Loading the VLM fails because Qwen3_5ForConditionalGeneration expects
# vocab_size at the top level, but it lives inside text_config.
# Fix: extract text_config to top level so transformers loads the text-only
# model (Qwen3_5TextForCausalLM) directly. Preserve the native model_type
# — do NOT remap to a different architecture (Qwen3.5 text backbone uses
# hybrid GatedDeltaNet + full-attention, incompatible with Qwen3).
cfg_path = os.path.join(model_dir, "config.json")
with open(cfg_path) as f:
    cfg = json.load(f)
if "text_config" in cfg and "model_type" in cfg.get("text_config", {}):
    text_cfg = cfg["text_config"]
    # Preserve top-level token IDs not in text_config.
    for key in ["bos_token_id", "eos_token_id", "pad_token_id"]:
        if key in cfg and key not in text_cfg:
            text_cfg[key] = cfg[key]
    with open(cfg_path, "w") as f:
        json.dump(text_cfg, f, indent=2)
    print(f"Extracted text_config: model_type={text_cfg.get('model_type')}")

# Detect hybrid architecture (e.g. Qwen3.5 with mixed linear_attention/full_attention).
# When heterogeneous layer types are present, use dynamic exclusion to skip attention
# modules and quantize only MLP/FFN — matching the official Qwen GPTQ-Int4 approach.
with open(cfg_path) as f:
    cfg_recheck = json.load(f)
dynamic_config = None
if "layer_types" in cfg_recheck:
    layer_types = cfg_recheck["layer_types"]
    unique_types = set(layer_types)
    if len(unique_types) > 1:
        print(f"Hybrid architecture detected: {dict((t, layer_types.count(t)) for t in unique_types)}")
        dynamic_config = {
            "-:.*attn.*": {},
            "-:.*shared_expert.*": {},
            "-:.*visual.*": {},
            "-:.*mtp.*": {},
        }
        print(f"Dynamic exclusion: {list(dynamic_config.keys())}")

# Memory management: cap GPU VRAM to leave headroom for quantization workspace.
# ROCm GPU driver also allocates GTT/system RAM outside the container cgroup,
# so reduced calibration samples (controlled via CR) is the main guard.
total_vram = torch.cuda.get_device_properties(0).total_memory
gpu_fraction = 0.80
try:
    torch.cuda.set_per_process_memory_fraction(gpu_fraction)
except RuntimeError:
    pass  # Not supported on all ROCm versions
print(f"Memory: GPU fraction={gpu_fraction} ({int(total_vram * gpu_fraction / (1024**3))}GiB of {total_vram // (1024**3)}GiB), container={max_memory_gb}Gi")

tokenizer = AutoTokenizer.from_pretrained(model_dir, trust_remote_code=True)
qcfg_kwargs = dict(bits=bits, group_size=group_size, sym=%s, desc_act=%s)
if dynamic_config is not None:
    qcfg_kwargs["dynamic"] = dynamic_config
quantize_config = QuantizeConfig(**qcfg_kwargs)
model = GPTQModel.load(
    model_dir,
    quantize_config=quantize_config,
    trust_remote_code=True,
)

dataset = load_dataset("mit-han-lab/pile-val-backup", split="validation")
examples = []
for sample in dataset.select(range(min(max_samples, len(dataset)))):
    tok = tokenizer(sample["text"], return_tensors="pt", max_length=max_seq_len, truncation=True)
    examples.append({"input_ids": tok.input_ids, "attention_mask": tok.attention_mask})

model.quantize(examples)
model.save(out_dir)
tokenizer.save_pretrained(out_dir)
PY

trap - EXIT

# Remove FP16 source weight files to reclaim disk space.
# Quantized output is already saved in OUT_DIR; source weights are no longer needed.
# Re-quantization requires a fresh download (delete .flexinfer_cached + prefetch job).
echo "Cleaning up FP16 source weight files..."
find "${MODEL_DIR}" -maxdepth 1 \( -name '*.safetensors' -o -name '*.bin' -o -name '*.pt' \) -delete
echo "FP16 source files removed"

COMPRESSED_SIZE=$(du -sb "${OUT_DIR}" | cut -f1)
echo "Compressed size: ${COMPRESSED_SIZE} bytes"
END_TS=$(date +%%s)
DURATION_SEC=$((END_TS - START_TS))

cat > "${MODEL_DIR}/.quantization-status.json" << METADATA
{
  "format": "GPTQ",
  "type": "${TYPE}",
  "originalSizeBytes": ${ORIGINAL_SIZE},
  "compressedSizeBytes": ${COMPRESSED_SIZE},
  "quantizationTimeSeconds": ${DURATION_SEC},
  "outputDir": "gptq-w${BITS}-g${GROUP_SIZE}"
}
METADATA

cat > /dev/termination-log << TERMINATION
{
  "type": "${TYPE}",
  "originalSizeBytes": ${ORIGINAL_SIZE},
  "compressedSizeBytes": ${COMPRESSED_SIZE},
  "quantizationTimeSeconds": ${DURATION_SEC}
}
TERMINATION

echo "=== Quantization complete ==="
echo "Output: ${OUT_DIR}"
echo "End: $(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)"
`, modelPath, bits, groupSize, memoryGB,
		maxSeqLen, maxSamples, symPy, descActPy,
		maxSeqLen, maxSamples, symPy, descActPy)
}

func buildGPUQuantizationJob(params JobParams, image, script string, memoryGB int32) (*batchv1.Job, error) {
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

func awqQuantizerImage() string {
	if img := os.Getenv("FLEXINFER_QUANTIZER_AWQ_IMAGE"); img != "" {
		return img
	}
	return DefaultAWQImage
}

func gptqQuantizerImage() string {
	if img := os.Getenv("FLEXINFER_QUANTIZER_GPTQ_IMAGE"); img != "" {
		return img
	}
	return DefaultGPTQImage
}

func gptqQuantizerROCmImage(gpuArch string) string {
	// Check arch-specific env var first (e.g. FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX906_IMAGE).
	if gpuArch != "" {
		envKey := "FLEXINFER_QUANTIZER_GPTQ_ROCM_" + strings.ToUpper(gpuArch) + "_IMAGE"
		if img := os.Getenv(envKey); img != "" {
			return img
		}
	}
	if gpuArch == "gfx906" {
		return DefaultGPTQROCmGFX906Image
	}
	// Generic ROCm override.
	if img := os.Getenv("FLEXINFER_QUANTIZER_GPTQ_ROCM_IMAGE"); img != "" {
		return img
	}
	return DefaultGPTQROCmImage
}
