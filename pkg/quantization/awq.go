package quantization

import (
	"fmt"
	"os"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// AWQJobBuilder generates Kubernetes Jobs for AWQ quantization.
type AWQJobBuilder struct{}

// Format returns the AWQ quantization format.
func (b *AWQJobBuilder) Format() aiv1alpha2.QuantizationFormat {
	return aiv1alpha2.QuantizationFormatAWQ
}

// Validate checks that the quantization spec is valid for AWQ.
func (b *AWQJobBuilder) Validate(spec *aiv1alpha2.QuantizationSpec) error {
	if spec.Format != aiv1alpha2.QuantizationFormatAWQ {
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

	env := b.buildEnv(params.ModelPath, bits, groupSize, params.Spec.Calibration)

	return buildGPUQuantizationJob(
		params,
		image,
		b.awqWrapperScript(),
		memoryGB,
		env,
	)
}

// buildEnv returns environment variables for the AWQ quantization script.
func (b *AWQJobBuilder) buildEnv(modelPath string, bits, groupSize int, calib *aiv1alpha2.CalibrationSpec) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "MODEL_DIR", Value: fmt.Sprintf("/cache/%s", modelPath)},
		{Name: "BITS", Value: fmt.Sprintf("%d", bits)},
		{Name: "GROUP_SIZE", Value: fmt.Sprintf("%d", groupSize)},
		{Name: "FLEXINFER_TELEMETRY", Value: "true"},
		{Name: "OUT_DIR", Value: fmt.Sprintf("/cache/%s/awq-w%d-g%d", modelPath, bits, groupSize)},
	}
	env = append(env, BuildCalibrationEnv(calib)...)
	return env
}

// awqWrapperScript returns the shell wrapper for AWQ quantization.
// It handles cleanup, size tracking, status files, and delegates to the Python script.
func (b *AWQJobBuilder) awqWrapperScript() string {
	return `set -euo pipefail
TYPE="W${BITS}_G${GROUP_SIZE}"
START_TS=$(date +%s)

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
echo "Start: $(date -u +%Y-%m-%dT%H:%M:%SZ)"

ORIGINAL_SIZE=$(du -sb "${MODEL_DIR}" | cut -f1)
echo "Original size: ${ORIGINAL_SIZE} bytes"

rm -rf "${OUT_DIR}"
mkdir -p "${OUT_DIR}"

python3 /opt/flexinfer/scripts/quantize_awq.py

trap - EXIT

find "${MODEL_DIR}" -maxdepth 1 \( -name '*.safetensors' -o -name '*.bin' -o -name '*.pt' \) \
    ! -path "${OUT_DIR}/*" -print -delete 2>/dev/null || true
echo "FP16 source files cleaned up"

COMPRESSED_SIZE=$(du -sb "${OUT_DIR}" | cut -f1)
echo "Compressed size: ${COMPRESSED_SIZE} bytes"
END_TS=$(date +%s)
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
echo "End: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
`
}

func awqQuantizerImage() string {
	if img := os.Getenv("FLEXINFER_QUANTIZER_AWQ_IMAGE"); img != "" {
		return img
	}
	return DefaultAWQImage
}
