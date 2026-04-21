package quantization

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

// GPTQScriptVersion must match FLEXINFER_SCRIPT_VERSION in quantize_gptq.py.
// Bump both when controller-side heredoc patches change to catch stale images.
//
// v13: .save-complete manifest marks save-phase completion; per-layer state
// writer (flag-gated) persists quantized layer tensors to the PVC so future
// resume work can skip already-quantized layers.
// v14: Phase B — reload cached layers before model.quantize() so the looper's
// find_modules filter naturally skips them. Adds write dedup across the
// multiple layer_complete fires per layer in v5.x GPTQModel.
const GPTQScriptVersion = "v14"

// GPTQJobBuilder generates Kubernetes Jobs for GPTQ quantization.
type GPTQJobBuilder struct{}

type gptqModelPolicy struct {
	Name                   string         `json:"name"`
	MatchModelTypes        []string       `json:"match_model_types,omitempty"`
	MatchPathSubstrings    []string       `json:"match_path_substrings,omitempty"`
	ExtractTextConfig      bool           `json:"extract_text_config,omitempty"`
	CopyRootKeys           []string       `json:"copy_root_keys,omitempty"`
	RemapModelType         string         `json:"remap_model_type,omitempty"`
	Architectures          []string       `json:"architectures,omitempty"`
	Loader                 string         `json:"loader,omitempty"`
	PythonPackages         []string       `json:"python_packages,omitempty"`
	QuantizeConfigOverride map[string]any `json:"quantize_config_overrides,omitempty"`
	CalibrationOverrides   map[string]int `json:"calibration_overrides,omitempty"`
	RuntimeOverrides       map[string]any `json:"runtime_overrides,omitempty"`
	ArtifactOverrides      map[string]any `json:"artifact_overrides,omitempty"`
}

// Format returns the GPTQ quantization format.
func (b *GPTQJobBuilder) Format() aiv1alpha2.QuantizationFormat {
	return aiv1alpha2.QuantizationFormatGPTQ
}

