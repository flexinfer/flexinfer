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
	// DefaultGGUFType is the default quantization level for GGUF.
	// Q4_K_M provides a good balance of quality and compression.
	DefaultGGUFType = "Q4_K_M"

	// DefaultGGUFImage is the container image for GGUF quantization.
	DefaultGGUFImage = "ghcr.io/flexinfer/quantizer:gguf"

	// DefaultGGUFMemoryGB is the default memory limit for GGUF jobs.
	DefaultGGUFMemoryGB = 32

	// DefaultGGUFCPU is the default CPU request for GGUF jobs.
	DefaultGGUFCPU = 8

	// DefaultActiveDeadlineSeconds is the max runtime for quantization jobs.
	// 70B models can take up to 60 minutes; we set a 2-hour deadline.
	DefaultActiveDeadlineSeconds int64 = 7200
)

// GGUFJobBuilder generates Kubernetes Jobs for GGUF quantization.
// GGUF conversion runs entirely on CPU using llama.cpp tools:
// 1. convert_hf_to_gguf.py — converts HuggingFace format to FP16 GGUF
// 2. llama-quantize — quantizes FP16 GGUF to the target type (Q4_K_M, etc.)
type GGUFJobBuilder struct{}

// Format returns the GGUF quantization format.
func (b *GGUFJobBuilder) Format() aiv1alpha1.QuantizationFormat {
	return aiv1alpha1.QuantizationFormatGGUF
}

// Validate checks that the quantization spec is valid for GGUF.
func (b *GGUFJobBuilder) Validate(spec *aiv1alpha1.QuantizationSpec) error {
	if spec.Format != aiv1alpha1.QuantizationFormatGGUF {
		return fmt.Errorf("GGUFJobBuilder only handles GGUF format, got %q", spec.Format)
	}
	if spec.GGUFType != "" && !IsValidGGUFType(spec.GGUFType) {
		return fmt.Errorf("invalid GGUF type %q; valid types: %s", spec.GGUFType, strings.Join(ValidGGUFTypes, ", "))
	}
	return nil
}

// BuildJob creates a batch/v1.Job that quantizes a model to GGUF format.
func (b *GGUFJobBuilder) BuildJob(params JobParams) (*batchv1.Job, error) {
	if err := b.Validate(params.Spec); err != nil {
		return nil, err
	}

	ggufType := params.Spec.GGUFType
	if ggufType == "" {
		ggufType = DefaultGGUFType
	}

	memoryGB := int32(DefaultGGUFMemoryGB)
	if params.Spec.MaxMemoryGB != nil {
		memoryGB = *params.Spec.MaxMemoryGB
	}

	image := quantizerImage()
	deadline := DefaultActiveDeadlineSeconds
	backoffLimit := int32(2)

	// Build the quantization script.
	// Step 1: Convert HF → FP16 GGUF (if not already GGUF)
	// Step 2: Quantize FP16 → target type
	// Step 3: Move output to final location on the PVC
	script := b.buildScript(params.ModelPath, ggufType)

	pvcVol, pvcMount := modelPVCVolume(params.PVCName)
	wsVol, wsMount := workspaceVolume(fmt.Sprintf("%dGi", memoryGB*2))

	job := &batchv1.Job{
		ObjectMeta: defaultJobMeta(params),
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: &deadline,
			BackoffLimit:          &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:    "quantizer",
							Image:   image,
							Command: []string{"/bin/sh", "-c"},
							Args:    []string{script},
							VolumeMounts: []corev1.VolumeMount{
								pvcMount,
								wsMount,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", DefaultGGUFCPU)),
									corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dGi", memoryGB)),
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						pvcVol,
						wsVol,
					},
				},
			},
		},
	}

	return job, nil
}

// buildScript generates the shell script for GGUF quantization.
func (b *GGUFJobBuilder) buildScript(modelPath, ggufType string) string {
	// The script:
	// 1. Converts HF safetensors/bin to FP16 GGUF
	// 2. Quantizes FP16 to the target type
	// 3. Moves the quantized file back to the PVC
	// 4. Records sizes for status reporting
	return fmt.Sprintf(`set -euo pipefail

MODEL_DIR="/cache/%s"
WORKSPACE="/workspace"
GGUF_TYPE="%s"
START_TS=$(date +%%s)

echo "=== GGUF Quantization ==="
echo "Model: ${MODEL_DIR}"
echo "Type: ${GGUF_TYPE}"
echo "Start: $(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)"

# Record original size
ORIGINAL_SIZE=$(du -sb "${MODEL_DIR}" | cut -f1)
echo "Original size: ${ORIGINAL_SIZE} bytes"

# Step 1: Convert HuggingFace to FP16 GGUF
echo "--- Step 1: Converting to FP16 GGUF ---"
python3 /opt/llama.cpp/convert_hf_to_gguf.py \
  "${MODEL_DIR}" \
  --outfile "${WORKSPACE}/model-fp16.gguf" \
  --outtype f16

# Step 2: Quantize to target type
echo "--- Step 2: Quantizing to ${GGUF_TYPE} ---"
/opt/llama.cpp/llama-quantize \
  "${WORKSPACE}/model-fp16.gguf" \
  "${WORKSPACE}/model-${GGUF_TYPE}.gguf" \
  "${GGUF_TYPE}"

# Step 3: Move quantized model to PVC
QUANTIZED_FILE="${MODEL_DIR}/model-${GGUF_TYPE}.gguf"
mv "${WORKSPACE}/model-${GGUF_TYPE}.gguf" "${QUANTIZED_FILE}"

# Clean up intermediate FP16 file
rm -f "${WORKSPACE}/model-fp16.gguf"

# Record compressed size
COMPRESSED_SIZE=$(stat -c %%s "${QUANTIZED_FILE}" 2>/dev/null || stat -f %%z "${QUANTIZED_FILE}")
echo "Compressed size: ${COMPRESSED_SIZE} bytes"
END_TS=$(date +%%s)
DURATION_SEC=$((END_TS - START_TS))

# Write metadata for the controller to read
cat > "${MODEL_DIR}/.quantization-status.json" << METADATA
{
  "format": "GGUF",
  "type": "${GGUF_TYPE}",
  "originalSizeBytes": ${ORIGINAL_SIZE},
  "compressedSizeBytes": ${COMPRESSED_SIZE},
  "quantizationTimeSeconds": ${DURATION_SEC},
  "outputFile": "model-${GGUF_TYPE}.gguf"
}
METADATA

# Mirror metadata to container termination message so controller can read it.
cat > /dev/termination-log << TERMINATION
{
  "type": "${GGUF_TYPE}",
  "originalSizeBytes": ${ORIGINAL_SIZE},
  "compressedSizeBytes": ${COMPRESSED_SIZE},
  "quantizationTimeSeconds": ${DURATION_SEC}
}
TERMINATION

echo "=== Quantization complete ==="
echo "Output: ${QUANTIZED_FILE}"
echo "End: $(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)"
`, modelPath, ggufType)
}

// quantizerImage returns the container image for GGUF quantization.
// Supports override via environment variable.
func quantizerImage() string {
	if img := os.Getenv("FLEXINFER_QUANTIZER_GGUF_IMAGE"); img != "" {
		return img
	}
	return DefaultGGUFImage
}
