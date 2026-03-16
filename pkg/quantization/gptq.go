package quantization

import (
	"fmt"
	"os"
	"strings"

	batchv1 "k8s.io/api/batch/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

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
		return fmt.Errorf("GPTQ: %w", ErrGPURequired)
	}

	bits := DefaultGPTQBits
	if spec.Bits != nil {
		bits = int(*spec.Bits)
	}
	switch bits {
	case 4, 8:
	default:
		return fmt.Errorf("GPTQ %w: got %d, want 4 or 8", ErrInvalidBits, bits)
	}

	groupSize := DefaultQuantizationGroupSize
	if spec.GroupSize != nil {
		groupSize = int(*spec.GroupSize)
	}
	if groupSize <= 0 {
		return fmt.Errorf("GPTQ: %w (got %d)", ErrInvalidGroupSize, groupSize)
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

	gpuMemFraction := DefaultGPUMemoryFraction
	if params.Spec.GPUMemoryFraction != nil {
		gpuMemFraction = *params.Spec.GPUMemoryFraction
	}
	dynamicExclusion := "auto"
	if params.Spec.DynamicExclusion != nil {
		dynamicExclusion = *params.Spec.DynamicExclusion
	}

	image := gptqQuantizerImage()
	if params.GPUVendor == "amd" {
		image = gptqQuantizerROCmImage(params.GPUArch)
	}
	// GPUProfile image override takes priority.
	if params.ProfileQuantizerImage != "" {
		image = params.ProfileQuantizerImage
	}

	return buildGPUQuantizationJob(
		params,
		image,
		b.buildScript(params.ModelPath, bits, groupSize, sym, descAct, memoryGB, gpuMemFraction, dynamicExclusion, params.Spec.Calibration),
		memoryGB,
	)
}

