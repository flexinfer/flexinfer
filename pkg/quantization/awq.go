package quantization

import (
	"fmt"
	"os"

	batchv1 "k8s.io/api/batch/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
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
		return fmt.Errorf("AWQ: %w", ErrGPURequired)
	}

	bits := DefaultAWQBits
	if spec.Bits != nil {
		bits = int(*spec.Bits)
	}
	if bits != 4 {
		return fmt.Errorf("AWQ %w: got %d, want 4", ErrInvalidBits, bits)
	}

	groupSize := DefaultQuantizationGroupSize
	if spec.GroupSize != nil {
		groupSize = int(*spec.GroupSize)
	}
	if groupSize <= 0 {
		return fmt.Errorf("AWQ: %w (got %d)", ErrInvalidGroupSize, groupSize)
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

	image := awqQuantizerImage()
	// GPUProfile image override takes priority.
	if params.ProfileQuantizerImage != "" {
		image = params.ProfileQuantizerImage
	}

	return buildGPUQuantizationJob(
		params,
		image,
		b.buildScript(params.ModelPath, bits, groupSize, params.Spec.Calibration),
		memoryGB,
	)
}

// buildScript generates the shell script for AWQ quantization.
func (b *AWQJobBuilder) buildScript(modelPath string, bits, groupSize int, calib *aiv1alpha1.CalibrationSpec) string {
	maxSeqLen := int32(DefaultCalibrationMaxSeqLen)
	maxSamples := int32(DefaultCalibrationMaxSamples)
	var nParallel *int32
	dataset := DefaultCalibrationDataset
	if calib != nil {
		if calib.MaxSeqLen != nil {
			maxSeqLen = *calib.MaxSeqLen
		}
		if calib.MaxSamples != nil {
			maxSamples = *calib.MaxSamples
		}
		nParallel = calib.NParallelCalibSamples
		if calib.Dataset != nil {
			dataset = *calib.Dataset
		}
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

cleanup() {
    local ec=$?
    if [ $ec -ne 0 ] && [ ! -f "${OUT_DIR}/config.json" ]; then
        rm -rf "${OUT_DIR}"
        echo "Cleaned up partial output (exit code $ec)"
    fi
}
trap cleanup EXIT

echo "=== AWQ Quantization ==="
echo "Model: ${MODEL_DIR}"
echo "Type: ${TYPE}"
echo "Calibration: maxSeqLen=%d maxSamples=%d nParallel=%s dataset=%s"
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

# Remove FP16 source weight files after successful save to reclaim disk space.
find "${MODEL_DIR}" -maxdepth 1 \( -name '*.safetensors' -o -name '*.bin' -o -name '*.pt' \) \
    ! -path "${OUT_DIR}/*" -print -delete 2>/dev/null || true
echo "FP16 source files cleaned up"

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
`, modelPath, bits, groupSize, maxSeqLen, maxSamples, nParallelLog, dataset, maxSeqLen, maxSamples, nParallelKwarg)
}

func awqQuantizerImage() string {
	if img := os.Getenv("FLEXINFER_QUANTIZER_AWQ_IMAGE"); img != "" {
		return img
	}
	return DefaultAWQImage
}
