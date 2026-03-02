package quantization

import (
	"fmt"
	"os"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

const (
	// DefaultAWQImage is the default image used for AWQ quantization jobs.
	DefaultAWQImage = "ghcr.io/flexinfer/quantizer:awq"

	// DefaultGPTQImage is the default image used for GPTQ quantization jobs.
	DefaultGPTQImage = "ghcr.io/flexinfer/quantizer:gptq"

	// DefaultGPUQuantizationMemoryGB is the default memory limit for AWQ/GPTQ jobs.
	DefaultGPUQuantizationMemoryGB = 48

	// DefaultGPUQuantizationCPU is the default CPU request for AWQ/GPTQ jobs.
	DefaultGPUQuantizationCPU = 8

	// DefaultCalibrationMaxSeqLen is the default max sequence length for calibration.
	DefaultCalibrationMaxSeqLen = 4096

	// DefaultCalibrationMaxSamples is the default number of calibration samples.
	DefaultCalibrationMaxSamples = 256

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
	if calib != nil {
		if calib.MaxSeqLen != nil {
			maxSeqLen = *calib.MaxSeqLen
		}
		if calib.MaxSamples != nil {
			maxSamples = *calib.MaxSamples
		}
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
echo "Calibration: maxSeqLen=%d maxSamples=%d"
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
)
model.save_quantized(out_dir)
tokenizer.save_pretrained(out_dir)
PY

trap - EXIT

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
`, modelPath, bits, groupSize, maxSeqLen, maxSamples, maxSeqLen, maxSamples)
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

	return buildGPUQuantizationJob(
		params,
		gptqQuantizerImage(),
		b.buildScript(params.ModelPath, bits, groupSize, params.Spec.Calibration),
		memoryGB,
	)
}

// buildScript generates the shell script for GPTQ quantization.
func (b *GPTQJobBuilder) buildScript(modelPath string, bits, groupSize int, calib *aiv1alpha1.CalibrationSpec) string {
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

	return fmt.Sprintf(`set -euo pipefail

MODEL_DIR="/cache/%s"
BITS=%d
GROUP_SIZE=%d
TYPE="W${BITS}_G${GROUP_SIZE}"
OUT_DIR="${MODEL_DIR}/gptq-w${BITS}-g${GROUP_SIZE}"
START_TS=$(date +%%s)

cleanup() { rm -rf "${OUT_DIR}"; echo "Cleaned up partial output"; }
trap cleanup EXIT

echo "=== GPTQ Quantization ==="
echo "Model: ${MODEL_DIR}"
echo "Type: ${TYPE}"
echo "Calibration: maxSeqLen=%d maxSamples=%d"
echo "Start: $(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)"

ORIGINAL_SIZE=$(du -sb "${MODEL_DIR}" | cut -f1)
echo "Original size: ${ORIGINAL_SIZE} bytes"

rm -rf "${OUT_DIR}"
mkdir -p "${OUT_DIR}"

export MODEL_DIR OUT_DIR BITS GROUP_SIZE
python3 - <<'PY'
import os
from datasets import load_dataset
from auto_gptq import AutoGPTQForCausalLM, BaseQuantizeConfig
from transformers import AutoTokenizer

model_dir = os.environ["MODEL_DIR"]
out_dir = os.environ["OUT_DIR"]
bits = int(os.environ["BITS"])
group_size = int(os.environ["GROUP_SIZE"])
max_seq_len = %d
max_samples = %d

tokenizer = AutoTokenizer.from_pretrained(model_dir, trust_remote_code=True)
quantize_config = BaseQuantizeConfig(bits=bits, group_size=group_size, desc_act=False)
model = AutoGPTQForCausalLM.from_pretrained(
    model_dir,
    quantize_config=quantize_config,
    trust_remote_code=True,
)

dataset = load_dataset("mit-han-lab/pile-val-backup", split="validation")
examples = []
for sample in dataset.select(range(min(max_samples, len(dataset)))):
    tok = tokenizer(sample["text"], return_tensors="pt", max_length=max_seq_len, truncation=True)
    examples.append(tok)

model.quantize(examples)
model.save_quantized(out_dir)
tokenizer.save_pretrained(out_dir)
PY

trap - EXIT

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
`, modelPath, bits, groupSize, maxSeqLen, maxSamples, maxSeqLen, maxSamples)
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

	podSpec := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{
			{
				Name:            "quantizer",
				Image:           image,
				ImagePullPolicy: corev1.PullAlways,
				Command:         []string{"/bin/bash", "-c"},
				Args:            []string{script},
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
