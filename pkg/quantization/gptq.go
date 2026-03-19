package quantization

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

// GPTQJobBuilder generates Kubernetes Jobs for GPTQ quantization.
type GPTQJobBuilder struct{}

type gptqModelPolicy struct {
	Name                   string                 `json:"name"`
	MatchModelTypes        []string               `json:"match_model_types,omitempty"`
	MatchPathSubstrings    []string               `json:"match_path_substrings,omitempty"`
	ExtractTextConfig      bool                   `json:"extract_text_config,omitempty"`
	CopyRootKeys           []string               `json:"copy_root_keys,omitempty"`
	RemapModelType         string                 `json:"remap_model_type,omitempty"`
	Architectures          []string               `json:"architectures,omitempty"`
	Loader                 string                 `json:"loader,omitempty"`
	PythonPackages         []string               `json:"python_packages,omitempty"`
	QuantizeConfigOverride map[string]interface{} `json:"quantize_config_overrides,omitempty"`
}

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
	// Prefer GPUProfile-specific runtime images first so per-arch/per-node
	// immutable digests can override the global fallback cleanly.
	if params.ProfileQuantizerImage != "" {
		image = params.ProfileQuantizerImage
	} else if img := runtimeImageForQuantization(); img != "" {
		image = img
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
	modelPolicies := os.Getenv("FLEXINFER_GPTQ_MODEL_POLICIES")
	if modelPolicies == "" {
		modelPolicies = defaultGPTQModelPoliciesJSON()
	}
	hessianRepair := getenvDefault("FLEXINFER_GPTQ_HESSIAN_REPAIR", "true")
	hessianSanitizeNonfinite := getenvDefault("FLEXINFER_GPTQ_HESSIAN_SANITIZE_NONFINITE", "true")
	hessianDiagFloorScale := getenvDefault("FLEXINFER_GPTQ_HESSIAN_DIAG_FLOOR_SCALE", "1e-6")
	hessianFloorMultiplier := getenvDefault("FLEXINFER_GPTQ_HESSIAN_FLOOR_MULTIPLIER", "10")
	hessianMaxFloorAttempts := getenvDefault("FLEXINFER_GPTQ_HESSIAN_MAX_FLOOR_ATTEMPTS", "6")
	hessianClampAbs := getenvDefault("FLEXINFER_GPTQ_HESSIAN_CLAMP_ABS", "0")
	dampPercentOverride := os.Getenv("FLEXINFER_GPTQ_DAMP_PERCENT_OVERRIDE")
	dampAutoIncrementOverride := os.Getenv("FLEXINFER_GPTQ_DAMP_AUTO_INCREMENT_OVERRIDE")
	resumeEnabled := getenvDefault("FLEXINFER_GPTQ_RESUME", "true")
	calibrationCacheEnabled := getenvDefault("FLEXINFER_GPTQ_CALIBRATION_CACHE", "true")

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
		{Name: "QUANTIZE_MODEL_POLICIES", Value: modelPolicies},
		{Name: "GPTQ_HESSIAN_REPAIR", Value: hessianRepair},
		{Name: "GPTQ_HESSIAN_SANITIZE_NONFINITE", Value: hessianSanitizeNonfinite},
		{Name: "GPTQ_HESSIAN_DIAG_FLOOR_SCALE", Value: hessianDiagFloorScale},
		{Name: "GPTQ_HESSIAN_FLOOR_MULTIPLIER", Value: hessianFloorMultiplier},
		{Name: "GPTQ_HESSIAN_MAX_FLOOR_ATTEMPTS", Value: hessianMaxFloorAttempts},
		{Name: "GPTQ_HESSIAN_CLAMP_ABS", Value: hessianClampAbs},
		{Name: "GPTQ_DAMP_PERCENT_OVERRIDE", Value: dampPercentOverride},
		{Name: "GPTQ_DAMP_AUTO_INCREMENT_OVERRIDE", Value: dampAutoIncrementOverride},
		{Name: "GPTQ_RESUME", Value: resumeEnabled},
		{Name: "GPTQ_CALIBRATION_CACHE", Value: calibrationCacheEnabled},
		{Name: "FLEXINFER_TELEMETRY", Value: "true"},
	}
}

func getenvDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func defaultGPTQModelPoliciesJSON() string {
	policies := []gptqModelPolicy{
		{
			Name:                "qwen3.5-text",
			MatchModelTypes:     []string{"qwen3_5_text"},
			MatchPathSubstrings: []string{"qwen35", "qwen3.5"},
			ExtractTextConfig:   true,
			CopyRootKeys:        []string{"bos_token_id", "eos_token_id", "pad_token_id"},
			RemapModelType:      "qwen3_5_text",
			Architectures:       []string{"Qwen3_5ForCausalLM"},
			Loader:              "manual_sharded_state_dict",
			PythonPackages: []string{
				"git+https://github.com/huggingface/transformers.git@529504b2fa98970c6c44d3fafaeb07a39c40e7ea",
			},
			QuantizeConfigOverride: map[string]interface{}{
				"offload_to_disk": false,
			},
		},
	}
	data, err := json.Marshal(policies)
	if err != nil {
		panic(fmt.Sprintf("marshal default GPTQ model policies: %v", err))
	}
	return string(data)
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

# GPTQModel's direct CPU path still injects device_map=cpu_device_map. In
# transformers, any device_map enables meta-device loading/dispatch, which is
# exactly the path failing for Qwen3.5 here. Strip device_map and force
# low_cpu_mem_usage=False on the direct path.
LOADER_PY=$(python3 -c "import gptqmodel.models.loader as l; print(l.__file__)" 2>/dev/null || true)
if [ -n "${LOADER_PY}" ] && ! grep -q 'direct_init_kwargs.pop("device_map", None)' "${LOADER_PY}" 2>/dev/null; then
    python3 - <<'PY'
from pathlib import Path

path = Path("/opt/venv/lib/python3.12/site-packages/gptqmodel/models/loader.py")
src = path.read_text()
old = '''        else:
            print("loading model directly to CPU (not using meta device or turtle_model)-----------")
            model = cls.loader.from_pretrained(model_local_path, config=config, **model_init_kwargs)
            if getattr(model, "config", None) is config:
                model.config = copy.deepcopy(config)
            defuser.convert_model(model, cleanup_original=False)
            model._model_init_kwargs = model_init_kwargs
            print_module_tree(model=model)

            turtle_model = None'''
new = '''        else:
            print("loading model directly to CPU (not using meta device or turtle_model)-----------")
            direct_init_kwargs = model_init_kwargs.copy()
            direct_init_kwargs.pop("device_map", None)
            direct_init_kwargs["low_cpu_mem_usage"] = False
            model = cls.loader.from_pretrained(model_local_path, config=config, **direct_init_kwargs)
            if getattr(model, "config", None) is config:
                model.config = copy.deepcopy(config)
            defuser.convert_model(model, cleanup_original=False)
            model._model_init_kwargs = direct_init_kwargs
            print_module_tree(model=model)

            turtle_model = None'''
if old in src:
    src = src.replace(old, new)
else:
    raise SystemExit("expected GPTQModel direct CPU load block not found in loader.py")
path.write_text(src)
PY
    echo "Patched GPTQModel loader.py direct CPU path to disable device_map/meta loading"
fi

# Patch the bundled quantize script so composite text_config models avoid
# GPTQModel's meta-device turtle-load path, which currently crashes in
# transformers when materializing Qwen3.5 weights from meta tensors.
GPTQ_SCRIPT=/opt/flexinfer/scripts/quantize_gptq.py
if [ -f "${GPTQ_SCRIPT}" ] && ! grep -q "Disabled GPTQ offload_to_disk for model_type=" "${GPTQ_SCRIPT}" 2>/dev/null; then
    python3 - <<'PY'
from pathlib import Path

path = Path("/opt/flexinfer/scripts/quantize_gptq.py")
src = path.read_text()
src = src.replace(
    'with open(cfg_path) as f:\n    cfg = json.load(f)\nif "text_config" in cfg and "model_type" in cfg.get("text_config", {}):',
    'with open(cfg_path) as f:\n    cfg = json.load(f)\nmodel_type = cfg.get("model_type") or cfg.get("text_config", {}).get("model_type", "")\ncomposite_text_model = "text_config" in cfg and "model_type" in cfg.get("text_config", {})\nif composite_text_model:',
)
src = src.replace(
    '    print(f"Extracted text_config: model_type={text_cfg.get(\'model_type\')}")',
    '    print(f"Extracted text_config: model_type={text_cfg.get(\'model_type\')}")\n    model_type = text_cfg.get("model_type", model_type)\nforce_direct_load = composite_text_model or model_type.startswith("qwen3_5")\nif force_direct_load:\n    print(f"Using direct GPTQ load path for model_type={model_type or \'unknown\'}")',
)
src = src.replace(
    'qcfg_kwargs = dict(bits=bits, group_size=group_size, sym=sym, desc_act=desc_act)\nif dynamic_config is not None:\n    qcfg_kwargs["dynamic"] = dynamic_config',
    'qcfg_kwargs = dict(bits=bits, group_size=group_size, sym=sym, desc_act=desc_act)\nif dynamic_config is not None:\n    qcfg_kwargs["dynamic"] = dynamic_config\nif force_direct_load:\n    qcfg_kwargs["offload_to_disk"] = False\n    print(f"Disabled GPTQ offload_to_disk for model_type={model_type or \'unknown\'}")',
)
path.write_text(src)
PY
    echo "Patched quantize_gptq.py to disable GPTQ offload_to_disk for Qwen3.5 direct load"
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

# GPTQModel 5.8.x in the runtime image can be present without its newer Python
# dependency set. Bootstrap the minimum compatible import/runtime deps here so
# the quantize job is self-contained even when the base image lags.
if ! python3 -c "import tokenicer, pcre, kernels, torchao" >/dev/null 2>&1; then
    echo "Installing missing GPTQModel runtime dependencies..."
    python3 -m pip install --no-cache-dir --quiet \
        "tokenicer>=0.0.10" \
        "pypcre>=0.2.13" \
        "kernels>=0.12.2" \
        "torchao>=0.16.0" \
        "accelerate>=1.13.0" \
        "numpy==2.2.6" \
        "pillow>=11.3.0" \
        "protobuf>=7.34.0" >/dev/null
    python3 -c "import tokenicer, pcre, kernels, torchao" >/dev/null
fi

# MAGMA fallback: vllm-dev base images lack MAGMA (GPU) and LAPACK (CPU),
# causing torch.linalg.{cholesky,eigh,svd,qr} to fail. Patch to use scipy as
# final fallback for linalg ops needed by GPTQ warmup and Hessian inverse.
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
_qr = torch.linalg.qr

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

def safe_qr(input, mode='reduced', *, out=None):
    try:
        return _qr(input, mode=mode, out=out)
    except RuntimeError as e:
        if 'MAGMA' not in str(e) and 'LAPACK' not in str(e):
            raise
    try:
        q, r = _qr(input.cpu(), mode=mode)
        return q.to(input.device), r.to(input.device)
    except RuntimeError:
        pass
    if not _HAS_SCIPY:
        raise RuntimeError("torch.linalg.qr needs MAGMA/LAPACK; scipy not available")
    arr = input.detach().cpu().to(torch.float64).numpy()
    mode_map = {'reduced': 'economic', 'complete': 'full', 'r': 'r', 'raw': 'raw'}
    scipy_mode = mode_map.get(mode, mode)
    result = _scipy_la.qr(arr, mode=scipy_mode)
    if mode == 'r':
        r = torch.from_numpy(result).to(dtype=input.dtype, device=input.device)
        return r
    if mode == 'raw':
        # scipy raw mode returns (Q, TAU); let torch handle uncommon modes if possible.
        raise RuntimeError("torch.linalg.qr raw mode needs MAGMA/LAPACK; scipy raw fallback unsupported")
    q_np, r_np = result
    q = torch.from_numpy(q_np).to(dtype=input.dtype, device=input.device)
    r = torch.from_numpy(r_np).to(dtype=input.dtype, device=input.device)
    return q, r

torch.linalg.cholesky = safe_cholesky
torch.linalg.eigh = safe_eigh
torch.linalg.svd = safe_svd
torch.linalg.qr = safe_qr
print("Patched torch.linalg.cholesky/eigh/svd/qr with MAGMA/LAPACK/scipy fallback")
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