// Validate checks that the quantization spec is valid for GPTQ.
func (b *GPTQJobBuilder) Validate(spec *aiv1alpha2.QuantizationSpec) error {
	if spec.Format != aiv1alpha2.QuantizationFormatGPTQ {
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

	if spec.DenseModulePolicy != nil {
		switch *spec.DenseModulePolicy {
		case "fallback", "validate":
		default:
			return fmt.Errorf("GPTQ denseModulePolicy must be fallback or validate, got %q", *spec.DenseModulePolicy)
		}
	}
	if spec.DenseModuleCosineThreshold != nil {
		threshold, err := strconv.ParseFloat(*spec.DenseModuleCosineThreshold, 64)
		if err != nil || threshold <= 0 || threshold > 1 {
			return fmt.Errorf("GPTQ denseModuleCosineThreshold must be > 0 and <= 1, got %q", *spec.DenseModuleCosineThreshold)
		}
	}

	return nil
}

// BuildJob creates a batch/v1.Job that quantizes a model to GPTQ format.
func (b *GPTQJobBuilder) BuildJob(params JobParams) (*batchv1.Job, error) {
	if err := b.Validate(params.Spec); err != nil {
		return nil, err
	}

	// Container memory priority: env var > spec > GPUProfile > hardcoded default.
	memoryGB := int32(DefaultGPUQuantizationMemoryGB)
	if params.MemoryConfig.ContainerMemoryGB > 0 {
		memoryGB = params.MemoryConfig.ContainerMemoryGB
	}
	if params.Spec.MaxMemoryGB != nil {
		memoryGB = *params.Spec.MaxMemoryGB
	}
	// Allow env var override for memory (e.g. when redirecting to a high-RAM node).
	if override := os.Getenv("FLEXINFER_GPTQ_MAX_MEMORY_GB"); override != "" {
		if v, err := strconv.Atoi(override); err == nil && v > 0 {
			memoryGB = int32(v)
		}
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

	image := ResolveImage(ImageFormatGPTQ, params.ProfileQuantizerImage, params.GPUVendor, params.GPUArch)

	outSubdir := GPTQOutputSubdir(params.ModelPath, bits, groupSize)

	env := b.buildEnv(params.ModelPath, outSubdir, bits, groupSize, sym, descAct, memoryGB, gpuMemFraction, dynamicExclusion, params.GPUArch, params.MemoryConfig.GPUVramMB, params.Spec.Calibration)
	env = append(env, b.buildDenseModuleValidationEnv(params.Spec)...)

	return buildGPUQuantizationJob(
		params,
		image,
		b.gptqWrapperScript(),
		memoryGB,
		env,
	)
}

// GPTQOutputSubdir returns the output directory name for GPTQ artifacts.
func GPTQOutputSubdir(modelPath string, bits, groupSize int) string {
	outSubdir := fmt.Sprintf("gptq-w%d-g%d", bits, groupSize)
	if strings.Contains(strings.ToLower(modelPath), "gemma4-26b-a4b") {
		// Version this experimental hybrid path so managed rebuilds cannot
		// overwrite the currently serving clean hybrid artifact in-place.
		return outSubdir + "-hybrid-v12"
	}
	return outSubdir
}

func (b *GPTQJobBuilder) buildDenseModuleValidationEnv(spec *aiv1alpha2.QuantizationSpec) []corev1.EnvVar {
	var env []corev1.EnvVar
	if spec.DenseModulePolicy != nil && *spec.DenseModulePolicy != "" {
		env = append(env,
			corev1.EnvVar{Name: "DENSE_GPTQ_POLICY", Value: *spec.DenseModulePolicy},
			corev1.EnvVar{Name: "GEMMA4_DENSE_GPTQ_POLICY", Value: *spec.DenseModulePolicy},
		)
	}
	if spec.DenseModuleCosineThreshold != nil && *spec.DenseModuleCosineThreshold != "" {
		env = append(env,
			corev1.EnvVar{Name: "DENSE_GPTQ_COSINE_THRESHOLD", Value: *spec.DenseModuleCosineThreshold},
			corev1.EnvVar{Name: "GEMMA4_DENSE_GPTQ_COSINE_THRESHOLD", Value: *spec.DenseModuleCosineThreshold},
		)
	}
	return env
}

// buildEnv returns environment variables for the GPTQ quantization script.
func (b *GPTQJobBuilder) buildEnv(modelPath, outSubdir string, bits, groupSize int, sym, descAct bool, memoryGB int32, gpuMemFraction, dynamicExclusion, gpuArch string, gpuVramMB int64, calib *aiv1alpha2.CalibrationSpec) []corev1.EnvVar {
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
	// Floor mode "mean" scales diagonal floor by mean(|diag|), which keeps every
	// attempt numerically meaningful relative to damp*mean. Legacy "abs_max"
	// scaled by max|diag| and was dominated by damp for typical activations.
	hessianDiagFloorMode := getenvDefault("FLEXINFER_GPTQ_HESSIAN_DIAG_FLOOR_MODE", "mean")
	defaultFloorScale := "0.01"
	if hessianDiagFloorMode == "abs_max" {
		defaultFloorScale = "1e-6"
	}
	hessianDiagFloorScale := getenvDefault("FLEXINFER_GPTQ_HESSIAN_DIAG_FLOOR_SCALE", defaultFloorScale)
	hessianFloorMultiplier := getenvDefault("FLEXINFER_GPTQ_HESSIAN_FLOOR_MULTIPLIER", "10")
	hessianMaxFloorAttempts := getenvDefault("FLEXINFER_GPTQ_HESSIAN_MAX_FLOOR_ATTEMPTS", "6")
	hessianClampAbs := getenvDefault("FLEXINFER_GPTQ_HESSIAN_CLAMP_ABS", "0")
	dampPercentOverride := os.Getenv("FLEXINFER_GPTQ_DAMP_PERCENT_OVERRIDE")
	// damp_step=0.1 (vs GPTQModel default 0.0015) cuts the initial damp sweep
	// from ~95 Cholesky iterations to ~10. On slow CPU backends (gfx906 with
	// ROCm LAPACK fallback), one Cholesky on a 21504² FP32 matrix is ~40s,
	// turning a ~60min sweep into ~7min.
	dampAutoIncrementOverride := getenvDefault("FLEXINFER_GPTQ_DAMP_AUTO_INCREMENT_OVERRIDE", "0.1")
	resumeEnabled := getenvDefault("FLEXINFER_GPTQ_RESUME", "true")
	calibrationCacheEnabled := getenvDefault("FLEXINFER_GPTQ_CALIBRATION_CACHE", "true")
	// Per-layer quantized-state persistence. Phase A ships the writer only
	// (safe, observable, low risk). Phase B will wire the reload+skip path
	// once we've verified the writer's artifacts against a live run.
	resumeLayersEnabled := getenvDefault("FLEXINFER_GPTQ_RESUME_LAYERS", "false")
	deviceMap := getenvDefault("FLEXINFER_GPTQ_DEVICE_MAP", "auto")
	// GPU path uses init_empty_weights + infer_auto_device_map +
	// load_checkpoint_in_model, which correctly materializes tensors on the
	// target device before GPTQModel sees them. Accelerate splits layers
	// between GPU and CPU RAM based on available memory. Per-layer
	// quantization moves each layer to GPU individually.
	// Override with FLEXINFER_GPTQ_DEVICE_MAP=cpu to force CPU-only loading.

	env := []corev1.EnvVar{
		{Name: "MODEL_DIR", Value: fmt.Sprintf("/cache/%s", modelPath)},
		{Name: "OUT_DIR", Value: fmt.Sprintf("/cache/%s/%s", modelPath, outSubdir)},
		{Name: "BITS", Value: fmt.Sprintf("%d", bits)},
		{Name: "GROUP_SIZE", Value: fmt.Sprintf("%d", groupSize)},
		{Name: "MAX_MEMORY_GB", Value: fmt.Sprintf("%d", memoryGB)},
		{Name: "GPU_VRAM_MB", Value: fmt.Sprintf("%d", gpuVramMB)},
		{Name: "SYM", Value: symStr},
		{Name: "DESC_ACT", Value: descActStr},
		{Name: "GPU_MEMORY_FRACTION", Value: gpuMemFraction},
		{Name: "DYNAMIC_EXCLUSION", Value: dynamicExclusion},
		{Name: "QUANTIZE_MODEL_POLICIES", Value: modelPolicies},
		{Name: "GPTQ_HESSIAN_REPAIR", Value: hessianRepair},
		{Name: "GPTQ_HESSIAN_SANITIZE_NONFINITE", Value: hessianSanitizeNonfinite},
		{Name: "GPTQ_HESSIAN_DIAG_FLOOR_MODE", Value: hessianDiagFloorMode},
		{Name: "GPTQ_HESSIAN_DIAG_FLOOR_SCALE", Value: hessianDiagFloorScale},
		{Name: "GPTQ_HESSIAN_FLOOR_MULTIPLIER", Value: hessianFloorMultiplier},
		{Name: "GPTQ_HESSIAN_MAX_FLOOR_ATTEMPTS", Value: hessianMaxFloorAttempts},
		{Name: "GPTQ_HESSIAN_CLAMP_ABS", Value: hessianClampAbs},
		{Name: "GPTQ_DAMP_PERCENT_OVERRIDE", Value: dampPercentOverride},
		{Name: "GPTQ_DAMP_AUTO_INCREMENT_OVERRIDE", Value: dampAutoIncrementOverride},
		{Name: "GPTQ_RESUME", Value: resumeEnabled},
		{Name: "GPTQ_RESUME_LAYERS", Value: resumeLayersEnabled},
		{Name: "GPTQ_CALIBRATION_CACHE", Value: calibrationCacheEnabled},
		{Name: "QUANTIZE_DEVICE_MAP", Value: deviceMap},
		{Name: "FLEXINFER_TELEMETRY", Value: "true"},
	}
	env = append(env, BuildCalibrationEnv(calib)...)
	return env
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
			QuantizeConfigOverride: map[string]any{
				"offload_to_disk": false,
			},
			CalibrationOverrides: map[string]int{
				"max_samples": 16,
				"max_seq_len": 512,
				"max_tokens":  8192,
			},
			RuntimeOverrides: map[string]any{
				"attn_implementation": "eager",
				"disable_qwen35_fla":  true,
				"fix_mistral_regex":   true,
			},
		},
		{
			Name:                "gemma4-text",
			MatchModelTypes:     []string{"gemma4_text"},
			MatchPathSubstrings: []string{"gemma4", "gemma-4"},
			ExtractTextConfig:   true,
			CopyRootKeys:        []string{"bos_token_id", "eos_token_id", "pad_token_id"},
			RemapModelType:      "gemma4_text",
			Architectures:       []string{"Gemma4ForCausalLM"},
			Loader:              "gptqmodel",
			PythonPackages: []string{
				"git+https://github.com/huggingface/transformers.git@f965b10b",
			},
			QuantizeConfigOverride: map[string]any{
				// Enable disk offload for full MoE quantization — 26B model
				// + expert Hessians may exceed GPU memory without offloading.
				"offload_to_disk": true,
			},
			CalibrationOverrides: map[string]int{
				// Increase samples for better MoE expert coverage (128 experts × top-8).
				"max_samples": 512,
				"max_seq_len": 2048,
				"max_tokens":  524288,
			},
			RuntimeOverrides: map[string]any{
				"attn_implementation": "eager",
			},
			ArtifactOverrides: map[string]any{
				// Preserve an HF-native checkpoint alongside the fused vLLM artifact
				// so Gemma4 outputs can be validated without re-running quantization.
				"preserve_native_output":    true,
				"refuse_moe_expert_tensors": true,
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

# Seed import-safe version sentinels into the emptyDir-backed workspace before
# checking whether the quantizer image is new enough for controller-side patches.
mkdir -p /workspace
if [ ! -f /workspace/quantize_gptq.py ]; then
    printf 'FLEXINFER_SCRIPT_VERSION = "` + GPTQScriptVersion + `"\n' > /workspace/quantize_gptq.py
fi
if [ ! -f /workspace/abliterate.py ]; then
    printf 'FLEXINFER_SCRIPT_VERSION = "v1"\n' > /workspace/abliterate.py
fi

# --- Script version check ---
# Catch stale quantizer images that are missing controller-side patches.
EXPECTED_VERSION="` + GPTQScriptVersion + `"
ACTUAL_VERSION=$(python3 -c "
import sys, importlib.util
spec = importlib.util.spec_from_file_location('q', '/workspace/quantize_gptq.py')
if spec is None:
    spec = importlib.util.spec_from_file_location('q', '/app/quantize_gptq.py')
if spec is None:
    for p in sys.path:
        import os
        f = os.path.join(p, 'quantize_gptq.py')
        if os.path.isfile(f):
            spec = importlib.util.spec_from_file_location('q', f)
            break
if spec is None:
    print('UNKNOWN')
else:
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    print(getattr(m, 'FLEXINFER_SCRIPT_VERSION', 'UNKNOWN'))
" 2>/dev/null || echo "UNKNOWN")
if [ "${ACTUAL_VERSION}" != "${EXPECTED_VERSION}" ]; then
    echo "FATAL: Script version mismatch. Image has ${ACTUAL_VERSION}, controller expects ${EXPECTED_VERSION}. Rebuild quantizer image."
    echo "FATAL: Script version mismatch (image=${ACTUAL_VERSION} controller=${EXPECTED_VERSION})" > /dev/termination-log
    exit 1
fi

TYPE="W${BITS}_G${GROUP_SIZE}"
START_TS=$(date +%s)
LOGFILE=/tmp/quantize-output.log
# Persist full log to PVC for post-mortem analysis (survives pod GC)
PVC_LOGDIR="${MODEL_DIR}/.flexinfer-gptq-cache"
PVC_LOGFILE="${PVC_LOGDIR}/quantize-$(date +%Y%m%d-%H%M%S).log"

# Structured JSON event emitter for Loki/OTEL queryability.
# Events go to stdout (Promtail → Loki) and are queryable via:
#   {namespace="flexinfer-system"} | json | event="quantization_start"
emit_event() {
    local event="$1"; shift
    local ts
    ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    local json="{\"ts\":\"${ts}\",\"component\":\"gptq-quantizer\",\"event\":\"${event}\""
    while [ $# -ge 2 ]; do
        local key="$1" val="$2"; shift 2
        # Quote numeric values without quotes, strings with quotes
        case "${val}" in
            ''|*[!0-9.]*) json="${json},\"${key}\":\"${val}\"" ;;
            *)            json="${json},\"${key}\":${val}" ;;
        esac
    done
    json="${json}}"
    echo "${json}"
}

cleanup() {
    local ec=$?
    # Persist log to PVC before anything else
    if [ -f "${LOGFILE}" ]; then
        mkdir -p "${PVC_LOGDIR}"
        cp "${LOGFILE}" "${PVC_LOGFILE}" 2>/dev/null || true
        # Keep only last 3 log files to avoid PVC bloat
        ls -t "${PVC_LOGDIR}"/quantize-*.log 2>/dev/null | tail -n +4 | xargs rm -f 2>/dev/null || true
    fi
    if [ $ec -ne 0 ]; then
        emit_event "quantization_error" "exit_code" "${ec}" "model" "${MODEL_DIR}" "type" "${TYPE}"
        # Write last 80 lines to termination-log so controller can capture the error
        {
            echo "exit_code=${ec}"
            echo "---"
            tail -80 "${LOGFILE}" 2>/dev/null || echo "(no log output captured)"
        } > /dev/termination-log 2>/dev/null || true
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

# Tee all output so cleanup can capture tail on failure
exec > >(tee -a "${LOGFILE}") 2>&1

# Patch GPTQModel writer.py to guard against ZeroDivisionError.
WRITER_PY=$(python3 -c "import importlib.util,os; s=importlib.util.find_spec('gptqmodel'); print(os.path.join(os.path.dirname(s.origin),'models','writer.py'))" 2>/dev/null || true)
if [ -n "${WRITER_PY}" ] && grep -q "pre_quantized_size_mb) \* 100" "${WRITER_PY}" 2>/dev/null; then
    sed -i 's|percent_diff = (size_diff_mb / pre_quantized_size_mb) \* 100|percent_diff = (size_diff_mb / pre_quantized_size_mb) * 100 if pre_quantized_size_mb > 0 else 0.0|' "${WRITER_PY}"
    echo "Patched GPTQModel writer.py for ZeroDivisionError"
fi

# GPTQModel's direct CPU path injects device_map=cpu_device_map. In transformers,
# any device_map enables meta-device loading/dispatch, which crashes GPTQModel's
# shell_module_materialize. Strip device_map and enable low_cpu_mem_usage=True so
# transformers loads shards incrementally (peak RSS ~ model_size + one shard).
# GPTQModel's quantize() handles GPU placement internally — it moves layers to
# GPU one at a time via get_best_device() for calibration forward passes.
LOADER_PY=$(python3 -c "import importlib.util,os; s=importlib.util.find_spec('gptqmodel'); print(os.path.join(os.path.dirname(s.origin),'models','loader.py'))" 2>/dev/null || true)
if [ -n "${LOADER_PY}" ] && ! grep -q 'direct_init_kwargs.pop("device_map", None)' "${LOADER_PY}" 2>/dev/null; then
    python3 - "${LOADER_PY}" <<'PY'
import sys
from pathlib import Path

path = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("/opt/venv/lib/python3.12/site-packages/gptqmodel/models/loader.py")
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
            direct_init_kwargs["low_cpu_mem_usage"] = True
            model = cls.loader.from_pretrained(model_local_path, config=config, **direct_init_kwargs)
            if getattr(model, "config", None) is config:
                model.config = copy.deepcopy(config)
            defuser.convert_model(model, cleanup_original=False)
            model._model_init_kwargs = direct_init_kwargs
            print_module_tree(model=model)

            turtle_model = None'''
if old in src:
    src = src.replace(old, new)
    path.write_text(src)
    print("Patched GPTQModel loader.py direct CPU path to disable device_map/meta loading")
else:
    print("WARN: GPTQModel loader.py direct CPU load block not found (may be a newer version); skipping patch")
PY
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
src = src.replace(
    'def load_model_manual_sharded_state_dict(model_dir, tokenizer, quantize_config):',
    'def load_state_dict_materialized(module, state_dict, strict=False):\n    """Load checkpoint shards into meta-backed modules when assign=True exists."""\n    try:\n        return module.load_state_dict(state_dict, strict=strict, assign=True)\n    except TypeError as exc:\n        if "assign" not in str(exc):\n            raise\n        print("WARN: load_state_dict(assign=True) unsupported by this runtime; retrying without assign")\n        return module.load_state_dict(state_dict, strict=strict)\n\n\ndef patch_gptq_save_meta_tensors():\n    """Skip meta-backed tensors before GPTQModel streams safetensors shards."""\n    import gptqmodel.utils.model as gptq_utils_model\n    from gptqmodel.models import writer as gptq_writer\n    if getattr(gptq_utils_model, "_flexinfer_meta_save_patch", False):\n        return\n    original_get_state_dict_for_save = gptq_utils_model.get_state_dict_for_save\n    def _patched_get_state_dict_for_save(model, offload_root=None):\n        state_dict = original_get_state_dict_for_save(model, offload_root)\n        dropped = []\n        for name, entry in list(state_dict.items()):\n            source = getattr(entry, "source", None)\n            if not isinstance(source, torch.Tensor):\n                continue\n            if getattr(source, "is_meta", False) or source.device.type == "meta":\n                dropped.append(name)\n                del state_dict[name]\n        if dropped:\n            preview = ", ".join(dropped[:8])\n            extra = len(dropped) - min(len(dropped), 8)\n            suffix = f" ... (+{extra} more)" if extra > 0 else ""\n            print("Dropped " f"{len(dropped)} meta-backed tensors from save state_dict: " f"{preview}{suffix}")\n        return state_dict\n    gptq_utils_model.get_state_dict_for_save = _patched_get_state_dict_for_save\n    gptq_writer.get_state_dict_for_save = _patched_get_state_dict_for_save\n    gptq_utils_model._flexinfer_meta_save_patch = True\n    print("Patched GPTQModel save path to skip meta-backed tensors")\n\n\ndef adapt_model_definition_for_loaded_model(model_definition, model):\n    """Align GPTQModel module roots with the instantiated HF model layout."""\n    if model is None or not hasattr(model, "model"):\n        return\n    inner_model = getattr(model, "model", None)\n    if inner_model is None or not hasattr(inner_model, "layers"):\n        return\n    module_tree = getattr(model_definition, "module_tree", None)\n    if not isinstance(module_tree, list) or module_tree[:3] != ["model", "language_model", "layers"]:\n        return\n    model_definition.module_tree = ["model", "layers", *copy.deepcopy(module_tree[3:])]\n    if getattr(model_definition, "pre_lm_head_norm_module", None) == "model.language_model.norm":\n        model_definition.pre_lm_head_norm_module = "model.norm"\n    if getattr(model_definition, "rotary_embedding", None) == "model.language_model.rotary_emb":\n        model_definition.rotary_embedding = "model.rotary_emb"\n    print("Adapted GPTQModel module tree for text-only Qwen3.5 causal LM (model.layers.*)")\n\n\ndef load_model_manual_sharded_state_dict(model_dir, tokenizer, quantize_config):',
)
if "\npatch_gptq_save_meta_tensors()\n" not in src:
    src = src.replace(
        'from transformers.modeling_utils import get_checkpoint_shard_files, load_state_dict\n',
        'from transformers.modeling_utils import get_checkpoint_shard_files, load_state_dict\n\npatch_gptq_save_meta_tensors()\n',
    )
src = src.replace(
    '    model._model_init_kwargs = init_kwargs.copy()\n    model.eval()',
    '    model._model_init_kwargs = init_kwargs.copy()\n    model.eval()\n    adapt_model_definition_for_loaded_model(model_definition, model)',
)
path.write_text(src)
PY
    echo "Patched quantize_gptq.py for Qwen3.5 direct load + text-only module tree"
fi

# Remap safetensors weight keys for VLM→text-only extraction.
# When extract_text_config extracts model.language_model config to top level,
# the text-only model class (e.g. Gemma4ForCausalLM) expects model.layers.*
# but the checkpoint has model.language_model.layers.*. Rename keys in the
# safetensors files to match the text-only model structure.
# Qwen3.5 doesn't need this (flat model.layers.* keys in checkpoint).
# Only Gemma4+ uses the model.language_model.* prefix.
# Handles both single-file (model.safetensors) and sharded models (index.json).
if [ ! -f "${MODEL_DIR}/.flexinfer-weights-remapped" ]; then
    HAS_SAFETENSORS=false
    if [ -f "${MODEL_DIR}/model.safetensors.index.json" ] || [ -f "${MODEL_DIR}/model.safetensors" ]; then
        HAS_SAFETENSORS=true
    fi
    if [ "${HAS_SAFETENSORS}" = "true" ]; then
        python3 - "${MODEL_DIR}" <<'REMAP_KEYS_PY'
import json, os, sys, time
from pathlib import Path

model_dir = Path(sys.argv[1])

# Determine shard files: either from index or single file
index_path = model_dir / "model.safetensors.index.json"
single_path = model_dir / "model.safetensors"

if index_path.exists():
    with open(index_path) as f:
        index = json.load(f)
    weight_map = index.get("weight_map", {})
    all_keys = list(weight_map.keys())
    shard_files = sorted(set(weight_map.values()))
else:
    # Single safetensors file — read keys directly
    from safetensors import safe_open
    with safe_open(str(single_path), framework="pt") as f:
        all_keys = list(f.keys())
    shard_files = ["model.safetensors"]
    index = None

# Check if any keys use the model.language_model.* prefix
lm_keys = [k for k in all_keys if k.startswith("model.language_model.")]
if not lm_keys:
    print("Weight keys already use flat model.* prefix, no remapping needed")
    (model_dir / ".flexinfer-weights-remapped").write_text("skipped\n")
    sys.exit(0)

print(f"Found {len(lm_keys)} keys with model.language_model.* prefix, remapping...")

# Prefixes to strip (multimodal towers not needed for text-only quantization)
SKIP_PREFIXES = ("model.audio_tower.", "model.vision_tower.", "model.multi_modal_projector.")

from safetensors import safe_open
from safetensors.torch import save_file

total_remapped = 0
total_dropped = 0

for shard_name in shard_files:
    shard_path = model_dir / shard_name
    if not shard_path.exists():
        print(f"  WARN: shard {shard_name} not found, skipping")
        continue

    t0 = time.time()
    tensors = {}
    metadata = {}
    remapped = 0
    dropped = 0

    with safe_open(str(shard_path), framework="pt") as f:
        meta = f.metadata()
        if meta:
            metadata = dict(meta)
        for key in f.keys():
            if any(key.startswith(p) for p in SKIP_PREFIXES):
                dropped += 1
                continue
            new_key = key
            if key.startswith("model.language_model."):
                new_key = "model." + key[len("model.language_model."):]
                remapped += 1
            tensors[new_key] = f.get_tensor(key)

    # Write back with renamed keys (atomic via temp + rename)
    tmp_path = str(shard_path) + ".tmp"
    save_file(tensors, tmp_path, metadata=metadata if metadata else None)
    os.replace(tmp_path, str(shard_path))
    elapsed = time.time() - t0
    sz_gb = shard_path.stat().st_size / (1024**3)
    print(f"  {shard_name}: {len(tensors)} tensors, {remapped} remapped, {dropped} dropped, {sz_gb:.1f}GB, {elapsed:.1f}s")
    total_remapped += remapped
    total_dropped += dropped
    del tensors  # free memory

# Update the index file if it exists
if index is not None:
    new_weight_map = {}
    for key, shard in index["weight_map"].items():
        if any(key.startswith(p) for p in SKIP_PREFIXES):
            continue
        if key.startswith("model.language_model."):
            new_key = "model." + key[len("model.language_model."):]
            new_weight_map[new_key] = shard
        else:
            new_weight_map[key] = shard
    index["weight_map"] = new_weight_map
    with open(index_path, "w") as f:
        json.dump(index, f, indent=2)

# Write marker so we don't re-remap on retries
(model_dir / ".flexinfer-weights-remapped").write_text(
    f"remapped={total_remapped} dropped={total_dropped}\n"
)
print(f"Safetensors weight key remapping complete: {total_remapped} renamed, {total_dropped} dropped")
REMAP_KEYS_PY
    fi
fi

# Inject conditional model loading into quantize_gptq.py.
# Replaces from_config + shard loading + dispatch in a SINGLE combined replacement.
# GPU path: init_empty_weights creates meta skeleton (0 bytes), then
# load_checkpoint_in_model materializes weights on target devices WITHOUT adding
# accelerate dispatch hooks (which conflict with GPTQModel's shell_module_materialize).
# CPU path: from_config creates real CPU tensors directly (peak RSS = model size),
# then shard loading replaces parameter data. This avoids meta tensors entirely,
# which prevents GPTQModel shell_module_materialize crash ("Cannot copy out of
# meta tensor"). CPU nodes (128GB+) have enough RAM for the direct allocation.
if [ -f "${GPTQ_SCRIPT}" ]; then
    python3 - <<'DEVICE_MAP_PY'
import os, re, sys
from pathlib import Path

path = Path(os.environ.get("GPTQ_SCRIPT", "/opt/flexinfer/scripts/quantize_gptq.py"))
src = path.read_text()
src = src.replace(
    'def load_state_dict_materialized(module, state_dict, *, strict=False):\n    """Load checkpoint shards into meta-backed modules when assign=True exists."""\n\n    load_kwargs = {"strict": strict}\n    try:\n        if "assign" in inspect.signature(module.load_state_dict).parameters:\n            load_kwargs["assign"] = True\n    except (TypeError, ValueError):\n        pass\n\n    try:\n        return module.load_state_dict(state_dict, **load_kwargs)\n    except TypeError as exc:\n        if "assign" not in load_kwargs or "assign" not in str(exc):\n            raise\n        print(\n            "WARN: load_state_dict(assign=True) unsupported by this runtime; "\n            "retrying without assign"\n        )\n        load_kwargs.pop("assign", None)\n        return module.load_state_dict(state_dict, **load_kwargs)\n',
    'def load_state_dict_materialized(module, state_dict, strict=False):\n    """Load checkpoint shards into meta-backed modules when assign=True exists."""\n    try:\n        return module.load_state_dict(state_dict, strict=strict, assign=True)\n    except TypeError as exc:\n        if "assign" not in str(exc):\n            raise\n        print("WARN: load_state_dict(assign=True) unsupported by this runtime; retrying without assign")\n        return module.load_state_dict(state_dict, strict=strict)\n',
)
if "Patched GPTQModel save path to skip meta-backed tensors" not in src:
    src = src.replace(
        'def adapt_model_definition_for_loaded_model(model_definition, model):',
        'def patch_gptq_save_meta_tensors():\n    """Skip meta-backed tensors before GPTQModel streams safetensors shards."""\n    import gptqmodel.utils.model as gptq_utils_model\n    from gptqmodel.models import writer as gptq_writer\n    if getattr(gptq_utils_model, "_flexinfer_meta_save_patch", False):\n        return\n    original_get_state_dict_for_save = gptq_utils_model.get_state_dict_for_save\n    def _patched_get_state_dict_for_save(model, offload_root=None):\n        state_dict = original_get_state_dict_for_save(model, offload_root)\n        dropped = []\n        for name, entry in list(state_dict.items()):\n            source = getattr(entry, "source", None)\n            if not isinstance(source, torch.Tensor):\n                continue\n            if getattr(source, "is_meta", False) or source.device.type == "meta":\n                dropped.append(name)\n                del state_dict[name]\n        if dropped:\n            preview = ", ".join(dropped[:8])\n            extra = len(dropped) - min(len(dropped), 8)\n            suffix = f" ... (+{extra} more)" if extra > 0 else ""\n            print("Dropped " f"{len(dropped)} meta-backed tensors from save state_dict: " f"{preview}{suffix}")\n        return state_dict\n    gptq_utils_model.get_state_dict_for_save = _patched_get_state_dict_for_save\n    gptq_writer.get_state_dict_for_save = _patched_get_state_dict_for_save\n    gptq_utils_model._flexinfer_meta_save_patch = True\n    print("Patched GPTQModel save path to skip meta-backed tensors")\n\n\ndef adapt_model_definition_for_loaded_model(model_definition, model):',
    )
if "\npatch_gptq_save_meta_tensors()\n" not in src:
    src = src.replace(
        'from transformers.modeling_utils import get_checkpoint_shard_files, load_state_dict\n',
        'from transformers.modeling_utils import get_checkpoint_shard_files, load_state_dict\n\npatch_gptq_save_meta_tensors()\n',
    )

# Add env var read near top of script (after imports)
if "quantize_device_map" not in src:
    marker = "gpu_memory_fraction = "
    if marker in src:
        idx = src.index(marker)
        end_line = src.index("\n", idx) + 1
        inject = 'quantize_device_map = os.environ.get("QUANTIZE_DEVICE_MAP", "cpu")\n'
        src = src[:end_line] + inject + src[end_line:]
    else:
        src = 'import os as _os_dm\nquantize_device_map = _os_dm.environ.get("QUANTIZE_DEVICE_MAP", "cpu")\n' + src

# Combined replacement: from_config through model.eval()
# Uses regex to find the active loader call robustly, then replaces everything
# through model.eval() with init_empty_weights + load_checkpoint_in_model while
# preserving whichever loader class the bundled script selected.
fc_pattern = re.compile(r'^([ \t]+)model = ([A-Za-z_][A-Za-z0-9_\.]*)\.from_config\(config, \*\*init_kwargs\)', re.MULTILINE)
fc_match = fc_pattern.search(src)
eval_marker = '    model.eval()'
eval_found = eval_marker in src

if fc_match and eval_found:
    indent = fc_match.group(1)
    loader_expr = fc_match.group(2)
    start_idx = fc_match.start()
    end_idx = src.index(eval_marker, fc_match.end()) + len(eval_marker)
    replacement = (
        f'{indent}# --- Injected by controller: conditional meta-device loading ---\n'
        f'{indent}if quantize_device_map and quantize_device_map != "cpu":\n'
        f'{indent}    from accelerate import init_empty_weights, infer_auto_device_map, load_checkpoint_in_model\n'
        f'{indent}    from accelerate.utils import get_max_memory\n'
        f'{indent}    with init_empty_weights():\n'
        f'{indent}        model = {loader_expr}.from_config(config, **init_kwargs)\n'
        f'{indent}    print("Model skeleton created on meta device (no memory allocated)")\n'
        f'{indent}    try:\n'
        f'{indent}        max_mem = get_max_memory()\n'
        f'{indent}    except Exception:\n'
        f'{indent}        max_mem = {{}}\n'
        f'{indent}    # Fallback for gfx906: hipMemGetInfo broken, use GPU_VRAM_MB from GPUProfile\n'
        f'{indent}    _gpu_vram_mb = int(os.environ.get("GPU_VRAM_MB", "0"))\n'
        f'{indent}    if _gpu_vram_mb > 0:\n'
        f'{indent}        _has_gpu_mem = any(v > 0 for k, v in max_mem.items() if k != "cpu")\n'
        f'{indent}        if not _has_gpu_mem and torch.cuda.is_available():\n'
        f'{indent}            max_mem[0] = _gpu_vram_mb * 1024 * 1024\n'
        f'{indent}            print(f"Using GPU_VRAM_MB={{_gpu_vram_mb}}MB as GPU memory (hipMemGetInfo fallback)")\n'
        f'{indent}    for dev_id in list(max_mem.keys()):\n'
        f'{indent}        if dev_id != "cpu":\n'
        f'{indent}            max_mem[dev_id] = int(max_mem[dev_id] * gpu_memory_fraction)\n'
        f'{indent}    device_map = infer_auto_device_map(model, max_memory=max_mem)\n'
        f'{indent}    gpu_layers = sum(1 for v in device_map.values() if v != "cpu")\n'
        f'{indent}    cpu_layers = sum(1 for v in device_map.values() if v == "cpu")\n'
        f'{indent}    print(f"Loading with device_map: gpu_layers={{gpu_layers}} cpu_layers={{cpu_layers}}")\n'
        f'{indent}    load_checkpoint_in_model(\n'
        f'{indent}        model, model_dir, device_map=device_map,\n'
        f'{indent}        dtype=dtype,\n'
        f'{indent}    )\n'
        f'{indent}    print("Model loaded via load_checkpoint_in_model (no dispatch hooks)")\n'
        f'{indent}else:\n'
        f'{indent}    # CPU path: create real tensors directly (no meta device) to avoid\n'
        f'{indent}    # GPTQModel shell_module_materialize crash on meta tensors.\n'
        f'{indent}    model = {loader_expr}.from_config(config, **init_kwargs)\n'
        f'{indent}    print(f"Model instantiated on CPU (device_map={{quantize_device_map}})")\n'
        f'{indent}    index_filename = resolve_checkpoint_index(model_dir)\n'
        f'{indent}    shard_files, shard_metadata = get_checkpoint_shard_files(\n'
        f'{indent}        model_dir, index_filename, local_files_only=True,\n'
        f'{indent}    )\n'
        f'{indent}    print(f"Loading {{len(shard_files)}} checkpoint shards (CPU-only)")\n'
        f'{indent}    for idx, shard_file in enumerate(shard_files, start=1):\n'
        f'{indent}        emit_progress("progress", phase="quantizing",\n'
        f'{indent}            percent=min(4.5, 1.0 + (idx / max(len(shard_files), 1)) * 3.0),\n'
        f'{indent}            detail=f"loading shard {{idx}}/{{len(shard_files)}}")\n'
        f'{indent}        state_dict = load_state_dict(shard_file, map_location="cpu")\n'
        f'{indent}        load_state_dict_materialized(model, state_dict, strict=False)\n'
        f'{indent}        del state_dict\n'
        f'{indent}        gc.collect()\n'
        f'{indent}model.eval()'
    )
    src = src[:start_idx] + replacement + src[end_idx:]
    # Remove any old post-load dispatch blocks
    src = re.sub(
        r'\n    # Injected by controller.*?print\(f"WARN: device_map dispatch.*?"\)\n',
        '\n', src, flags=re.DOTALL
    )
    src = re.sub(
        r'\n    # Dispatch model across devices.*?print\(f"WARN: device_map dispatch.*?"\)\n',
        '\n', src, flags=re.DOTALL
    )
    print("Injected init_empty_weights + load_checkpoint_in_model (no dispatch hooks)")
else:
    print(f"WARN: could not find markers for combined replacement", file=sys.stderr)
    print(f"  from_config match: {fc_match is not None} (pattern: {fc_pattern.pattern})", file=sys.stderr)
    print(f"  model.eval() found: {eval_found}", file=sys.stderr)
    if not fc_match:
        # Dump lines containing from_config for debugging
        for i, line in enumerate(src.split('\n'), 1):
            if 'from_config' in line:
                print(f"  line {i}: {repr(line)}", file=sys.stderr)
    sys.exit(1)

path.write_text(src)
DEVICE_MAP_PY
fi

# Patch dataset loading to handle "name:config" format (e.g. wikitext:wikitext-2-raw-v1).
# HuggingFace load_dataset needs separate (name, config) args for datasets with configs.
if [ -f "${GPTQ_SCRIPT}" ] && grep -q 'dataset_name = os.environ.get("DATASET"' "${GPTQ_SCRIPT}" 2>/dev/null; then
    python3 - <<'DATASET_PY'
from pathlib import Path

p = Path("/opt/flexinfer/scripts/quantize_gptq.py")
src = p.read_text()
src = src.replace(
    'dataset_name = os.environ.get("DATASET", "mit-han-lab/pile-val-backup")',
    'dataset_raw = os.environ.get("DATASET", "mit-han-lab/pile-val-backup")\n'
    '# Support "name:config" format (e.g. "wikitext:wikitext-2-raw-v1")\n'
    'if ":" in dataset_raw:\n'
    '    dataset_name, dataset_config = dataset_raw.split(":", 1)\n'
    'else:\n'
    '    dataset_name, dataset_config = dataset_raw, None',
)
src = src.replace(
    '    dataset = load_dataset(dataset_name, split="validation")',
    '    ds_args = [dataset_name]\n'
    '    if dataset_config:\n'
    '        ds_args.append(dataset_config)\n'
    '    dataset = load_dataset(*ds_args, split="validation")',
)
src = src.replace('"dataset": dataset_name,', '"dataset": dataset_raw,')
# Fix empty-sample handling: iterate over all entries, skip blanks, collect up to max_samples.
# Original code used dataset.select(range(max_samples)) which breaks on datasets with empty rows.
src = src.replace(
    '    for sample in dataset.select(range(min(effective_max_samples, len(dataset)))):\n'
    '        tok = tokenizer(\n'
    '            sample["text"],',
    '    for sample in dataset:\n'
    '        if len(examples) >= effective_max_samples or total_tokens >= effective_max_tokens:\n'
    '            break\n'
    '        text = sample.get("text", "")\n'
    '        if not text.strip():\n'
    '            continue\n'
    '        tok = tokenizer(\n'
    '            text,',
)
src = src.replace(
    '        if sample_tokens <= 0:\n'
    '            break\n',
    '        if sample_tokens <= 1:\n'
    '            continue\n',
)
# Remove the trailing total_tokens break (now handled at loop top)
src = src.replace(
    '        examples.append(tok)\n'
    '        total_tokens += sample_tokens\n'
    '        if total_tokens >= effective_max_tokens:\n'
    '            break',
    '        examples.append(tok)\n'
    '        total_tokens += sample_tokens',
)
p.write_text(src)
print("Patched dataset loading for name:config format + empty sample handling")
DATASET_PY
fi

# Gemma4 GPTQModel compat patches: inject monkey-patches into quantize_gptq.py
# right before model.quantize(). Fixes two issues:
# 1. PLE: module_looper doesn't pass per_layer_input -> TypeError on multiply
# 2. RoPE: module_looper replays position_embeddings from sliding_attention (256-dim)
#    for full_attention layers (512-dim) -> shape mismatch in apply_rotary_pos_emb
if [ -f "${GPTQ_SCRIPT}" ] && grep -q 'model.quantize(examples)' "${GPTQ_SCRIPT}" 2>/dev/null; then
    python3 - <<'GEMMA4_COMPAT_PY'
from pathlib import Path

p = Path("/opt/flexinfer/scripts/quantize_gptq.py")
src = p.read_text()
if "_gemma4_safe_fwd" not in src:
    gemma4_patch = '''# Gemma4/GPTQModel compat: patch decoder layer forward for two issues:
# 1. PLE (per_layer_input): module_looper doesn't capture per_layer_input kwarg
#    computed by parent Gemma4TextModel. Guard: disable PLE when input is None.
# 2. RoPE (position_embeddings): heterogeneous head_dim (256 sliding vs 512 full)
#    means position_embeddings captured from sliding layers have wrong dimensions
#    for full_attention layers. Fix: recompute via stored rotary_emb reference.
import functools as _ft
import torch as _torch

_g4_mod = __import__('sys').modules.get('transformers.models.gemma4.modeling_gemma4')
if _g4_mod is None:
    try:
        import importlib
        _g4_mod = importlib.import_module('transformers.models.gemma4.modeling_gemma4')
    except (ImportError, ModuleNotFoundError):
        _g4_mod = None

_g4_cls = getattr(_g4_mod, 'Gemma4TextDecoderLayer', None) or getattr(_g4_mod, 'Gemma4DecoderLayer', None)
if _g4_cls is not None:
    # Store rotary_emb and layer_type refs on each decoder layer for RoPE recomputation.
    # Model nesting: GPTQModel -> HF CausalLM -> TextModel (has rotary_emb + layers).
    _rotary_emb = None
    # layer_types may be on top-level config or nested in text_config
    _layer_types = getattr(model.config, 'layer_types', None)
    if _layer_types is None:
        _text_cfg = getattr(model.config, 'text_config', None)
        if _text_cfg is not None:
            _layer_types = getattr(_text_cfg, 'layer_types', None)
    _layers = None
    # Traverse common nesting patterns to find rotary_emb
    for _candidate in [
        getattr(getattr(model, 'model', None), 'model', None),  # GPTQModel.model.model (Gemma4TextModel)
        getattr(model, 'model', None),                           # GPTQModel.model (if flat)
        getattr(getattr(getattr(model, 'model', None), 'language_model', None), 'model', None),  # VLM path
    ]:
        if _candidate is not None and hasattr(_candidate, 'rotary_emb') and hasattr(_candidate, 'layers'):
            _rotary_emb = _candidate.rotary_emb
            _layers = _candidate.layers
            print(f"Found rotary_emb at {type(_candidate).__name__} ({len(_layers)} layers)")
            break

    _need_rope_fix = False
    if _rotary_emb is not None and _layer_types is not None and _layers is not None:
        _unique_types = set(_layer_types)
        if len(_unique_types) > 1:
            _need_rope_fix = True
            for _i, _lyr in enumerate(_layers):
                _lyr._g4_rotary_emb = _rotary_emb
                _lyr._g4_layer_type = _layer_types[_i]
            print(f"Stored rotary_emb refs on {len(_layers)} layers (types: {_unique_types})")

    _g4_rope_logged = set()  # track which layers we logged recomputation for
    _g4_orig = _g4_cls.forward
    @_ft.wraps(_g4_orig)
    def _gemma4_safe_fwd(self, *args, per_layer_input=None, **kwargs):
        # --- RoPE dimension fix ---
        _pe = kwargs.get('position_embeddings')
        if _pe is not None and hasattr(self, '_g4_rotary_emb'):
            _cos, _sin = _pe
            _expected = getattr(self.self_attn, 'head_dim', _cos.shape[-1])
            if _cos.shape[-1] != _expected:
                _pid = kwargs.get('position_ids')
                if _pid is None:
                    _sl = _cos.shape[-2] if _cos.dim() >= 2 else _cos.shape[0]
                    _pid = _torch.arange(_sl, device=_cos.device).unsqueeze(0)
                _new_cos, _new_sin = self._g4_rotary_emb(_cos, _pid, layer_type=self._g4_layer_type)
                kwargs['position_embeddings'] = (_new_cos, _new_sin)
                _lidx = getattr(self, 'layer_idx', '?')
                if _lidx not in _g4_rope_logged:
                    _g4_rope_logged.add(_lidx)
                    print(f"Recomputed position_embeddings for layer {_lidx} ({self._g4_layer_type}): {_cos.shape[-1]} -> {_new_cos.shape[-1]}")

        # --- PLE per_layer_input guard ---
        if per_layer_input is None and getattr(self, 'hidden_size_per_layer_input', 0) > 0:
            _saved = self.hidden_size_per_layer_input
            self.hidden_size_per_layer_input = 0
            try:
                return _g4_orig(self, *args, per_layer_input=None, **kwargs)
            finally:
                self.hidden_size_per_layer_input = _saved
        return _g4_orig(self, *args, per_layer_input=per_layer_input, **kwargs)

    _g4_cls.forward = _gemma4_safe_fwd
    _fixes = ["PLE guard"]
    if _need_rope_fix:
        _fixes.append("RoPE recompute")
    print(f"Patched {_g4_cls.__name__}.forward ({', '.join(_fixes)})")
else:
    print("INFO: Gemma4 decoder layer class not found, compat patches skipped (non-Gemma4 model)")
'''
    src = src.replace(
        'model.quantize(examples)',
        gemma4_patch + 'model.quantize(examples)',
    )
    p.write_text(src)
    print("Injected Gemma4 compat patches before model.quantize()")
else:
    print("Gemma4 compat patches already present in quantize_gptq.py")
GEMMA4_COMPAT_PY
fi

# MoE module_tree patch is now baked into quantize_gptq.py (after emit_progress
# "model loaded"). No longer needs runtime injection — the _has_defused_experts
# block detects defused experts and patches module_tree before model.quantize().

# Inject safetensors integrity validation into quantize_gptq.py after save.
# GPTQModel can silently truncate large unquantized tensors (e.g. PLE embedding
# tables) during sharded save over NFS, producing files that pass existence checks
# but fail at load time with "incomplete metadata, file not fully covered".
if [ -f "${GPTQ_SCRIPT}" ] && ! grep -q "Safetensors integrity check" "${GPTQ_SCRIPT}" 2>/dev/null; then
    python3 - <<'SAFETENSORS_VALIDATE_PY'
from pathlib import Path

p = Path("/opt/flexinfer/scripts/quantize_gptq.py")
src = p.read_text()
# Find the simple validation block and enhance it with size verification
old_block = """if not shard_files or not has_config:
    raise RuntimeError(
        f"Save validation failed: shards={len(shard_files)} config={has_config}"
    )"""
if old_block in src:
    new_block = old_block + """

# Safetensors integrity check: verify each shard has enough data for its tensors.
# fsync each file first to flush NFS write buffers to the server.
import struct as _struct

for shard_name in shard_files:
    shard_path = os.path.join(save_tmp, shard_name)
    with open(shard_path, 'rb') as _fsync_f:
        os.fsync(_fsync_f.fileno())
    fsize = os.path.getsize(shard_path)
    with open(shard_path, "rb") as sf:
        hdr_size = _struct.unpack("<Q", sf.read(8))[0]
        hdr = json.loads(sf.read(hdr_size))
    data_start = 8 + hdr_size
    data_available = fsize - data_start
    max_end = 0
    for tname, tmeta in hdr.items():
        if tname == "__metadata__":
            continue
        offsets = tmeta.get("data_offsets") or tmeta.get("offsets")
        if offsets and offsets[1] > max_end:
            max_end = offsets[1]
    if max_end == 0:
        dtype_sizes = {"F16": 2, "BF16": 2, "F32": 4, "I32": 4, "I8": 1, "U8": 1, "F64": 8, "I64": 8, "I16": 2}
        expected = 0
        for tname, tmeta in hdr.items():
            if tname == "__metadata__":
                continue
            dt = tmeta.get("dtype", "F32")
            shape = tmeta.get("shape", [])
            elem_size = dtype_sizes.get(dt, 4)
            tensor_bytes = elem_size
            for dim in shape:
                tensor_bytes *= dim
            expected += tensor_bytes
        max_end = expected
    if data_available < max_end:
        raise RuntimeError(
            f"Safetensors integrity check failed for {shard_name}: "
            f"data_section={data_available} bytes but tensors need {max_end} bytes "
            f"(missing {max_end - data_available} bytes). File is truncated."
        )
    print(f"Verified {shard_name}: {fsize} bytes, {len([k for k in hdr if k != '__metadata__'])} tensors OK")"""
    src = src.replace(old_block, new_block)
    p.write_text(src)
    print("Injected safetensors integrity validation after save")
else:
    print("INFO: Safetensors validation already present or simple validation block not found")
SAFETENSORS_VALIDATE_PY
fi

# Gemma4 full-attention layers don't have v_proj (K and V are fused into k_proj).
# GPTQModel's Llama-based module_tree expects v_proj in every layer and crashes:
#   ValueError: layer module item 'self_attn.v_proj' not found in model
# Patch module_looper.py ON DISK so the fix persists into the quantization process.
ML_PY=$(python3 -c "import importlib.util,os; s=importlib.util.find_spec('gptqmodel'); print(os.path.join(os.path.dirname(s.origin),'looper','module_looper.py'))" 2>/dev/null || true)
if [ -n "${ML_PY}" ] && grep -q "not found in model, please check" "${ML_PY}" 2>/dev/null && ! grep -q "continue  # skip missing" "${ML_PY}" 2>/dev/null; then
    python3 - <<'SKIP_MISSING_PY'
from pathlib import Path
import importlib.util, os

spec = importlib.util.find_spec('gptqmodel')
ml_path = Path(os.path.dirname(spec.origin)) / 'looper' / 'module_looper.py'
src = ml_path.read_text()
# Find the raise ValueError line for missing modules and replace with continue
old_marker = 'raise ValueError(f"layer module item'
if old_marker in src and 'continue  # skip missing' not in src:
    lines = src.split('\n')
    for i, line in enumerate(lines):
        if old_marker in line:
            indent = '                '
            lines[i] = indent + "print(f\"WARN: skipping missing module '{n}' in layer (heterogeneous attention)\")\n" + indent + "continue  # skip missing"
            break
    ml_path.write_text('\n'.join(lines))
    print("Patched create_named_modules to skip missing modules (Gemma4 heterogeneous attention)")
else:
    print("create_named_modules already patched or marker not found")
SKIP_MISSING_PY
else
    if [ -n "${ML_PY}" ] && grep -q "continue  # skip missing" "${ML_PY}" 2>/dev/null; then
        echo "create_named_modules already patched"
    else
        echo "WARN: module_looper.py not found or unexpected structure"
    fi
fi

# Patch auto-mode dynamic exclusion to add MoE detection (Gemma4 enable_moe_block)
# and fix exclusion patterns (experts/router instead of blanket .*attn.*).
if [ -f "${GPTQ_SCRIPT}" ] && ! grep -q "enable_moe_block" "${GPTQ_SCRIPT}" 2>/dev/null; then
    python3 - <<'MOE_FIX_PY'
from pathlib import Path

p = Path("/opt/flexinfer/scripts/quantize_gptq.py")
src = p.read_text()

# Match the auto-mode block in the image's quantize_gptq.py.
# The image has only hybrid layer detection, no MoE detection.
old_block = '''    # "auto" mode — auto-detect hybrid architectures and exclude attention/expert/
    # vision/MTP modules (matches official Qwen GPTQ-Int4 approach).
    with open(cfg_path) as f:
        cfg_recheck = json.load(f)
    dynamic_config = None
    if "layer_types" in cfg_recheck:
        layer_types = cfg_recheck["layer_types"]
        unique_types = set(layer_types)
        if len(unique_types) > 1:
            print(
                f"Hybrid architecture detected: {dict((t, layer_types.count(t)) for t in unique_types)}"
            )
            dynamic_config = {
                "-:.*attn.*": {},
                "-:.*shared_expert.*": {},
                "-:.*visual.*": {},
                "-:.*mtp.*": {},
            }
            print(f"Dynamic exclusion: {list(dynamic_config.keys())}")'''

new_block = '''    # "auto" mode — auto-detect hybrid/MoE architectures and exclude modules
    # that crash GPTQ's 2D matrix quantizer (fused 3D expert tensors, etc.).
    with open(cfg_path) as f:
        cfg_recheck = json.load(f)
    dynamic_config = None
    exclusion_reasons = []
    has_hybrid_layers = False
    has_moe = False

    if "layer_types" in cfg_recheck:
        layer_types = cfg_recheck["layer_types"]
        unique_types = set(layer_types)
        if len(unique_types) > 1:
            has_hybrid_layers = True
            exclusion_reasons.append(
                f"hybrid layers: {dict((t, layer_types.count(t)) for t in unique_types)}"
            )

    # Detect MoE architecture (fused 3D expert tensors crash GPTQ).
    # Check multiple indicators: num_local_experts (Mixtral/Qwen),
    # num_experts (Gemma4), enable_moe_block (Gemma4), top_k_experts.
    search_scopes = [cfg_recheck, cfg_recheck.get("text_config", {})]
    for moe_key in ("num_local_experts", "num_experts"):
        for scope in search_scopes:
            val = scope.get(moe_key, 0)
            if isinstance(val, int) and val > 1:
                has_moe = True
                exclusion_reasons.append(f"MoE: {moe_key}={val}")
                break
        if has_moe:
            break
    if not has_moe:
        for scope in search_scopes:
            if scope.get("enable_moe_block") is True:
                has_moe = True
                n_exp = scope.get("num_experts", scope.get("top_k_experts", "?"))
                exclusion_reasons.append(f"MoE: enable_moe_block=True (experts={n_exp})")
                break

    if has_hybrid_layers or has_moe:
        print(f"Architecture detection: {'; '.join(exclusion_reasons)}")
        dynamic_config = {}
        if has_hybrid_layers and not has_moe:
            dynamic_config["-:.*shared_expert.*"] = {}
            dynamic_config["-:.*visual.*"] = {}
            dynamic_config["-:.*mtp.*"] = {}
        if has_moe:
            dynamic_config["-:.*experts.*"] = {}
            dynamic_config["-:.*block_sparse_moe.*"] = {}
            dynamic_config["-:.*router.*"] = {}
            dynamic_config["-:.*mlp.*"] = {}
            dynamic_config["-:.*shared_expert.*"] = {}
            dynamic_config["-:.*visual.*"] = {}
            dynamic_config["-:.*mtp.*"] = {}
        print(f"Dynamic exclusion: {list(dynamic_config.keys())}")'''

if old_block in src:
    src = src.replace(old_block, new_block)
    p.write_text(src)
    print("Patched MoE detection: enable_moe_block + fixed exclusion patterns")
else:
    print("MoE detection already patched or block not found")
MOE_FIX_PY
fi

# Auto-detect gfx900 (Radeon VII).
if command -v rocminfo &>/dev/null; then
    GPU_GFX=$(rocminfo 2>/dev/null | grep -oP 'gfx\d+' | head -1 || true)
    if [ "${GPU_GFX}" = "gfx900" ] || [ "${GPU_GFX}" = "gfx906" ]; then
        export HSA_OVERRIDE_GFX_VERSION=9.0.6
        # Test if GPU compute actually works (mixa3607 ROCm 6.3.3 images restore it).
        # Fall back to CPU-only if torch.empty fails (ROCm 6.4+ without rebuilt Tensile).
        if python3 -c "import torch; torch.empty(1, device='cuda')" 2>/dev/null; then
            echo "Detected ${GPU_GFX} (Radeon VII), GPU compute functional"
        else
            export HIP_VISIBLE_DEVICES=-1
            export CUDA_VISIBLE_DEVICES=""
            echo "Detected ${GPU_GFX} (Radeon VII), GPU broken — falling back to CPU-only mode"
        fi
    else
        echo "Detected GPU: ${GPU_GFX:-unknown}"
    fi
fi

emit_event "quantization_start" "model" "${MODEL_DIR}" "type" "${TYPE}" "bits" "${BITS}" "group_size" "${GROUP_SIZE}" "memory_gb" "${MAX_MEMORY_GB}"
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

# Short-circuit: if quantization already completed (quantize_config.json + safetensors
# in OUT_DIR), re-emit metadata and exit 0. Handles Job recreation after TTL GC.
#
# Preferred signal: .save-complete manifest written by quantize_gptq.py after the
# save + integrity-check phase succeeds. When present, we trust the explicit
# marker + shard-size check and skip the du(1)/min-size heuristic below.
#
# Fallback for pre-v13 artifacts (no .save-complete): require the shard index
# (written last by save_quantized) and compressed size ≥ 10% of original.
QUANT_STATUS="${MODEL_DIR}/.quantization-status.json"
SAVE_COMPLETE="${OUT_DIR}/.save-complete"
if [ -f "${OUT_DIR}/quantize_config.json" ] && ls "${OUT_DIR}"/*.safetensors &>/dev/null; then
    COMPRESSED_SIZE=$(du -sb "${OUT_DIR}" | cut -f1)
    SHARD_INDEX="${OUT_DIR}/model.safetensors.index.json"
    SINGLE_MODEL="${OUT_DIR}/model.safetensors"
    MIN_SIZE=$((ORIGINAL_SIZE / 10))

    SAVE_COMPLETE_OK="no"
    SAVE_COMPLETE_REASON=""
    if [ -f "${SAVE_COMPLETE}" ]; then
        if python3 - "${OUT_DIR}" "${SAVE_COMPLETE}" <<'VERIFY_SAVE_COMPLETE' 2>&1; then
import json, os, sys

out_dir, marker_path = sys.argv[1], sys.argv[2]
with open(marker_path) as fh:
    manifest = json.load(fh)
shards = manifest.get("shards") or []
if not shards:
    print("manifest has no shards", file=sys.stderr)
    sys.exit(2)
for entry in shards:
    name = entry.get("name")
    want = entry.get("size_bytes")
    if not name or not isinstance(want, int):
        print(f"malformed shard entry: {entry!r}", file=sys.stderr)
        sys.exit(2)
    shard_path = os.path.join(out_dir, name)
    if not os.path.isfile(shard_path):
        print(f"missing shard: {name}", file=sys.stderr)
        sys.exit(3)
    got = os.path.getsize(shard_path)
    if got != want:
        print(f"size mismatch {name}: on-disk={got} manifest={want}", file=sys.stderr)
        sys.exit(3)
print(f"save-complete verified: {len(shards)} shards match manifest")
VERIFY_SAVE_COMPLETE
            SAVE_COMPLETE_OK="yes"
        else
            SAVE_COMPLETE_REASON="manifest check failed; treating as partial"
        fi
    fi

    if [ "${SAVE_COMPLETE_OK}" = "yes" ]; then
        emit_event "quantization_cached" "model" "${MODEL_DIR}" "type" "${TYPE}" "original_bytes" "${ORIGINAL_SIZE}" "compressed_bytes" "${COMPRESSED_SIZE}" "source" "save_complete"
        echo "Quantization already complete in ${OUT_DIR} (verified via .save-complete)"
        echo "Output size: ${COMPRESSED_SIZE} bytes (original: ${ORIGINAL_SIZE})"
        if [ -f "${QUANT_STATUS}" ]; then
            cat "${QUANT_STATUS}" > /dev/termination-log 2>/dev/null || true
        else
            END_TS=$(date +%s)
            DURATION_SEC=$((END_TS - START_TS))
            cat > /dev/termination-log << TERMINATION
{
  "type": "${TYPE}",
  "originalSizeBytes": ${ORIGINAL_SIZE},
  "compressedSizeBytes": ${COMPRESSED_SIZE},
  "quantizationTimeSeconds": ${DURATION_SEC}
}
TERMINATION
        fi
        exit 0
    elif { [ -f "${SHARD_INDEX}" ] || [ -f "${SINGLE_MODEL}" ]; } && [ "${COMPRESSED_SIZE}" -gt "${MIN_SIZE}" ]; then
        emit_event "quantization_cached" "model" "${MODEL_DIR}" "type" "${TYPE}" "original_bytes" "${ORIGINAL_SIZE}" "compressed_bytes" "${COMPRESSED_SIZE}" "source" "heuristic"
        echo "Quantization already complete in ${OUT_DIR} (heuristic: no .save-complete marker)"
        echo "Output size: ${COMPRESSED_SIZE} bytes (original: ${ORIGINAL_SIZE})"
        if [ -f "${QUANT_STATUS}" ]; then
            cat "${QUANT_STATUS}" > /dev/termination-log 2>/dev/null || true
        else
            END_TS=$(date +%s)
            DURATION_SEC=$((END_TS - START_TS))
            cat > /dev/termination-log << TERMINATION
{
  "type": "${TYPE}",
  "originalSizeBytes": ${ORIGINAL_SIZE},
  "compressedSizeBytes": ${COMPRESSED_SIZE},
  "quantizationTimeSeconds": ${DURATION_SEC}
}
TERMINATION
        fi
        exit 0
    else
        emit_event "quantization_partial_detected" "model" "${MODEL_DIR}" "type" "${TYPE}" "compressed_bytes" "${COMPRESSED_SIZE}" "min_expected" "${MIN_SIZE}" "has_index" "$([ -f \"${SHARD_INDEX}\" ] && echo yes || echo no)" "has_single" "$([ -f \"${SINGLE_MODEL}\" ] && echo yes || echo no)" "has_save_complete" "$([ -f \"${SAVE_COMPLETE}\" ] && echo yes || echo no)" "reason" "${SAVE_COMPLETE_REASON:-missing marker and below heuristic}"
        echo "WARNING: Output dir has quantize_config.json but save appears incomplete"
        echo "  shard_index_exists=$([ -f \"${SHARD_INDEX}\" ] && echo yes || echo no)"
        echo "  single_model_exists=$([ -f \"${SINGLE_MODEL}\" ] && echo yes || echo no)"
        echo "  save_complete_exists=$([ -f \"${SAVE_COMPLETE}\" ] && echo yes || echo no)"
        echo "  save_complete_reason=${SAVE_COMPLETE_REASON:-(absent)}"
        echo "  compressed_size=${COMPRESSED_SIZE} min_expected=${MIN_SIZE}"
        echo "Cleaning partial output and re-running quantization"
        rm -rf "${OUT_DIR}"
    fi
fi

# Clean stale temp save dir from previous interrupted saves
if [ -d "${OUT_DIR}.saving" ]; then
    emit_event "quantization_cleanup" "detail" "removing stale save temp dir" "path" "${OUT_DIR}.saving"
    echo "Cleaning stale save temp dir: ${OUT_DIR}.saving"
    rm -rf "${OUT_DIR}.saving"
fi

# Only clean output dir if no valid partial output exists.
# On retries (backoff), this preserves any checkpoint data.
if [ -d "${OUT_DIR}" ]; then
    if [ -f "${OUT_DIR}/quantize_config.json" ] || ls "${OUT_DIR}"/*.safetensors &>/dev/null 2>&1; then
        echo "WARNING: Partial output exists in ${OUT_DIR} but missing quantize_config.json or safetensors — cleaning"
    fi
    rm -rf "${OUT_DIR}"
fi
mkdir -p "${OUT_DIR}"
mkdir -p /workspace/offload

# torchao can core-dump (SIGABRT) on torch dev builds (e.g. 2.9.1+git) due to
# incompatible cpp extensions. GPTQModel imports torchao transitively, so a
# broken torchao kills the entire quantization pipeline. Remove proactively —
# GPTQModel works fine without it. Only skip removal on gfx906 (no torchao).
if [ "${PYTORCH_ROCM_ARCH:-}" != "gfx906" ] && [ "${GPU_GFX:-}" != "gfx906" ]; then
    if pip show torchao >/dev/null 2>&1; then
        echo "Removing torchao (incompatible cpp extensions crash on this torch build)..."
        python3 -m pip uninstall -y torchao >/dev/null 2>&1 || true
    fi
fi

# GPTQModel runtime dependencies are baked into the unified runtime image for
# quantizer-enabled profiles. Keep this fast-fail guard so older images still
# self-heal, but do not pay the install penalty when the image is already baked.
GPTQ_PY_IMPORTS="import tokenicer, pcre, kernels"
GPTQ_PIP_ARGS=(
    "tokenicer>=0.0.10"
    "pypcre>=0.2.13"
    "kernels>=0.12.2"
    "accelerate>=1.13.0"
    "hf_transfer>=0.1.9"
    "numpy>=1.26,<2"
    "pillow>=11.3.0"
    "protobuf>=7.34.0"
)
# torchao is removed early (crashes on torch dev builds). Skip arch-specific
# handling on gfx1100. On gfx906: SIGILL on Broadwell, also skip.
if [ "${PYTORCH_ROCM_ARCH:-}" = "gfx906" ] || [ "${GPU_GFX:-}" = "gfx900" ] || [ "${GPU_GFX:-}" = "gfx906" ]; then
    echo "Skipping torchao on gfx906/gfx900; wheel triggers SIGILL on older x86 hosts"
    # pypcre wheels SIGILL on Broadwell-era hosts. GPTQModel only needs a
    # pcre module; a stdlib re shim is sufficient for its import path here.
    python3 -m pip uninstall -y pypcre >/dev/null 2>&1 || true
    cat > /tmp/pcre.py <<'PY'
from re import *
PY
    export PYTHONPATH="/tmp${PYTHONPATH:+:${PYTHONPATH}}"
    GPTQ_PY_IMPORTS="import tokenicer; from gptqmodel import GPTQModel, QuantizeConfig"
    # Remove pypcre from pip args — it SIGILLs on Broadwell-era gfx906 hosts.
    # The /tmp/pcre.py shim satisfies GPTQModel's import requirement.
    GPTQ_PIP_ARGS=("${GPTQ_PIP_ARGS[@]/pypcre*/}")
    SKIP_PYPCRE_CHECK=1
fi

# Ensure gptqmodel + deps are available. Use pip show (no Python import) to
# avoid SIGABRT from broken native extensions in the import chain.
if ! pip show gptqmodel >/dev/null 2>&1; then
    echo "Installing GPTQModel (--no-build-isolation --no-deps)..."
    python3 -m pip install --no-cache-dir --no-build-isolation --no-deps "gptqmodel>=2.8.0" 2>&1 | tail -3
fi
# Check required deps via pip show (fast, no import side-effects).
MISSING_DEPS=()
for dep in tokenicer pypcre kernels accelerate; do
    # Skip pypcre on gfx906 — we use the /tmp/pcre.py re shim instead
    if [ "$dep" = "pypcre" ] && [ "${SKIP_PYPCRE_CHECK:-}" = "1" ]; then
        continue
    fi
    if ! pip show "$dep" >/dev/null 2>&1; then
        MISSING_DEPS+=("$dep")
    fi
done
if [ ${#MISSING_DEPS[@]} -gt 0 ]; then
    echo "Installing missing deps: ${MISSING_DEPS[*]}..."
    python3 -m pip install --no-cache-dir --quiet "${GPTQ_PIP_ARGS[@]}" 2>&1 | tail -3
fi

# MAGMA fallback: vllm-dev base images lack MAGMA (GPU) and LAPACK (CPU),
# causing torch.linalg.{cholesky,eigh,svd,qr} to fail. Patch to use scipy as
# final fallback for linalg ops needed by GPTQ warmup and Hessian inverse.
cat > /tmp/_magma_fallback.py << 'MAGMA_PATCH'
import torch, sys, runpy, threading

# GPTQModel's nogil_patcher patches JITFunction.run and Autotuner.run,
# expecting _cache_lock, _cache, _cache_futures. FLA's fused_norm_gate
# creates Triton kernel instances before nogil_patcher runs.
try:
    from triton.runtime.jit import JITFunction as _JIT
    if not hasattr(_JIT, '_cache_lock'):
        _JIT._cache_lock = threading.Lock()
        print("Patched JITFunction._cache_lock")
except Exception as _e:
    print(f"WARN: JITFunction patch failed: {_e}")
try:
    from triton.runtime.autotuner import Autotuner as _AT
    _at_patched = []
    if not hasattr(_AT, '_cache_lock'):
        _AT._cache_lock = threading.Lock()
        _at_patched.append('_cache_lock')
    if not hasattr(_AT, '_cache_futures'):
        _AT._cache_futures = {}
        _at_patched.append('_cache_futures')
    if not hasattr(_AT, '_flexinfer_init_patched'):
        _orig_at_init = _AT.__init__
        def _patched_at_init(self, *a, **kw):
            _orig_at_init(self, *a, **kw)
            if not hasattr(self, '_cache'):
                self._cache = getattr(self, 'cache', {})
            if not hasattr(self, '_cache_lock'):
                self._cache_lock = threading.Lock()
            if not hasattr(self, '_cache_futures'):
                self._cache_futures = {}
        _AT.__init__ = _patched_at_init
        _AT._flexinfer_init_patched = True
        _at_patched.append('__init__')
    if _at_patched:
        print(f"Patched Autotuner({','.join(_at_patched)}) for GPTQModel/FLA compat")
except Exception as _e:
    print(f"WARN: Autotuner patch failed: {_e}")
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

# Gemma4 compat patches: injected directly into quantize_gptq.py (see
# GEMMA4_COMPAT_PY block in wrapper script). Handles PLE per_layer_input
# guard + RoPE position_embeddings recomputation for heterogeneous head_dim.

sys.argv = ['quantize_gptq.py']
runpy.run_path('/opt/flexinfer/scripts/quantize_gptq.py', run_name='__main__')
MAGMA_PATCH

python3 /tmp/_magma_fallback.py

trap - EXIT

if ! ls "${OUT_DIR}"/*.safetensors &>/dev/null; then
    echo "ERROR: No safetensors files in output dir"
    exit 1
fi

# Force NFS write buffers to disk before any size checks. On NFS, kernel-level
# write buffers can report correct file sizes to stat() even when the NFS server
# hasn't committed all data. sync + drop_caches forces the kernel to flush and
# re-read actual sizes from the NFS server.
echo "Syncing NFS write buffers..."
sync
# Drop page cache so subsequent stat() reads true NFS-committed sizes
echo 3 > /proc/sys/vm/drop_caches 2>/dev/null || true

# Post-save safetensors integrity check (shell-level, after NFS sync).
# Catches truncation that the Python-level check can miss due to NFS buffering.
python3 -c "
import os, struct, json, sys
base = '${OUT_DIR}'
errors = []
for f in sorted(os.listdir(base)):
    if not f.endswith('.safetensors'):
        continue
    path = os.path.join(base, f)
    sz = os.path.getsize(path)
    with open(path, 'rb') as fh:
        hdr_size = struct.unpack('<Q', fh.read(8))[0]
        hdr = json.loads(fh.read(hdr_size))
    tensors = {k: v for k, v in hdr.items() if k != '__metadata__'}
    max_end = max((t['data_offsets'][1] for t in tensors.values()), default=0)
    expected = 8 + hdr_size + max_end
    if sz < expected:
        errors.append(f'{f}: {sz} bytes on disk, header says {expected} (missing {expected - sz} bytes)')
        print(f'FAIL {f}: TRUNCATED - {sz} < {expected} (missing {expected - sz} bytes)', file=sys.stderr)
    else:
        print(f'OK   {f}: {sz} bytes, {len(tensors)} tensors verified')
if errors:
    print(f'ERROR: {len(errors)} safetensors file(s) truncated after NFS sync:', file=sys.stderr)
    for e in errors:
        print(f'  {e}', file=sys.stderr)
    sys.exit(1)
print('All safetensors files passed post-NFS-sync integrity check')
"

COMPRESSED_SIZE=$(du -sb "${OUT_DIR}" | cut -f1)
OUTPUT_BASENAME=$(basename "${OUT_DIR}")
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
  "outputDir": "${OUTPUT_BASENAME}"
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

RATIO="0"
if [ "${ORIGINAL_SIZE}" -gt 0 ]; then
    RATIO=$(python3 -c "print(f'{${ORIGINAL_SIZE}/${COMPRESSED_SIZE}:.2f}')" 2>/dev/null || echo "0")
fi
emit_event "quantization_complete" "model" "${MODEL_DIR}" "type" "${TYPE}" "original_bytes" "${ORIGINAL_SIZE}" "compressed_bytes" "${COMPRESSED_SIZE}" "duration_sec" "${DURATION_SEC}" "compression_ratio" "${RATIO}"
echo "=== Quantization complete ==="
echo "Output: ${OUT_DIR}"
echo "End: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
`
}
