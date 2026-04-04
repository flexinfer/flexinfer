package quantization

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

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

	env := b.buildEnv(params.ModelPath, bits, groupSize, sym, descAct, memoryGB, gpuMemFraction, dynamicExclusion, params.GPUArch, params.Spec.Calibration)

	return buildGPUQuantizationJob(
		params,
		image,
		b.gptqWrapperScript(),
		memoryGB,
		env,
	)
}

// buildEnv returns environment variables for the GPTQ quantization script.
func (b *GPTQJobBuilder) buildEnv(modelPath string, bits, groupSize int, sym, descAct bool, memoryGB int32, gpuMemFraction, dynamicExclusion, gpuArch string, calib *aiv1alpha2.CalibrationSpec) []corev1.EnvVar {
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
	deviceMap := getenvDefault("FLEXINFER_GPTQ_DEVICE_MAP", "auto")
	// Force CPU model loading on AMD GPUs to avoid GPTQModel's
	// shell_module_materialize meta tensor crash. When device_map=auto, the
	// init_empty_weights() injection creates meta tensors that GPTQModel
	// cannot handle — shell_module_materialize crashes on .to(device).
	// device_map=cpu skips init_empty_weights entirely, using GPTQModel's
	// direct from_pretrained path with low_cpu_mem_usage=True. GPU compute
	// is still used for per-layer quantization; this only affects loading.
	// The CPU path needs enough memory for the full model in RAM plus GPTQ
	// overhead (~54GB model + ~4-6GB overhead for 27B). Set maxMemoryGB >= 60.
	if (gpuArch == "gfx906" || gpuArch == "gfx1100") && deviceMap == "auto" {
		deviceMap = "cpu"
	}

	env := []corev1.EnvVar{
		{Name: "MODEL_DIR", Value: fmt.Sprintf("/cache/%s", modelPath)},
		{Name: "OUT_DIR", Value: fmt.Sprintf("/cache/%s/gptq-w%d-g%d", modelPath, bits, groupSize)},
		{Name: "BITS", Value: fmt.Sprintf("%d", bits)},
		{Name: "GROUP_SIZE", Value: fmt.Sprintf("%d", groupSize)},
		{Name: "MAX_MEMORY_GB", Value: fmt.Sprintf("%d", memoryGB)},
		{Name: "SYM", Value: symStr},
		{Name: "DESC_ACT", Value: descActStr},
		{Name: "GPU_MEMORY_FRACTION", Value: gpuMemFraction},
		{Name: "DYNAMIC_EXCLUSION", Value: dynamicExclusion},
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
			CalibrationOverrides: map[string]int{
				"max_samples": 128,
				"max_seq_len": 2048,
				"max_tokens":  262144,
			},
			RuntimeOverrides: map[string]any{
				"attn_implementation": "eager",
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

# GPTQModel's direct CPU path still injects device_map=cpu_device_map. In
# transformers, any device_map enables meta-device loading/dispatch, which is
# exactly the path failing for Qwen3.5 here. Strip device_map and enable
# low_cpu_mem_usage=True so transformers loads shards incrementally (peak RSS
# ~ model_size + one shard) instead of loading full state_dict at once (~2x).
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
        f'{indent}    max_mem = get_max_memory()\n'
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
# Validates that the shard index file exists (written last by save_quantized) and that
# the compressed size is at least 10% of the original — prevents false positives from
# partial saves that wrote config + 1 shard before dying.
QUANT_STATUS="${MODEL_DIR}/.quantization-status.json"
if [ -f "${OUT_DIR}/quantize_config.json" ] && ls "${OUT_DIR}"/*.safetensors &>/dev/null; then
    COMPRESSED_SIZE=$(du -sb "${OUT_DIR}" | cut -f1)
    SHARD_INDEX="${OUT_DIR}/model.safetensors.index.json"
    SINGLE_MODEL="${OUT_DIR}/model.safetensors"
    MIN_SIZE=$((ORIGINAL_SIZE / 10))
    if { [ -f "${SHARD_INDEX}" ] || [ -f "${SINGLE_MODEL}" ]; } && [ "${COMPRESSED_SIZE}" -gt "${MIN_SIZE}" ]; then
        emit_event "quantization_cached" "model" "${MODEL_DIR}" "type" "${TYPE}" "original_bytes" "${ORIGINAL_SIZE}" "compressed_bytes" "${COMPRESSED_SIZE}"
        echo "Quantization already complete in ${OUT_DIR}"
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
        emit_event "quantization_partial_detected" "model" "${MODEL_DIR}" "type" "${TYPE}" "compressed_bytes" "${COMPRESSED_SIZE}" "min_expected" "${MIN_SIZE}" "has_index" "$([ -f \"${SHARD_INDEX}\" ] && echo yes || echo no)" "has_single" "$([ -f \"${SINGLE_MODEL}\" ] && echo yes || echo no)"
        echo "WARNING: Output dir has quantize_config.json but save appears incomplete"
        echo "  shard_index_exists=$([ -f \"${SHARD_INDEX}\" ] && echo yes || echo no)"
        echo "  single_model_exists=$([ -f \"${SINGLE_MODEL}\" ] && echo yes || echo no)"
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
