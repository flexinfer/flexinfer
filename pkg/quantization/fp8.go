package quantization

import (
	"fmt"

	batchv1 "k8s.io/api/batch/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

const (
	// DefaultFP8Image is the default image used for FP8 quantization jobs.
	DefaultFP8Image = "ghcr.io/flexinfer/quantizer:fp8"

	// DefaultFP8Bits is the only currently supported FP8 bit width.
	DefaultFP8Bits = 8
)

// FP8JobBuilder generates Kubernetes Jobs for FP8 quantization.
type FP8JobBuilder struct{}

// Format returns the FP8 quantization format.
func (b *FP8JobBuilder) Format() aiv1alpha2.QuantizationFormat {
	return aiv1alpha2.QuantizationFormatFP8
}

// Validate checks that the quantization spec is valid for FP8.
func (b *FP8JobBuilder) Validate(spec *aiv1alpha2.QuantizationSpec) error {
	if spec.Format != aiv1alpha2.QuantizationFormatFP8 {
		return fmt.Errorf("FP8JobBuilder only handles FP8 format, got %q", spec.Format)
	}
	if !spec.UseGPU {
		return fmt.Errorf("FP8: %w", ErrGPURequired)
	}

	bits := DefaultFP8Bits
	if spec.Bits != nil {
		bits = int(*spec.Bits)
	}
	if bits != DefaultFP8Bits {
		return fmt.Errorf("FP8 %w: got %d, want %d", ErrInvalidBits, bits, DefaultFP8Bits)
	}

	if spec.GroupSize != nil {
		return fmt.Errorf("FP8 does not use groupSize; omit spec.groupSize")
	}

	return nil
}

// BuildJob creates a batch/v1.Job that quantizes a model to FP8 format.
func (b *FP8JobBuilder) BuildJob(params JobParams) (*batchv1.Job, error) {
	if err := b.Validate(params.Spec); err != nil {
		return nil, err
	}

	// Container memory priority: spec > GPUProfile > hardcoded default.
	memoryGB := resolveQuantizationMemoryGB(params.Spec, params.MemoryConfig)

	bits := DefaultFP8Bits
	if params.Spec.Bits != nil {
		bits = int(*params.Spec.Bits)
	}

	return buildFormatQuantizationJob(
		params,
		ImageFormatFP8,
		b.buildScript(params.ModelPath, bits),
		memoryGB,
		nil,
	)
}

// buildScript generates the shell script for FP8 quantization.
func (b *FP8JobBuilder) buildScript(modelPath string, bits int) string {
	return fmt.Sprintf(`set -euo pipefail

MODEL_DIR="/cache/%s"
BITS=%d
TYPE="FP8_B${BITS}"
OUT_DIR="${MODEL_DIR}/fp8-b${BITS}"
START_TS=$(date +%%s)

cleanup() { rm -rf "${OUT_DIR}"; echo "Cleaned up partial output"; }
trap cleanup EXIT

echo "=== FP8 Quantization ==="
echo "Model: ${MODEL_DIR}"
echo "Type: ${TYPE}"
echo "Start: $(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)"

ORIGINAL_SIZE=$(du -sb "${MODEL_DIR}" | cut -f1)
echo "Original size: ${ORIGINAL_SIZE} bytes"

rm -rf "${OUT_DIR}"
mkdir -p "${OUT_DIR}"

export MODEL_DIR OUT_DIR BITS
python3 - <<'PY'
import os
import subprocess
import sys

model_dir = os.environ["MODEL_DIR"]
out_dir = os.environ["OUT_DIR"]
bits = os.environ["BITS"]
convert_script = os.environ.get("FP8_CONVERT_SCRIPT", "/opt/fp8/convert.py")

cmd = [
    sys.executable,
    convert_script,
    "-i", model_dir,
    "-o", out_dir,
    "--dtype", "fp8",
    "--bits", bits,
]
subprocess.check_call(cmd)
PY

trap - EXIT

COMPRESSED_SIZE=$(du -sb "${OUT_DIR}" | cut -f1)
echo "Compressed size: ${COMPRESSED_SIZE} bytes"
END_TS=$(date +%%s)
DURATION_SEC=$((END_TS - START_TS))

cat > "${MODEL_DIR}/.quantization-status.json" << METADATA
{
  "format": "FP8",
  "type": "${TYPE}",
  "originalSizeBytes": ${ORIGINAL_SIZE},
  "compressedSizeBytes": ${COMPRESSED_SIZE},
  "quantizationTimeSeconds": ${DURATION_SEC},
  "outputDir": "fp8-b${BITS}"
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
`, modelPath, bits)
}
