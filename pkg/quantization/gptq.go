package quantization

import (
	"fmt"
	"os"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

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

	env := b.buildEnv(params.ModelPath, bits, groupSize, sym, descAct, memoryGB, gpuMemFraction, dynamicExclusion, params.Spec.Calibration)

	return buildGPUQuantizationJob(
		params,
		image,
		b.gptqWrapperScript(),
		memoryGB,
		env,
	)
}

// buildEnv returns environment variables for the GPTQ quantization script.
func (b *GPTQJobBuilder) buildEnv(modelPath string, bits, groupSize int, sym, descAct bool, memoryGB int32, gpuMemFraction, dynamicExclusion string, calib *aiv1alpha1.CalibrationSpec) []corev1.EnvVar {
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

	symStr := "True"
	if !sym {
		symStr = "False"
	}
	descActStr := "False"
	if descAct {
		descActStr = "True"
	}

	return []corev1.EnvVar{
		{Name: "MODEL_DIR", Value: fmt.Sprintf("/cache/%s", modelPath)},
		{Name: "OUT_DIR", Value: fmt.Sprintf("/cache/%s/gptq-w%d-g%d", modelPath, bits, groupSize)},
		{Name: "BITS", Value: fmt.Sprintf("%d", bits)},
		{Name: "GROUP_SIZE", Value: fmt.Sprintf("%d", groupSize)},
		{Name: "MAX_MEMORY_GB", Value: fmt.Sprintf("%d", memoryGB)},
		{Name: "MAX_SEQ_LEN", Value: fmt.Sprintf("%d", maxSeqLen)},
		{Name: "MAX_SAMPLES", Value: fmt.Sprintf("%d", maxSamples)},
		{Name: "SYM", Value: symStr},
		{Name: "DESC_ACT", Value: descActStr},
		{Name: "GPU_MEMORY_FRACTION", Value: gpuMemFraction},
		{Name: "DYNAMIC_EXCLUSION", Value: dynamicExclusion},
		{Name: "DATASET", Value: dataset},
		{Name: "FLEXINFER_TELEMETRY", Value: "true"},
	}
}

