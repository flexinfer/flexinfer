package quantization

import (
	"fmt"
	"os"

	batchv1 "k8s.io/api/batch/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

const (
	// DefaultEXL2Image is the default image used for EXL2 quantization jobs.
	DefaultEXL2Image = "ghcr.io/flexinfer/quantizer:exl2"

	// DefaultEXL2Bits is the default bit width for EXL2.
	DefaultEXL2Bits = 4

	// MinEXL2Bits is the minimum supported EXL2 bit width.
	MinEXL2Bits = 2

	// MaxEXL2Bits is the maximum supported EXL2 bit width.
	MaxEXL2Bits = 6
)

// EXL2JobBuilder generates Kubernetes Jobs for EXL2 quantization.
type EXL2JobBuilder struct{}

// Format returns the EXL2 quantization format.
func (b *EXL2JobBuilder) Format() aiv1alpha1.QuantizationFormat {
	return aiv1alpha1.QuantizationFormatEXL2
}

// Validate checks that the quantization spec is valid for EXL2.
func (b *EXL2JobBuilder) Validate(spec *aiv1alpha1.QuantizationSpec) error {
	if spec.Format != aiv1alpha1.QuantizationFormatEXL2 {
		return fmt.Errorf("EXL2JobBuilder only handles EXL2 format, got %q", spec.Format)
	}
	if !spec.UseGPU {
		return fmt.Errorf("EXL2 quantization requires useGPU=true")
	}

	bits := DefaultEXL2Bits
	if spec.Bits != nil {
		bits = int(*spec.Bits)
	}
	if bits < MinEXL2Bits || bits > MaxEXL2Bits {
		return fmt.Errorf("EXL2 currently supports %d-%d bit quantization, got %d", MinEXL2Bits, MaxEXL2Bits, bits)
	}

	if spec.GroupSize != nil && *spec.GroupSize <= 0 {
		return fmt.Errorf("EXL2 groupSize must be > 0 when set, got %d", *spec.GroupSize)
	}

	return nil
}

// BuildJob creates a batch/v1.Job that quantizes a model to EXL2 format.
func (b *EXL2JobBuilder) BuildJob(params JobParams) (*batchv1.Job, error) {
	if err := b.Validate(params.Spec); err != nil {
		return nil, err
	}

	memoryGB := int32(DefaultGPUQuantizationMemoryGB)
	if params.Spec.MaxMemoryGB != nil {
		memoryGB = *params.Spec.MaxMemoryGB
	}

	bits := DefaultEXL2Bits
	if params.Spec.Bits != nil {
		bits = int(*params.Spec.Bits)
	}

	return buildGPUQuantizationJob(
		params,
		exl2QuantizerImage(),
		b.buildScript(params.ModelPath, bits),
		memoryGB,
	)
}

// buildScript generates the shell script for EXL2 quantization.
func (b *EXL2JobBuilder) buildScript(modelPath string, bits int) string {
	return fmt.Sprintf(`set -euo pipefail

MODEL_DIR="/cache/%s"
BITS=%d
TYPE="EXL2_B${BITS}"
OUT_DIR="${MODEL_DIR}/exl2-b${BITS}"
START_TS=$(date +%%s)

cleanup() { rm -rf "${OUT_DIR}"; echo "Cleaned up partial output"; }
trap cleanup EXIT

echo "=== EXL2 Quantization ==="
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
convert_script = os.environ.get("EXL2_CONVERT_SCRIPT", "/opt/exllamav2/convert.py")

cmd = [
    sys.executable,
    convert_script,
    "-i", model_dir,
    "-o", out_dir,
    "-b", bits,
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
  "format": "EXL2",
  "type": "${TYPE}",
  "originalSizeBytes": ${ORIGINAL_SIZE},
  "compressedSizeBytes": ${COMPRESSED_SIZE},
  "quantizationTimeSeconds": ${DURATION_SEC},
  "outputDir": "exl2-b${BITS}"
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

func exl2QuantizerImage() string {
	if img := os.Getenv("FLEXINFER_QUANTIZER_EXL2_IMAGE"); img != "" {
		return img
	}
	return DefaultEXL2Image
}