// buildScript generates the shell script for GPTQ quantization using GPTQModel.
func (b *GPTQJobBuilder) buildScript(modelPath string, bits, groupSize int, sym, descAct bool, memoryGB int32, gpuMemFraction, dynamicExclusion string, calib *aiv1alpha1.CalibrationSpec) string {
	maxSeqLen := int32(DefaultCalibrationMaxSeqLen)
	maxSamples := int32(DefaultCalibrationMaxSamples)
	dataset := DefaultCalibrationDataset
	if calib != nil {
		if calib.MaxSeqLen != nil {
			maxSeqLen = *calib.MaxSeqLen
		}
		if calib.MaxSamples != nil {
			maxSamples = *calib.MaxSamples
		}
		if calib.Dataset != nil {
			dataset = *calib.Dataset
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

	// Build the dynamic exclusion Python snippet conditionally.
	// "auto" emits hybrid architecture detection; "none" emits a simple assignment.
	var dynamicExclusionSnippet string
	if dynamicExclusion == "none" {
		dynamicExclusionSnippet = `# Dynamic exclusion: disabled (mode=none). All modules quantized to target bit width.
dynamic_config = None
print("Dynamic exclusion disabled (mode=none) — all modules will be quantized")`
	} else {
		dynamicExclusionSnippet = `# Dynamic exclusion: auto-detect hybrid architectures.
# When heterogeneous layer types are present (e.g. Qwen3.5 mixed attention),
# exclude attention/expert/vision/MTP modules — matching official GPTQ-Int4 approach.
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
        print(f"Dynamic exclusion: {list(dynamic_config.keys())}")`
	}

	return fmt.Sprintf(`set -euo pipefail

MODEL_DIR="/cache/%s"
BITS=%d
GROUP_SIZE=%d
MAX_MEMORY_GB=%d
TYPE="W${BITS}_G${GROUP_SIZE}"
OUT_DIR="${MODEL_DIR}/gptq-w${BITS}-g${GROUP_SIZE}"
START_TS=$(date +%%s)

cleanup() {
    local ec=$?
    if [ $ec -ne 0 ]; then
        # Only clean up if no quantized output was written.
        # Check for both quantize_config.json and safetensors files —
        # GPTQModel may crash after writing weights but before writing config.
        if [ ! -f "${OUT_DIR}/quantize_config.json" ] && \
           ! ls "${OUT_DIR}"/*.safetensors &>/dev/null; then
            rm -rf "${OUT_DIR}"
            echo "Cleaned up partial output (exit code $ec)"
        else
            echo "Preserving output (safetensors or config found despite exit code $ec)"
        fi
    fi
}
trap cleanup EXIT

# Patch GPTQModel writer.py to guard against ZeroDivisionError.
# Turtle offload moves source files during quantization; save_quantized
# tries to compute a size comparison from them and crashes if they're gone.
WRITER_PY=$(python3 -c "import gptqmodel.models.writer as w; print(w.__file__)" 2>/dev/null || true)
if [ -n "${WRITER_PY}" ] && grep -q "pre_quantized_size_mb) \* 100" "${WRITER_PY}" 2>/dev/null; then
    sed -i 's|percent_diff = (size_diff_mb / pre_quantized_size_mb) \* 100|percent_diff = (size_diff_mb / pre_quantized_size_mb) * 100 if pre_quantized_size_mb > 0 else 0.0|' "${WRITER_PY}"
    echo "Patched GPTQModel writer.py for ZeroDivisionError"
fi

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
echo "Calibration: maxSeqLen=%d maxSamples=%d sym=%s descAct=%s dataset=%s"
echo "GPU memory fraction: %s  Dynamic exclusion: %s"
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

%s

# Memory management: cap GPU VRAM to leave headroom for quantization workspace.
# ROCm GPU driver also allocates GTT/system RAM outside the container cgroup,
# so reduced calibration samples (controlled via CR) is the main guard.
total_vram = torch.cuda.get_device_properties(0).total_memory
gpu_fraction = %s
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

dataset = load_dataset("%s", split="validation")
examples = []
for sample in dataset.select(range(min(max_samples, len(dataset)))):
    tok = tokenizer(sample["text"], return_tensors="pt", max_length=max_seq_len, truncation=True)
    examples.append({"input_ids": tok.input_ids, "attention_mask": tok.attention_mask})

model.quantize(examples)
model.save(out_dir)
tokenizer.save_pretrained(out_dir)
PY

trap - EXIT

# Verify quantized output exists before proceeding to cleanup.
if ! ls "${OUT_DIR}"/*.safetensors &>/dev/null; then
    echo "ERROR: No safetensors files in output dir — quantization may have failed silently"
    exit 1
fi

COMPRESSED_SIZE=$(du -sb "${OUT_DIR}" | cut -f1)
echo "Compressed size: ${COMPRESSED_SIZE} bytes"

# Remove FP16 source weight files after successful save to reclaim disk space.
# Re-quantization requires a fresh download (delete .flexinfer_cached + prefetch job).
FP16_COUNT=$(find "${MODEL_DIR}" -maxdepth 1 \( -name '*.safetensors' -o -name '*.bin' -o -name '*.pt' \) \
    ! -path "${OUT_DIR}/*" 2>/dev/null | wc -l)
if [ "${FP16_COUNT}" -gt 0 ]; then
    find "${MODEL_DIR}" -maxdepth 1 \( -name '*.safetensors' -o -name '*.bin' -o -name '*.pt' \) \
        ! -path "${OUT_DIR}/*" -print -delete 2>/dev/null || true
    echo "FP16 source files cleaned up (${FP16_COUNT} files)"
else
    echo "No FP16 source files to clean up (already removed or turtle-offloaded)"
fi

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
		maxSeqLen, maxSamples, symPy, descActPy, dataset, gpuMemFraction, dynamicExclusion,
		maxSeqLen, maxSamples, dynamicExclusionSnippet, gpuMemFraction, symPy, descActPy, dataset)
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
