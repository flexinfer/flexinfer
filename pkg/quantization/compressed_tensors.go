package quantization

import (
	"fmt"
	"os"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

const (
	// DefaultCompressedTensorsBits is the only supported weight bit width today.
	DefaultCompressedTensorsBits = 4

	// DefaultCompressedTensorsActivationBits is fixed to A16 for current experiments.
	DefaultCompressedTensorsActivationBits = 16

	// DefaultCompressedTensorsGroupSize is the only supported group size today.
	DefaultCompressedTensorsGroupSize = 128
)

// CompressedTensorsJobBuilder generates Kubernetes Jobs for compressed-tensors quantization.
type CompressedTensorsJobBuilder struct{}

// Format returns the compressed-tensors quantization format.
func (b *CompressedTensorsJobBuilder) Format() aiv1alpha2.QuantizationFormat {
	return aiv1alpha2.QuantizationFormatCompressedTensors
}

// Validate checks that the quantization spec is valid for compressed-tensors.
func (b *CompressedTensorsJobBuilder) Validate(spec *aiv1alpha2.QuantizationSpec) error {
	if spec.Format != aiv1alpha2.QuantizationFormatCompressedTensors {
		return fmt.Errorf("CompressedTensorsJobBuilder only handles COMPRESSED_TENSORS format, got %q", spec.Format)
	}
	if !spec.UseGPU {
		return fmt.Errorf("COMPRESSED_TENSORS: %w", ErrGPURequired)
	}

	bits := DefaultCompressedTensorsBits
	if spec.Bits != nil {
		bits = int(*spec.Bits)
	}
	if bits != DefaultCompressedTensorsBits {
		return fmt.Errorf(
			"COMPRESSED_TENSORS %w: got %d, want %d for W4A16",
			ErrInvalidBits,
			bits,
			DefaultCompressedTensorsBits,
		)
	}

	groupSize := DefaultCompressedTensorsGroupSize
	if spec.GroupSize != nil {
		groupSize = int(*spec.GroupSize)
	}
	if groupSize != DefaultCompressedTensorsGroupSize {
		return fmt.Errorf(
			"COMPRESSED_TENSORS groupSize must be %d for W4A16 (got %d)",
			DefaultCompressedTensorsGroupSize,
			groupSize,
		)
	}

	return nil
}

// BuildJob creates a batch/v1.Job for compressed-tensors quantization.
// This builder is intentionally guarded until runtime/image wiring is explicitly provided.
func (b *CompressedTensorsJobBuilder) BuildJob(params JobParams) (*batchv1.Job, error) {
	if err := b.Validate(params.Spec); err != nil {
		return nil, err
	}

	// Container memory priority: spec > GPUProfile > hardcoded default.
	memoryGB := int32(DefaultGPUQuantizationMemoryGB)
	if params.MemoryConfig.ContainerMemoryGB > 0 {
		memoryGB = params.MemoryConfig.ContainerMemoryGB
	}
	if params.Spec.MaxMemoryGB != nil {
		memoryGB = *params.Spec.MaxMemoryGB
	}

	bits := DefaultCompressedTensorsBits
	if params.Spec.Bits != nil {
		bits = int(*params.Spec.Bits)
	}
	groupSize := DefaultCompressedTensorsGroupSize
	if params.Spec.GroupSize != nil {
		groupSize = int(*params.Spec.GroupSize)
	}

	image := ResolveImage(ImageFormatCompressedTensors, params.ProfileQuantizerImage, params.GPUVendor, params.GPUArch)
	command := strings.TrimSpace(os.Getenv("FLEXINFER_COMPRESSED_TENSORS_COMMAND"))

	missing := make([]string, 0, 2)
	if image == "" {
		missing = append(missing, "quantizer image")
	}
	if command == "" {
		missing = append(missing, "runner command")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf(
			"COMPRESSED_TENSORS: %w: missing %s (set FLEXINFER_QUANTIZER_COMPRESSED_TENSORS_IMAGE or GPUProfile/runtime image, and FLEXINFER_COMPRESSED_TENSORS_COMMAND)",
			ErrFormatNotConfigured,
			strings.Join(missing, " and "),
		)
	}

	return buildGPUQuantizationJob(
		params,
		image,
		b.wrapperScript(),
		memoryGB,
		b.buildEnv(params.ModelPath, bits, groupSize, command),
	)
}

// CompressedTensorsType returns the canonical type descriptor (e.g. W4A16_G128).
func CompressedTensorsType(bits, groupSize int) string {
	return fmt.Sprintf("W%dA%d_G%d", bits, DefaultCompressedTensorsActivationBits, groupSize)
}

// CompressedTensorsOutputSubdir returns a deterministic output directory name.
func CompressedTensorsOutputSubdir(bits, groupSize int) string {
	return fmt.Sprintf("compressed-tensors-w%d-g%d", bits, groupSize)
}

func (b *CompressedTensorsJobBuilder) buildEnv(modelPath string, bits, groupSize int, command string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "MODEL_DIR", Value: fmt.Sprintf("/cache/%s", modelPath)},
		{Name: "BITS", Value: fmt.Sprintf("%d", bits)},
		{Name: "GROUP_SIZE", Value: fmt.Sprintf("%d", groupSize)},
		{Name: "TYPE", Value: CompressedTensorsType(bits, groupSize)},
		{Name: "OUTPUT_SUBDIR", Value: CompressedTensorsOutputSubdir(bits, groupSize)},
		{Name: "OUT_DIR", Value: fmt.Sprintf("/cache/%s/%s", modelPath, CompressedTensorsOutputSubdir(bits, groupSize))},
		{Name: "COMPRESSED_TENSORS_COMMAND", Value: command},
		{Name: "FLEXINFER_TELEMETRY", Value: "true"},
	}
}

func (b *CompressedTensorsJobBuilder) wrapperScript() string {
	return `set -euo pipefail
START_TS=$(date +%s)

cleanup() {
    local ec=$?
    if [ $ec -ne 0 ] && [ ! -f "${OUT_DIR}/config.json" ]; then
        rm -rf "${OUT_DIR}" || true
    fi
}
trap cleanup EXIT

echo "=== COMPRESSED_TENSORS Quantization ==="
echo "Model: ${MODEL_DIR}"
echo "Type: ${TYPE}"
echo "Command: ${COMPRESSED_TENSORS_COMMAND}"

ORIGINAL_SIZE=$(du -sb "${MODEL_DIR}" | cut -f1)
rm -rf "${OUT_DIR}"
mkdir -p "${OUT_DIR}"

bash -lc "${COMPRESSED_TENSORS_COMMAND}"

trap - EXIT
COMPRESSED_SIZE=$(du -sb "${OUT_DIR}" | cut -f1)
END_TS=$(date +%s)
DURATION_SEC=$((END_TS - START_TS))

cat > "${MODEL_DIR}/.quantization-status.json" << METADATA
{
  "format": "COMPRESSED_TENSORS",
  "type": "${TYPE}",
  "originalSizeBytes": ${ORIGINAL_SIZE},
  "compressedSizeBytes": ${COMPRESSED_SIZE},
  "quantizationTimeSeconds": ${DURATION_SEC},
  "outputDir": "${OUTPUT_SUBDIR}"
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
`
}