// gptqWrapperScript returns the shell wrapper for GPTQ quantization.
// It handles cleanup, GPTQModel patching, ROCm detection, size tracking,
// status files, and delegates to the Python script.
func (b *GPTQJobBuilder) gptqWrapperScript() string {
	return `set -euo pipefail
TYPE="W${BITS}_G${GROUP_SIZE}"
START_TS=$(date +%s)

cleanup() {
    local ec=$?
    if [ $ec -ne 0 ]; then
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
WRITER_PY=$(python3 -c "import gptqmodel.models.writer as w; print(w.__file__)" 2>/dev/null || true)
if [ -n "${WRITER_PY}" ] && grep -q "pre_quantized_size_mb) \* 100" "${WRITER_PY}" 2>/dev/null; then
    sed -i 's|percent_diff = (size_diff_mb / pre_quantized_size_mb) \* 100|percent_diff = (size_diff_mb / pre_quantized_size_mb) * 100 if pre_quantized_size_mb > 0 else 0.0|' "${WRITER_PY}"
    echo "Patched GPTQModel writer.py for ZeroDivisionError"
fi

# Auto-detect gfx900 (Radeon VII).
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
echo "Container memory limit: ${MAX_MEMORY_GB}Gi"
echo "Start: $(date -u +%Y-%m-%dT%H:%M:%SZ)"

if [ ! -d "${MODEL_DIR}" ]; then
    echo "ERROR: MODEL_DIR does not exist: ${MODEL_DIR}"
    echo "Download may not have completed. Listing /cache/:"
    ls -la /cache/ 2>/dev/null || echo "(empty or inaccessible)"
    exit 1
fi

if [ ! -f "${MODEL_DIR}/config.json" ]; then
    echo "ERROR: No config.json in MODEL_DIR: ${MODEL_DIR}"
    echo "Contents:"
    ls -la "${MODEL_DIR}/" 2>/dev/null || echo "(empty)"
    exit 1
fi

ORIGINAL_SIZE=$(du -sb "${MODEL_DIR}" | cut -f1)
echo "Original size: ${ORIGINAL_SIZE} bytes"

rm -rf "${OUT_DIR}"
mkdir -p "${OUT_DIR}"
mkdir -p /workspace/offload

# MAGMA fallback: vllm-dev base images lack MAGMA (GPU) and LAPACK (CPU),
# causing torch.linalg.cholesky/eigh to fail. Patch to use scipy as final
# fallback for linalg ops needed by GPTQ Hessian inverse.
cat > /tmp/_magma_fallback.py << 'MAGMA_PATCH'
import torch, sys, runpy
import numpy as np
try:
    import scipy.linalg as _scipy_la
    _HAS_SCIPY = True
except ImportError:
    _HAS_SCIPY = False

_chol = torch.linalg.cholesky
_eigh = torch.linalg.eigh
_svd = torch.linalg.svd

def safe_cholesky(input, *, upper=False, out=None):
    try:
        return _chol(input, upper=upper, out=out)
    except RuntimeError as e:
        if 'MAGMA' not in str(e) and 'LAPACK' not in str(e):
            raise
    # GPU/CPU torch failed, try CPU torch first
    try:
        r = _chol(input.cpu(), upper=upper)
        return r.to(input.device) if out is None else out.copy_(r.to(input.device))
    except RuntimeError:
        pass
    # Final fallback: scipy
    if not _HAS_SCIPY:
        raise RuntimeError("torch.linalg.cholesky needs MAGMA/LAPACK; scipy not available")
    arr = input.detach().cpu().to(torch.float64).numpy()
    result = _scipy_la.cholesky(arr, lower=not upper)
    t = torch.from_numpy(result).to(dtype=input.dtype, device=input.device)
    return t

def safe_eigh(input, UPLO='L'):
    try:
        return _eigh(input, UPLO=UPLO)
    except RuntimeError as e:
        if 'MAGMA' not in str(e) and 'LAPACK' not in str(e):
            raise
    try:
        w, v = _eigh(input.cpu(), UPLO=UPLO)
        return w.to(input.device), v.to(input.device)
    except RuntimeError:
        pass
    if not _HAS_SCIPY:
        raise RuntimeError("torch.linalg.eigh needs MAGMA/LAPACK; scipy not available")
    arr = input.detach().cpu().to(torch.float64).numpy()
    w_np, v_np = _scipy_la.eigh(arr, lower=(UPLO == 'L'))
    w = torch.from_numpy(w_np).to(dtype=input.dtype, device=input.device)
    v = torch.from_numpy(v_np).to(dtype=input.dtype, device=input.device)
    return w, v

def safe_svd(input, full_matrices=True, *, driver=None, out=None):
    try:
        return _svd(input, full_matrices=full_matrices, driver=driver, out=out)
    except RuntimeError as e:
        if 'MAGMA' not in str(e) and 'LAPACK' not in str(e):
            raise
    try:
        u, s, vh = _svd(input.cpu(), full_matrices=full_matrices, driver=driver)
        return u.to(input.device), s.to(input.device), vh.to(input.device)
    except RuntimeError:
        pass
    if not _HAS_SCIPY:
        raise RuntimeError("torch.linalg.svd needs MAGMA/LAPACK; scipy not available")
    arr = input.detach().cpu().to(torch.float64).numpy()
    u_np, s_np, vh_np = _scipy_la.svd(arr, full_matrices=full_matrices)
    u = torch.from_numpy(u_np).to(dtype=input.dtype, device=input.device)
    s = torch.from_numpy(s_np).to(dtype=input.dtype, device=input.device)
    vh = torch.from_numpy(vh_np).to(dtype=input.dtype, device=input.device)
    return u, s, vh

torch.linalg.cholesky = safe_cholesky
torch.linalg.eigh = safe_eigh
torch.linalg.svd = safe_svd
print("Patched torch.linalg.cholesky/eigh/svd with MAGMA/LAPACK/scipy fallback")
sys.argv = ['quantize_gptq.py']
runpy.run_path('/opt/flexinfer/scripts/quantize_gptq.py', run_name='__main__')
MAGMA_PATCH

python3 /tmp/_magma_fallback.py

trap - EXIT

if ! ls "${OUT_DIR}"/*.safetensors &>/dev/null; then
    echo "ERROR: No safetensors files in output dir"
    exit 1
fi

COMPRESSED_SIZE=$(du -sb "${OUT_DIR}" | cut -f1)
echo "Compressed size: ${COMPRESSED_SIZE} bytes"

FP16_COUNT=$(find "${MODEL_DIR}" -maxdepth 1 \( -name '*.safetensors' -o -name '*.bin' -o -name '*.pt' \) \
    ! -path "${OUT_DIR}/*" 2>/dev/null | wc -l)
if [ "${FP16_COUNT}" -gt 0 ]; then
    find "${MODEL_DIR}" -maxdepth 1 \( -name '*.safetensors' -o -name '*.bin' -o -name '*.pt' \) \
        ! -path "${OUT_DIR}/*" -print -delete 2>/dev/null || true
    echo "FP16 source files cleaned up (${FP16_COUNT} files)"
fi

END_TS=$(date +%s)
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
echo "End: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
`
}

func gptqQuantizerImage() string {
	if img := os.Getenv("FLEXINFER_QUANTIZER_GPTQ_IMAGE"); img != "" {
		return img
	}
	return DefaultGPTQImage
}

func gptqQuantizerROCmImage(gpuArch string) string {
	// Prefer unified runtime image when available.
	if img := runtimeImageForQuantization(); img != "" {
		return img
	}
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

// runtimeImageForQuantization returns the unified runtime image when
// FLEXINFER_USE_RUNTIME_FOR_QUANTIZE is set. This eliminates the need
// for separate 60GB+ quantizer images.
func runtimeImageForQuantization() string {
	if os.Getenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE") != "true" {
		return ""
	}
	return os.Getenv("FLEXINFER_RUNTIME_IMAGE")
}
